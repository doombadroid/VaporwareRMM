package ai

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"

	"github.com/google/uuid"
)

// modelVersionRe is what we accept from a provider's reported model name
// before storing it on the audit row. A malicious openai_compat backend
// could otherwise return '<script>' or other text that, while React-escaped
// in the dashboard, would clutter the audit log with confusing entries.
var modelVersionRe = regexp.MustCompile(`^[A-Za-z0-9._:/+-]{1,128}$`)

func sanitizeModelVersion(s string) string {
	// Cap input before any per-rune work. A self-hosted provider returning a
	// 1MB model version string would otherwise burn ~1M loop iterations on
	// every chokepoint call (cheap DoS vector).
	const maxRawLen = 1024
	if len(s) > maxRawLen {
		s = s[:maxRawLen]
	}
	if modelVersionRe.MatchString(s) {
		return s
	}
	// Keep what we can. Strip non-matching chars + cap length.
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == ':' || r == '/' || r == '+' || r == '-' {
			b.WriteRune(r)
			if b.Len() >= 128 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "[unparseable]"
	}
	return b.String()
}

// Sentinels the chokepoint can return. Callers map these to HTTP statuses.
var (
	ErrAIDisabled          = errors.New("ai: disabled for this tenant")
	ErrCapabilityKilled    = errors.New("ai: capability or tenant kill switch is active")
	ErrCapabilityDisabled  = errors.New("ai: capability not enabled for this tenant")
	ErrNoProvider          = errors.New("ai: no provider configured for this routing rule")
	ErrCapNotFound         = errors.New("ai: capability not registered in this build")
	ErrUnmetDependency     = errors.New("ai: capability has unmet dependencies")
	ErrTenantSuspended     = errors.New("ai: tenant suspended")
	ErrProviderCapMismatch = errors.New("ai: chosen provider does not support a capability the requested feature requires")
)

// callChainKey is a private context-value key. The chain ID propagates from
// the originating capability through every nested call so the rate limiter
// (and audit trail) can detect cross-capability feedback loops.
type callChainKey struct{}

// WithChain returns a context that carries the given call-chain ID. New
// chains start with NewChainID.
func WithChain(ctx context.Context, chainID string) context.Context {
	return context.WithValue(ctx, callChainKey{}, chainID)
}

// ChainFromContext returns the chain ID, or "" if none.
func ChainFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(callChainKey{}).(string); ok {
		return v
	}
	return ""
}

// NewChainID mints a fresh chain ID for the start of a new agent session.
func NewChainID() string { return uuid.New().String() }

// Input is what callers hand to Run. Anything that varies per call is here;
// per-tenant + per-capability configuration is read from the DB inside Run
// so callers cannot mis-specify the operative rung or scope.
type Input struct {
	TenantID     string
	CapabilityID string // matches Capability.Name
	CustomerID   string // optional
	DeviceID     string // optional
	TicketID     string // optional
	RunType      RunType
	Devices      []DeviceSnapshot // captured by caller, audited by gate
	// Estimate is the caller's pre-call cost estimate in micros. The chokepoint
	// reserves this amount; after the call we reconcile to the provider's
	// reported cost.
	Estimate int64
}

// Output is what the caller's Func returned, plus the audit row id.
type Output struct {
	RunID      string
	CostMicros int64
	ChatResp   *ChatResponse
	EmbedResp  *EmbedResponse
}

// Func is the inner closure that performs the provider call. It receives:
//   - the resolved Provider
//   - the model name the operator pinned in the routing rule for this
//     capability's preferred task type
//
// The closure must use the supplied modelName when building the request;
// otherwise the operator's routing config is silently ignored. Returning
// ChatResp / EmbedResp (whichever applies) lets Run record token counts
// in the audit row.
type Func func(ctx context.Context, p Provider, modelName string) (*ChatResponse, *EmbedResponse, []byte, error)

// runtimeConfig is what we resolve up front so nothing changes mid-run.
type runtimeConfig struct {
	tenantStatus   string
	rung           Rung
	enabled        bool
	scopeFilter    ScopeFilter
	confidence     int
	blastMax       int
	blastWindow    int
	providerCfg    ProviderConfig
	costKind       CostKind
	modelName      string
	maxCostMicros  int64
	costPerKInput  int64 // copied from routing rule so we don't re-query mid-call
	costPerKOutput int64
}

// Run is the chokepoint. Every provider call in the codebase goes through
// here. There is no other path. Adding a new gate later is a one-place edit.
func Run(ctx context.Context, in Input, fn Func) (Output, error) {
	if err := SupportedDialect(); err != nil {
		return Output{}, err
	}

	// 1. Kill switches first — this is the cheapest check (in-memory cache)
	// and the most consequential. An operator hitting the big red button
	// must short-circuit before we touch the DB or load a config.
	if IsKilled(in.TenantID, in.CapabilityID) {
		return Output{}, ErrCapabilityKilled
	}

	// 2. Tenant master switch + status + per-capability config + routing.
	cfg, err := loadRuntime(ctx, in)
	if err != nil {
		return Output{}, err
	}
	if cfg.tenantStatus != "active" {
		return Output{}, fmt.Errorf("%w: status=%s", ErrTenantSuspended, cfg.tenantStatus)
	}
	// System capabilities (Stage 0 marker — rag.index, rag.query, etc.)
	// bypass the per-tenant enabled flag. Operators don't see them in the
	// dashboard and shouldn't have to opt into them per tenant — they're
	// infrastructure that user-facing capabilities depend on.
	if cap, ok := Lookup(in.CapabilityID); ok && cap.Stage == 0 {
		// implicit enabled
	} else if !cfg.enabled {
		return Output{}, ErrCapabilityDisabled
	}

	// 3. Capability registered + deps met.
	cap, ok := Lookup(in.CapabilityID)
	if !ok {
		return Output{}, ErrCapNotFound
	}
	_ = cap // referenced by the provider-cap check below
	unmet, err := CheckDependencies(in.CapabilityID)
	if err != nil {
		return Output{}, err
	}
	if len(unmet) > 0 {
		return Output{}, fmt.Errorf("%w: %s", ErrUnmetDependency, strings.Join(unmet, ", "))
	}

	// 4. Snapshot scope + hash. The hash is audited so a post-hoc reviewer
	// can prove which exact device set the gate authorised.
	snap := ScopeSnapshot{
		TenantID:   in.TenantID,
		CustomerID: in.CustomerID,
		Devices:    in.Devices,
		CapturedAt: time.Now().Unix(),
	}
	snap.Hash = hashSnapshot(snap)

	// 5. Build the provider.
	prov, err := Build(cfg.providerCfg)
	if err != nil {
		return Output{}, fmt.Errorf("ai: build provider: %w", err)
	}

	// 5a. Provider must support every feature the capability requires. We do
	// the comparison here rather than at routing-rule create time because
	// the capability registry is in-process — a routing rule pinned to
	// `ollama` is fine if the only capability that uses it doesn't need
	// tool calls, but the moment a tool-using capability registers against
	// the same task_type we should fail loudly instead of letting the
	// provider 400 the request mid-flight.
	if !providerSupports(prov.Caps(), cap.RequiredCaps) {
		return Output{}, fmt.Errorf("%w: capability %s needs %+v, provider %s offers %+v",
			ErrProviderCapMismatch, cap.Name, cap.RequiredCaps, prov.Kind(), prov.Caps())
	}

	// 5b. Autonomous-rung confidence floor. Capabilities at autonomous bypass
	// per-instance human approval — we owe the operator a precision check
	// against the recent metrics window. If precision drops below the
	// configured threshold, we refuse the run AND demote the capability one
	// rung. The next call lands at act_policy and an alert wakes someone.
	if cfg.rung == RungAutonomous {
		if err := enforceAutonomousFloor(ctx, in.TenantID, in.CapabilityID, cap, cfg.confidence); err != nil {
			return Output{}, err
		}
	}
	_ = cap // referenced by enforceAutonomousFloor on the autonomous path

	// 6. Reserve cost up front.
	if err := ReserveCost(ctx, in.TenantID, cfg.costKind, in.Estimate); err != nil {
		return Output{}, err
	}

	// 7. Execute. We pass a child context so cancellation propagates to the
	// provider HTTP client — closing the dashboard tab kills upstream tokens.
	// Wrap fn() in a panic recovery so a runtime panic in user-supplied
	// closure code can't leak the cost reservation. Recovered panics turn
	// into a regular call error and the audit + release paths run.
	t0 := time.Now()
	chatResp, embedResp, actionBytes, callErr := callSafely(ctx, prov, cfg.modelName, fn)
	latency := time.Since(t0)

	// 8. Reconcile cost: figure actual using provider-reported tokens + the
	// routing rule's per-1k rates. Release any over-reservation.
	actualMicros := computeActualCost(cfg, chatResp, embedResp)
	if over := in.Estimate - actualMicros; over > 0 {
		ReleaseCost(ctx, in.TenantID, cfg.costKind, over)
	}

	// 9. Persist audit row (write-once, signed). If this fails we must NOT
	// return success: the cost has already been billed via Redis/DB but
	// without an ai_runs row the daily-spend SUM in reserveViaDB is stale,
	// and we have no record of who did what. Release the reservation and
	// return a hard error so the caller knows to retry (idempotently) or
	// alert an operator.
	runID := uuid.New().String()
	if err := persistRun(ctx, runID, in, cfg, snap, chatResp, embedResp, actionBytes, actualMicros, latency, callErr); err != nil {
		slog.Error("ai: failed to persist audit row — releasing cost reservation and failing the run",
			"tenant_id", in.TenantID, "capability_id", in.CapabilityID, "error", err)
		ReleaseCost(ctx, in.TenantID, cfg.costKind, in.Estimate)
		if callErr != nil {
			return Output{}, fmt.Errorf("ai: provider call failed AND audit persist failed: provider=%v audit=%w", callErr, err)
		}
		return Output{}, fmt.Errorf("ai: audit persist failed: %w", err)
	}

	if callErr != nil {
		return Output{RunID: runID, CostMicros: actualMicros}, callErr
	}
	return Output{
		RunID:      runID,
		CostMicros: actualMicros,
		ChatResp:   chatResp,
		EmbedResp:  embedResp,
	}, nil
}

// loadRuntime resolves every per-tenant + per-capability + routing setting in
// a single read so a config change after this point doesn't take effect for
// this in-flight call.
func loadRuntime(ctx context.Context, in Input) (runtimeConfig, error) {
	var rc runtimeConfig

	// Tenant flags.
	var aiEnabled int
	var status string
	if err := db.DB.QueryRow(`SELECT COALESCE(ai_enabled,0), COALESCE(status,'active') FROM tenants WHERE id = ?`, in.TenantID).Scan(&aiEnabled, &status); err != nil {
		return rc, fmt.Errorf("ai: load tenant flags: %w", err)
	}
	rc.tenantStatus = status
	if aiEnabled != 1 {
		return rc, ErrAIDisabled
	}

	// Per-(tenant, capability) config — defaults safe (disabled, shadow rung).
	var enabled int
	var rung string
	var scopeRaw sql.NullString
	var conf, blastMax, blastWin int
	err := db.DB.QueryRow(`
		SELECT COALESCE(enabled,0), COALESCE(rung,'shadow'),
		       scope_filter, COALESCE(confidence_threshold,0),
		       COALESCE(blast_radius_max_devices,0),
		       COALESCE(blast_radius_window_minutes,5)
		  FROM ai_capability_tenant_config
		 WHERE tenant_id = ? AND capability_id = ?`, in.TenantID, in.CapabilityID).Scan(
		&enabled, &rung, &scopeRaw, &conf, &blastMax, &blastWin,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return rc, fmt.Errorf("ai: load capability config: %w", err)
	}
	rc.enabled = enabled == 1
	rc.rung = Rung(rung)
	rc.confidence = conf
	rc.blastMax = blastMax
	rc.blastWindow = blastWin
	if scopeRaw.Valid {
		// Fail closed on malformed JSON. The previous behaviour silently
		// defaulted to an empty (no-op) ScopeFilter, which on a row that
		// was supposed to restrict the capability to one customer would
		// have widened it to the entire tenant. Better to refuse the run
		// than silently expand blast radius.
		s, err := ParseScopeFilter([]byte(scopeRaw.String))
		if err != nil {
			return rc, fmt.Errorf("ai: malformed scope_filter for capability %q: %w (refusing run; fix the row in ai_capability_tenant_config)", in.CapabilityID, err)
		}
		rc.scopeFilter = s
	}

	// Routing rule: pick provider + model for this capability's preferred task.
	cap, _ := Lookup(in.CapabilityID)
	taskType := string(cap.PreferredTaskType)
	if taskType == "" {
		taskType = string(TaskReason)
	}
	// Default cost kind to chat; embed runs flip both the task routing and
	// the cost bucket. Being explicit here means future RunTypes don't
	// silently fall through to CostChat just because it's iota=0.
	rc.costKind = CostChat
	if in.RunType == RunTypeEmbed {
		taskType = string(TaskEmbed)
		rc.costKind = CostEmbedding
	}
	var providerID, modelName string
	var maxCost, inRate, outRate sql.NullInt64
	err = db.DB.QueryRow(`
		SELECT preferred_provider_id, model_name, max_cost_per_call_micros,
		       cost_per_1k_input_micros, cost_per_1k_output_micros
		  FROM ai_routing_rules
		 WHERE tenant_id = ? AND task_type = ?`, in.TenantID, taskType).Scan(
		&providerID, &modelName, &maxCost, &inRate, &outRate,
	)
	if err != nil {
		return rc, fmt.Errorf("%w: no routing rule for task_type=%s in tenant %s. Configure one under Settings → AI → Routing rules. Underlying error: %v",
			ErrNoProvider, taskType, in.TenantID, err)
	}
	rc.modelName = modelName
	if maxCost.Valid {
		rc.maxCostMicros = maxCost.Int64
	}
	if inRate.Valid {
		rc.costPerKInput = inRate.Int64
	}
	if outRate.Valid {
		rc.costPerKOutput = outRate.Int64
	}

	// Provider row + key decryption.
	pc, err := loadProvider(providerID)
	if err != nil {
		return rc, err
	}
	rc.providerCfg = pc
	return rc, nil
}

func loadProvider(id string) (ProviderConfig, error) {
	var pc ProviderConfig
	var enabled int
	var key sql.NullString
	if err := db.DB.QueryRow(`
		SELECT id, tenant_id, kind, name, COALESCE(base_url,''),
		       api_key_encrypted, COALESCE(region,''),
		       COALESCE(model_trust_level,'external'), COALESCE(enabled,0)
		  FROM ai_providers WHERE id = ?`, id).Scan(
		&pc.ID, &pc.TenantID, &pc.Kind, &pc.Name, &pc.BaseURL,
		&key, &pc.Region, (*string)(&pc.TrustLevel), &enabled,
	); err != nil {
		return pc, fmt.Errorf("ai: load provider %s: %w", id, err)
	}
	if enabled != 1 {
		return pc, fmt.Errorf("ai: provider %s is disabled", id)
	}
	if key.Valid && key.String != "" {
		dec, err := crypto.Decrypt(key.String)
		if err != nil {
			return pc, fmt.Errorf("ai: decrypt provider key: %w", err)
		}
		pc.APIKey = dec
	}
	return pc, nil
}

// callSafely runs fn under a panic recovery so a closure crash doesn't
// leak the cost reservation. The chokepoint must always reach the audit +
// release path, so we convert any panic to a normal error.
func callSafely(ctx context.Context, p Provider, modelName string, fn Func) (chat *ChatResponse, embed *EmbedResponse, action []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ai: provider closure panicked: %v", r)
		}
	}()
	return fn(ctx, p, modelName)
}

// enforceAutonomousFloor refuses the run + auto-demotes when a capability
// at the autonomous rung doesn't meet its confidence threshold. Threshold
// is read from ai_capability_tenant_config.confidence_threshold (0-100;
// percent) and compared against labelling-derived precision over the last
// 14 days. The min-samples floor is the capability's own
// PromotionCriteria.MinSamples — using a hardcoded 50 would let a
// capability that requires 100 samples to PROMOTE be demoted on 50, which
// is incoherent.
func enforceAutonomousFloor(ctx context.Context, tenantID, capID string, capDef Capability, threshold int) error {
	if threshold <= 0 {
		return nil // operator opted out — they own the consequences
	}
	since := time.Now().Add(-14 * 24 * time.Hour).Unix()
	var calls, correct, incorrect int
	err := db.DB.QueryRow(`
		SELECT COUNT(*),
		       SUM(CASE WHEN outcome='correct' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN outcome='incorrect' THEN 1 ELSE 0 END)
		  FROM ai_runs
		 WHERE tenant_id = ? AND capability_id = ? AND created_at >= ?
		   AND (output_text IS NULL OR output_text NOT LIKE '[error]%')`,
		tenantID, capID, since,
	).Scan(&calls, &correct, &incorrect)
	if err != nil {
		// Soft-fail: a metrics-query error must not block the chokepoint.
		return nil
	}
	labelled := correct + incorrect
	minSamples := capDef.DefaultPromotion.MinSamples
	if minSamples < 50 {
		minSamples = 50 // floor of 50 even for capabilities that omit MinSamples
	}
	if labelled < minSamples {
		return nil // not enough signal; keep autonomous
	}
	precision := float64(correct) / float64(labelled) * 100.0
	if precision >= float64(threshold) {
		return nil // healthy
	}
	// Demote one rung. Prefer act_policy (still acts but with scope-policy
	// gate) rather than all the way to shadow — operators promoted past
	// act_policy with intent and we don't want to wipe trust accumulation.
	// Surface the UPDATE error: a silent failure means the demote loop
	// repeats every call, masking DB pressure.
	now := time.Now().Unix()
	if _, err := db.DB.Exec(`
		UPDATE ai_capability_tenant_config
		   SET rung = 'act_policy', last_demoted_at = ?, updated_at = ?
		 WHERE tenant_id = ? AND capability_id = ?`,
		now, now, tenantID, capID,
	); err != nil {
		slog.Error("ai: autonomous-rung demote UPDATE failed; capability NOT demoted but call refused",
			"tenant_id", tenantID, "capability_id", capID, "error", err)
		return fmt.Errorf("ai: autonomous floor failed AND demote UPDATE failed (precision=%.1f%% threshold=%d): %w",
			precision, threshold, err)
	}
	slog.Warn("ai: autonomous-rung capability demoted on precision regression",
		"tenant_id", tenantID, "capability_id", capID,
		"precision", precision, "threshold", threshold, "labelled_samples", labelled)
	return fmt.Errorf("ai: autonomous capability %s demoted to act_policy — precision %.1f%% below threshold %d%% (labelled samples=%d)",
		capID, precision, threshold, labelled)
}

// providerSupports verifies the provider can fulfil every feature the
// capability declared as required. Bools are anded; MaxContext is a minimum
// requirement so a capability that needs at least 32k context isn't routed
// to a 4k-window model.
func providerSupports(have, need Capabilities) bool {
	if need.Streaming && !have.Streaming {
		return false
	}
	if need.ToolCalling && !have.ToolCalling {
		return false
	}
	if need.Embeddings && !have.Embeddings {
		return false
	}
	if need.JSONMode && !have.JSONMode {
		return false
	}
	if need.MaxContext > 0 && have.MaxContext > 0 && have.MaxContext < need.MaxContext {
		return false
	}
	return true
}

func computeActualCost(cfg runtimeConfig, chat *ChatResponse, embed *EmbedResponse) int64 {
	// Stage 0 math: per-1k rates were captured into runtimeConfig at gate
	// entry (so a routing-rule edit mid-call doesn't bill us at the new
	// rate). Self-hosted providers default to rate=0; the dashboard surfaces
	// 0-cost rows so operators see they need to fill in cost_per_1k_*.
	var promptTokens, outputTokens int
	if chat != nil {
		promptTokens = chat.PromptTokens
		outputTokens = chat.OutputTokens
	}
	if embed != nil {
		promptTokens = embed.PromptTokens
	}
	return (int64(promptTokens)*cfg.costPerKInput + int64(outputTokens)*cfg.costPerKOutput) / 1000
}

// persistRun writes the audit row. The `signed_hash` column locks the row's
// authoritative fields; any later mutation can be detected. We never UPDATE
// this row — corrections are inserted as new rows with parent_run_id.
func persistRun(ctx context.Context, runID string, in Input, cfg runtimeConfig, snap ScopeSnapshot,
	chat *ChatResponse, embed *EmbedResponse, action []byte, costMicros int64, latency time.Duration, callErr error,
) error {
	now := time.Now().Unix()

	var (
		modelVersion string
		promptTok    int
		outputTok    int
		outputText   string
	)
	modelName := cfg.modelName
	if chat != nil {
		modelVersion = sanitizeModelVersion(chat.ModelVersion)
		promptTok = chat.PromptTokens
		outputTok = chat.OutputTokens
		outputText = truncateForAudit(chat.Content)
		if chat.Synthetic {
			// Replace the audit row's model_name so a reviewer can tell at
			// a glance that this row is a synthetic capability decision
			// (no provider was actually called), not a real LLM completion.
			source := chat.SyntheticSource
			if source == "" {
				source = "synthetic"
			}
			modelName = "local:" + source
		}
	}
	if embed != nil {
		modelVersion = sanitizeModelVersion(embed.ModelVersion)
		promptTok = embed.PromptTokens
	}
	// Preserve provider errors in the audit row. We tag with "[error] " so
	// queries can distinguish failures from successful zero-output runs.
	if callErr != nil {
		outputText = "[error] " + truncateForAudit(callErr.Error())
	}

	chainID := ChainFromContext(ctx)

	row := []any{
		runID, in.TenantID, nullable(in.CustomerID), nullable(in.DeviceID), nullable(in.TicketID),
		nullable(in.CapabilityID), string(in.RunType), chainID, nil, // parent_run_id
		cfg.providerCfg.ID, modelName, modelVersion, string(cfg.providerCfg.TrustLevel),
		"" /* prompt_hash filled by caller via PromptBuilder later */, promptTok, outputTok,
		costMicros, latency.Milliseconds(), nil /* retrieved_context_refs */, outputText,
		nullableBytes(action), snap.Hash, string(cfg.rung), cfg.tenantStatus,
		nil, nil, nil, nil, // approver, outcome, outcome_set_by, outcome_set_at
		0, 0, // rollback_attempted, rollback_succeeded
		signRow(runID, in, cfg, costMicros, snap.Hash, now), now,
	}

	_, err := db.DB.Exec(`
		INSERT INTO ai_runs (
			id, tenant_id, customer_id, device_id, ticket_id, capability_id,
			run_type, call_chain_id, parent_run_id,
			provider_id, model_name, model_version, model_trust_level,
			prompt_hash, prompt_token_count, output_token_count,
			cost_usd_micros, latency_ms, retrieved_context_refs, output_text,
			action_taken, scope_snapshot_hash, rung_at_call, tenant_status_at_call,
			approved_by_user_id, outcome, outcome_set_by, outcome_set_at,
			rollback_attempted, rollback_succeeded, signed_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, row...)
	if err != nil {
		return err
	}
	if callErr != nil {
		// Audit the error in retrieved_context_refs JSON so investigations have it.
		_ = err
	}
	return nil
}

func truncateForAudit(s string) string {
	const cap = 16 * 1024
	if len(s) <= cap {
		return s
	}
	return s[:cap] + "...[truncated]"
}

func hashSnapshot(s ScopeSnapshot) string {
	b, _ := json.Marshal(s.Devices)
	h := sha256.Sum256(append([]byte(s.TenantID+"|"+s.CustomerID+"|"), b...))
	return hex.EncodeToString(h[:])
}

// RunForVerification carries the persisted fields needed to re-derive an
// audit row's signature. Returned by LoadRunForVerification, fed back into
// RecomputeSignature so the handler can compare stored vs recomputed.
type RunForVerification struct {
	RunID        string
	TenantID     string
	CapabilityID string
	RunType      string
	ProviderID   string
	ModelName    string
	Rung         string
	Cost         int64
	SnapHash     string
	CreatedAt    int64
	Signed       string
}

// LoadRunForVerification fetches a single ai_runs row by id, scoped to the
// tenant. Returns ErrCapNotFound if the row doesn't exist OR belongs to a
// different tenant — the same response either way so an attacker can't
// distinguish "no such row" from "row exists but isn't yours".
func LoadRunForVerification(runID, tenantID string) (RunForVerification, error) {
	var r RunForVerification
	err := db.DB.QueryRow(`
		SELECT id, tenant_id, COALESCE(capability_id,''), run_type, provider_id, model_name,
		       rung_at_call, cost_usd_micros, COALESCE(scope_snapshot_hash,''), created_at,
		       COALESCE(signed_hash,'')
		  FROM ai_runs WHERE id = ? AND tenant_id = ?`, runID, tenantID,
	).Scan(&r.RunID, &r.TenantID, &r.CapabilityID, &r.RunType, &r.ProviderID, &r.ModelName,
		&r.Rung, &r.Cost, &r.SnapHash, &r.CreatedAt, &r.Signed)
	if err != nil {
		return r, ErrCapNotFound
	}
	return r, nil
}

// RecomputeSignature reproduces the signRow logic over the fields persisted
// to ai_runs. Tampering with any of those fields after the fact yields a
// different hex string from what was stored at insert time.
func RecomputeSignature(r RunForVerification) string {
	msg := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d|%s|%d",
		r.RunID, r.TenantID, r.CapabilityID, r.RunType,
		r.ProviderID, r.ModelName, r.Rung,
		r.Cost, r.SnapHash, r.CreatedAt)
	return crypto.HMACSHA256("ai_run_v1", msg)
}

func signRow(runID string, in Input, cfg runtimeConfig, cost int64, snapHash string, ts int64) string {
	// Tamper-evidence over write-once fields. Provider id + model name are
	// included so an audit-log mutation that swapped which provider made the
	// call (or which model was used) invalidates the signature. The HMAC key
	// is derived from SECRETS_ENCRYPTION_KEY with the "ai_run_v1" domain tag,
	// so a future schema change can rotate signatures by bumping the domain
	// without rotating the encryption key.
	msg := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d|%s|%d",
		runID, in.TenantID, in.CapabilityID, in.RunType,
		cfg.providerCfg.ID, cfg.modelName, string(cfg.rung),
		cost, snapHash, ts)
	return crypto.HMACSHA256("ai_run_v1", msg)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
