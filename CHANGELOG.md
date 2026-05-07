# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- Agent commands blocked by dangerous pattern matcher (rm -rf, curl | sh, wget | bash, mkfs, etc.)
- Agent token SHA-256 hashing with legacy plaintext auto-migration
- Password strength validation (8+ chars, upper, lower, digit)
- CSRF double-submit cookie protection for state-changing requests
- IP-based rate limiting (20 req/5min per IP) + per-email login rate limiting
- HTTPS redirect + HSTS when TLS configured
- httpOnly auth_token cookie with SameSite=Strict

### Added
- API versioning with `/api/v1/*` routes and backward-compatible redirects
- OpenAPI 3.0 spec at `/api/openapi.json`, `/swagger` redirect
- Session management (`user_sessions` table, GET/DELETE `/api/sessions`)
- Forgot password flow (`password_resets` table, `/auth/forgot-password`, `/auth/reset-password`)
- Prometheus metrics endpoint (`/metrics`) with http_requests_total, http_request_duration_seconds, active_devices, registered_devices_total, db pool gauges
- Grafana dashboard JSON
- Database migration system (`schema_migrations` table, 10 migrations)
- Audit logging (`audit_logs` table, async helper, GET `/api/audit-logs`)
- Webhook support (`webhooks` table, HMAC-SHA256 signatures, CRUD endpoints)
- WebSocket hub (`/ws`) broadcasting device online/offline events
- Device detail page (`/devices/[id]`)
- Device bulk delete (`POST /api/devices/bulk-delete`)
- Device CSV export (`GET /api/devices/export?format=csv`)
- Device tags column + filtering/sorting
- User management CRUD (`GET/POST/DELETE /api/v1/users`)
- Patch management (`patches` table, CRUD endpoints)
- File transfer support (`file_transfers` table, agent endpoints, status tracking)
- Email alerting (SMTP config + alert rules tables, `net/smtp` integration)
- Request ID / lightweight tracing middleware (`X-Trace-ID` header)
- Playwright E2E tests
- Integration tests with in-memory SQLite
- Benchmark tests for health, device query, heartbeat parse, JWT, bcrypt
- Multi-arch CI builds (linux/amd64, linux/arm64, windows/amd64, darwin/amd64, darwin/arm64)
- Docker image Trivy scan in CI
- Staging docker-compose with Prometheus + Grafana
- Docker secrets support (`readSecret()` helper, `JWT_SECRET_FILE` env var)
- Dark/light theme toggle via `next-themes`
- Mobile responsive hamburger menu
- `sonner` toast notifications for all API errors
- Backup script (`scripts/backup.sh`)

### Changed
- All logging migrated to `log/slog` with structured key-value pairs
- Emoji icons replaced with `lucide-react` SVG icons
- Go version pinned to 1.23.0 across all packages and CI
- Body limit set to 4MB with read/write/idle timeouts
- Offline detection configurable via `OFFLINE_THRESHOLD_SECONDS`
- Metrics retention configurable via `METRICS_RETENTION_SECONDS`
- Auth middleware prefers cookie over Authorization header
- Agent graceful shutdown on SIGINT/SIGTERM

### Fixed
- JWT json.Marshal encoding (now uses RawURLEncoding per RFC 7515)
- AuthGuard expiry check (decodes JWT payload, validates `exp` claim)
- Dashboard divide-by-zero on empty device list
- `@lib/utils` import alias changed to `@/lib/utils`
- Agent token format updated to `vapr_` prefix
- CLI agent ID format test expectation corrected
