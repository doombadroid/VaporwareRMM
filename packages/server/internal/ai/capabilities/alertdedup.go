// Package capabilities holds the concrete AI capabilities. Each file
// registers one capability via init() and exposes a single entry point
// the rest of the codebase calls when the corresponding signal fires.
//
// Stage 1 first capability: alert_dedup. Listens for alerts coming off
// the existing event bus, predicts which active cluster they belong to,
// and (in higher rungs) updates the cluster + posts a suggestion. In
// shadow mode the prediction is recorded to ai_runs only; no clusters
// are mutated and no UI is surfaced.
package capabilities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/prompt"
	"vaporrmm/server/internal/db"
)

// AlertContext is the input the dedup capability needs. Callers (the
// existing alert pipeline) build this struct and call PredictAlertCluster.
// We keep it small and provider-neutral so the alert source can evolve
// without touching this file.
type AlertContext struct {
	TenantID   string
	CustomerID string // optional
	DeviceID   string
	Severity   string // info|warning|critical
	Title      string
	Body       string
	Source     string // "rule:cpu_high" | "agent:exit_status" | etc.
	OccurredAt time.Time
}

// AlertCluster is what the capability decides about an incoming alert.
// At shadow rung the chokepoint persists this to ai_runs.action_taken
// without applying it to the ticket_clusters table.
type AlertCluster struct {
	ClusterID   string `json:"cluster_id"`
	Action      string `json:"action"`        // "join_existing" | "open_new"
	Signature   string `json:"signature"`
	Name        string `json:"name,omitempty"`
	LikelyCause string `json:"likely_cause,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// alertDedupCapName is the registry key. Other code references it by string
// to avoid a circular import; we still export it as a constant for clarity.
const alertDedupCapName = "alert_dedup"

func init() {
	ai.Register(ai.Capability{
		Name:              alertDedupCapName,
		Category:          ai.CategoryObservation,
		Description:       "Group similar alerts into clusters and propose a likely root cause. Shadow mode logs predictions; suggest mode surfaces them in the operator queue; act_low mode auto-creates ticket clusters.",
		Stage:             1,
		PreferredTaskType: ai.TaskClassify,
		// We need tool calling so the model can return structured cluster
		// decisions. Plain JSON mode also works; tool calling is the more
		// robust contract across providers.
		RequiredCaps: ai.Capabilities{JSONMode: true},
		DependsOn:    []string{"device_classification"},
	})
}

// PredictAlertCluster is the public entry point. The alert pipeline calls
// this asynchronously after an alert is recorded. The capability's rung
// governs side-effects:
//
//   - shadow: write prediction to ai_runs, no other change
//   - suggest: also create a ticket_clusters row with status='suggested'
//   - act_low + above: also link the source ticket/alert to the cluster
//
// At Stage 1 the suggest+higher branches are inactive — capabilities default
// to shadow and only super_admin can promote. We still implement the lookup
// and signature math because shadow mode needs accurate predictions to
// gather the precision/FPR metrics that gate promotion.
func PredictAlertCluster(ctx context.Context, in AlertContext) (AlertCluster, error) {
	if in.TenantID == "" {
		return AlertCluster{}, errors.New("alert_dedup: empty TenantID")
	}

	sig := alertSignature(in)

	// Cheap path: signature exact-match against active clusters in the same
	// tenant. If we hit, the LLM is not needed — we already know the cluster.
	// This is the bulk of de-dup in practice (a flapping service produces
	// identical alert text every flap).
	if existingID, existingName, err := lookupClusterBySignature(ctx, in.TenantID, sig); err == nil && existingID != "" {
		out, runErr := ai.Run(ctx, ai.Input{
			TenantID:     in.TenantID,
			CustomerID:   in.CustomerID,
			DeviceID:     in.DeviceID,
			CapabilityID: alertDedupCapName,
			RunType:      ai.RunTypeChat, // logical "prediction" run, no provider call below
			Estimate:     0,
		}, func(_ context.Context, _ ai.Provider, _ string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
			cluster := AlertCluster{
				ClusterID: existingID, Action: "join_existing", Signature: sig, Name: existingName,
				Reason: "signature exact-match against active cluster",
			}
			payload, _ := json.Marshal(cluster)
			// Synthetic ChatResponse: cost stays at $0 and the chokepoint
			// rewrites model_name to "local:exact-match" so audit reviewers
			// can tell this row is a cache hit, not a real provider call.
			return &ai.ChatResponse{
				Content:         string(payload),
				ModelVersion:    "local",
				Synthetic:       true,
				SyntheticSource: "alert_dedup_exact_match",
			}, nil, payload, nil
		})
		if runErr != nil {
			return AlertCluster{}, runErr
		}
		_ = out
		return AlertCluster{ClusterID: existingID, Action: "join_existing", Signature: sig, Name: existingName, Reason: "signature exact-match"}, nil
	}

	// LLM path: ask the model to either join one of the recent active clusters
	// or open a new one, with a short rationale. This is where the per-tenant
	// routing rule sends the call to whichever provider/model the operator
	// configured for `classify`.
	candidates, _ := recentClusters(ctx, in.TenantID, in.CustomerID, 10)

	// Sanitise alert metadata before injecting as trusted context. AlertContext
	// is server-internal today, but sanitising defensively means a future
	// webhook-ingest path doesn't open a prompt-injection channel through
	// fields like Severity or Source.
	pb := prompt.New(prompt.Scope{TenantID: in.TenantID, CustomerID: in.CustomerID}).
		SystemRules(`Decide whether the incoming alert belongs to one of the listed active clusters or warrants a new cluster.
Respond with strict JSON: {"action":"join_existing"|"open_new","cluster_id":"<id-or-empty>","name":"<short-noun-phrase>","likely_cause":"<one-sentence>"}.
Use join_existing only when the alert is plausibly the same incident as an existing cluster (same service, same device class, same failure mode).`).
		TrustedContext(formatCandidates(candidates)).
		UntrustedInput("alert_title", in.Title).
		UntrustedInput("alert_body", in.Body).
		TrustedContext(fmt.Sprintf("alert metadata: severity=%s source=%s device_id=%s occurred_at=%s",
			ai.SanitizeFreeText(in.Severity),
			ai.SanitizeFreeText(in.Source),
			ai.SanitizeFreeText(in.DeviceID),
			in.OccurredAt.Format(time.RFC3339)))

	var prediction AlertCluster
	prediction.Signature = sig
	out, err := ai.Run(ctx, ai.Input{
		TenantID:     in.TenantID,
		CustomerID:   in.CustomerID,
		DeviceID:     in.DeviceID,
		CapabilityID: alertDedupCapName,
		RunType:      ai.RunTypeChat,
		Estimate:     2_000, // ~2k tokens at typical classify-model rates; reconciled to actual after the call
	}, func(ctx context.Context, p ai.Provider, modelName string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		// modelName comes from the operator's routing rule for the
		// capability's preferred task type (classify) — never hardcoded
		// here, so a config change in the dashboard takes effect on the
		// next call without a code edit.
		req, _, perr := pb.Render(modelName, 256)
		if perr != nil {
			return nil, nil, nil, perr
		}
		resp, cerr := p.Chat(ctx, req)
		if cerr != nil {
			return nil, nil, nil, cerr
		}
		// Best-effort parse of the model's JSON. A bad parse becomes
		// "open_new" — safer to over-cluster than to misroute an alert
		// into the wrong incident.
		_ = json.Unmarshal([]byte(resp.Content), &prediction)
		if prediction.Action != "join_existing" && prediction.Action != "open_new" {
			prediction.Action = "open_new"
		}
		if prediction.Action == "open_new" {
			prediction.ClusterID = ""
		}
		prediction.Signature = sig
		payload, _ := json.Marshal(prediction)
		return &resp, nil, payload, nil
	})
	if err != nil {
		return AlertCluster{}, err
	}
	_ = out
	return prediction, nil
}

// alertSignature is what we use for the cheap exact-match path. We
// normalise the body (lowercase, strip numeric tokens that change every
// time — pids, timestamps, durations) so two runs of the same incident
// hash to the same value.
var (
	numericTokenRe = regexp.MustCompile(`\d+`)
	wsRe           = regexp.MustCompile(`\s+`)
)

func alertSignature(in AlertContext) string {
	body := strings.ToLower(in.Title + "\n" + in.Body)
	body = numericTokenRe.ReplaceAllString(body, "N")
	body = wsRe.ReplaceAllString(body, " ")
	body = strings.TrimSpace(body)
	h := sha256.New()
	h.Write([]byte(in.Source))
	h.Write([]byte{'|'})
	h.Write([]byte(in.Severity))
	h.Write([]byte{'|'})
	h.Write([]byte(body))
	return hex.EncodeToString(h.Sum(nil))
}

// lookupClusterBySignature returns the id + name of an active cluster in
// this tenant whose signature_hash matches. Used for the cheap exact-match
// path before any LLM call.
func lookupClusterBySignature(ctx context.Context, tenantID, sig string) (id, name string, err error) {
	err = db.DB.QueryRow(`
		SELECT id, COALESCE(name,'')
		  FROM ticket_clusters
		 WHERE tenant_id = ? AND signature_hash = ? AND status = 'active'
		 LIMIT 1`, tenantID, sig).Scan(&id, &name)
	return
}

// recentClusters returns up to N active clusters in the tenant for the LLM
// to consider when deciding join vs open-new. Customer scope, when set,
// narrows the candidate set so cross-customer clusters aren't shown.
func recentClusters(ctx context.Context, tenantID, customerID string, limit int) ([]clusterCandidate, error) {
	q := `SELECT id, COALESCE(name,''), COALESCE(likely_cause,''), count, last_seen
	        FROM ticket_clusters
	       WHERE tenant_id = ? AND status = 'active'`
	args := []any{tenantID}
	if customerID != "" {
		q += ` AND (customer_id = ? OR customer_id IS NULL)`
		args = append(args, customerID)
	}
	q += ` ORDER BY last_seen DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []clusterCandidate{}
	for rows.Next() {
		var c clusterCandidate
		if err := rows.Scan(&c.ID, &c.Name, &c.LikelyCause, &c.Count, &c.LastSeen); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

type clusterCandidate struct {
	ID, Name, LikelyCause string
	Count                 int
	LastSeen              int64
}

func formatCandidates(cs []clusterCandidate) string {
	if len(cs) == 0 {
		return "active clusters: (none)"
	}
	var sb strings.Builder
	sb.WriteString("active clusters in this tenant (most recent first):\n")
	for _, c := range cs {
		fmt.Fprintf(&sb, "- id=%s name=%q occurrences=%d last_seen=%s likely_cause=%q\n",
			c.ID, c.Name, c.Count, time.Unix(c.LastSeen, 0).UTC().Format(time.RFC3339), c.LikelyCause)
	}
	return sb.String()
}

// CreateClusterIfPromoted is the higher-rung side-effect path. Capabilities
// promoted past shadow call this after PredictAlertCluster to actually
// commit the prediction. At suggest rung we mark status='suggested' so the
// operator can dismiss; at act_low+ we mark it 'active' immediately.
//
// Stage 1 reserves this entry point but doesn't wire callers to it — every
// capability stays in shadow until Stage 2 surfaces the suggest queue.
//
// IMPORTANT: cluster name + likely_cause are sanitised on write. They came
// from a previous LLM call and will be re-injected as TRUSTED context to
// future LLM calls (recentClusters → formatCandidates). Any prompt-injection
// payload that snuck through one decision must not poison every subsequent
// decision.
func CreateClusterIfPromoted(ctx context.Context, in AlertContext, pred AlertCluster, status string) (string, error) {
	if pred.Action != "open_new" {
		return pred.ClusterID, nil
	}
	now := time.Now().Unix()
	id := uuid.New().String()
	_, err := db.DB.Exec(`
		INSERT INTO ticket_clusters (id, tenant_id, customer_id, signature_hash, name, likely_cause, first_seen, last_seen, count, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, signature_hash) DO UPDATE
		   SET last_seen = EXCLUDED.last_seen,
		       count = ticket_clusters.count + 1`,
		id, in.TenantID, nullableStr(in.CustomerID), pred.Signature,
		nullableStr(ai.SanitizeFreeText(pred.Name)),
		nullableStr(ai.SanitizeFreeText(pred.LikelyCause)),
		now, now, 1, status, now,
	)
	if err != nil {
		return "", fmt.Errorf("alert_dedup: create cluster: %w", err)
	}
	return id, nil
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
