package db

import (
	"testing"
)

func TestWrapper_q(t *testing.T) {
	sqlite := &Wrapper{Dialect: "sqlite"}
	postgres := &Wrapper{Dialect: "postgres"}

	cases := []struct {
		name     string
		wrapper  *Wrapper
		query    string
		expected string
	}{
		{
			name:     "sqlite no-op",
			wrapper:  sqlite,
			query:    `SELECT * FROM devices WHERE status = ? AND os_name = ?`,
			expected: `SELECT * FROM devices WHERE status = ? AND os_name = ?`,
		},
		{
			name:     "postgres simple",
			wrapper:  postgres,
			query:    `SELECT * FROM devices WHERE status = ? AND os_name = ?`,
			expected: `SELECT * FROM devices WHERE status = $1 AND os_name = $2`,
		},
		{
			name:     "postgres skips string literal",
			wrapper:  postgres,
			query:    `SELECT * FROM devices WHERE name = 'What?' AND status = ?`,
			expected: `SELECT * FROM devices WHERE name = 'What?' AND status = $1`,
		},
		{
			name:     "postgres multiple string literals",
			wrapper:  postgres,
			query:    `INSERT INTO devices (name, status) VALUES ('?', ?)`,
			expected: `INSERT INTO devices (name, status) VALUES ('?', $1)`,
		},
		{
			name:     "postgres no placeholders",
			wrapper:  postgres,
			query:    `SELECT COUNT(*) FROM devices`,
			expected: `SELECT COUNT(*) FROM devices`,
		},
		{
			name:     "postgres mixed",
			wrapper:  postgres,
			query:    `SELECT * FROM devices WHERE name LIKE '?' AND status = ? AND os = 'Linux?' AND version = ?`,
			expected: `SELECT * FROM devices WHERE name LIKE '?' AND status = $1 AND os = 'Linux?' AND version = $2`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.wrapper.q(tc.query)
			if got != tc.expected {
				t.Errorf("q() = %q, want %q", got, tc.expected)
			}
		})
	}
}
