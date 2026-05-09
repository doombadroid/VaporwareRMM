package capabilities

import (
	"strings"
	"testing"
	"time"
)

func TestAlertSignatureNormalisesNumericTokens(t *testing.T) {
	// Two flaps of the same incident differ only in PIDs / timestamps. The
	// signature should match so the cheap exact-match path catches the
	// duplicate before any LLM call.
	a := AlertContext{
		Source:   "rule:cpu_high",
		Severity: "warning",
		Title:    "High CPU on web-01",
		Body:     "Process pid=1234 used 91% over 60s starting at 2026-05-08T10:01:00",
	}
	b := AlertContext{
		Source:   "rule:cpu_high",
		Severity: "warning",
		Title:    "High CPU on web-01",
		Body:     "Process pid=5678 used 88% over 60s starting at 2026-05-08T10:05:30",
	}
	sa, sb := alertSignature(a), alertSignature(b)
	if sa != sb {
		t.Errorf("expected signatures to match after numeric normalisation, got\n  %s\n  %s", sa, sb)
	}
}

func TestAlertSignatureDistinguishesDifferentSources(t *testing.T) {
	a := AlertContext{Source: "rule:cpu_high", Severity: "warning", Title: "x", Body: "y"}
	b := AlertContext{Source: "rule:disk_full", Severity: "warning", Title: "x", Body: "y"}
	if alertSignature(a) == alertSignature(b) {
		t.Error("alerts from different sources must hash differently")
	}
}

func TestAlertSignatureDistinguishesSeverity(t *testing.T) {
	a := AlertContext{Source: "rule:x", Severity: "warning", Title: "x", Body: "y"}
	b := AlertContext{Source: "rule:x", Severity: "critical", Title: "x", Body: "y"}
	if alertSignature(a) == alertSignature(b) {
		t.Error("warning vs critical must hash differently")
	}
}

func TestAlertSignatureLengthIs64HexChars(t *testing.T) {
	s := alertSignature(AlertContext{Title: "x"})
	if len(s) != 64 {
		t.Errorf("expected 64-char SHA-256 hex, got %d", len(s))
	}
}

func TestFormatCandidatesEmpty(t *testing.T) {
	got := formatCandidates(nil)
	if !strings.Contains(got, "(none)") {
		t.Errorf("empty candidate list should mention '(none)', got %q", got)
	}
}

func TestFormatCandidatesIncludesAllFields(t *testing.T) {
	got := formatCandidates([]clusterCandidate{
		{ID: "abc", Name: "test", LikelyCause: "service flap", Count: 5, LastSeen: time.Now().Unix()},
	})
	for _, want := range []string{"abc", "test", "service flap", "occurrences=5"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got %q", want, got)
		}
	}
}
