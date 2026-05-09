package playbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"vaporrmm/server/internal/db"
)

// agentbridge is the playbook framework's interface to the existing
// agent-command queue (the table the device-command HTTP handler writes to).
// We deliberately do NOT call the agent directly — playbook execution flows
// through the same path a tech's manual command flows through, which gets
// us the existing dangerousPatterns blocklist + audit trail + agent-side
// timeout for free.
//
// queueAgentCommand returns the command_id so the orchestrator can poll
// status / retrieve output later. Polling is the orchestrator's job; this
// helper is fire-and-record.

type agentCommand struct {
	ID         string
	DeviceID   string
	Type       string
	Payload    string // JSON
	TenantID   string
}

// queueAgentCommand inserts into device_commands. The command type is one of
// the existing types the agent already understands (shell|powershell|update|
// custom). For Stage 3 playbooks we use "shell" or "powershell" depending
// on os_class.
//
// Validates the device exists in the target tenant before queueing. A typo
// from the model or operator would otherwise leave a row sitting pending
// against a non-existent device — silent data junk that confuses the audit
// trail.
func queueAgentCommand(ctx context.Context, target Target, cmdType, body string) (string, error) {
	if body == "" {
		return "", fmt.Errorf("queueAgentCommand: empty command body (likely an unsupported os_class for this playbook)")
	}
	if target.DeviceID == "" || target.TenantID == "" {
		return "", fmt.Errorf("queueAgentCommand: target missing device_id or tenant_id")
	}
	var exists int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE id = ? AND tenant_id = ?`,
		target.DeviceID, target.TenantID).Scan(&exists); err != nil || exists == 0 {
		return "", fmt.Errorf("queueAgentCommand: device %s not in tenant %s", target.DeviceID, target.TenantID)
	}
	id := uuid.New().String()
	now := time.Now().Unix()
	payload, _ := json.Marshal(map[string]string{"command": body})
	_, err := db.DB.Exec(`
		INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, target.DeviceID, cmdType, string(payload), "pending", now, target.TenantID,
	)
	if err != nil {
		return "", fmt.Errorf("queueAgentCommand: %w", err)
	}
	return id, nil
}

// awaitAgentCommand polls the command's status until completed/failed or
// timeout. Used by Apply implementations that need to know the outcome
// before declaring success. Most playbooks should be fire-and-forget —
// rolling back later if regression is detected — but a few (free_disk_space)
// need the actual freed bytes to decide whether they actually fixed
// anything.
//
// We use exponential backoff (500ms → 1s → 2s, capped at 4s) instead of
// flat 500ms polling. At 100 concurrent applies a flat poll would issue
// 14k DB queries over a 70s timeout; backoff cuts that to ~600 queries
// while keeping the first-second latency reasonable for fast commands.
func awaitAgentCommand(ctx context.Context, commandID string, timeout time.Duration) (status, output string, err error) {
	deadline := time.Now().Add(timeout)
	wait := 500 * time.Millisecond
	const maxWait = 4 * time.Second
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}
		err = db.DB.QueryRow(`SELECT status, COALESCE(output,'') FROM device_commands WHERE id = ?`, commandID).Scan(&status, &output)
		if err != nil {
			return "", "", err
		}
		if status == "completed" || status == "failed" {
			return status, output, nil
		}
		time.Sleep(wait)
		if wait < maxWait {
			wait *= 2
			if wait > maxWait {
				wait = maxWait
			}
		}
	}
	return status, output, fmt.Errorf("awaitAgentCommand: timeout after %v (last status=%s)", timeout, status)
}
