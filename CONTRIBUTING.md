# Contributing to Vaporware RMM

Thank you for your interest in contributing! This document covers the basics.

By submitting a pull request, you agree that your contributions are licensed
under the project's [AGPL-3.0 license](LICENSE) and that you have the right to
license them. We do not require a separate CLA. Significant features should be
discussed in an issue first so we can agree on scope before code is written.

**Security issues:** do not file them as a regular issue or PR. Email
`security@tcitsys.com`. See [SECURITY.md](SECURITY.md).

**Code of ethics:** see [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

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

Open an issue or discussion on GitHub. For security questions, email
`security@tcitsys.com` instead.
