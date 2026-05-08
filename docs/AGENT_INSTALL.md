# Agent Install — Per Platform

The dashboard's `Get install command` button gives you a one-liner per OS. This doc is the manual fallback when that's not enough.

## Linux

Tested on Ubuntu 22.04+, Debian 12+, RHEL 9 / Alma 9, Gentoo, Arch.

```bash
curl -fsSL https://rmm.yourdomain.com/api/branding/agent-install?format=script \
  | sudo REGISTRATION_SECRET='vrt_xxxxx' bash -s -- --server https://rmm.yourdomain.com
```

The install script:

1. Downloads the architecture-matched agent binary to `/usr/local/bin/<app>-agent`
2. Generates a stable `VAPOR_AGENT_TOKEN` and persists it to `/etc/<app>/agent_token` (mode 0600)
3. Writes `/etc/<app>/agent.env` with `VAPOR_SERVER_URL`, `VAPOR_AGENT_TOKEN`, and (one-time) `REGISTRATION_SECRET`
4. Drops a systemd unit (`/etc/systemd/system/<app>-agent.service`) that sources the env file
5. On systems without systemd, falls back to OpenRC; failing that, runs in the background via `nohup`

To uninstall:

```bash
sudo systemctl stop <app>-agent && sudo systemctl disable <app>-agent
sudo rm -rf /etc/<app> /etc/systemd/system/<app>-agent.service /usr/local/bin/<app>-agent
sudo systemctl daemon-reload
```

## macOS

The agent runs headless on macOS (no system tray icon — fyne.io/systray needs CGO + Cocoa).

```bash
curl -fsSL https://rmm.yourdomain.com/api/branding/agent-install?format=script \
  | sudo REGISTRATION_SECRET='vrt_xxxxx' bash -s -- --server https://rmm.yourdomain.com
```

The install script writes a launchd plist at `/Library/LaunchDaemons/com.<app>.agent.plist` instead of a systemd unit. (Note: launchd path is currently approximate; verify on your build.)

If you need the system-tray help-request UI on macOS, build the agent locally with CGO:

```bash
cd packages/agent
CGO_ENABLED=1 go build -o agent .
```

This requires Xcode command-line tools.

## Windows

PowerShell, run as **Administrator**:

```powershell
$env:REGISTRATION_SECRET='vrt_xxxxx'
iwr -UseBasicParsing https://rmm.yourdomain.com/api/branding/agent-install?format=script | iex
```

> Note: the install script currently emits Bash. On Windows you'll want a PowerShell-native installer. For 10-tenant scope this is a known gap — workaround is to manually:
>
> 1. Download `agent-windows-amd64.exe` from `https://rmm.yourdomain.com/download/agent-windows-amd64`
> 2. Save as `C:\Program Files\<app>\agent.exe`
> 3. Create `C:\ProgramData\<app>\agent.env` with `VAPOR_SERVER_URL`, `VAPOR_AGENT_TOKEN` (random hex), `REGISTRATION_SECRET`
> 4. Register a Windows service:
>    ```powershell
>    sc.exe create "<app>-agent" binPath= "C:\Program Files\<app>\agent.exe" start= auto
>    sc.exe start "<app>-agent"
>    ```

A native Windows MSI installer is a Tier 5 item (signed binary + SmartScreen reputation). Until then, the manual flow above works.

## Verifying registration

Watch the server logs:

```bash
docker compose logs -f server | grep agent_register
```

Or hit the dashboard: Tenants → tenant detail → device list. New machine appears within ~30 seconds of first heartbeat.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Register returns 401 "Invalid registration secret" | Secret mismatch or rotated | Pull a fresh install command from the tenants page |
| Register returns 403 "Tenant inactive" | Tenant suspended | Reactivate or wait until grace ends |
| Register returns 403 "Device limit reached" | `max_devices` cap hit | Bump cap on the tenant page or upgrade plan |
| Heartbeat stuck on `connection refused` | Network/firewall to `rmm.yourdomain.com:443` | Check proxy / Tailscale routing |
| `systemctl status` shows `failed` immediately | Wrong arch binary (e.g. arm on amd64) | Re-run install — script auto-detects arch |
| macOS Gatekeeper rejects binary | Unsigned binary | `xattr -d com.apple.quarantine /usr/local/bin/<app>-agent` (one-shot) or codesign properly |

## Cross-platform build matrix

The Makefile target `make agent-build-all` produces binaries for:

| OS | Arch | CGO | Notes |
|---|---|---|---|
| linux | amd64 | off | Stripped, ~9.5 MB |
| linux | arm64 | off | Stripped, ~8.8 MB |
| windows | amd64 | off | `.exe`, no Authenticode signature (sign separately) |
| darwin | amd64 | off | Headless, no tray icon |
| darwin | arm64 | off | Headless, no tray icon |

Output lands in `bin/`. To ship them to clients, `scp` them to the server's `/tmp/vaporrmm-agent-<os>-<arch>` so the `/download/agent-...` endpoint serves them.
