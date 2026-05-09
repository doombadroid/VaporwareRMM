package capabilities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/playbooks"
	"vaporrmm/server/internal/db"
)

// The rollback orchestrator is the deferred regression check that turns
// auto_remediate's "fire and forget" Apply into a self-correcting action.
//
// Flow:
//  1. auto_remediate.Apply succeeds, returns RollbackToken + RollbackWindow.
//  2. Capability calls registerRollbackProbe() — adds a probe to the
//     in-process queue with RunAt = now + window.
//  3. The orchestrator goroutine wakes up periodically, finds probes whose
//     RunAt has passed, and checks: did the alert that triggered the
//     remediation re-fire within the window?
//  4. If yes (signature seen again in alert audit) → call playbook.Rollback
//     with the precondition guard. Audit row's rollback_attempted +
//     rollback_succeeded fields are updated.
//  5. If no → mark the probe as "outcome=correct" and prompt label-rate
//     metrics tick up.
//
// The probe queue is in-process for v1. A multi-node deployment loses pending
// probes if all nodes restart simultaneously — acceptable degradation
// because act_low capabilities should have small windows (minutes) and
// operators get audit-log evidence of the missed check. Stage 2 ships
// a Postgres-backed probe table when capabilities reach act_policy.

type rollbackProbe struct {
	TenantID           string
	DeviceID           string
	Playbook           string
	Token              string
	AlertSignature     string // matches alertdedup.alertSignature for the originating alert
	Preconditions      string
	RunAt              int64 // unix
	RollbackWindowEnds int64
}

var (
	probesMu sync.Mutex
	probes   = []rollbackProbe{}
)

// registerRollbackProbe enqueues. Idempotent on (Token, AlertSignature) so
// re-registering doesn't double-schedule.
func registerRollbackProbe(p rollbackProbe) {
	probesMu.Lock()
	defer probesMu.Unlock()
	for _, existing := range probes {
		if existing.Token == p.Token && existing.AlertSignature == p.AlertSignature {
			return
		}
	}
	probes = append(probes, p)
}

// StartRollbackOrchestrator boots the background goroutine. main.go calls
// this once at server startup. The orchestrator polls every 30s — short
// enough to fire well within typical RollbackWindow (minutes), long enough
// to keep the polling overhead negligible.
func StartRollbackOrchestrator(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runRollbackPass(ctx)
			}
		}
	}()
}

func runRollbackPass(ctx context.Context) {
	now := time.Now().Unix()
	probesMu.Lock()
	due := []rollbackProbe{}
	keep := probes[:0]
	for _, p := range probes {
		if p.RunAt <= now {
			due = append(due, p)
		} else {
			keep = append(keep, p)
		}
	}
	probes = keep
	probesMu.Unlock()

	for _, probe := range due {
		// Per-probe panic guard. A buggy Rollback in any single playbook must
		// not kill the orchestrator goroutine — that would silently strand
		// every subsequent probe, leaving operators blind to whether
		// rollbacks are happening.
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("rollback orchestrator: per-probe panic",
						"playbook", probe.Playbook, "device_id", probe.DeviceID, "panic", r)
					markRunOutcomeAuto(ctx, probe, "incorrect", fmt.Sprintf("rollback panicked: %v", r))
				}
			}()
		// Was the alert seen again within the rollback window? We look at
		// ai_runs (where alert_dedup writes the signature into action_taken
		// JSON) for the same signature within the window.
		regressed := alertSignatureSeenSince(ctx, probe.TenantID, probe.AlertSignature, probe.RunAt-int64((5*time.Minute).Seconds()))
		if !regressed {
			// Healthy outcome. Mark the originating ai_runs row as
			// outcome=correct so labelling-rate metrics improve without
			// requiring a tech to manually click. Auto-labelling is a
			// limited promotion signal — techs still need to label
			// suggest-rung output explicitly — but for act_low+ outcomes
			// the regression check IS the ground truth.
			markRunOutcomeAuto(ctx, probe, "correct", "no regression in window")
			return // early-exit the per-probe closure, NOT continue (we're inside func())
		}
		// Regression detected. Attempt rollback.
		pbk, ok := playbooks.Lookup(probe.Playbook)
		if !ok {
			slog.Warn("rollback orchestrator: unknown playbook", "playbook", probe.Playbook)
			return
		}
		target := playbooks.Target{TenantID: probe.TenantID, DeviceID: probe.DeviceID}
		// Re-resolve os_class + tags so Rollback's precondition checks see
		// current state.
		_ = db.DB.QueryRow(`SELECT COALESCE(os_class,'unknown') FROM devices WHERE id = ? AND tenant_id = ?`,
			probe.DeviceID, probe.TenantID).Scan(&target.OSClass)
		err := pbk.Rollback(ctx, target, probe.Token)
		switch {
		case errors.Is(err, playbooks.ErrPreconditionsNotMet):
			markRunOutcomeAuto(ctx, probe, "unclear", "rollback skipped — preconditions no longer met")
		case err != nil:
			slog.Warn("rollback orchestrator: Rollback failed", "playbook", probe.Playbook, "device_id", probe.DeviceID, "error", err)
			markRunOutcomeAuto(ctx, probe, "incorrect", "rollback failed: "+err.Error())
		default:
			markRunOutcomeAuto(ctx, probe, "incorrect", "alert regressed; rollback succeeded")
		}
		}() // end per-probe panic-guard func
	}
}

// alertSignatureSeenSince checks whether alert_dedup recorded the same
// signature in its action_taken JSON within the window. JSON_EXTRACT on the
// "$.signature" key avoids the false-positive risk of LIKE '%sig%' matching
// the hex-string substring anywhere in the JSON (e.g., a cluster_id or
// hash field that happens to share a prefix). The cap is Postgres-only, so
// we can rely on JSON_EXTRACT semantics.
func alertSignatureSeenSince(ctx context.Context, tenantID, signature string, since int64) bool {
	if signature == "" {
		return false
	}
	var n int
	_ = db.DB.QueryRow(`
		SELECT COUNT(*) FROM ai_runs
		 WHERE tenant_id = ? AND capability_id = ? AND created_at >= ?
		   AND action_taken::jsonb @> ?::jsonb`,
		tenantID, alertDedupCapName, since,
		`{"signature":"`+signature+`"}`,
	).Scan(&n)
	return n > 0
}

// markRunOutcomeAuto records the orchestrator's verdict on the originating
// remediation run. We pick the most recent auto_remediate run for the
// (tenant, capability, device) tuple that has the rollback token stamped
// into action_taken. The capability_id filter is load-bearing — without it,
// a future capability that also writes to ai_runs for the same device
// could have its row mis-updated.
func markRunOutcomeAuto(ctx context.Context, probe rollbackProbe, outcome, reason string) {
	now := time.Now().Unix()
	_, _ = db.DB.Exec(`
		UPDATE ai_runs
		   SET outcome = ?, outcome_set_by = 'rollback_orchestrator',
		       outcome_set_at = ?,
		       rollback_attempted = ?, rollback_succeeded = ?
		 WHERE id = (
		     SELECT id FROM ai_runs
		      WHERE tenant_id = ? AND capability_id = ?
		        AND device_id = ? AND action_taken LIKE ?
		        AND outcome IS NULL
		      ORDER BY created_at DESC LIMIT 1
		 )`,
		outcome, now,
		boolToInt(outcome != "correct"),
		boolToInt(outcome == "incorrect" && reason == "alert regressed; rollback succeeded"),
		probe.TenantID, autoRemediateCapName,
		probe.DeviceID, "%rollback_token="+probe.Token+"%",
	)
	_ = ai.SanitizeFreeText(reason) // avoid lint about unused import in some builds
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
