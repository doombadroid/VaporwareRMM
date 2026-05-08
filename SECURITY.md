# Security policy

Vaporware RMM is **alpha software** with no external audit. Treat all
network-exposed instances as a security surface that needs hardening.

## Reporting a vulnerability

**Do not open a public GitHub issue for security reports.**

Email **`security@tcitsys.com`**. PGP welcome but not required.

Include:

- A description of the issue and the impact you believe it has.
- Reproduction steps (a curl/Go script is best, screenshots second).
- The commit SHA you reproduced against (`git rev-parse HEAD`).
- Whether you have already disclosed this elsewhere.

You will get an acknowledgement within **5 business days**. After triage, we
aim to ship a fix within **30 days** for high/critical issues. We will credit
you in the changelog unless you ask otherwise.

## Scope

### In scope

- The Go server (`packages/server`).
- The agent (`packages/agent`) and CLI (`packages/cli`).
- The Next.js dashboard (`apps/dashboard`).
- The shipped Caddyfile, docker-compose files, and install scripts.
- Cross-tenant data leaks of any kind, including via WebSocket events,
  Prometheus metrics, error messages, or response timing.
- Anything that lets an unauthenticated user bypass auth or CSRF.
- Anything that lets a `user`-role account perform `admin` or `super_admin`
  actions, or escape its tenant.
- Privilege escalation through impersonation, invite tokens, or registration
  secrets.
- Crypto issues with the at-rest secret encryption (`internal/crypto`).
- SSRF/injection/CRLF in admin probes, branding scripts, and email sending.

### Out of scope

- Issues in third-party submodules (Sunshine, Moonlight, Moonlight Web).
  Report those to their upstream maintainers. We will pin past known-bad
  commits when notified.
- Self-XSS or anything requiring an attacker to control the victim's machine.
- Attacks requiring a malicious operator with `super_admin` access — that
  role is fully privileged by design.
- DoS via simply sending many requests; the rate limiter is best-effort.
- Findings against `localhost` / `*.local` development defaults
  (`admin@vaporrmm.local`, `localhost` in `.env.example`, etc.).
- Missing security headers when the dashboard is served outside Caddy. The
  shipped Caddyfile is the supported deployment.
- "JWT in cookie" or "session expires after N hours" without a concrete
  attack path.

## Threat model summary

A separate [`THREAT_MODEL.md`](THREAT_MODEL.md) documents the full trust
boundaries, assumed attacker, and mitigations. Read that before filing
"this looks vulnerable" reports — many concerns (e.g. agents trusting the
server's response payload) are documented design choices.

## Safe-harbor

Good-faith research is welcome. We will not pursue legal action against
researchers who:

- Stay within the scope above.
- Do not access, modify, or destroy data they do not own.
- Do not impair availability for other users (no DoS/load testing against
  third-party-hosted instances without their permission).
- Give us a reasonable window to fix before publishing.
