package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
)

// RegisterAIRoutes wires the Settings → AI surface. All routes are admin-only;
// promotion past the `suggest` rung and global-scope kill switches are
// further restricted to super_admin inside the individual handlers.
//
// Every handler short-circuits with 503 when the database dialect can't
// host AI features (we require Postgres). The dashboard checks the same
// signal and hides the AI section, but the gate at the API layer is the
// one that stops a hand-crafted curl from reaching the chokepoint on a
// SQLite deployment.
func RegisterAIRoutes(api fiber.Router) {
	g := api.Group("/admin/ai", auth.AdminMiddleware(), aiDialectGate())

	// ── Tenant-level master switch + caps + DPA ack ───────────────────
	g.Get("/tenant", aiGetTenant)
	g.Patch("/tenant", aiPatchTenant)

	// ── Providers (per-tenant) ────────────────────────────────────────
	g.Get("/providers", aiListProviders)
	g.Post("/providers", aiCreateProvider)
	g.Patch("/providers/:id", aiPatchProvider)
	g.Delete("/providers/:id", aiDeleteProvider)

	// ── Routing rules ─────────────────────────────────────────────────
	g.Get("/routing", aiListRouting)
	g.Post("/routing", aiCreateRouting)
	g.Patch("/routing/:id", aiPatchRouting)
	g.Delete("/routing/:id", aiDeleteRouting)

	// ── Capability registry + per-tenant config ───────────────────────
	g.Get("/capabilities", aiListCapabilities)
	g.Patch("/capabilities/:name", aiPatchCapability)

	// ── Audit log ─────────────────────────────────────────────────────
	g.Get("/runs", aiListRuns)

	// ── Kill switches ─────────────────────────────────────────────────
	g.Get("/kill", aiListKill)
	g.Put("/kill", aiSetKill)
}

// aiDialectGate refuses every AI request when the DB isn't Postgres.
func aiDialectGate() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if err := ai.SupportedDialect(); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).
				JSON(fiber.Map{"error": err.Error()})
		}
		return c.Next()
	}
}

// targetTenant returns the tenant id this request operates on. tenant_admin
// callers are pinned to their own tenant. super_admin callers may target any
// tenant via ?tenant_id=, defaulting to their own.
func targetTenant(c *fiber.Ctx) string {
	role, _ := c.Locals("user_role").(string)
	caller := callerTenantID(c)
	if !auth.IsSuperAdmin(role) {
		return caller
	}
	if q := c.Query("tenant_id"); q != "" {
		return q
	}
	return caller
}

// ── Tenant master switch ─────────────────────────────────────────────────

func aiGetTenant(c *fiber.Ctx) error {
	tid := targetTenant(c)
	var (
		enabled, dpa  sql.NullInt64
		billing       sql.NullString
		chatCap, embCap sql.NullInt64
	)
	err := db.DB.QueryRow(`
		SELECT COALESCE(ai_enabled,0), COALESCE(ai_billing_mode,'absorb'),
		       COALESCE(ai_max_chat_cost_per_day_micros,0),
		       COALESCE(ai_max_embedding_cost_per_day_micros,0),
		       ai_dpa_acknowledged_at
		  FROM tenants WHERE id = ?`, tid,
	).Scan(&enabled, &billing, &chatCap, &embCap, &dpa)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "tenant not found"})
	}
	return c.JSON(fiber.Map{
		"tenant_id":                       tid,
		"ai_enabled":                      enabled.Int64 == 1,
		"ai_billing_mode":                 billing.String,
		"ai_max_chat_cost_per_day_micros": chatCap.Int64,
		"ai_max_embedding_cost_per_day_micros": embCap.Int64,
		"ai_dpa_acknowledged_at":          nullableInt(dpa),
	})
}

type tenantPatch struct {
	AIEnabled              *bool   `json:"ai_enabled,omitempty"`
	AIBillingMode          *string `json:"ai_billing_mode,omitempty"`
	AIMaxChatCostPerDay    *int64  `json:"ai_max_chat_cost_per_day_micros,omitempty"`
	AIMaxEmbedCostPerDay   *int64  `json:"ai_max_embedding_cost_per_day_micros,omitempty"`
	AcknowledgeDPA         *bool   `json:"acknowledge_dpa,omitempty"`
}

func aiPatchTenant(c *fiber.Ctx) error {
	role, _ := c.Locals("user_role").(string)
	tid := targetTenant(c)
	var p tenantPatch
	if err := c.BodyParser(&p); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}
	// Flipping the master switch on requires super_admin — same control plane
	// as enabling a new tenant. Tenant_admin can only flip OFF (kill their
	// own AI usage in a hurry) and acknowledge DPA + tune their own caps.
	if p.AIEnabled != nil && *p.AIEnabled && !auth.IsSuperAdmin(role) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "only super_admin may enable AI for a tenant"})
	}

	fields := []string{}
	args := []any{}
	if p.AIEnabled != nil {
		fields = append(fields, "ai_enabled = ?")
		args = append(args, boolToInt(*p.AIEnabled))
	}
	if p.AIBillingMode != nil {
		if *p.AIBillingMode != "absorb" && *p.AIBillingMode != "passthrough" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ai_billing_mode must be 'absorb' or 'passthrough'"})
		}
		fields = append(fields, "ai_billing_mode = ?")
		args = append(args, *p.AIBillingMode)
	}
	if p.AIMaxChatCostPerDay != nil {
		if *p.AIMaxChatCostPerDay < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cost cap must be non-negative"})
		}
		fields = append(fields, "ai_max_chat_cost_per_day_micros = ?")
		args = append(args, *p.AIMaxChatCostPerDay)
	}
	if p.AIMaxEmbedCostPerDay != nil {
		if *p.AIMaxEmbedCostPerDay < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cost cap must be non-negative"})
		}
		fields = append(fields, "ai_max_embedding_cost_per_day_micros = ?")
		args = append(args, *p.AIMaxEmbedCostPerDay)
	}
	if p.AcknowledgeDPA != nil && *p.AcknowledgeDPA {
		fields = append(fields, "ai_dpa_acknowledged_at = ?")
		args = append(args, time.Now().Unix())
	}
	if len(fields) == 0 {
		return c.JSON(fiber.Map{"message": "no changes"})
	}
	args = append(args, tid)
	if _, err := db.DB.Exec("UPDATE tenants SET "+joinFields(fields)+" WHERE id = ?", args...); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update failed"})
	}
	uid, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tid, uid, "ai.tenant.update", "tenant", tid, "updated AI tenant settings", c.IP())
	return c.JSON(fiber.Map{"message": "updated"})
}

// ── Providers ─────────────────────────────────────────────────────────────

func aiListProviders(c *fiber.Ctx) error {
	tid := targetTenant(c)
	rows, err := db.DB.Query(`
		SELECT id, kind, name, COALESCE(base_url,''), COALESCE(region,''),
		       COALESCE(model_trust_level,'external'), enabled, created_at, updated_at
		  FROM ai_providers WHERE tenant_id = ? ORDER BY name`, tid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "list failed"})
	}
	defer rows.Close()
	out := []fiber.Map{}
	for rows.Next() {
		var (
			id, kind, name, baseURL, region, trust string
			enabled                                int
			createdAt, updatedAt                   int64
		)
		if err := rows.Scan(&id, &kind, &name, &baseURL, &region, &trust, &enabled, &createdAt, &updatedAt); err != nil {
			continue
		}
		out = append(out, fiber.Map{
			"id": id, "kind": kind, "name": name, "base_url": baseURL,
			"region": region, "model_trust_level": trust, "enabled": enabled == 1,
			"created_at": createdAt, "updated_at": updatedAt,
			// api_key never returned
		})
	}
	return c.JSON(fiber.Map{"providers": out, "kinds": ai.KnownKinds()})
}

type providerCreate struct {
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	Region          string `json:"region"`
	ModelTrustLevel string `json:"model_trust_level"`
	Enabled         bool   `json:"enabled"`
}

func aiCreateProvider(c *fiber.Ctx) error {
	tid := targetTenant(c)
	var p providerCreate
	if err := c.BodyParser(&p); err != nil || p.Kind == "" || p.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "kind and name required"})
	}
	if !knownKind(p.Kind) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unknown provider kind"})
	}
	trust := p.ModelTrustLevel
	if trust == "" {
		trust = "external"
	}
	if trust != "local" && trust != "external" && trust != "self_hosted" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid model_trust_level"})
	}
	encKey := ""
	if p.APIKey != "" {
		v, err := crypto.Encrypt(p.APIKey)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "key encrypt failed"})
		}
		encKey = v
	}
	id := uuid.New().String()
	now := time.Now().Unix()
	_, err := db.DB.Exec(`
		INSERT INTO ai_providers (id, tenant_id, kind, name, base_url, api_key_encrypted, region, model_trust_level, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, tid, p.Kind, p.Name, p.BaseURL, nullableStr(encKey), p.Region, trust, boolToInt(p.Enabled), now, now,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "create failed"})
	}
	uid, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tid, uid, "ai.provider.create", "ai_provider", id, fmt.Sprintf("kind=%s name=%s", p.Kind, p.Name), c.IP())
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
}

type providerPatch struct {
	Name            *string `json:"name,omitempty"`
	BaseURL         *string `json:"base_url,omitempty"`
	APIKey          *string `json:"api_key,omitempty"`
	Region          *string `json:"region,omitempty"`
	ModelTrustLevel *string `json:"model_trust_level,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

func aiPatchProvider(c *fiber.Ctx) error {
	tid := targetTenant(c)
	id := c.Params("id")
	var p providerPatch
	if err := c.BodyParser(&p); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}
	fields := []string{"updated_at = ?"}
	args := []any{time.Now().Unix()}
	if p.Name != nil {
		fields = append(fields, "name = ?")
		args = append(args, *p.Name)
	}
	if p.BaseURL != nil {
		fields = append(fields, "base_url = ?")
		args = append(args, *p.BaseURL)
	}
	if p.APIKey != nil {
		// Empty string clears the key; otherwise re-encrypt.
		if *p.APIKey == "" {
			fields = append(fields, "api_key_encrypted = ?")
			args = append(args, nil)
		} else {
			enc, err := crypto.Encrypt(*p.APIKey)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "key encrypt failed"})
			}
			fields = append(fields, "api_key_encrypted = ?")
			args = append(args, enc)
		}
	}
	if p.Region != nil {
		fields = append(fields, "region = ?")
		args = append(args, *p.Region)
	}
	if p.ModelTrustLevel != nil {
		v := *p.ModelTrustLevel
		if v != "local" && v != "external" && v != "self_hosted" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid model_trust_level"})
		}
		fields = append(fields, "model_trust_level = ?")
		args = append(args, v)
	}
	if p.Enabled != nil {
		fields = append(fields, "enabled = ?")
		args = append(args, boolToInt(*p.Enabled))
	}
	args = append(args, id, tid)
	res, err := db.DB.Exec("UPDATE ai_providers SET "+joinFields(fields)+" WHERE id = ? AND tenant_id = ?", args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update failed"})
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "provider not found"})
	}
	uid, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tid, uid, "ai.provider.update", "ai_provider", id, "updated provider", c.IP())
	return c.JSON(fiber.Map{"message": "updated"})
}

func aiDeleteProvider(c *fiber.Ctx) error {
	tid := targetTenant(c)
	id := c.Params("id")
	res, err := db.DB.Exec(`DELETE FROM ai_providers WHERE id = ? AND tenant_id = ?`, id, tid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "provider not found"})
	}
	uid, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tid, uid, "ai.provider.delete", "ai_provider", id, "deleted provider", c.IP())
	return c.JSON(fiber.Map{"message": "deleted"})
}

// ── Routing rules ─────────────────────────────────────────────────────────

func aiListRouting(c *fiber.Ctx) error {
	tid := targetTenant(c)
	rows, err := db.DB.Query(`
		SELECT id, task_type, preferred_provider_id, COALESCE(fallback_provider_id,''),
		       model_name, COALESCE(embedding_model_name,''),
		       COALESCE(max_cost_per_call_micros,0), COALESCE(max_input_tokens,0),
		       COALESCE(max_output_tokens,0),
		       COALESCE(cost_per_1k_input_micros,0), COALESCE(cost_per_1k_output_micros,0)
		  FROM ai_routing_rules WHERE tenant_id = ? ORDER BY task_type`, tid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "list failed"})
	}
	defer rows.Close()
	out := []fiber.Map{}
	for rows.Next() {
		var (
			id, task, pp, fp, model, embedModel string
			maxCost, inTok, outTok, inRate, outRate int64
		)
		if err := rows.Scan(&id, &task, &pp, &fp, &model, &embedModel,
			&maxCost, &inTok, &outTok, &inRate, &outRate); err != nil {
			continue
		}
		out = append(out, fiber.Map{
			"id": id, "task_type": task, "preferred_provider_id": pp,
			"fallback_provider_id": fp, "model_name": model,
			"embedding_model_name":      embedModel,
			"max_cost_per_call_micros":  maxCost,
			"max_input_tokens":          inTok,
			"max_output_tokens":         outTok,
			"cost_per_1k_input_micros":  inRate,
			"cost_per_1k_output_micros": outRate,
		})
	}
	return c.JSON(fiber.Map{"routing_rules": out})
}

type routingCreate struct {
	TaskType            string `json:"task_type"`
	PreferredProviderID string `json:"preferred_provider_id"`
	FallbackProviderID  string `json:"fallback_provider_id,omitempty"`
	ModelName           string `json:"model_name"`
	EmbeddingModelName  string `json:"embedding_model_name,omitempty"`
	MaxCostPerCall      int64  `json:"max_cost_per_call_micros,omitempty"`
	MaxInputTokens      int    `json:"max_input_tokens,omitempty"`
	MaxOutputTokens     int    `json:"max_output_tokens,omitempty"`
	CostPer1kInput      int64  `json:"cost_per_1k_input_micros,omitempty"`
	CostPer1kOutput     int64  `json:"cost_per_1k_output_micros,omitempty"`
}

func aiCreateRouting(c *fiber.Ctx) error {
	tid := targetTenant(c)
	var p routingCreate
	if err := c.BodyParser(&p); err != nil || p.TaskType == "" || p.PreferredProviderID == "" || p.ModelName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "task_type, preferred_provider_id, model_name required"})
	}
	if !validTaskType(p.TaskType) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid task_type"})
	}
	// Verify provider exists in this tenant.
	if !providerOwned(p.PreferredProviderID, tid) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "preferred provider not found in tenant"})
	}
	if p.FallbackProviderID != "" && !providerOwned(p.FallbackProviderID, tid) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "fallback provider not found in tenant"})
	}
	id := uuid.New().String()
	now := time.Now().Unix()
	_, err := db.DB.Exec(`
		INSERT INTO ai_routing_rules (
			id, tenant_id, task_type, preferred_provider_id, fallback_provider_id,
			model_name, embedding_model_name, max_cost_per_call_micros,
			max_input_tokens, max_output_tokens,
			cost_per_1k_input_micros, cost_per_1k_output_micros,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, tid, p.TaskType, p.PreferredProviderID, nullableStr(p.FallbackProviderID),
		p.ModelName, nullableStr(p.EmbeddingModelName),
		p.MaxCostPerCall, p.MaxInputTokens, p.MaxOutputTokens,
		p.CostPer1kInput, p.CostPer1kOutput, now, now,
	)
	if err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "create failed (routing rule for this task_type may already exist)"})
	}
	uid, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tid, uid, "ai.routing.create", "ai_routing_rule", id, "task="+p.TaskType, c.IP())
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
}

func aiPatchRouting(c *fiber.Ctx) error {
	tid := targetTenant(c)
	id := c.Params("id")
	var p routingCreate
	if err := c.BodyParser(&p); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}
	fields := []string{"updated_at = ?"}
	args := []any{time.Now().Unix()}
	add := func(col string, val any) { fields = append(fields, col+" = ?"); args = append(args, val) }
	if p.PreferredProviderID != "" {
		if !providerOwned(p.PreferredProviderID, tid) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "preferred provider not in tenant"})
		}
		add("preferred_provider_id", p.PreferredProviderID)
	}
	if p.FallbackProviderID != "" {
		if !providerOwned(p.FallbackProviderID, tid) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "fallback provider not in tenant"})
		}
		add("fallback_provider_id", p.FallbackProviderID)
	}
	if p.ModelName != "" {
		add("model_name", p.ModelName)
	}
	if p.EmbeddingModelName != "" {
		add("embedding_model_name", p.EmbeddingModelName)
	}
	if p.MaxCostPerCall > 0 {
		add("max_cost_per_call_micros", p.MaxCostPerCall)
	}
	if p.MaxInputTokens > 0 {
		add("max_input_tokens", p.MaxInputTokens)
	}
	if p.MaxOutputTokens > 0 {
		add("max_output_tokens", p.MaxOutputTokens)
	}
	if p.CostPer1kInput >= 0 {
		add("cost_per_1k_input_micros", p.CostPer1kInput)
	}
	if p.CostPer1kOutput >= 0 {
		add("cost_per_1k_output_micros", p.CostPer1kOutput)
	}
	args = append(args, id, tid)
	res, err := db.DB.Exec("UPDATE ai_routing_rules SET "+joinFields(fields)+" WHERE id = ? AND tenant_id = ?", args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update failed"})
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "routing rule not found"})
	}
	uid, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tid, uid, "ai.routing.update", "ai_routing_rule", id, "updated routing rule", c.IP())
	return c.JSON(fiber.Map{"message": "updated"})
}

func aiDeleteRouting(c *fiber.Ctx) error {
	tid := targetTenant(c)
	id := c.Params("id")
	res, err := db.DB.Exec(`DELETE FROM ai_routing_rules WHERE id = ? AND tenant_id = ?`, id, tid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "routing rule not found"})
	}
	uid, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tid, uid, "ai.routing.delete", "ai_routing_rule", id, "deleted routing rule", c.IP())
	return c.JSON(fiber.Map{"message": "deleted"})
}

// ── Capabilities ──────────────────────────────────────────────────────────

func aiListCapabilities(c *fiber.Ctx) error {
	tid := targetTenant(c)
	caps := ai.All()
	out := make([]fiber.Map, 0, len(caps))
	for _, cap := range caps {
		// Per-tenant config (may be missing → defaults)
		var (
			enabled, conf, blastMax, blastWin, killSwitch int
			rung                                          string
			scopeRaw                                      sql.NullString
		)
		err := db.DB.QueryRow(`
			SELECT COALESCE(enabled,0), COALESCE(rung,'shadow'),
			       scope_filter, COALESCE(confidence_threshold,0),
			       COALESCE(blast_radius_max_devices,0),
			       COALESCE(blast_radius_window_minutes,5),
			       COALESCE(kill_switch,0)
			  FROM ai_capability_tenant_config
			 WHERE tenant_id = ? AND capability_id = ?`,
			tid, cap.Name,
		).Scan(&enabled, &rung, &scopeRaw, &conf, &blastMax, &blastWin, &killSwitch)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			continue
		}
		var scope ai.ScopeFilter
		if scopeRaw.Valid {
			scope, _ = ai.ParseScopeFilter([]byte(scopeRaw.String))
		} else {
			scope = cap.DefaultScope
		}
		unmet, _ := ai.CheckDependencies(cap.Name)
		out = append(out, fiber.Map{
			"name":                       cap.Name,
			"category":                   string(cap.Category),
			"description":                cap.Description,
			"stage":                      cap.Stage,
			"depends_on":                 cap.DependsOn,
			"unmet_dependencies":         unmet,
			"required_caps":              cap.RequiredCaps,
			"preferred_task_type":        string(cap.PreferredTaskType),
			"enabled":                    enabled == 1,
			"rung":                       rung,
			"scope_filter":               scope,
			"confidence_threshold":       conf,
			"blast_radius_max_devices":   blastMax,
			"blast_radius_window_minutes": blastWin,
			"kill_switch":                killSwitch == 1,
		})
	}
	return c.JSON(fiber.Map{"capabilities": out})
}

type capabilityPatch struct {
	Enabled                  *bool             `json:"enabled,omitempty"`
	Rung                     *string           `json:"rung,omitempty"`
	ScopeFilter              *ai.ScopeFilter   `json:"scope_filter,omitempty"`
	ConfidenceThreshold      *int              `json:"confidence_threshold,omitempty"`
	BlastRadiusMaxDevices    *int              `json:"blast_radius_max_devices,omitempty"`
	BlastRadiusWindowMinutes *int              `json:"blast_radius_window_minutes,omitempty"`
	KillSwitch               *bool             `json:"kill_switch,omitempty"`
}

func aiPatchCapability(c *fiber.Ctx) error {
	tid := targetTenant(c)
	role, _ := c.Locals("user_role").(string)
	name := c.Params("name")
	cap, ok := ai.Lookup(name)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "capability not registered"})
	}
	var p capabilityPatch
	if err := c.BodyParser(&p); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}

	// Promotion past suggest is super_admin only.
	if p.Rung != nil {
		newRung := ai.Rung(*p.Rung)
		switch newRung {
		case ai.RungShadow, ai.RungSuggest, ai.RungActLow, ai.RungActPolicy, ai.RungAutonomous:
		default:
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rung"})
		}
		if (newRung == ai.RungActLow || newRung == ai.RungActPolicy || newRung == ai.RungAutonomous) && !auth.IsSuperAdmin(role) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "only super_admin may promote past 'suggest'"})
		}
	}

	// Refuse to enable a capability whose dependencies aren't met.
	if p.Enabled != nil && *p.Enabled {
		unmet, _ := ai.CheckDependencies(name)
		if len(unmet) > 0 {
			return c.Status(fiber.StatusFailedDependency).JSON(fiber.Map{"error": "unmet capability dependencies", "unmet": unmet})
		}
	}

	now := time.Now().Unix()
	// Upsert. Default values for new rows match the capability's defaults so a
	// fresh PATCH from the dashboard doesn't accidentally widen scope.
	scopeJSON := ""
	scope := cap.DefaultScope
	if p.ScopeFilter != nil {
		scope = *p.ScopeFilter
	}
	if b, err := ai.ScopeFilterJSON(scope); err == nil {
		scopeJSON = string(b)
	}

	// Read existing row; if present, merge fields.
	id := ""
	var existing capabilityPatch
	var (
		existingEnabled, existingConf, existingBlastMax, existingBlastWin, existingKill int
		existingRung                                                                    string
		existingScopeRaw                                                                sql.NullString
	)
	err := db.DB.QueryRow(`
		SELECT id, COALESCE(enabled,0), COALESCE(rung,'shadow'),
		       scope_filter, COALESCE(confidence_threshold,0),
		       COALESCE(blast_radius_max_devices,0),
		       COALESCE(blast_radius_window_minutes,5),
		       COALESCE(kill_switch,0)
		  FROM ai_capability_tenant_config
		 WHERE tenant_id = ? AND capability_id = ?`,
		tid, name,
	).Scan(&id, &existingEnabled, &existingRung, &existingScopeRaw, &existingConf, &existingBlastMax, &existingBlastWin, &existingKill)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "load failed"})
	}
	if id == "" {
		id = uuid.New().String()
	}
	enabled := existingEnabled
	if p.Enabled != nil {
		enabled = boolToInt(*p.Enabled)
	}
	rung := existingRung
	if p.Rung != nil {
		rung = *p.Rung
	}
	conf := existingConf
	if p.ConfidenceThreshold != nil {
		conf = *p.ConfidenceThreshold
	}
	blastMax := existingBlastMax
	if p.BlastRadiusMaxDevices != nil {
		blastMax = *p.BlastRadiusMaxDevices
	}
	blastWin := existingBlastWin
	if p.BlastRadiusWindowMinutes != nil {
		blastWin = *p.BlastRadiusWindowMinutes
	}
	kill := existingKill
	if p.KillSwitch != nil {
		kill = boolToInt(*p.KillSwitch)
	}
	if p.ScopeFilter == nil && existingScopeRaw.Valid {
		scopeJSON = existingScopeRaw.String
	}
	_ = existing
	// Promotion criteria stays default for now (Stage 0 doesn't expose them via
	// the API; Stage 1 will surface them when capability metrics ship).
	promJSON, _ := json.Marshal(cap.DefaultPromotion)

	_, err = db.DB.Exec(`
		INSERT INTO ai_capability_tenant_config (
			id, tenant_id, capability_id, enabled, rung, scope_filter,
			confidence_threshold, blast_radius_max_devices, blast_radius_window_minutes,
			promotion_criteria, kill_switch, last_promoted_at, last_demoted_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, capability_id) DO UPDATE
		   SET enabled=EXCLUDED.enabled, rung=EXCLUDED.rung,
		       scope_filter=EXCLUDED.scope_filter,
		       confidence_threshold=EXCLUDED.confidence_threshold,
		       blast_radius_max_devices=EXCLUDED.blast_radius_max_devices,
		       blast_radius_window_minutes=EXCLUDED.blast_radius_window_minutes,
		       kill_switch=EXCLUDED.kill_switch,
		       updated_at=EXCLUDED.updated_at`,
		id, tid, name, enabled, rung, nullableStr(scopeJSON),
		conf, blastMax, blastWin, string(promJSON), kill, nil, nil, now,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "upsert failed"})
	}

	// Mirror the per-capability kill switch into the kill-switch cache so the
	// chokepoint sees changes without a poll wait.
	if p.KillSwitch != nil {
		uid, _ := c.Locals("user_id").(string)
		_ = ai.SetKill("tenant:"+tid+":capability:"+name, *p.KillSwitch, "set via dashboard", uid)
	}

	uid, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tid, uid, "ai.capability.update", "ai_capability", name,
		fmt.Sprintf("enabled=%v rung=%s", enabled == 1, rung), c.IP())
	return c.JSON(fiber.Map{"message": "updated"})
}

// ── Audit log ─────────────────────────────────────────────────────────────

func aiListRuns(c *fiber.Ctx) error {
	tid := targetTenant(c)
	limit, _ := strconv.Atoi(c.Query("limit", "100"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	capFilter := c.Query("capability_id")
	q := `SELECT id, capability_id, run_type, model_name, COALESCE(model_version,''),
	             COALESCE(model_trust_level,''), prompt_token_count, output_token_count,
	             cost_usd_micros, latency_ms, rung_at_call,
	             COALESCE(outcome,''), rollback_attempted, rollback_succeeded, created_at
	      FROM ai_runs WHERE tenant_id = ?`
	args := []any{tid}
	if capFilter != "" {
		q += ` AND capability_id = ?`
		args = append(args, capFilter)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.DB.Query(q, args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "list failed"})
	}
	defer rows.Close()
	out := []fiber.Map{}
	for rows.Next() {
		var (
			id, capID, runType, model, version, trust, rung, outcome string
			ptok, otok, latency, rbAtt, rbOk                         int
			cost                                                     int64
			created                                                  int64
		)
		if err := rows.Scan(&id, &capID, &runType, &model, &version, &trust,
			&ptok, &otok, &cost, &latency, &rung, &outcome, &rbAtt, &rbOk, &created); err != nil {
			continue
		}
		out = append(out, fiber.Map{
			"id": id, "capability_id": capID, "run_type": runType,
			"model_name": model, "model_version": version, "model_trust_level": trust,
			"prompt_tokens": ptok, "output_tokens": otok,
			"cost_usd_micros": cost, "latency_ms": latency,
			"rung_at_call": rung, "outcome": outcome,
			"rollback_attempted": rbAtt == 1, "rollback_succeeded": rbOk == 1,
			"created_at": created,
		})
	}
	return c.JSON(fiber.Map{"runs": out, "limit": limit, "offset": offset})
}

// ── Kill switches ─────────────────────────────────────────────────────────

func aiListKill(c *fiber.Ctx) error {
	role, _ := c.Locals("user_role").(string)
	rows, err := db.DB.Query(`SELECT scope, enabled, COALESCE(reason,''), COALESCE(set_by_user_id,''), set_at FROM ai_kill_switches ORDER BY scope`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "list failed"})
	}
	defer rows.Close()
	out := []fiber.Map{}
	tid := callerTenantID(c)
	for rows.Next() {
		var (
			scope, reason, setBy string
			enabled              int
			setAt                int64
		)
		if err := rows.Scan(&scope, &enabled, &reason, &setBy, &setAt); err != nil {
			continue
		}
		// Tenant_admin only sees their own scope + global. Super_admin sees everything.
		if !auth.IsSuperAdmin(role) {
			if scope != "global" && !startsWith(scope, "tenant:"+tid) {
				continue
			}
		}
		out = append(out, fiber.Map{
			"scope": scope, "enabled": enabled == 1, "reason": reason,
			"set_by_user_id": setBy, "set_at": setAt,
		})
	}
	return c.JSON(fiber.Map{"kill_switches": out})
}

type killReq struct {
	Scope  string `json:"scope"`
	Killed bool   `json:"killed"`
	Reason string `json:"reason"`
}

func aiSetKill(c *fiber.Ctx) error {
	role, _ := c.Locals("user_role").(string)
	tid := callerTenantID(c)
	var p killReq
	if err := c.BodyParser(&p); err != nil || p.Scope == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "scope required"})
	}
	// Authorisation:
	// - global / capability:* — super_admin only
	// - tenant:<own>:* — tenant_admin or super_admin
	// - tenant:<other>:* — super_admin only
	switch {
	case p.Scope == "global":
		if !auth.IsSuperAdmin(role) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "global kill switch is super_admin only"})
		}
	case startsWith(p.Scope, "capability:"):
		if !auth.IsSuperAdmin(role) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "global capability kill switch is super_admin only"})
		}
	case startsWith(p.Scope, "tenant:"):
		if !auth.IsSuperAdmin(role) && !startsWith(p.Scope, "tenant:"+tid) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "cannot flip kill switch for another tenant"})
		}
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "scope must be 'global' | 'tenant:<id>' | 'capability:<id>' | 'tenant:<id>:capability:<id>'"})
	}
	uid, _ := c.Locals("user_id").(string)
	if err := ai.SetKill(p.Scope, p.Killed, p.Reason, uid); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	events.AuditLogTenant(tid, uid, "ai.killswitch.set", "ai_kill_switch", p.Scope,
		fmt.Sprintf("killed=%v reason=%q", p.Killed, p.Reason), c.IP())
	return c.JSON(fiber.Map{"message": "set"})
}

// ── helpers ───────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}

func knownKind(k string) bool {
	for _, known := range ai.KnownKinds() {
		if known == k {
			return true
		}
	}
	return false
}

func validTaskType(t string) bool {
	switch ai.TaskType(t) {
	case ai.TaskClassify, ai.TaskReason, ai.TaskSummarize, ai.TaskEmbed, ai.TaskGenerate:
		return true
	}
	return false
}

func providerOwned(id, tenantID string) bool {
	var n int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM ai_providers WHERE id = ? AND tenant_id = ?`, id, tenantID).Scan(&n)
	return n > 0
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
