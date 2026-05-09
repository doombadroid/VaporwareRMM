package playbooks

import (
	"context"
	"testing"
	"time"

	"vaporrmm/server/internal/ai"
)

func TestSeverityForRung(t *testing.T) {
	cases := []struct {
		sev     Severity
		rung    ai.Rung
		wantErr bool
	}{
		{SeverityLow, ai.RungShadow, true},
		{SeverityLow, ai.RungSuggest, true},
		{SeverityLow, ai.RungActLow, false},
		{SeverityLow, ai.RungActPolicy, false},
		{SeverityMedium, ai.RungActLow, false},
		{SeverityMedium, ai.RungSuggest, true},
		{SeverityHigh, ai.RungActLow, true},
		{SeverityHigh, ai.RungActPolicy, false},
		{SeverityCritical, ai.RungActPolicy, true},
		{SeverityCritical, ai.RungAutonomous, false},
	}
	for _, c := range cases {
		err := SeverityForRung(c.sev, c.rung)
		if (err != nil) != c.wantErr {
			t.Errorf("SeverityForRung(%s, %s) err=%v wantErr=%v", c.sev, c.rung, err, c.wantErr)
		}
	}
}

func TestRestartServiceAppliesTo(t *testing.T) {
	r := restartService{}
	cases := []struct {
		t    Target
		want bool
	}{
		{Target{OSClass: "windows-server"}, true},
		{Target{OSClass: "linux-server"}, true},
		{Target{OSClass: "linux-workstation"}, true},
		{Target{OSClass: "mac"}, false},
		{Target{OSClass: "unknown"}, false},
		{Target{OSClass: "linux-server", Tags: []string{"regulated"}}, false},
		{Target{OSClass: "windows-server", Tags: []string{"domain_controller"}}, false},
		{Target{OSClass: "linux-server", Tags: []string{"hypervisor"}}, false},
		{Target{OSClass: "windows-server", Tags: []string{"file_server"}}, false},
		{Target{OSClass: "linux-server", Tags: []string{"prod"}}, true},
	}
	for _, c := range cases {
		if got := r.AppliesTo(c.t); got != c.want {
			t.Errorf("AppliesTo(os=%s tags=%v) = %v, want %v", c.t.OSClass, c.t.Tags, got, c.want)
		}
	}
}

func TestLooksSafeServiceName(t *testing.T) {
	good := []string{"nginx", "iis", "fail2ban", "service.unit", "queue@worker", "PostgreSQL-12", "snmp_walk"}
	bad := []string{"", "rm -rf /", "nginx;reboot", "$(whoami)", "../etc/passwd", "nginx`whoami`", "nginx |sh", "nginx&", "name with space"}
	for _, n := range good {
		if !looksSafeServiceName(n) {
			t.Errorf("expected %q to be considered safe", n)
		}
	}
	for _, n := range bad {
		if looksSafeServiceName(n) {
			t.Errorf("expected %q to be REJECTED", n)
		}
	}
}

func TestServiceLooksRunning(t *testing.T) {
	cases := []struct {
		osClass, output string
		want            bool
	}{
		{"linux-server", "active\n", true},
		{"linux-server", "inactive\n", false},
		{"linux-server", "failed", false},
		{"windows-server", "Running", true},
		{"windows-server", "Stopped", false},
		{"windows-server", "  Running  ", true},
		{"mac", "active", false},
		{"unknown", "active", false},
	}
	for _, c := range cases {
		got := serviceLooksRunning(c.osClass, c.output)
		if got != c.want {
			t.Errorf("serviceLooksRunning(%q, %q) = %v, want %v", c.osClass, c.output, got, c.want)
		}
	}
}

func TestCandidatesForFiltersByApplicability(t *testing.T) {
	// All registered playbooks return false for unknown os_class — the
	// candidates list should be empty for an unclassified target.
	got := CandidatesFor(Target{OSClass: "unknown"})
	if len(got) > 0 {
		t.Errorf("expected zero candidates for unknown OS, got %d (%v)", len(got), got)
	}
	// At least restart_service should appear for linux-server (no excluded tag).
	got = CandidatesFor(Target{OSClass: "linux-server"})
	found := false
	for _, p := range got {
		if p.Name() == "restart_service" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected restart_service in candidates for linux-server, got %d items", len(got))
	}
}

func TestBlastReserveLocalSlidingWindow(t *testing.T) {
	// Wipe local counter for isolation.
	localBlast.mu.Lock()
	localBlast.windows = map[string]*window{}
	localBlast.mu.Unlock()

	cfg := BlastConfig{MaxDevices: 3, WindowMinutes: 1}
	for i := 0; i < 3; i++ {
		if err := reserveLocal("cap1", "tA", cfg); err != nil {
			t.Fatalf("reservation %d should succeed, got %v", i+1, err)
		}
	}
	// 4th reservation must fail.
	if err := reserveLocal("cap1", "tA", cfg); err == nil {
		t.Error("4th reservation should exceed cap")
	}
	// Different tenant has its own window.
	if err := reserveLocal("cap1", "tB", cfg); err != nil {
		t.Errorf("different tenant should not share budget: %v", err)
	}
	// Different capability has its own window.
	if err := reserveLocal("cap2", "tA", cfg); err != nil {
		t.Errorf("different capability should not share budget: %v", err)
	}
}

func TestBlastReserveLocalUnlimited(t *testing.T) {
	cfg := BlastConfig{MaxDevices: 0, WindowMinutes: 1}
	for i := 0; i < 100; i++ {
		if err := Reserve(context.Background(), "any", "any", cfg); err != nil {
			t.Fatalf("unlimited cfg should never refuse, got %v on iter %d", err, i)
		}
	}
}

func TestBlastWindowExpiresOldEntries(t *testing.T) {
	localBlast.mu.Lock()
	localBlast.windows = map[string]*window{}
	localBlast.mu.Unlock()

	cfg := BlastConfig{MaxDevices: 2, WindowMinutes: 1}
	// Stuff an old entry into the window manually.
	localBlast.mu.Lock()
	localBlast.windows["x|y"] = &window{timestamps: []time.Time{
		time.Now().Add(-5 * time.Minute),
		time.Now().Add(-3 * time.Minute),
	}}
	localBlast.mu.Unlock()

	// Both entries are >1 min old — they should be evicted on the next call,
	// so a new reservation succeeds.
	if err := reserveLocal("x", "y", cfg); err != nil {
		t.Errorf("expected stale entries to expire and reservation to succeed, got %v", err)
	}
}
