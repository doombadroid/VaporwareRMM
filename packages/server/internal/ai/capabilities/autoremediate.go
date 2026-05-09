package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/playbooks"
	"vaporrmm/server/internal/ai/prompt"
	"vaporrmm/server/internal/db"
)

// auto_remediate is the action-tier capability. Given an alert (or cluster of
// alerts) the model decides whether a registered playbook should run,
// returns the {playbook, args} choice as a structured tool call, and the
// chokepoint executes the playbook against the agent-command pipeline.
//
// Default rung: shadow. The capability records its decision to ai_runs
// without touching anything. Promotion to act_low requires super_admin and
// a scope filter that excludes regulated devices, domain controllers, file
// servers, and hypervisors. Promotion past act_low is intentionally hard.
//
// The orchestrator (RegisterRollbackProbe below) registers a deferred check
// for each successful Apply: if the alert that triggered the capability
// re-fires within the playbook's RollbackWindow, we attempt Rollback.

const autoRemediateCapName = "auto_remediate"

func init() {
	ai.Register(ai.Capability{
		Name:              autoRemediateCapName,
		Category:          ai.CategoryAction,
		Description:       "Match an alert to a registered remediation playbook and execute. Default rung shadow; act_low requires super_admin promotion + scope filter excluding regulated/critical/DC/file-server/hypervisor.",
		Stage:             3,
		PreferredTaskType: ai.TaskReason,
		RequiredCaps:      ai.Capabilities{ToolCalling: true, JSONMode: true},
		DependsOn:         []string{"device_classification"},
		// Conservative defaults — operator must consciously widen.
		DefaultPromotion: ai.PromotionCriteria{
			PrecisionMin:         0.95,
			FalsePositiveRateMax: 0.02,
			LabelingRateMin:      0.30,
			WeeksCleanRequired:   4,
			MinSamples:           50,
		},
	})
}

// RemediationDecision is the model's structured output. The chokepoint reads
// this from action_taken on the audit row.
type RemediationDecision struct {
	Playbook      string         `json:"playbook"`
	Args          map[string]any `json:"args"`
	Reason        string         `json:"reason"`
	Decline       bool           `json:"decline"` // true = the model refused to remediate
	// RollbackToken is set after a successful Apply so the rollback
	// orchestrator can find the originating run row by token-substring
	// search on action_taken. Empty for shadow runs and decline-paths.
	RollbackToken string `json:"rollback_token,omitempty"`
}

// RemediationResult bundles decision + execution outcome (when run at
// act_low+) so the audit log + dashboard see one row per attempt.
type RemediationResult struct {
	Decision        RemediationDecision `json:"decision"`
	Applied         bool                `json:"applied"`
	RollbackToken   string              `json:"rollback_token,omitempty"`
	OutcomeAt       int64               `json:"outcome_check_at,omitempty"`
}

// Remediate runs the capability for a given alert context. The chokepoint's
// per-capability rung decides whether we apply the playbook or just log the
// decision. Callers (typically the alert pipeline after a cluster crosses
// an occurrence threshold) invoke this asynchronously.
func Remediate(ctx context.Context, alert AlertContext) (RemediationResult, error) {
	if alert.TenantID == "" {
		return RemediationResult{}, errors.New("auto_remediate: empty TenantID")
	}

	// Resolve the target's os_class + tags so we can prefilter playbooks.
	target := resolveTarget(ctx, alert)
	candidates := playbooks.CandidatesFor(target)
	if len(candidates) == 0 {
		// Nothing to do — log to ai_runs as a no-op decision so metrics
		// reflect that the capability fired and saw no path forward.
		out, err := logNoOp(ctx, alert, "no applicable playbook")
		_ = out
		return RemediationResult{}, err
	}

	// Build the model's choices. We expose only Name + Description + the
	// pre-filtered Severity so the model has no opportunity to ask for an
	// arbitrary command.
	choices := make([]map[string]string, len(candidates))
	for i, p := range candidates {
		choices[i] = map[string]string{
			"name":        p.Name(),
			"description": p.Description(),
			"severity":    string(p.Severity()),
		}
	}
	choicesJSON, _ := json.Marshal(choices)

	pb := prompt.New(prompt.Scope{TenantID: alert.TenantID, CustomerID: alert.CustomerID}).
		SystemRules(`Decide whether one of the listed playbooks should run to remediate the alert. Strict JSON output:
{"playbook":"<name-from-list-or-empty>","args":{...},"reason":"<one-sentence>","decline":<bool>}

Rules:
- decline=true if no playbook is appropriate. Better to do nothing than to act on the wrong device.
- Choose only from the listed playbooks; do not invent commands.
- args must match the playbook's expected schema (e.g., restart_service expects {"service":"<name>"}).
- Refuse if the alert mentions a domain controller, hypervisor, file server, or anything tagged "regulated"/"critical".`).
		TrustedContext("available playbooks: " + string(choicesJSON)).
		TrustedContext(fmt.Sprintf("device: id=%s os_class=%s tags=%s", target.DeviceID, target.OSClass, strings.Join(target.Tags, ","))).
		UntrustedInput("alert_title", alert.Title).
		UntrustedInput("alert_body", alert.Body).
		TrustedContext(fmt.Sprintf("alert metadata: severity=%s source=%s",
			ai.SanitizeFreeText(alert.Severity), ai.SanitizeFreeText(alert.Source)))

	var decision RemediationDecision
	devSnap := []ai.DeviceSnapshot{{
		ID: target.DeviceID, TenantID: target.TenantID, OSClass: target.OSClass, Tags: target.Tags,
	}}

	out, err := ai.Run(ctx, ai.Input{
		TenantID:     alert.TenantID,
		CustomerID:   alert.CustomerID,
		DeviceID:     alert.DeviceID,
		CapabilityID: autoRemediateCapName,
		RunType:      ai.RunTypeChat,
		Devices:      devSnap,
		Estimate:     3_000,
	}, func(ctx context.Context, p ai.Provider, modelName string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		req, _, perr := pb.Render(modelName, 512)
		if perr != nil {
			return nil, nil, nil, perr
		}
		resp, cerr := p.Chat(ctx, req)
		if cerr != nil {
			return nil, nil, nil, cerr
		}
		_ = json.Unmarshal([]byte(resp.Content), &decision)
		// Defensive: if the model picked a playbook not in our list, treat
		// it as a decline. The chokepoint already would refuse a tool call
		// for an unregistered tool, but auto_remediate uses structured
		// output rather than tool calls so we sanity-check here.
		valid := false
		for _, c := range candidates {
			if c.Name() == decision.Playbook {
				valid = true
				break
			}
		}
		if !valid {
			decision.Decline = true
			decision.Playbook = ""
			decision.Reason = "model picked an unlisted playbook: " + decision.Reason
		}
		payload, _ := json.Marshal(decision)
		return &resp, nil, payload, nil
	})
	if err != nil {
		return RemediationResult{}, err
	}
	_ = out

	res := RemediationResult{Decision: decision}
	if decision.Decline || decision.Playbook == "" {
		return res, nil
	}

	// Read the per-tenant capability rung so we know whether to actually
	// apply or just log. Shadow + suggest do nothing here; act_low+ runs.
	rung := loadCapabilityRung(alert.TenantID, autoRemediateCapName)
	if !rungAllowsApply(rung) {
		return res, nil
	}

	// Severity-vs-rung gate. SeverityForRung returns nil when the chosen
	// playbook is permitted at the operative rung.
	pbk, _ := playbooks.Lookup(decision.Playbook)
	if pbk == nil {
		return res, nil // already handled above; defensive
	}
	if err := playbooks.SeverityForRung(pbk.Severity(), rung); err != nil {
		return res, nil
	}

	// Blast radius gate. Reserve before Apply; refusal fast-fails without
	// touching the device.
	cfg := playbooks.LoadConfig(alert.TenantID, autoRemediateCapName)
	if err := playbooks.Reserve(ctx, autoRemediateCapName, alert.TenantID, cfg); err != nil {
		return res, err
	}

	applied, err := pbk.Apply(ctx, target, decision.Args)
	if err != nil || !applied.Success {
		return res, err
	}
	res.Applied = true
	res.RollbackToken = applied.RollbackToken

	// Stamp the rollback token onto the most recent ai_runs row for this
	// (tenant, capability, device) so the rollback orchestrator can find
	// the originating decision later. Without this the orchestrator's
	// LIKE '%token%' search misses (Apply runs AFTER the chokepoint has
	// already persisted action_taken from the model's decision JSON).
	stampRollbackToken(ctx, alert.TenantID, alert.DeviceID, applied.RollbackToken)

	// Schedule the deferred regression check. The orchestrator wakes up
	// after RollbackWindow and calls Rollback if the alert is still firing.
	if applied.RollbackWindow > 0 {
		outcomeAt := time.Now().Add(applied.RollbackWindow).Unix()
		res.OutcomeAt = outcomeAt
		registerRollbackProbe(rollbackProbe{
			TenantID:           alert.TenantID,
			DeviceID:           alert.DeviceID,
			Playbook:           decision.Playbook,
			Token:              applied.RollbackToken,
			AlertSignature:     alertSignature(alert),
			Preconditions:      applied.RollbackPreconditions,
			RunAt:              outcomeAt,
			RollbackWindowEnds: outcomeAt,
		})
	}
	return res, nil
}

// stampRollbackToken appends the token to the most recent auto_remediate
// audit row for the (tenant, device) tuple. We use SQL's || string concat
// to keep the original action_taken intact + add a sentinel marker the
// orchestrator can LIKE-match. The "rollback_token=" prefix avoids
// collision with the playbook name or any other field.
func stampRollbackToken(ctx context.Context, tenantID, deviceID, token string) {
	if token == "" || tenantID == "" {
		return
	}
	_, _ = db.DB.Exec(`
		UPDATE ai_runs
		   SET action_taken = COALESCE(action_taken, '') || ?
		 WHERE id = (
		     SELECT id FROM ai_runs
		      WHERE tenant_id = ? AND capability_id = ? AND device_id = ?
		      ORDER BY created_at DESC LIMIT 1
		 )`,
		" |rollback_token="+token, tenantID, autoRemediateCapName, deviceID,
	)
}

// resolveTarget pulls the target device's os_class + tags from the database
// so the playbook layer doesn't need DB access. Missing device rows return
// a target with OSClass="unknown" — playbook AppliesTo returns false for
// that, which means the capability declines correctly.
func resolveTarget(ctx context.Context, alert AlertContext) playbooks.Target {
	t := playbooks.Target{
		TenantID: alert.TenantID, CustomerID: alert.CustomerID, DeviceID: alert.DeviceID,
	}
	if alert.DeviceID == "" {
		return t
	}
	var osClass, tagsCSV string
	_ = db.DB.QueryRow(`SELECT COALESCE(os_class,'unknown'), COALESCE(tags,'') FROM devices WHERE id = ? AND tenant_id = ?`,
		alert.DeviceID, alert.TenantID).Scan(&osClass, &tagsCSV)
	t.OSClass = osClass
	if tagsCSV != "" {
		for _, tag := range strings.Split(tagsCSV, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				t.Tags = append(t.Tags, tag)
			}
		}
	}
	return t
}

// logNoOp is the path we take when there are no candidate playbooks. We
// still go through the chokepoint so the capability's call counter ticks
// (otherwise metrics show false health — "0 calls" looks like the capability
// was never invoked).
func logNoOp(ctx context.Context, alert AlertContext, reason string) (ai.Output, error) {
	return ai.Run(ctx, ai.Input{
		TenantID:     alert.TenantID,
		CustomerID:   alert.CustomerID,
		DeviceID:     alert.DeviceID,
		CapabilityID: autoRemediateCapName,
		RunType:      ai.RunTypeChat,
		Estimate:     0,
	}, func(_ context.Context, _ ai.Provider, _ string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		dec := RemediationDecision{Decline: true, Reason: reason}
		payload, _ := json.Marshal(dec)
		return &ai.ChatResponse{
			Content: string(payload), ModelVersion: "local",
			Synthetic: true, SyntheticSource: "auto_remediate_noop",
		}, nil, payload, nil
	})
}

// loadCapabilityRung reads the per-(tenant, capability) rung. Default shadow
// when no row exists — same conservative default the chokepoint applies.
func loadCapabilityRung(tenantID, capName string) ai.Rung {
	var r string
	_ = db.DB.QueryRow(`SELECT COALESCE(rung,'shadow') FROM ai_capability_tenant_config WHERE tenant_id = ? AND capability_id = ?`,
		tenantID, capName).Scan(&r)
	if r == "" {
		return ai.RungShadow
	}
	return ai.Rung(r)
}

func rungAllowsApply(r ai.Rung) bool {
	switch r {
	case ai.RungActLow, ai.RungActPolicy, ai.RungAutonomous:
		return true
	}
	return false
}
