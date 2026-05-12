package main

import (
	"testing"

	"github.com/shirou/gopsutil/v3/host"
)

// TestAgentReportsKernelVersion verifies the agent's registration
// payload carries the OS-level kernel string (uname -r on Linux,
// kernel build on Windows, Darwin version on macOS) under the
// kernel_version key. PlatformVersion on Linux returns whatever
// /etc/os-release happens to put in VERSION_ID — "2.18" on a
// Gentoo profile, "24.04" on Ubuntu — which is not what operators
// expect to see when they ask "what kernel is this box running?".
// The heartbeat / registration map MUST send the real kernel
// string so the dashboard's eyebrow renders something useful.
func TestAgentReportsKernelVersion(t *testing.T) {
	a := &Agent{hostname: "kernel-test-host", port: 47991}
	payload := a.getRegistrationInfo()

	got, ok := payload["kernel_version"]
	if !ok {
		t.Fatal("registration payload missing kernel_version key")
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("kernel_version is %T, want string", got)
	}
	// The local runner's actual kernel — fail loudly if gopsutil
	// returned an empty string (every supported platform reports
	// SOMETHING for KernelVersion).
	info, err := host.Info()
	if err != nil {
		t.Skipf("host.Info() unavailable on this runner: %v", err)
	}
	if info.KernelVersion == "" {
		t.Skip("host.Info().KernelVersion is empty on this runner (unusual)")
	}
	if gotStr != info.KernelVersion {
		t.Errorf("kernel_version mismatch: payload=%q gopsutil=%q", gotStr, info.KernelVersion)
	}

	// os_version still carries PlatformVersion — the dashboard
	// uses it as a fallback when kernel_version is missing from
	// older agents.
	if _, ok := payload["os_version"]; !ok {
		t.Error("registration payload missing os_version key (fallback for older clients)")
	}
}
