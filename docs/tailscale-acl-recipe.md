# Tailscale ACL recipe — v1 single-tailnet cross-tenant isolation

This file is the paste-ready starting config that locks in v1
cross-tenant isolation. The dashboard's Settings → Network page
references it directly. Copy it into Tailscale's admin console at
[tailscale.com/admin/acls](https://login.tailscale.com/admin/acls)
after connecting Tailscale via the setup wizard.

## What the recipe does

VaporwareRMM v1 puts every managed endpoint on a single tailnet
(the one your OAuth credential is bound to). Per-tenant isolation
is enforced via Tailscale **ACLs you author**, not by VaporwareRMM
code. The recipe below:

- Tags every device the server enrolls with
  `tag:vaporrmm-tenant-<tenant_id>` (Phase 2 wires this — see
  issue #18). VaporwareRMM's preauth endpoint will mint auth keys
  pre-bound to the per-tenant tag.
- Denies inbound traffic across tenant tags by default.
- Allows intra-tenant device-to-device traffic.
- Allows your operator workstations (tagged
  `tag:vaporrmm-operator`) to reach every tenant's devices for
  management. Adjust to your workflow.

## Paste-ready

```jsonc
// VaporwareRMM v1 single-tailnet per-tenant isolation recipe.
// Issue #19 (v2) will move per-tenant tailnets into their own
// Tailscale tailnets — at which point this ACL becomes vestigial.
// Issue #20 (v3) lets tenants BYO credentials and is orthogonal
// to this recipe.
{
  // tagOwners: who can apply each tag. VaporwareRMM's OAuth client
  // creates auth keys pre-bound to per-tenant tags; granting
  // autogroup:admin keeps the option to apply tags manually too.
  "tagOwners": {
    "tag:vaporrmm-managed":  ["autogroup:admin"],
    "tag:vaporrmm-operator": ["autogroup:admin"],
    // Add one per tenant. Phase 2 will populate this list from
    // VaporwareRMM's tenants table at install time. For now,
    // author manually — replace "<tenant_id>" with each tenant's
    // slug.
    "tag:vaporrmm-tenant-default": ["autogroup:admin"]
    // "tag:vaporrmm-tenant-acme":    ["autogroup:admin"],
    // "tag:vaporrmm-tenant-globex":  ["autogroup:admin"],
  },

  // acls: order matters. First match wins on accept. Default deny
  // is implicit when no rule matches.
  "acls": [
    // 1. Operators can reach every managed device. Adjust the src
    //    list to whoever runs help-desk operations from the
    //    dashboard. Common variant: src = ["autogroup:member"]
    //    if your tailnet is small and trusted.
    {
      "action": "accept",
      "src":    ["tag:vaporrmm-operator"],
      "dst":    ["tag:vaporrmm-managed:*"]
    },

    // 2. Devices within the same tenant can reach each other.
    //    Add one block per tenant. The src/dst tag must match
    //    EXACTLY — wildcards across tenant tags would re-open
    //    the cross-tenant gap this recipe exists to close.
    {
      "action": "accept",
      "src":    ["tag:vaporrmm-tenant-default"],
      "dst":    ["tag:vaporrmm-tenant-default:*"]
    }
    // {
    //   "action": "accept",
    //   "src":    ["tag:vaporrmm-tenant-acme"],
    //   "dst":    ["tag:vaporrmm-tenant-acme:*"]
    // },
    // {
    //   "action": "accept",
    //   "src":    ["tag:vaporrmm-tenant-globex"],
    //   "dst":    ["tag:vaporrmm-tenant-globex:*"]
    // }
  ],

  // Optional: a test block so a misconfiguration that opens
  // cross-tenant traffic is caught by Tailscale's ACL tester
  // before it hits production. Run via the "Test access" panel
  // in the admin console.
  "tests": [
    {
      "src":    "tag:vaporrmm-tenant-default",
      "accept": ["tag:vaporrmm-tenant-default:22"],
      "deny":   ["tag:vaporrmm-tenant-other:22"]
    },
    {
      "src":    "tag:vaporrmm-operator",
      "accept": ["tag:vaporrmm-managed:22"]
    }
  ]
}
```

## What it does NOT enforce

- VaporwareRMM cannot audit your ACLs. If you edit this file in
  Tailscale's admin and accidentally widen a rule across tenants,
  no alert fires from the dashboard. Treat the ACL as the source
  of truth for cross-tenant isolation and review changes
  accordingly.
- This is not a substitute for tenant-level data plane isolation
  in regulated industries. Issue #19 (v2) will provide hard
  per-tenant tailnet isolation; until that lands, this ACL is the
  best available.

## Verifying after install

1. From Tailscale's admin → ACLs → "Test access":
   - Source: `tag:vaporrmm-tenant-default`, destination
     `tag:vaporrmm-tenant-other:*`. Expect: deny.
   - Source: `tag:vaporrmm-operator`, destination
     `tag:vaporrmm-managed:22`. Expect: accept.
2. On a freshly-enrolled endpoint, run `tailscale status` and
   confirm the device is tagged
   `tag:vaporrmm-tenant-<your-tenant_id>` and
   `tag:vaporrmm-managed`.
3. From another tenant's endpoint, try
   `nc -vz <target-tailscale-ip> 22` and confirm the connection
   refuses.

## Related issues

- [#18](https://github.com/doombadroid/VaporwareRMM/issues/18) —
  Tailscale integration design, the source of this recipe.
- [#19](https://github.com/doombadroid/VaporwareRMM/issues/19) —
  v2 per-tenant tailnets (hard isolation, supersedes this recipe).
- [#20](https://github.com/doombadroid/VaporwareRMM/issues/20) —
  v3 BYO per-tenant credentials.
