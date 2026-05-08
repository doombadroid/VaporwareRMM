# Third-party submodule pins

Each submodule is pinned to a specific commit SHA recorded in the parent repo's
tree (no `branch=` directive in `.gitmodules`, so `git submodule update --init`
reproduces the exact commit below). To bump a pin: enter the submodule, fetch +
checkout the new SHA, then `git add third_party/<name>` from the parent repo.

| Submodule | URL | Pinned SHA |
|-----------|-----|------------|
| `third_party/sunshine` | https://github.com/LizardByte/Sunshine.git | `8362d58017d70bbac2543473ad91f41cf55dceff` |
| `third_party/moonlight-qt` | https://github.com/moonlight-stream/moonlight-qt.git | `8cc3b3064211dfd91d1f9bfdbe6aeed97b1a9db8` |
| `third_party/moonlight-web` | https://github.com/MrCreativ3001/moonlight-web-stream.git | `0a42ea1852f37a506ff770a75693664499688a94` |

## Upgrade procedure

```bash
cd third_party/sunshine
git fetch origin
git checkout <new-sha>
cd ../..
git add third_party/sunshine
git commit -m "bump sunshine to <new-sha>"
```

Always test the agent install scripts and remote-desktop launch flow after a
bump — Sunshine's release naming has shifted between major versions and the
download URL pattern in `internal/handlers/branding.go` may need updating.
