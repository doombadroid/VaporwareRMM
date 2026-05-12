package main

import (
	"strings"
	"testing"
)

// TestAgent_ServerURLPrecedence locks in the documented order:
//   1. --server-url flag wins over VAPOR_SERVER_URL.
//   2. VAPOR_SERVER_URL is used when the flag is empty.
//   3. Missing both produces an error — no silent localhost fallback.
//
// The previous default (http://localhost:8080) masked env-loading
// regressions in the install script's OpenRC service file. With
// commit be1c37c fixing the service file AND this commit refusing
// to start without a URL, both halves of the failure mode produce
// loud, actionable errors instead of confused 401 traffic against
// 127.0.0.1.
func TestAgent_ServerURLPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		env     string
		want    string
		wantErr string
	}{
		{
			name: "flag wins over env",
			flag: "https://from-flag.example.com",
			env:  "https://from-env.example.com",
			want: "https://from-flag.example.com",
		},
		{
			name: "env when flag is empty",
			flag: "",
			env:  "https://from-env.example.com",
			want: "https://from-env.example.com",
		},
		{
			name:    "missing both is an error",
			flag:    "",
			env:     "",
			wantErr: "no server URL configured",
		},
		{
			name: "trailing slash is trimmed",
			flag: "https://from-flag.example.com/",
			env:  "",
			want: "https://from-flag.example.com",
		},
		{
			name: "whitespace-only flag falls through to env",
			flag: "   ",
			env:  "https://from-env.example.com",
			want: "https://from-env.example.com",
		},
		{
			name:    "whitespace-only env is empty too",
			flag:    "",
			env:     "   ",
			wantErr: "no server URL configured",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveServerURL(tc.flag, tc.env)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (resolved=%q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q should contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
