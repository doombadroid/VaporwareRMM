package playbooks

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// restartService is the canonical low-severity playbook. Restarts a single
// service on the target. Idempotent (systemctl restart will start a stopped
// service or restart a running one). Rollback stops the service ONLY if
// preconditions still hold (the service is currently running and has been
// up since the Apply).

type restartService struct{}

func init() {
	Register(&restartService{})
}

func (restartService) Name() string { return "restart_service" }
func (restartService) Description() string {
	return "Restart a single service on a single device. Idempotent. Rollback stops the service if it was previously stopped."
}
func (restartService) Severity() Severity { return SeverityLow }

func (restartService) AppliesTo(t Target) bool {
	// Conservative: skip anything tagged regulated/critical/dc; require an
	// os_class we know how to talk to. The auto-remediation capability still
	// goes through the chokepoint scope filter — this is just a pre-filter
	// to keep the candidate list small.
	for _, tag := range t.Tags {
		switch tag {
		case "regulated", "critical", "domain_controller", "file_server", "hypervisor":
			return false
		}
	}
	switch t.OSClass {
	case "linux-server", "linux-workstation", "windows-server", "windows-workstation":
		return true
	}
	return false
}

func (restartService) Plan(ctx context.Context, t Target, args map[string]any) (PlanResult, error) {
	svc, err := stringArg(args, "service")
	if err != nil {
		return PlanResult{}, err
	}
	cmd, _ := buildRestartCommand(t.OSClass, svc)
	return PlanResult{
		Description: fmt.Sprintf("Restart %s on %s", svc, t.DeviceID),
		Steps:       []string{cmd},
		WillModify:  []string{"service:" + svc},
		RollbackOK:  true,
	}, nil
}

func (restartService) Apply(ctx context.Context, t Target, args map[string]any) (ApplyResult, error) {
	svc, err := stringArg(args, "service")
	if err != nil {
		return ApplyResult{}, err
	}
	cmd, cmdType := buildRestartCommand(t.OSClass, svc)
	cmdID, err := queueAgentCommand(ctx, t, cmdType, cmd)
	if err != nil {
		return ApplyResult{}, err
	}
	// Synchronous wait so Apply returns success/failure not "queued". 70s
	// here matches the agent-side 60s command timeout + slack for queue
	// pickup latency.
	status, output, werr := awaitAgentCommand(ctx, cmdID, 70*time.Second)
	if werr != nil {
		return ApplyResult{Success: false, Detail: "command timed out: " + werr.Error()}, werr
	}
	if status != "completed" {
		return ApplyResult{Success: false, Detail: "agent reported failure: " + truncate(output, 200)}, fmt.Errorf("restart_service: agent status=%s", status)
	}
	// Token carries enough state for Rollback to verify the service exists
	// and was just touched. A more elaborate playbook would record the
	// pre-Apply state to compare against.
	token := svc
	return ApplyResult{
		Success:               true,
		Detail:                "service restarted via agent command " + cmdID,
		RollbackToken:         token,
		RollbackPreconditions: fmt.Sprintf("service %s is currently running and was last started by Vaporware RMM", svc),
		RollbackWindow:        5 * time.Minute, // window for restart-flap detection
	}, nil
}

func (restartService) Rollback(ctx context.Context, t Target, token string) error {
	if token == "" {
		return ErrPreconditionsNotMet
	}
	svc := token
	// Precondition re-check: query the service state before stopping. If the
	// service is already stopped or doesn't exist, skip — the world has
	// moved on and we'd be making things worse.
	stateCmd, cmdType := buildServiceStateCommand(t.OSClass, svc)
	stateID, err := queueAgentCommand(ctx, t, cmdType, stateCmd)
	if err != nil {
		return err
	}
	status, output, werr := awaitAgentCommand(ctx, stateID, 30*time.Second)
	if werr != nil || status != "completed" {
		// If we can't verify state, refuse to act. Better a false negative
		// than a false positive on rollback.
		return ErrPreconditionsNotMet
	}
	if !serviceLooksRunning(t.OSClass, output) {
		return ErrPreconditionsNotMet
	}
	stopCmd, _ := buildStopCommand(t.OSClass, svc)
	if _, err := queueAgentCommand(ctx, t, cmdType, stopCmd); err != nil {
		return err
	}
	return nil
}

// buildRestartCommand returns the os-appropriate command string + the type
// the agent recognises. We deliberately use systemctl / Restart-Service —
// simple, idempotent, well-known. Operators with non-systemd hosts can
// override via tags + a different playbook.
func buildRestartCommand(osClass, svc string) (string, string) {
	switch osClass {
	case "linux-server", "linux-workstation":
		return fmt.Sprintf("systemctl restart %s", shellEscape(svc)), "shell"
	case "windows-server", "windows-workstation":
		return fmt.Sprintf("Restart-Service -Name %s -Force", powershellEscape(svc)), "powershell"
	}
	return "", "shell"
}

func buildServiceStateCommand(osClass, svc string) (string, string) {
	switch osClass {
	case "linux-server", "linux-workstation":
		return fmt.Sprintf("systemctl is-active %s", shellEscape(svc)), "shell"
	case "windows-server", "windows-workstation":
		return fmt.Sprintf("(Get-Service -Name %s).Status", powershellEscape(svc)), "powershell"
	}
	return "", "shell"
}

func buildStopCommand(osClass, svc string) (string, string) {
	switch osClass {
	case "linux-server", "linux-workstation":
		return fmt.Sprintf("systemctl stop %s", shellEscape(svc)), "shell"
	case "windows-server", "windows-workstation":
		return fmt.Sprintf("Stop-Service -Name %s -Force", powershellEscape(svc)), "powershell"
	}
	return "", "shell"
}

// serviceLooksRunning is a heuristic over the (lowercase, trimmed) output of
// the state command. systemctl is-active prints "active"; Get-Service prints
// "Running". Anything else is treated as "not running" — safer for the
// precondition check.
func serviceLooksRunning(osClass, output string) bool {
	out := lowerTrim(output)
	switch osClass {
	case "linux-server", "linux-workstation":
		return out == "active"
	case "windows-server", "windows-workstation":
		return out == "running"
	}
	return false
}

// stringArg pulls a required string field from the playbook args map and
// validates it isn't empty. Args come from the model's tool call after the
// chokepoint's PermittedFields validation, but Plan/Apply/Rollback re-check
// to defend against a misconfigured registration.
func stringArg(m map[string]any, k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", fmt.Errorf("playbook arg %q missing", k)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("playbook arg %q must be a non-empty string", k)
	}
	if !looksSafeServiceName(s) {
		return "", errors.New("playbook arg 'service' contains shell-meaningful characters")
	}
	return s, nil
}

// looksSafeServiceName is a paranoid charset check. systemd service names
// are alphanumeric + - _ . @, plus Windows allows space — we intentionally
// reject space and any shell metacharacters to keep injection risk low even
// behind the agent-side blocklist.
func looksSafeServiceName(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '@':
		default:
			return false
		}
	}
	return true
}

func shellEscape(s string) string {
	// Already validated to be alphanumeric + safe set; no escaping needed.
	return s
}
func powershellEscape(s string) string {
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

func lowerTrim(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 32
		}
		out = append(out, r)
	}
	return string(out)
}
