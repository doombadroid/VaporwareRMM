package playbooks

import (
	"context"
	"fmt"
	"time"
)

// forceGpupdate forces a Windows Group Policy refresh on workstations or
// member servers. Idempotent (gpupdate /force is safe to repeat). Does NOT
// apply to domain controllers — that's a different operation (replication).

type forceGpupdate struct{}

func init() { Register(&forceGpupdate{}) }

func (forceGpupdate) Name() string        { return "force_gpupdate" }
func (forceGpupdate) Description() string { return "Force Group Policy refresh via gpupdate /force. Windows only. Excludes domain controllers." }
func (forceGpupdate) Severity() Severity  { return SeverityLow }

func (forceGpupdate) AppliesTo(t Target) bool {
	if t.OSClass != "windows-server" && t.OSClass != "windows-workstation" {
		return false
	}
	for _, tag := range t.Tags {
		switch tag {
		// Hypervisors + file servers are excluded too: a refreshed Group
		// Policy that locks down NTLM, RDP, or local-admin policies on a
		// hypervisor or file server is a critical availability risk
		// (operator gets locked out of guest management or the share goes
		// inaccessible). gpupdate stays a workstation/member-server tool.
		case "domain_controller", "regulated", "critical", "file_server", "hypervisor":
			return false
		}
	}
	return true
}

func (forceGpupdate) Plan(ctx context.Context, t Target, args map[string]any) (PlanResult, error) {
	return PlanResult{
		Description: "Force Group Policy refresh on " + t.DeviceID,
		Steps:       []string{"gpupdate /force /target:Computer", "gpupdate /force /target:User"},
		WillModify:  []string{"local Group Policy state"},
		RollbackOK:  false,
	}, nil
}

func (forceGpupdate) Apply(ctx context.Context, t Target, args map[string]any) (ApplyResult, error) {
	if t.OSClass != "windows-server" && t.OSClass != "windows-workstation" {
		return ApplyResult{Success: false, Detail: "Windows only"}, fmt.Errorf("force_gpupdate: not Windows")
	}
	cmd := "gpupdate /force /target:Computer; gpupdate /force /target:User"
	cmdID, err := queueAgentCommand(ctx, t, "powershell", cmd)
	if err != nil {
		return ApplyResult{}, err
	}
	status, output, werr := awaitAgentCommand(ctx, cmdID, 90*time.Second) // gpupdate is slow
	if werr != nil || status != "completed" {
		return ApplyResult{Success: false, Detail: "agent reported failure: " + truncate(output, 400)}, fmt.Errorf("force_gpupdate: status=%s err=%v", status, werr)
	}
	return ApplyResult{
		Success: true,
		Detail:  "gpupdate ran via agent command " + cmdID,
		// gpupdate is not rollbackable in any meaningful sense — once
		// policies are refreshed they're refreshed. We skip the
		// orchestrator's regression check.
		RollbackToken:         "",
		RollbackPreconditions: "Group Policy refresh is not reversible",
		RollbackWindow:        0,
	}, nil
}

func (forceGpupdate) Rollback(ctx context.Context, t Target, token string) error {
	return ErrPreconditionsNotMet
}
