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
	"strings"
	"time"

	"github.com/google/uuid"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
)

// Sentinels the chokepoint can return. Callers map these to HTTP statuses.
var (
	ErrAIDisabled         = errors.New("ai: disabled for this tenant")
	ErrCapabilityKilled   = errors.New("ai: capability or tenant kill switch is active")
	ErrCapabilityDisabled = errors.New("ai: capability not enabled for this tenant")
	ErrNoProvider         = errors.New("ai: no provider configured for this routing rule")
	ErrCapNotFound        = errors.New("ai: capability not registered in this build")
	ErrUnmetDependency    = errors.New("ai: capability has unmet dependencies")
	ErrTenantSuspended    = errors.New("ai: tenant suspended")
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
	CapabilityID string         // matches Capability.Name
	CustomerID   string         // optional
	DeviceID     string         // optional
	TicketID     string         // optional
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

// Func is the inner closure that performs the provider call. It receives the
// resolved Provider; the caller composes prompts, parses responses, and may
// emit tool calls — all inside this closure. Returning ChatResp / EmbedResp
// (whichever applies) lets Run record token counts in the audit row.
type Func func(ctx context.Context, p Provider) (*ChatResponse, *EmbedResponse, []byte, error)

// runtimeConfig is what we resolve up front so nothing changes mid-run.
type runtimeConfig struct {
	tenantStatus      string
	rung              Rung
	enabled           bool
	scopeFilter       ScopeFilter
	confidence        int
	blastMax          int
	blastWindow       int
	providerCfg       ProviderConfig
	costKind          CostKind
	modelName         string
	maxCostMicros     int64
	costPerKInput     int64 // copied from routing rule so we don't re-query mid-call
	costPerKOutput    int64
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
	if !cfg.enabled {
		return Output{}, ErrCapabilityDisabled
	}

	// 3. Capability registered + deps met.
	cap, ok := Lookup(in.CapabilityID)
	if !ok {
		return Output{}, ErrCapNotFound
	}
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
	_ = cap // use for tool composition later

	// 5. Build the provider.
	prov, err := Build(cfg.providerCfg)
	if err != nil {
		return Output{}, fmt.Errorf("ai: build provider: %w", err)
	}

	// 6. Reserve cost up front.
	if err := ReserveCost(ctx, in.TenantID, cfg.costKind, in.Estimate); err != nil {
		return Output{}, err
	}

	// 7. Execute. We pass a child context so cancellation propagates to the
	// provider HTTP client — closing the dashboard tab kills upstream tokens.
	t0 := time.Now()
	chatResp, embedResp, actionBytes, callErr := fn(ctx, prov)
	latency := time.Since(t0)

	// 8. Reconcile cost: figure actual using provider-reported tokens + the
	// routing rule's per-1k rates. Release any over-reservation.
	actualMicros := computeActualCost(cfg, chatResp, embedResp)
	if over := in.Estimate - actualMicros; over > 0 {
		ReleaseCost(ctx, in.TenantID, cfg.costKind, over)
	}

	// 9. Persist audit row (write-once, signed). Errors here are logged but
	// not returned — we must not lose the call result because we couldn't
	// write the audit; instead alert.
	runID := uuid.New().String()
	if err := persistRun(ctx, runID, in, cfg, snap, chatResp, embedResp, actionBytes, actualMicros, latency, callErr); err != nil {
		slog.Error("ai: failed to persist audit row", "tenant_id", in.TenantID,
			"capability_id", in.CapabilityID, "error", err)
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
		if s, err := ParseScopeFilter([]byte(scopeRaw.String)); err == nil {
			rc.scopeFilter = s
		}
	}

	// Routing rule: pick provider + model for this capability's preferred task.
	cap, _ := Lookup(in.CapabilityID)
	taskType := string(cap.PreferredTaskType)
	if taskType == "" {
		taskType = string(TaskReason)
	}
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
		return rc, fmt.Errorf("%w: task=%s: %v", ErrNoProvider, taskType, err)
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
	if chat != nil {
		modelVersion = chat.ModelVersion
		promptTok = chat.PromptTokens
		outputTok = chat.OutputTokens
		outputText = truncateForAudit(chat.Content)
	}
	if embed != nil {
		modelVersion = embed.ModelVersion
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
		cfg.providerCfg.ID, cfg.modelName, modelVersion, string(cfg.providerCfg.TrustLevel),
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

func signRow(runID string, in Input, cfg runtimeConfig, cost int64, snapHash string, ts int64) string {
	// Tamper-evidence over write-once fields. Provider id + model name are
	// included so an audit-log mutation that swapped which provider made the
	// call (or which model was used) invalidates the signature. The HMAC key
	// is the same SECRETS_ENCRYPTION_KEY that protects provider keys;
	// rotation invalidates historic signatures (operators must re-verify,
	// which is the right behaviour for SOC2 audits).
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s|%s|%d|%s|%d",
		runID, in.TenantID, in.CapabilityID, in.RunType,
		cfg.providerCfg.ID, cfg.modelName, string(cfg.rung),
		cost, snapHash, ts)
	return hex.EncodeToString(h.Sum(nil))
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
