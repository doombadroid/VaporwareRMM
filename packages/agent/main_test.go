package main

import (
	"strings"
	"testing"
)

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
