package rag

import (
	"strings"
	"testing"
)

func TestVectorLiteralRoundTrip(t *testing.T) {
	cases := []struct {
		in   []float32
		want string
	}{
		{[]float32{0.1, 0.2, 0.3}, "[0.1,0.2,0.3]"},
		{[]float32{-1, 0, 1}, "[-1,0,1]"},
		{[]float32{}, "[]"},
	}
	for _, c := range cases {
		got := vectorLiteral(c.in)
		if got != c.want {
			t.Errorf("vectorLiteral(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVectorLiteralIsParseableByPgvector(t *testing.T) {
	// pgvector accepts: [<f1>,<f2>,...] with no whitespace, square brackets,
	// numeric tokens parseable by C strtod. Our literal must match.
	v := []float32{1.5, -2.5, 0.0, 3.14159}
	got := vectorLiteral(v)
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
		t.Errorf("not bracketed: %q", got)
	}
	if strings.Contains(got, " ") {
		t.Errorf("must not contain whitespace: %q", got)
	}
}

func TestHashTextDeterministic(t *testing.T) {
	a := hashText("hello world")
	b := hashText("hello world")
	c := hashText("hello world!")
	if a != b {
		t.Error("same input must yield same hash")
	}
	if a == c {
		t.Error("different input must yield different hash")
	}
	if len(a) != 64 {
		t.Errorf("expected 64-char hex SHA-256, got %d", len(a))
	}
}

func TestNullableStr(t *testing.T) {
	if nullableStr("") != nil {
		t.Error("empty string must marshal to nil")
	}
	if v := nullableStr("foo"); v != "foo" {
		t.Errorf("non-empty must pass through, got %v", v)
	}
}
