# Contributing to vaporRMM

Thank you for your interest in contributing! This document covers the basics.

## Getting Started

1. Clone the repository
2. Install dependencies:
   - Go 1.23+
   - Node.js 20+ (for dashboard)
   - pnpm (for dashboard)
3. Copy `.env.example` to `.env` and fill in required values
4. Run `docker compose up -d` for full stack, or `go run ./packages/server` for server only

## Development Workflow

1. Create a feature branch: `git checkout -b feature/my-feature`
2. Make your changes
3. Run tests:
   ```bash
   go test ./packages/server/...
   go test ./packages/agent/...
   go test ./packages/cli/...
   ```
4. Run benchmarks:
   ```bash
   go test -bench=. ./packages/server/...
   ```
5. Build all packages:
   ```bash
   go build ./packages/server/...
   go build ./packages/agent/...
   go build ./packages/cli/...
   ```
6. Commit with a clear message
7. Open a pull request

## Code Style

- Go: standard `gofmt`, keep functions under 100 lines when possible
- Use `log/slog` for all logging (key-value pairs, no string formatting)
- Prefer explicit error handling over panics
- Database queries use `?` placeholders (dbWrapper rewrites for PostgreSQL)
- All new endpoints must be under `/api/v1/*`
- New tables need a migration in `runMigrations()`

## Testing

- Add tests for new handlers and DB operations
- Integration tests go in `integration_test.go`
- Benchmarks go in `benchmark_test.go`
- E2E tests go in `apps/dashboard/e2e/`

## Security

- Never commit secrets or `.env` files
- Use `readSecret()` helper for sensitive config (supports Docker secrets)
- Validate all user input (hostnames, commands, file paths)
- Add dangerous patterns to `dangerousPatterns` if adding new command types

## Questions?

Open an issue or discussion on GitHub.
