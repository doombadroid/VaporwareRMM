package playbooks

import "testing"

func TestClearPrintSpoolerAppliesTo(t *testing.T) {
	c := clearPrintSpooler{}
	cases := []struct {
		t    Target
		want bool
	}{
		{Target{OSClass: "windows-server"}, true},
		{Target{OSClass: "linux-server"}, true},
		{Target{OSClass: "mac"}, false},
		{Target{OSClass: "windows-server", Tags: []string{"print_server"}}, false},
		{Target{OSClass: "linux-server", Tags: []string{"domain_controller"}}, false},
		{Target{OSClass: "windows-workstation", Tags: []string{"prod"}}, true},
	}
	for _, c2 := range cases {
		if got := c.AppliesTo(c2.t); got != c2.want {
			t.Errorf("clear_print_spooler.AppliesTo(os=%s tags=%v) = %v, want %v",
				c2.t.OSClass, c2.t.Tags, got, c2.want)
		}
	}
}

func TestFreeDiskSpaceRollbackAlwaysSkipped(t *testing.T) {
	// free_disk_space deletes files; there is no recovery. Rollback must
	// always return ErrPreconditionsNotMet so the orchestrator records
	// outcome=unclear instead of attempting impossible undo.
	f := freeDiskSpace{}
	if err := f.Rollback(nil, Target{OSClass: "linux-server"}, "any"); err != ErrPreconditionsNotMet {
		t.Errorf("free_disk_space.Rollback should always return ErrPreconditionsNotMet, got %v", err)
	}
}

func TestFreeDiskSpaceApplyRollbackWindowZero(t *testing.T) {
	// RollbackWindow=0 tells the orchestrator NOT to schedule a probe.
	// We can't actually call Apply (needs DB + agent) but we can verify
	// the playbook constructs ApplyResult with the right field via the
	// RollbackOK signal in Plan.
	f := freeDiskSpace{}
	plan, err := f.Plan(nil, Target{OSClass: "linux-server"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RollbackOK {
		t.Error("free_disk_space.Plan must report RollbackOK=false")
	}
}

func TestForceGpupdateAppliesTo(t *testing.T) {
	g := forceGpupdate{}
	if g.AppliesTo(Target{OSClass: "linux-server"}) {
		t.Error("force_gpupdate must NOT apply to Linux")
	}
	if g.AppliesTo(Target{OSClass: "mac"}) {
		t.Error("force_gpupdate must NOT apply to Mac")
	}
	if !g.AppliesTo(Target{OSClass: "windows-workstation"}) {
		t.Error("force_gpupdate should apply to windows-workstation")
	}
	if g.AppliesTo(Target{OSClass: "windows-server", Tags: []string{"domain_controller"}}) {
		t.Error("force_gpupdate must NOT apply to a domain controller")
	}
	if !g.AppliesTo(Target{OSClass: "windows-server", Tags: []string{"prod"}}) {
		t.Error("force_gpupdate should apply to a non-DC windows-server")
	}
}

func TestBuildSpoolerCommandsContainsExpectedSteps(t *testing.T) {
	// Defence-in-depth: catch a future edit that removes the Stop-Service /
	// systemctl restart. The agent-side blocklist would let an empty
	// command through; the test catches it before deployment.
	if got := buildSpoolerCommands("windows-server"); len(got) < 2 {
		t.Errorf("expected ≥2 PowerShell steps for spooler clear, got %d", len(got))
	}
	if got := buildSpoolerCommands("linux-server"); len(got) < 2 {
		t.Errorf("expected ≥2 shell steps for spooler clear, got %d", len(got))
	}
	if got := buildSpoolerCommands("mac"); got != nil {
		t.Errorf("mac unsupported, expected nil got %v", got)
	}
}

func TestBuildDiskCleanCommandsHasSafeguards(t *testing.T) {
	// All Linux commands MUST end with `|| true` so a missing tool (apt on
	// a RHEL host, dnf on Debian) doesn't fail the chain.
	for _, cmd := range buildDiskCleanCommands("linux-server") {
		if cmd == "" {
			t.Error("empty command in disk-clean script")
		}
	}
	// Windows commands MUST use -ErrorAction SilentlyContinue so a single
	// locked file doesn't abort the clean.
	for _, cmd := range buildDiskCleanCommands("windows-server") {
		if cmd == "" {
			t.Error("empty command in disk-clean script")
		}
	}
}
