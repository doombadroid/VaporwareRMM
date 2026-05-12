package main

import (
	"testing"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// TestAgentRegistrationIncludesMemoryAndDisk asserts the register
// payload carries memory + disk_size under the schema-matching key
// names. Earlier versions sent memory as "ram" and disk as
// "storage"; the server never read those keys, so every device
// showed N/A in the dashboard.
func TestAgentRegistrationIncludesMemoryAndDisk(t *testing.T) {
	a := &Agent{hostname: "mem-disk-host", port: 47991}
	payload := a.getRegistrationInfo()

	mem, ok := payload["memory"].(uint64)
	if !ok {
		t.Fatalf("memory: type %T, want uint64", payload["memory"])
	}
	if mem == 0 {
		t.Errorf("memory is zero; expected the host's total RAM in bytes")
	}

	disk, ok := payload["disk_size"].(uint64)
	if !ok {
		t.Fatalf("disk_size: type %T, want uint64", payload["disk_size"])
	}
	if disk == 0 {
		t.Errorf("disk_size is zero; expected the root filesystem total in bytes")
	}

	// Belt-and-suspenders: the dead "ram" / "storage" keys must NOT
	// appear in the payload anymore — they were the bug.
	if _, has := payload["ram"]; has {
		t.Errorf("legacy key 'ram' still in payload; rename was incomplete")
	}
	if _, has := payload["storage"]; has {
		t.Errorf("legacy key 'storage' still in payload; rename was incomplete")
	}
}

// TestAgentHeartbeatIncludesMemoryAndDisk asserts the heartbeat
// payload carries memory + disk_size + kernel_version on every
// tick so devices that registered before the wiring landed
// self-heal on the next heartbeat (no operator action needed).
func TestAgentHeartbeatIncludesMemoryAndDisk(t *testing.T) {
	a := &Agent{hostname: "mem-disk-host-hb", port: 47991, deviceID: "test-id"}
	payload := a.getStatus()

	for _, key := range []string{"memory", "disk_size", "kernel_version"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("heartbeat payload missing %q", key)
		}
	}

	memVal, ok := payload["memory"].(uint64)
	if !ok {
		t.Fatalf("heartbeat memory: type %T, want uint64", payload["memory"])
	}
	if memVal == 0 {
		// On this runner gopsutil should return a real value; if
		// not, skip rather than fail the test on infrastructure.
		if info, err := mem.VirtualMemory(); err == nil && info.Total > 0 {
			t.Errorf("heartbeat memory is 0 but gopsutil reports %d on this runner", info.Total)
		}
	}

	diskVal, ok := payload["disk_size"].(uint64)
	if !ok {
		t.Fatalf("heartbeat disk_size: type %T, want uint64", payload["disk_size"])
	}
	if diskVal == 0 {
		if info, err := disk.Usage("/"); err == nil && info.Total > 0 {
			t.Errorf("heartbeat disk_size is 0 but gopsutil reports %d on this runner", info.Total)
		}
	}
}
