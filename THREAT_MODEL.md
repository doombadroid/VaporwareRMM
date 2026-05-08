# Threat model

Vaporware RMM is a multi-tenant remote monitoring and management server. This
document records the trust boundaries, the assumed attacker, and the
mitigations actually shipped — so security reports can target real gaps and
not documented design choices.

## Roles

| Role | Capability |
|------|-----------|
| `super_admin` | Full read/write on every tenant. Trusted operator of the deployment. Can impersonate any tenant. Compromising this role compromises the whole system; that is by design. |
| `admin` (tenant) | Full read/write within their `tenant_id`. Cannot read other tenants. |
| `user` | Read-only on tenant data, plus their own devices/profile. |
| `agent` | Authenticated by per-device bearer token + tenant-scoped registration secret. Talks only on its device's behalf, scoped to its tenant. |
| Anonymous | `/login`, `/api/auth/*`, `/health`, `/api/branding/*` (read-only public branding), `/caddy/ask` from Caddy's loopback. |

## Trust boundaries

```
[ Internet ]
     │   TLS (Caddy 2, on-demand cert per subdomain)
     ▼
[ Caddy ] ── /caddy/ask ──► [ Server :8080 ] ── SQL ──► [ Postgres / SQLite ]
     │                            │   pub/sub
     │                            └────────────► [ Redis (optional, multi-node) ]
     ▼
[ Dashboard SSR ] ──► [ Server API ]
                          ▲
[ Agent (Tailscale tailnet OR public TLS) ] ────┘
```

- **Caddy → Server** is loopback over Docker network; the `/caddy/ask`
  endpoint is gated to that hop and never exposed to the public Caddy
  apex.
- **Agents** authenticate with a 30-day rotating bearer token issued at
  registration. The registration secret is per-tenant, SHA-256 hashed in
  the DB, and only revealed to the operator at create/rotate time.
- **WebSocket events** are filtered by `tenant_id` and role server-side
  before send. The wire envelope carries no other tenants' data.
- **Multi-node fan-out** uses Redis pub/sub; the envelope contains tenant
  + role filters that each node re-applies before delivering to its
  local sockets. A compromised Redis can drop events but cannot inject
  cross-tenant data into a node that has the row-level filter.

## Assumed attacker

- **External, unauthenticated.** Can reach `:443` and any unauth
  endpoint. Cannot read host disk or DB.
- **Authenticated tenant `user`.** Has a valid JWT, knows their own
  tenant slug, can call any API endpoint. Goal: escalate to `admin`,
  read other tenants, exfil agent install secrets.
- **Authenticated tenant `admin`.** Goal: cross-tenant read/write.
- **Compromised agent.** Has a valid agent token. Goal: send forged
  heartbeats for *other* devices, read other tenants' agent registry,
  access dashboard endpoints.

The deployment operator (who controls `JWT_SECRET`,
`SECRETS_ENCRYPTION_KEY`, the DB, and `super_admin` accounts) is fully
trusted. Reports based on "if I have shell on the server I can read
everything" are out of scope.

## Mitigations actually shipped

| Concern | Mitigation |
|---------|------------|
| Cross-tenant read | Every query is keyed on `tenant_id`. Tenant resolved from JWT `tid` claim, never from request body. Integration tests verify both `user` and `admin` cannot read across. |
| Cross-tenant write | Same. Agent endpoints additionally bind to `device_id → tenant_id` lookup before acting. |
| CSRF | Double-submit cookie on every state-changing route. WS upgrade verified via origin + cookie. |
| Auth bypass | Sessions stateful in `user_sessions`; logout, password change, role change all invalidate all sessions for that user. JWT alone insufficient. |
| Rate-limit DoS | Priority `agent > tenant > IP`; agents do not strangle dashboards. Per-tenant limits configurable. |
| Secret-at-rest | SMTP passwords + webhook secrets AES-256-GCM with `SECRETS_ENCRYPTION_KEY`. Registration secrets SHA-256 hashed (one-way). |
| Header injection (email) | `stripHeaderCRLF` on To/From/Subject; alert recipients also scrubbed. |
| Branding template injection | `brandAppNameRe` / `brandColorRe` regex validation + `scrubForComment` defense-in-depth in install-script generator. |
| Subdomain takeover | `/caddy/ask` validates against `tenants.subdomain_slug` before issuing on-demand cert. Slug cache 60s TTL, busted on tenant CRUD. |
| Replay of registration secret | Plaintext shown once; rotating regenerates+rehashes. Cache-Control: no-store on the response. |
| Impersonation tampering | Original role/tenant pulled from DB (not JWT) on end-impersonate. Audit logged at start and end. |
| SSRF in admin probes | All probes are POST so CSRF middleware applies; URLs scheme-checked (`http`/`https`); no user-controlled fetch arbitrarily. |
| TOTP brute-force | Rate-limited per email; backup codes single-use; sessions invalidated on TOTP enable/disable. |

## Known limitations

- **Tenant resource limits are best-effort.** TOCTOU is possible between
  the count check and the insert. Acceptable for v1; documented.
- **No external security audit.** Self-audited.
- **Agent → server transport.** Defaults to public TLS. Tailscale is a
  recommended optional layer; not required.
- **Sunshine + Moonlight upstreams.** Submodules are pinned (see
  `third_party/PINS.md`). We do not audit them; report issues there
  upstream.
- **DB-at-rest encryption.** Provided by Postgres TDE / SQLite-encrypted
  if the operator opts in. The server itself does not transparently
  encrypt the entire DB.
- **`super_admin` is fully privileged.** No customer-data isolation
  from the operator. This is the supported MSP model.

## Out-of-scope concerns

See [`SECURITY.md`](SECURITY.md). Highlights: third-party submodule
issues, `localhost` defaults, attacks requiring `super_admin` already
compromised, and DoS by request-volume against an instance with the
shipped rate limiter.
