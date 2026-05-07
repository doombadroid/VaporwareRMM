package main

import (
	"strings"
	"testing"
)

func TestGenerateSecureKey(t *testing.T) {
	key1 := generateSecureKey()
	key2 := generateSecureKey()

	if key1 == "" {
		t.Error("generateSecureKey returned empty string")
	}
	if key1 == key2 {
		t.Error("generateSecureKey should produce unique keys")
	}
}

func TestGenerateAgentID(t *testing.T) {
	id1 := generateAgentID()
	id2 := generateAgentID()

	if id1 == "" {
		t.Error("generateAgentID returned empty string")
	}
	if id1 == id2 {
		t.Error("generateAgentID should produce unique IDs")
	}
	if !strings.HasPrefix(id1, "agent-") {
		t.Errorf("expected ID to start with 'agent-', got %s", id1)
	}
}
