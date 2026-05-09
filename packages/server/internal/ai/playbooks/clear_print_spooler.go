package playbooks

import (
	"context"
	"fmt"
	"time"
)

// clearPrintSpooler restarts the spooler service after deleting any stuck
// jobs. Common Windows ticket pattern. Linux equivalent (CUPS) uses
// cancel -a + restart cups.
//
// Idempotent because the underlying steps both produce a clean state:
//   - Delete-PrintJob -All on a queue with no jobs is a no-op.
//   - cancel -a on CUPS is a no-op when no jobs are queued.
//   - Restart-Service / systemctl restart on a stopped service starts it.

type clearPrintSpooler struct{}

func init() { Register(&clearPrintSpooler{}) }

func (clearPrintSpooler) Name() string        { return "clear_print_spooler" }
func (clearPrintSpooler) Description() string { return "Cancel queued print jobs + restart the print spooler service. Idempotent (no jobs = no-op delete)." }
func (clearPrintSpooler) Severity() Severity  { return SeverityLow }

func (clearPrintSpooler) AppliesTo(t Target) bool {
	for _, tag := range t.Tags {
		switch tag {
		case "regulated", "critical", "domain_controller", "file_server", "hypervisor", "print_server":
			// Print servers explicitly excluded — restarting the spooler on
			// the central server cascades job loss across the org. Print
			// servers warrant a manual playbook with operator review.
			return false
		}
	}
	switch t.OSClass {
	case "windows-server", "windows-workstation", "linux-server", "linux-workstation":
		return true
	}
	return false
}

func (clearPrintSpooler) Plan(ctx context.Context, t Target, args map[string]any) (PlanResult, error) {
	cmds := buildSpoolerCommands(t.OSClass)
	return PlanResult{
		Description: "Cancel queued print jobs + restart the print spooler",
		Steps:       cmds,
		WillModify:  []string{"service:spooler", "queued print jobs"},
		RollbackOK:  false, // cancelled jobs are not recoverable; rollback only restarts the service
	}, nil
}

func (clearPrintSpooler) Apply(ctx context.Context, t Target, args map[string]any) (ApplyResult, error) {
	cmds := buildSpoolerCommands(t.OSClass)
	if len(cmds) == 0 {
		return ApplyResult{Success: false, Detail: "unsupported os_class"}, fmt.Errorf("clear_print_spooler: unsupported os_class %q", t.OSClass)
	}
	cmdType := "shell"
	if isWindows(t.OSClass) {
		cmdType = "powershell"
	}
	// Concatenate steps into one agent command. Bash uses && so a failure
	// halts the chain; PowerShell uses `;` (statements) — both are valid
	// inside the single command body the agent executes via shell -c.
	body := joinCommands(cmds, t.OSClass)
	cmdID, err := queueAgentCommand(ctx, t, cmdType, body)
	if err != nil {
		return ApplyResult{}, err
	}
	status, output, werr := awaitAgentCommand(ctx, cmdID, 70*time.Second)
	if werr != nil || status != "completed" {
		return ApplyResult{Success: false, Detail: "agent reported failure: " + truncate(output, 200)}, fmt.Errorf("clear_print_spooler: status=%s err=%v", status, werr)
	}
	return ApplyResult{
		Success:               true,
		Detail:                "spooler cleared via agent command " + cmdID,
		RollbackToken:         "",
		RollbackPreconditions: "deleted print jobs are not recoverable",
		// RollbackWindow=0 tells the orchestrator NOT to schedule a probe.
		// Spooler clear is non-rollbackable in any meaningful sense — the
		// jobs are gone — so registering a probe just creates audit clutter
		// (every probe ends in outcome=unclear).
		RollbackWindow: 0,
	}, nil
}

func (clearPrintSpooler) Rollback(ctx context.Context, t Target, token string) error {
	// "Rollback" for spooler clear is necessarily limited — cancelled jobs
	// don't come back. We can only restart the service if it's running and
	// the operator wants a clean re-init. We treat this as essentially a
	// no-op: the precondition check will almost always return
	// ErrPreconditionsNotMet because Apply already restarted it cleanly.
	stateCmd, cmdType := buildServiceStateCommand(t.OSClass, "spooler")
	if isLinux(t.OSClass) {
		// CUPS uses systemctl unit "cups", not "spooler"
		stateCmd, cmdType = buildServiceStateCommand(t.OSClass, "cups")
	}
	id, err := queueAgentCommand(ctx, t, cmdType, stateCmd)
	if err != nil {
		return err
	}
	status, output, werr := awaitAgentCommand(ctx, id, 30*time.Second)
	if werr != nil || status != "completed" {
		return ErrPreconditionsNotMet
	}
	if !serviceLooksRunning(t.OSClass, output) {
		return ErrPreconditionsNotMet
	}
	// Service is running cleanly — nothing to revert. The orchestrator
	// records this as outcome=unclear (preconditions not met for a true
	// rollback), which is the honest answer.
	return ErrPreconditionsNotMet
}

func buildSpoolerCommands(osClass string) []string {
	switch osClass {
	case "windows-server", "windows-workstation":
		return []string{
			"Get-Printer | ForEach-Object { Get-PrintJob -PrinterName $_.Name | Remove-PrintJob -ErrorAction SilentlyContinue }",
			"Restart-Service -Name Spooler -Force",
		}
	case "linux-server", "linux-workstation":
		return []string{
			"cancel -a -u root || true",
			"systemctl restart cups",
		}
	}
	return nil
}

func joinCommands(cmds []string, osClass string) string {
	if isWindows(osClass) {
		return joinWith(cmds, "; ")
	}
	return joinWith(cmds, " && ")
}

func joinWith(cmds []string, sep string) string {
	out := ""
	for i, c := range cmds {
		if i > 0 {
			out += sep
		}
		out += c
	}
	return out
}

func isWindows(osClass string) bool {
	return osClass == "windows-server" || osClass == "windows-workstation"
}
func isLinux(osClass string) bool {
	return osClass == "linux-server" || osClass == "linux-workstation"
}
