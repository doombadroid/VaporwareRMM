//go:build !linux && !windows

package main

// On platforms without a system-tray backend (e.g. macOS cross-compiled
// without CGO), the agent runs headless. The functions in tray.go default
// to no-ops; nothing else needed here.
