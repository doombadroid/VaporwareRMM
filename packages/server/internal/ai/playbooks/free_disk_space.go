package playbooks

import (
	"context"
	"fmt"
	"time"
)

// freeDiskSpace empties common safe-to-delete locations: Windows temp, the
// SoftwareDistribution download cache, the recycle bin, and on Linux the
// apt/dnf cache + journal logs older than 7 days. Conservative — no user
// home directories, no app caches that might hold session state.
//
// This playbook is INHERENTLY non-rollbackable (deleted files don't come
// back), so RollbackToken is empty + RollbackOK=false. The rollback
// orchestrator will skip the regression check and just label outcome based
// on whether the disk usage actually improved.

type freeDiskSpace struct{}

func init() { Register(&freeDiskSpace{}) }

func (freeDiskSpace) Name() string { return "free_disk_space" }
func (freeDiskSpace) Description() string {
	return "Clear safe temp locations + package caches + old journal logs. Non-rollbackable."
}
func (freeDiskSpace) Severity() Severity { return SeverityMedium }

func (freeDiskSpace) AppliesTo(t Target) bool {
	for _, tag := range t.Tags {
		switch tag {
		case "regulated", "critical", "domain_controller", "file_server", "hypervisor":
			return false
		}
	}
	switch t.OSClass {
	case "windows-server", "windows-workstation", "linux-server", "linux-workstation":
		return true
	}
	return false
}

func (freeDiskSpace) Plan(ctx context.Context, t Target, args map[string]any) (PlanResult, error) {
	cmds := buildDiskCleanCommands(t.OSClass)
	if len(cmds) == 0 {
		return PlanResult{}, fmt.Errorf("free_disk_space: unsupported os_class %q", t.OSClass)
	}
	return PlanResult{
		Description: "Clear safe temp locations + package caches + journal logs >7d",
		Steps:       cmds,
		WillModify:  []string{"$env:TEMP", "C:\\Windows\\Temp", "Recycle Bin", "/var/cache/apt", "/var/cache/dnf", "journald >7d"},
		RollbackOK:  false,
	}, nil
}

func (freeDiskSpace) Apply(ctx context.Context, t Target, args map[string]any) (ApplyResult, error) {
	cmds := buildDiskCleanCommands(t.OSClass)
	if len(cmds) == 0 {
		return ApplyResult{Success: false, Detail: "unsupported os_class"}, fmt.Errorf("free_disk_space: unsupported os_class")
	}
	cmdType := "shell"
	if isWindows(t.OSClass) {
		cmdType = "powershell"
	}
	body := joinCommands(cmds, t.OSClass)
	cmdID, err := queueAgentCommand(ctx, t, cmdType, body)
	if err != nil {
		return ApplyResult{}, err
	}
	status, output, werr := awaitAgentCommand(ctx, cmdID, 5*time.Minute) // disk ops can be slow
	if werr != nil || status != "completed" {
		return ApplyResult{Success: false, Detail: "agent reported failure: " + truncate(output, 400)}, fmt.Errorf("free_disk_space: status=%s err=%v", status, werr)
	}
	return ApplyResult{
		Success:               true,
		Detail:                "disk clean ran via agent command " + cmdID,
		RollbackToken:         "", // explicit: no rollback possible
		RollbackPreconditions: "deleted temp/cache contents are not recoverable",
		RollbackWindow:        0, // skip the orchestrator's regression check
	}, nil
}

func (freeDiskSpace) Rollback(ctx context.Context, t Target, token string) error {
	// Files are gone. No rollback. Orchestrator's RollbackWindow=0 means
	// it never schedules a probe for this playbook, so this method is here
	// for interface compliance only.
	return ErrPreconditionsNotMet
}

func buildDiskCleanCommands(osClass string) []string {
	switch osClass {
	case "windows-server", "windows-workstation":
		return []string{
			// Temp folders. -Force allows hidden, -Recurse for subdirs,
			// -ErrorAction SilentlyContinue so a single locked file doesn't
			// abort the whole clean.
			"Get-ChildItem -Path $env:TEMP -Recurse -Force -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue",
			"Get-ChildItem -Path 'C:\\Windows\\Temp' -Recurse -Force -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue",
			// SoftwareDistribution download cache (Windows Update). Stop the
			// service inside try/finally so the Start ALWAYS runs even if
			// the agent is killed mid-execution — otherwise wuauserv stays
			// stopped and Windows Update is broken until reboot.
			`try { Stop-Service -Name wuauserv -Force -ErrorAction SilentlyContinue; Get-ChildItem -Path 'C:\Windows\SoftwareDistribution\Download' -Recurse -Force -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue } finally { Start-Service -Name wuauserv -ErrorAction SilentlyContinue }`,
			// Recycle bin
			"Clear-RecycleBin -Force -ErrorAction SilentlyContinue",
		}
	case "linux-server", "linux-workstation":
		return []string{
			// apt cache (no-op on non-Debian)
			"command -v apt-get >/dev/null && apt-get clean -y || true",
			// dnf/yum cache (no-op on non-RHEL)
			"command -v dnf >/dev/null && dnf clean all || true",
			"command -v yum >/dev/null && yum clean all || true",
			// systemd-journald — keep last 7 days
			"command -v journalctl >/dev/null && journalctl --vacuum-time=7d || true",
			// /tmp old files (>30d, conservative)
			"find /tmp -type f -atime +30 -delete 2>/dev/null || true",
		}
	}
	return nil
}
