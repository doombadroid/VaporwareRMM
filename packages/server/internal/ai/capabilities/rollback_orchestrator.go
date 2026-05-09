package capabilities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/playbooks"
	"vaporrmm/server/internal/db"

	"github.com/google/uuid"
)

// The rollback orchestrator turns auto_remediate's "fire and forget" Apply
// into a self-correcting action.
//
// Stage 4 moves the probe queue from an in-process slice (lost on restart)
// to a persistent ai_rollback_probes table. A multi-node deployment can
// process probes from any node; the SELECT FOR UPDATE on row pickup
// prevents two nodes claiming the same probe.
//
// Flow:
//  1. auto_remediate.Apply succeeds, returns RollbackToken + RollbackWindow.
//  2. Capability calls registerRollbackProbe() — INSERT row with
//     status='pending', run_at = now + window.
//  3. Orchestrator goroutine wakes every 30s, claims pending probes whose
//     run_at has passed via SELECT FOR UPDATE SKIP LOCKED, processes each
//     in its own panic-guarded closure.
//  4. Healthy outcome → mark probe done + auto-label originating ai_runs.
//     Regression → call playbook Rollback under precondition guard.
//  5. Probe retains its row with the final outcome so operators can audit
//     the rollback history. A daily prune job (Stage 4 follow-up) removes
//     done rows older than 90 days.

type rollbackProbe struct {
	ID                 string
	TenantID           string
	DeviceID           string
	CapabilityID       string
	Playbook           string
	Token              string
	AlertSignature     string
	Preconditions      string
	RunAt              int64
	RollbackWindowEnds int64
}

// registerRollbackProbe persists a probe to the database. Idempotent on
// (token, alert_signature) — a duplicate registration (capability fired
// twice in quick succession) is harmless and we don't double-process.
func registerRollbackProbe(p rollbackProbe) {
	if err := ai.SupportedDialect(); err != nil {
		// Stage 0 gate already ensures we only reach here on Postgres,
		// but defensively skip if not — losing a probe is preferable to
		// crashing the capability that called us.
		slog.Warn("registerRollbackProbe: AI not supported on this dialect; skipping", "error", err)
		return
	}
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.CapabilityID == "" {
		p.CapabilityID = autoRemediateCapName
	}
	now := time.Now().Unix()
	// ON CONFLICT no-op via the dedup unique index from migration 032 —
	// a duplicate (tenant, token, alert_signature) is harmless and we
	// don't want to schedule the same regression check twice.
	_, err := db.DB.Exec(`
		INSERT INTO ai_rollback_probes (
			id, tenant_id, device_id, capability_id, playbook, token,
			alert_signature, preconditions, run_at, rollback_window_ends,
			status, attempts, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)
		ON CONFLICT (tenant_id, token, (COALESCE(alert_signature, '__null__'))) DO NOTHING`,
		p.ID, p.TenantID, p.DeviceID, p.CapabilityID, p.Playbook, p.Token,
		nullableStr(p.AlertSignature), nullableStr(p.Preconditions),
		p.RunAt, p.RollbackWindowEnds, now, now,
	)
	if err != nil {
		slog.Warn("registerRollbackProbe insert failed", "tenant_id", p.TenantID, "playbook", p.Playbook, "error", err)
	}
}

// StartRollbackOrchestrator boots the background goroutine. main.go calls
// this once at server startup. The orchestrator polls every 30s.
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
	if err := ai.SupportedDialect(); err != nil {
		return // AI features off; nothing to do
	}
	now := time.Now().Unix()

	// Claim due probes via SELECT FOR UPDATE SKIP LOCKED — multi-node safe;
	// each node grabs a disjoint set without blocking the others.
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		slog.Warn("rollback orchestrator: tx begin failed", "error", err)
		return
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, tenant_id, device_id, capability_id, playbook, token,
		       COALESCE(alert_signature,''), COALESCE(preconditions,''),
		       run_at, rollback_window_ends
		  FROM ai_rollback_probes
		 WHERE status = 'pending' AND run_at <= ?
		 ORDER BY run_at ASC
		 LIMIT 50
		 FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		_ = tx.Rollback()
		slog.Warn("rollback orchestrator: claim query failed", "error", err)
		return
	}
	due := []rollbackProbe{}
	for rows.Next() {
		var p rollbackProbe
		if err := rows.Scan(&p.ID, &p.TenantID, &p.DeviceID, &p.CapabilityID,
			&p.Playbook, &p.Token, &p.AlertSignature, &p.Preconditions,
			&p.RunAt, &p.RollbackWindowEnds); err != nil {
			continue
		}
		due = append(due, p)
	}
	rows.Close()
	// Mark them in-progress inside the same tx so nobody else grabs them
	// while we're processing. Failure to update is fatal for this pass.
	for _, p := range due {
		if _, err := tx.ExecContext(ctx, `UPDATE ai_rollback_probes SET status = 'in_progress', attempts = attempts + 1, updated_at = ? WHERE id = ?`, now, p.ID); err != nil {
			slog.Warn("rollback orchestrator: mark in_progress failed", "id", p.ID, "error", err)
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Warn("rollback orchestrator: tx commit failed", "error", err)
		return
	}

	for _, probe := range due {
		// Per-probe panic guard. A buggy Rollback in any single playbook must
		// not silently strand every subsequent probe.
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("rollback orchestrator: per-probe panic",
						"playbook", probe.Playbook, "device_id", probe.DeviceID, "panic", r)
					finishProbe(ctx, probe, "incorrect", fmt.Sprintf("rollback panicked: %v", r))
				}
			}()
			processProbe(ctx, probe)
		}()
	}
}

// processProbe runs a single probe's regression check + rollback.
func processProbe(ctx context.Context, probe rollbackProbe) {
	// 5 min lookback before run_at — if alert_dedup saw the signature in
	// the last 5 minutes BEFORE the rollback window elapsed, it counts as
	// regression.
	regressed := alertSignatureSeenSince(ctx, probe.TenantID, probe.AlertSignature, probe.RunAt-int64((5*time.Minute).Seconds()))
	if !regressed {
		// Healthy outcome. Auto-label the originating run.
		markRunOutcomeAuto(ctx, probe, "correct", "no regression in window")
		finishProbe(ctx, probe, "correct", "no regression in window")
		return
	}
	pbk, ok := playbooks.Lookup(probe.Playbook)
	if !ok {
		slog.Warn("rollback orchestrator: unknown playbook", "playbook", probe.Playbook)
		finishProbe(ctx, probe, "unclear", "playbook no longer registered")
		return
	}
	target := playbooks.Target{TenantID: probe.TenantID, DeviceID: probe.DeviceID}
	_ = db.DB.QueryRow(`SELECT COALESCE(os_class,'unknown') FROM devices WHERE id = ? AND tenant_id = ?`,
		probe.DeviceID, probe.TenantID).Scan(&target.OSClass)
	err := pbk.Rollback(ctx, target, probe.Token)
	switch {
	case errors.Is(err, playbooks.ErrPreconditionsNotMet):
		markRunOutcomeAuto(ctx, probe, "unclear", "rollback skipped — preconditions no longer met")
		finishProbe(ctx, probe, "unclear", "preconditions not met")
	case err != nil:
		slog.Warn("rollback orchestrator: Rollback failed", "playbook", probe.Playbook, "device_id", probe.DeviceID, "error", err)
		markRunOutcomeAuto(ctx, probe, "incorrect", "rollback failed: "+err.Error())
		finishProbe(ctx, probe, "incorrect", "rollback failed: "+err.Error())
	default:
		markRunOutcomeAuto(ctx, probe, "incorrect", "alert regressed; rollback succeeded")
		finishProbe(ctx, probe, "incorrect", "alert regressed; rollback succeeded")
	}
}

// finishProbe marks the probe row with its terminal outcome. We deliberately
// keep the row (don't DELETE) so operators can audit the history; a daily
// prune job (Stage 4 follow-up) removes done rows older than 90 days.
//
// outcome_reason is a separate column — the original preconditions text is
// audit data the operator might want to read unchanged; appending the
// outcome there would conflate two different things.
func finishProbe(ctx context.Context, probe rollbackProbe, outcome, reason string) {
	now := time.Now().Unix()
	_, _ = db.DB.Exec(`
		UPDATE ai_rollback_probes
		   SET status = 'done', outcome = ?, outcome_reason = ?,
		       outcome_set_at = ?, updated_at = ?
		 WHERE id = ?`,
		outcome, ai.SanitizeFreeText(reason), now, now, probe.ID,
	)
}

// alertSignatureSeenSince checks whether alert_dedup recorded the same
// signature in its action_taken JSON within the window. JSON containment
// (@>) avoids the false-positive risk of LIKE '%sig%' matching a hex
// substring inside a different field.
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
// remediation run. Subquery includes capability_id so a future capability
// writing to ai_runs for the same device cannot have its row mis-updated.
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
		probe.TenantID, probe.CapabilityID,
		probe.DeviceID, "%rollback_token="+probe.Token+"%",
	)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
