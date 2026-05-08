<!--
Thanks for the PR. Fill in the sections below.

If this is a security fix, please coordinate with security@tcitsys.com first
so we can ship a release before the patch lands publicly.
-->

## Summary

<!-- One paragraph: what changed, and why. -->

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor (no behavior change)
- [ ] Documentation
- [ ] Build / infra / CI

## How was this tested?

<!--
- Unit / integration tests added or updated?
- Manual repro steps?
- Multi-tenant boundaries verified (cross-tenant read/write denied)?
- For DB schema changes: migration tested up + down on both SQLite and Postgres?
-->

## Checklist

- [ ] No new lint or test failures (`make test-unit`).
- [ ] Cross-tenant isolation preserved (no new query missing `tenant_id`).
- [ ] No new secrets in code, fixtures, or env defaults.
- [ ] If user-facing: dashboard screenshot or short Loom included.
- [ ] If schema change: new numbered migration in `internal/db/db.go`.
