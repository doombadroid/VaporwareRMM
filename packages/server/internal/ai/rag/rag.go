// Package rag is the retrieval-augmented-generation layer. It embeds
// operator-authored content (closed tickets, KB articles, runbook docs)
// into pgvector and serves topK similarity queries to capabilities.
//
// Two non-negotiable invariants this package enforces:
//
//  1. Every retrieval pushes (tenant_id, customer_id) into the WHERE clause
//     BEFORE the vector search, never as a post-K filter. A capability
//     scoped to (tenant=A, customer=X) cannot accidentally retrieve
//     content embedded by tenant B.
//  2. The embedding column has a fixed dimension (matches migration 030
//     and the embedding model that was active when the index was built).
//     Switching embedding models requires a re-index pass.
//
// Stage 1 ships indexing for closed tickets only. KB articles + runbook
// docs are deferred to Stage 2 when the assistance capabilities land.
package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/db"

	"github.com/google/uuid"
)

// Defaults. Operators can pin a different embedding model per routing rule;
// this is what we fall back to when nothing is configured.
const (
	DefaultModel = "text-embedding-3-small"
	DefaultDim   = 1536
)

// Scope is the (tenant, optional customer) envelope for every operation.
// CustomerID="" means tenant-wide, which is the only scope a super_admin
// retrieval should ever use.
type Scope struct {
	TenantID   string
	CustomerID string
}

// SourceKind enumerates what kind of document a row in ai_embeddings
// represents. Capabilities filter retrieval on this so a ticket-clustering
// query doesn't pull KB articles.
type SourceKind string

const (
	SourceTicket  SourceKind = "ticket"
	SourceKB      SourceKind = "kb"
	SourceRunbook SourceKind = "runbook"
)

// Hit is one retrieved item. Score is a cosine-distance proxy in [0,2];
// lower is closer.
type Hit struct {
	SourceKind SourceKind
	SourceID   string
	CustomerID string
	Text       string
	Score      float64
}

// Indexer issues an Index call for one document. Embedding cost is billed
// against the tenant's embedding cap; failure is the operator's signal that
// something needs attention (capacity exhausted, model deprecated, etc.).
//
// Idempotent: if the same (source_kind, source_id, model) tuple already
// exists with the same text_hash, we skip the embed call entirely. Re-index
// pipelines can pass the same content repeatedly without burning budget.
type Indexer interface {
	Index(ctx context.Context, scope Scope, kind SourceKind, sourceID, text string) error
}

// Retriever runs topK similarity search inside the scope. The query text is
// embedded with the same model that's pinned for this tenant; mismatched
// dims will error rather than return nonsense.
type Retriever interface {
	Retrieve(ctx context.Context, scope Scope, kind SourceKind, queryText string, topK int) ([]Hit, error)
}

// Service is the concrete implementation backed by ai.Run + Postgres pgvector.
type Service struct{}

func New() *Service { return &Service{} }

// Index upserts an embedding for (scope, kind, sourceID). The text is hashed;
// if the hash matches a previously-indexed row, we skip the embed call to
// save cost. The unique constraint in migration 030 ensures we don't
// accidentally double-index.
func (s *Service) Index(ctx context.Context, scope Scope, kind SourceKind, sourceID, text string) error {
	if err := ai.SupportedDialect(); err != nil {
		return err
	}
	if scope.TenantID == "" {
		return errors.New("rag.Index: empty TenantID")
	}
	if text == "" {
		return errors.New("rag.Index: empty text")
	}
	textHash := hashText(text)

	// Skip if up-to-date row exists with the same text hash + same model.
	model := embeddingModelFor(scope.TenantID)
	var existingHash string
	err := db.DB.QueryRow(`
		SELECT text_hash FROM ai_embeddings
		 WHERE tenant_id = ? AND source_kind = ? AND source_id = ? AND model_name = ?`,
		scope.TenantID, string(kind), sourceID, model,
	).Scan(&existingHash)
	if err == nil && existingHash == textHash {
		return nil
	}

	// Embed. We don't tie this to a capability — indexing is operator-initiated
	// background work. We still go through ai.Run() so the embedding budget
	// counter ticks and the audit row records the cost. The capability_id is
	// "rag.index" which we register below in init().
	out, err := ai.Run(ctx, ai.Input{
		TenantID:     scope.TenantID,
		CustomerID:   scope.CustomerID,
		CapabilityID: "rag.index",
		RunType:      ai.RunTypeEmbed,
	}, func(ctx context.Context, p ai.Provider, _ string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		// Embedding model comes from embeddingModelFor(), not the routing
		// rule's chat-model name. The chokepoint passes the chat model to fn
		// for completeness but rag deliberately ignores it.
		resp, err := p.Embed(ctx, ai.EmbedRequest{Model: model, Inputs: []string{text}})
		if err != nil {
			return nil, nil, nil, err
		}
		return nil, &resp, nil, nil
	})
	if err != nil {
		return fmt.Errorf("rag.Index: %w", err)
	}
	if out.EmbedResp == nil || len(out.EmbedResp.Vectors) == 0 {
		return errors.New("rag.Index: provider returned no vectors")
	}
	vec := out.EmbedResp.Vectors[0]
	if len(vec) != DefaultDim {
		return fmt.Errorf("rag.Index: embedding dim %d != schema dim %d (model mismatch — re-index required)", len(vec), DefaultDim)
	}

	// Upsert. ON CONFLICT replaces the embedding when text_hash changed so
	// re-indexing a modified document overwrites the old vector cleanly.
	_, err = db.DB.Exec(`
		INSERT INTO ai_embeddings (id, tenant_id, customer_id, source_kind, source_id, text_hash, model_name, dim, embedding, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, source_kind, source_id, model_name) DO UPDATE
		   SET text_hash = EXCLUDED.text_hash,
		       embedding = EXCLUDED.embedding,
		       customer_id = EXCLUDED.customer_id,
		       created_at = EXCLUDED.created_at`,
		uuid.New().String(), scope.TenantID, nullableStr(scope.CustomerID),
		string(kind), sourceID, textHash, model, DefaultDim, vectorLiteral(vec),
		nowUnix(),
	)
	if err != nil {
		return fmt.Errorf("rag.Index upsert: %w", err)
	}
	return nil
}

// Retrieve runs cosine-distance topK search inside the scope. Tenant + (when
// set) customer filters go INTO the WHERE clause so the vector index never
// considers cross-tenant rows — even if the ANN index returns them as
// neighbours, the WHERE filter cuts them before they leave the database.
func (s *Service) Retrieve(ctx context.Context, scope Scope, kind SourceKind, queryText string, topK int) ([]Hit, error) {
	if err := ai.SupportedDialect(); err != nil {
		return nil, err
	}
	if scope.TenantID == "" {
		return nil, errors.New("rag.Retrieve: empty TenantID")
	}
	if topK <= 0 || topK > 100 {
		topK = 10
	}
	model := embeddingModelFor(scope.TenantID)

	// Embed the query. Same routing rule as indexing — model swap mid-stream
	// would silently break similarity unless the index was rebuilt.
	out, err := ai.Run(ctx, ai.Input{
		TenantID:     scope.TenantID,
		CustomerID:   scope.CustomerID,
		CapabilityID: "rag.query",
		RunType:      ai.RunTypeEmbed,
	}, func(ctx context.Context, p ai.Provider, _ string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		resp, err := p.Embed(ctx, ai.EmbedRequest{Model: model, Inputs: []string{queryText}})
		if err != nil {
			return nil, nil, nil, err
		}
		return nil, &resp, nil, nil
	})
	if err != nil {
		return nil, fmt.Errorf("rag.Retrieve embed: %w", err)
	}
	if out.EmbedResp == nil || len(out.EmbedResp.Vectors) == 0 {
		return nil, errors.New("rag.Retrieve: provider returned no vectors")
	}
	vec := out.EmbedResp.Vectors[0]
	if len(vec) != DefaultDim {
		return nil, fmt.Errorf("rag.Retrieve: embedding dim %d != schema dim %d", len(vec), DefaultDim)
	}

	// pgvector cosine-distance operator <=> with ORDER BY ASC = closest first.
	// Tenant + customer filter pushed in. We deliberately accept rows where
	// customer_id IS NULL so tenant-wide content (e.g., MSP-shared KB) is
	// retrievable inside any customer scope; rows with a different
	// non-null customer_id are excluded.
	q := `SELECT source_kind, source_id, COALESCE(customer_id,''),
	             '' AS text,
	             embedding <=> ? AS score
	        FROM ai_embeddings
	       WHERE tenant_id = ? AND source_kind = ?`
	args := []any{vectorLiteral(vec), scope.TenantID, string(kind)}
	if scope.CustomerID != "" {
		q += ` AND (customer_id = ? OR customer_id IS NULL)`
		args = append(args, scope.CustomerID)
	}
	q += ` ORDER BY embedding <=> ? ASC LIMIT ?`
	args = append(args, vectorLiteral(vec), topK)

	rows, err := db.DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("rag.Retrieve query: %w", err)
	}
	defer rows.Close()
	hits := []Hit{}
	for rows.Next() {
		var h Hit
		var srcKind string
		if err := rows.Scan(&srcKind, &h.SourceID, &h.CustomerID, &h.Text, &h.Score); err != nil {
			continue
		}
		h.SourceKind = SourceKind(srcKind)
		hits = append(hits, h)
	}
	// Text isn't stored in ai_embeddings; callers re-fetch it from the source
	// table by id. Keeps the embedding row small and avoids storing
	// regulated content twice.
	return hits, nil
}

// embeddingModelFor reads the routing rule for TaskEmbed and returns the
// pinned embedding_model_name. Falls back to DefaultModel.
func embeddingModelFor(tenantID string) string {
	var model string
	_ = db.DB.QueryRow(`SELECT COALESCE(embedding_model_name,'') FROM ai_routing_rules WHERE tenant_id = ? AND task_type = ?`, tenantID, "embed").Scan(&model)
	if model == "" {
		return DefaultModel
	}
	return model
}

// vectorLiteral renders a []float32 as the string pgvector expects:
// "[0.1,0.2,0.3]". We pass it as a string parameter; pgvector's text input
// parser handles it. Avoids needing a typed driver for vector(N).
//
// NaN / +Inf / -Inf are coerced to 0 — pgvector's text parser would reject
// them and we'd rather degrade silently than abort an indexing batch
// because of a single malformed component (most likely from a buggy
// self-hosted backend). The audit log carries the model_version so the
// operator can identify the offending provider.
func vectorLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		f64 := float64(f)
		if f64 != f64 || f64 > 1e38 || f64 < -1e38 { // NaN or ±Inf-ish
			f64 = 0
		}
		parts[i] = strconv.FormatFloat(f64, 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func hashText(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nowUnix() int64 {
	return time.Now().Unix()
}

// Capability descriptors for the rag.index / rag.query system pseudo-capabilities.
// They're registered so ai.Run() can resolve them, but they are NOT user-visible
// (Stage = 0 marker) and have no operator-tunable scope.
func init() {
	ai.Register(ai.Capability{
		Name:              "rag.index",
		Category:          ai.CategoryAssistance,
		Description:       "Embed a document into the per-tenant vector index. System capability; not operator-toggleable.",
		Stage:             0,
		PreferredTaskType: ai.TaskEmbed,
		RequiredCaps:      ai.Capabilities{Embeddings: true},
	})
	ai.Register(ai.Capability{
		Name:              "rag.query",
		Category:          ai.CategoryAssistance,
		Description:       "Embed a query for similarity search. System capability; not operator-toggleable.",
		Stage:             0,
		PreferredTaskType: ai.TaskEmbed,
		RequiredCaps:      ai.Capabilities{Embeddings: true},
	})
}
