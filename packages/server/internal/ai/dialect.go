package ai

import (
	"errors"

	"vaporrmm/server/internal/db"
)

// ErrAINotSupported is returned when an AI feature is invoked on a database
// dialect that we don't support for AI workloads. Today that means SQLite —
// the embedding store + audit log volume + atomic cost counters all want
// Postgres semantics. The dashboard checks this and hides the AI tab.
var ErrAINotSupported = errors.New("ai: AI features require PostgreSQL; current deployment uses SQLite")

// SupportedDialect returns nil if the running database can host AI features,
// otherwise ErrAINotSupported. Every AI HTTP handler calls this first.
func SupportedDialect() error {
	if db.DB == nil {
		return ErrAINotSupported
	}
	if db.DB.Dialect != "postgres" {
		return ErrAINotSupported
	}
	return nil
}
