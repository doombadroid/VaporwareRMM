# ADR 001: SQLite + PostgreSQL Dual Database Support

## Status
Accepted

## Context
vaporRMM needs to support both small single-node deployments (home lab, small MSP) and larger multi-node production environments. Requiring PostgreSQL for all deployments creates operational overhead for users who just want to try the tool or manage a small fleet (< 50 devices).

## Decision
Support both SQLite (default) and PostgreSQL (production) via runtime detection of `DATABASE_URL` vs `DATABASE_PATH`.

## Consequences

### Positive
- Zero-config startup: SQLite works out of the box with no external dependencies
- Easy onboarding: new users can `go run` without Docker
- Single binary deployment for small setups
- Migration path: users can start with SQLite and migrate to PostgreSQL later

### Negative
- SQL dialect differences require a wrapper (`dbWrapper`) to rewrite `?` → `$N` placeholders
- Some PostgreSQL features (JSONB, full-text search) cannot be used in SQLite mode
- Migration scripts must be tested against both dialects
- Slightly more complex codebase

## Alternatives Considered
- **Only PostgreSQL**: simpler code, but raises barrier to entry
- **Only SQLite**: impossible to scale beyond single-node, no concurrent write support
- **ORM (GORM)**: would abstract dialect differences, but adds heavy dependency and reduces control over queries
