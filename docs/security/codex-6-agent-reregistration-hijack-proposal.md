# Codex #6 — Agent Re-Registration Hijack (Implemented)

**Status:** Implemented. Design A (proof-of-possession of the existing agent token) was selected and shipped on branch `fix/codex-6-impl`. This proposal document is preserved as the design-decision artifact.

**Severity:** High. Precondition is possession of a tenant's `REGISTRATION_SECRET` (a shared, often-exported value), which is realistic in an MSP context — anyone who has ever built or deployed an agent has it.

## Locked decisions (operator-confirmed before implementation)

1. **Mandatory PoP**: existing-token proof-of-possession is required for ALL re-registrations. No carve-outs.
2. **Grace window**: 60 seconds. `agent_tokens` carries a single `previous_token_hash` + `previous_token_rotated_at`. Single previous, not a chain. Audit-log tag depends on whether the new token has heartbeated since rotation: `grace_window_used_rotation_ack` (yes — stale in-flight request) or `grace_window_used_crash_recovery` (no — agent didn't persist new token).
3. **Conflict response**: HTTP 409. `device.registration_conflict` webhook is rate-limited 1/device/hour. Every conflict is audit-logged unconditionally.
4. **Migration**: one-time legacy bypass per device (`devices.legacy_pop_bypass_used`), gated by `VAPOR_REFUSE_LEGACY_BYPASS_AFTER` (RFC3339).

Out of scope (explicitly rejected): auto-update mechanism, multi-previous-token chains, auto-lock after N conflicts, per-tenant configurability of any of the above.

## Implementation manifest (branch `fix/codex-6-impl`)

| Commit | Subject |
|---|---|
| `ce62eaf` | Schema migration 045 + `VerifyAgentPoP` + `IsLegacyAgentEligibleForBypass` + `RegisterAgentToken` rotation linkage |
| `450290c` | Handler-side PoP gate; 409 on rejection; audit tags |
| `9f253b6` | `TriggerRegistrationConflictWebhook` (1/device/hour) + test-only helpers |
| `f69a548` | One-time legacy bypass branch + `VAPOR_REFUSE_LEGACY_BYPASS_AFTER` cutoff |
| `4ec697a` | Agent-side bearer persistence; `X-Existing-Agent-Token` header; `VAPOR_ROTATE_TOKEN=1` rotation |
| (tests + this doc) | Server + agent integration tests; this doc update; threatmodel close |

---

## 1. The primitive

`POST /agent/register` at `packages/server/internal/handlers/agent.go:25` performs a check-then-act dedup on the device row. The matching tuple is **entirely client-controlled**:

```sql
SELECT id FROM devices
 WHERE tenant_id = ?
   AND hostname  = ?
   AND COALESCE(mac_address, '') = ?
```

On a match it `UPDATE`s the existing row in place (line 124), then calls `auth.RegisterAgentToken(...)` which inserts a fresh token bound to that `device_id` and **supersedes the prior agent's token after a 60s grace window** (`packages/server/internal/auth/auth.go:817`, column `agent_tokens.superseded_at`).

The supersede was introduced in `f18213c fix(auth): supersede prior agent_tokens on re-register (third pass #3)` to stop stale tokens from staying valid forever after a legitimate re-install. It also turned re-registration into a take-over primitive.

## 2. Attack walkthrough

Adversary capabilities required:
- Network reach to `POST /agent/register` (any internet-exposed deployment qualifies).
- One value of `REGISTRATION_SECRET` for any tenant.
- Knowledge of (or ability to enumerate) a target device's hostname + MAC.

Steps:
1. Read a target device's hostname/MAC. Hostnames are often guessable (`LAPTOP-CEO01`, `SQL-PROD-03`); MACs leak via heartbeat-derived data shown to all dashboard users, in logs, and via the `/api/v1/devices` listing for anyone with read access.
2. `POST /agent/register` with `X-Registration-Secret: <tenant-secret>`, body `{hostname, mac_address, ...attacker-controlled os/ip}` and a fresh 32-byte bearer.
3. Server matches the existing row, `UPDATE`s `ip_address`/`os_name`/`agent_ip` to attacker values, and binds the attacker's bearer to the existing `device_id`. The legitimate agent's token gets `superseded_at = now()` and dies in 60s.
4. The dashboard now thinks the attacker's machine *is* that device. Any operator-issued command (script run, file pull, package install) routes to the attacker. Reverse: the attacker can poll the command queue and execute whatever the operator queues for "Bob's laptop."

This is a fleet-pivot primitive: REGISTRATION_SECRET → take over any device's identity.

## 3. Why earlier passes missed it

`threatmodel.md` already lists "agent re-registration hijack" as a known blind spot. The supersede UPDATE was added to close a different bug (stale tokens) and the dedup was added in the cleanup pass to handle legitimate reinstalls. Neither change re-considered whether the *match keys themselves* were a trust boundary. They are.

The auth precondition (`REGISTRATION_SECRET`) is what kept this from being Critical, but in MSP deployments that secret is widely held — every technician who builds an agent installer has it.

---

## 4. Candidate fixes

Three designs, in order of presentation, not preference.

### Design A — Proof-of-possession of the existing agent token (recommended)

**Idea.** On a re-registration that matches an existing (tenant_id, hostname, mac_address) tuple, require the request to prove it controls the *current* agent token for that device. New behavior:

- Add a new header `X-Existing-Agent-Token: <plaintext-bearer>` (optional, only used when the row already exists).
- Server hashes it and compares against `agent_tokens.token_hash WHERE device_id = ? AND superseded_at IN (NULL, 0)`.
- On match: proceed with the UPDATE + supersede as today.
- On mismatch or missing header: return `409 Conflict` with `error: "device already exists; admin recovery required"`.

The agent already persists its token; the routine "agent restarted, re-registering itself" path can present its own current token and proceed without admin involvement. The attacker doesn't have it.

**Operational recovery** (lost token / fresh OS install): a new admin endpoint `POST /api/v1/devices/:id/reset-registration` clears the token rows, allowing the next `register` call to take the row. Audit-logged, JWT + CSRF + RBAC-gated.

**Closes the attack?** Yes — attacker has the registration secret but not the per-device token.

**Cost.**
- Agents that lost their token (reformat, disk failure) need an admin click before they can come back. This is a known operational chore, not a regression.
- One new column nullable in `agent_tokens` is not needed; the existing schema is enough.
- New admin endpoint + dashboard button + audit row.
- One extra DB read on every re-registration (cheap).

**Risk.** A bug in the existing-token comparison (timing, hash mismatch handling) re-opens the attack. Use `subtle.ConstantTimeCompare` on the hashes. The comparison path must run *before* any UPDATE.

### Design B — Admin-approval workflow for dedup

**Idea.** A re-registration matching an existing row never replaces it in-band. Instead:

- Insert a row in a new `pending_registrations` table: `(id, tenant_id, existing_device_id, claimed_hostname, claimed_mac, proposed_token_hash, requested_at, requesting_ip, status='pending')`.
- Return `202 Accepted` to the agent with `{status: "pending_admin_approval", reference_id}`. The agent retries on a backoff.
- A dashboard reviewer (RBAC: admin) sees the pending row alongside the existing device, including IP/UA delta from the previous registration. Approve → swap tokens, supersede the old. Reject → discard.

**Closes the attack?** Yes — human in the loop.

**Cost.**
- High friction. Every legitimate reinstall blocks until an admin clicks. Doesn't scale for MSPs with thousands of endpoints; reformats are routine.
- New table + endpoints + dashboard surface.
- Admins are social-engineerable; "the new laptop's MAC changed, please approve" emails are exactly the social engineering path this introduces.
- Notification plumbing required (admin must *know* there's something to approve) — otherwise legitimate agents stay offline indefinitely.

**Risk.** Trades a token-theft attack for a phishing-the-admin attack. May be acceptable in regulated environments where every device change should be paper-trailed anyway.

### Design C — INSERT-only registration; explicit admin delete required for reinstall

**Idea.** Remove the dedup branch entirely. Registration is INSERT-only. If `(tenant_id, hostname, mac_address)` already exists:

- Return `409 Conflict` with `error: "device exists; admin must remove before re-registering"`.
- Admin deletes the prior `devices` row via the existing `DELETE /api/v1/devices/:id` (already audit-logged, already RBAC-gated). The next `register` from the new agent succeeds because the conflicting row is gone.

**Closes the attack?** Yes — no in-band path to take over an existing device.

**Cost.**
- Same friction as Design B but synchronous and simpler. Admin click is "delete the old device row," then the agent registers fresh.
- A *new* `device_id` is issued, so historical alerts/tickets/audit entries against the old row don't carry over to the new one. Most MSPs treat reformatted machines as new entities anyway, so this is often fine; for compliance contexts where device continuity matters it is not.
- Smallest code change: delete the `UPDATE` branch, return 409 on conflict.
- No new tables, no new endpoints, no PoP comparison logic.

**Risk.** Lowest implementation risk of the three. Highest *operational* friction in fleets with high reformat churn.

---

## 5. Recommendation

**Design A (proof-of-possession).** Reasoning:

1. **Closes the attack at the protocol layer**, not the workflow layer. Designs B and C close it via human-in-the-loop friction; humans get social-engineered, especially MSP help-desk admins.
2. **Preserves device identity** across legitimate re-installs that *retain the agent's persisted token* (the common case: agent service restarts, agent updates itself, agent moves between Tailscale routes). No admin involvement, no new device_id, no broken alert/ticket history.
3. **Admin recovery path is exactly the path that already exists for "lost token"** — operators will hit this once per actually-reformatted machine, which is rare. Designs B and C make this the *every-registration* path, which is wrong.
4. **Smallest new attack surface**: one DB read, one constant-time compare, one new admin endpoint that mirrors the existing token-revocation pattern.

Design C is the credible second choice if reviewers want to minimize code change and accept the device_id discontinuity. Design B is least preferred — it introduces a new social-engineering path and a queue that someone has to remember to clear.

## 6. Open questions — RESOLVED

The five questions raised at proposal time were resolved before
implementation. Outcomes:

- **Mandatory PoP?** Mandatory, no carve-outs. Implemented.
- **Previous-token grace window?** Yes, single previous, 60s (matches `superseded_at`). Implemented as `previous_token_hash` + `previous_token_rotated_at` columns on `agent_tokens` row 045.
- **Conflict response code + alerting?** HTTP 409. `device.registration_conflict` webhook fires at most 1× per device per hour. Audit fires unconditionally. Implemented as `events.TriggerRegistrationConflictWebhook`.
- **Auto-lock threshold?** No auto-lock. Alert only. Per locked decisions.
- **Migration path for already-deployed agents?** One-time bypass per device, gated by `VAPOR_REFUSE_LEGACY_BYPASS_AFTER` (RFC3339). Operator flips the env var once the fleet has rotated. Implemented as `auth.IsLegacyAgentEligibleForBypass` + `auth.MarkLegacyBypassConsumed`.

## 7. What this branch contains (historical — proposal branch only)

The proposal branch (`fix/codex-agent-reregistration-hijack-PROPOSAL`) contained this document and no code changes. It is preserved as the design-decision artifact. Implementation lives on `fix/codex-6-impl` per the manifest at the top of this document.
