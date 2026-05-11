# AGENTS.md

Before any security-sensitive change, read `/threatmodel.md` if it is
present locally. The file is intentionally **not** tracked in this
repository (see `.gitignore`) because it names the open Critical/High
findings and the attack-path framing for the codebase, and the public
repo is not the place for that. Operators keep a canonical copy out of
band and drop it into the worktree before kicking off a review session.
If `/threatmodel.md` is missing in your checkout, ask the operator for
the current version before touching auth, sessions, tokens, the agent's
HTTP surface (server-side and agent-side), RBAC, multi-tenancy
enforcement, OIDC, AI playbook actions, secret handling, deployment
artifacts, or CI workflows.

The threat model is the lens, not the list. Open findings live in the
issue tracker; the model captures the framing, asset list, trust
boundaries, severity calibration, and the known blind spots of prior
automated audit passes that triage and fix decisions need to be made
against.
