# Third-Party Components

This directory contains vendored copies of upstream projects that vaporRMM depends on.
They are included as git submodules so they can be **independently security-hardened**
without relying on upstream release binaries.

## Projects

| Project | Path | Language | Role |
|---------|------|----------|------|
| **Sunshine** | `sunshine/` | C++ | Game stream host (renders desktop, encodes video) |
| **Moonlight Web** | `moonlight-web/` | Rust + TS | Browser-based Moonlight client |
| **Moonlight Qt** | `moonlight-qt/` | C++ (Qt) | Desktop Moonlight client |

## Why Clone Instead of Download?

1. **Supply-chain security** — We build from audited source instead of trusting upstream release binaries
2. **Patchability** — We can apply security patches without waiting for upstream releases
3. **Reproducibility** — Same source tree across all deployments
4. **Hardening** — Custom compiler flags, stripped symbols, non-root containers

## Build

```bash
# Initialize submodules (first time only)
git submodule update --init --recursive --depth 1

# Build all components with security hardening
../scripts/build-third-party.sh
```

Or build individually:

```bash
# Moonlight Web Stream (Rust + TypeScript)
docker build -f docker/Dockerfile.moonlight-web -t vaporrmm/moonlight-web:latest .

# Sunshine (C++)
docker build -f docker/Dockerfile.sunshine -t vaporrmm/sunshine:latest .

# Moonlight Qt (C++ Qt — produces artifacts in dist/)
docker build -f docker/Dockerfile.moonlight-qt -t vaporrmm/moonlight-qt:build .
```

## Security Hardening Applied

### All Containers
- **Non-root user** — Services run as unprivileged `UID 1001`
- **Minimal base image** — `debian:bookworm-slim` or `ubuntu:24.04` with only runtime deps
- **No build tooling** in final image (multi-stage builds)
- **Read-only filesystem** where possible
- **No shell** in final stage (except where required for entrypoint)

### Sunshine
- Strips debug symbols from binary
- Runs as `sunshine` user (not root)
- Config directory mounted as volume for persistence
- Ports exposed only for Sunshine protocol

### Moonlight Web
- Non-root `moonlight` user
- Static assets pre-compiled; no Node.js runtime in container
- WebRTC port range configurable via env var

## Updating Submodules

```bash
# Update all submodules to latest upstream
git submodule update --remote --merge

# Update a specific submodule
cd third_party/sunshine && git pull origin master && cd ../..
git add third_party/sunshine && git commit -m "Update Sunshine to latest"
```

## Auditing

Before deploying to production, audit the submodules:

```bash
# Check for known vulnerabilities in dependencies
# Sunshine (C++ — manual review of CMake deps)
cd third_party/sunshine && git log --oneline -20

# Moonlight Web (Rust — cargo audit)
cd third_party/moonlight-web && cargo audit 2>/dev/null || echo "Install cargo-audit: cargo install cargo-audit"

# Moonlight Qt (C++ — manual review)
cd third_party/moonlight-qt && git log --oneline -20
```

## License Notes

These projects retain their original licenses (GPL-3.0). vaporRMM does not claim ownership. See each project's `LICENSE` file for details.
