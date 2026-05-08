package main

// Tray operations are platform-specific and split across files with build tags:
//   tray_native.go    linux || windows  → real fyne.io/systray backend
//   tray_noop.go      !(linux || windows) → empty stubs (e.g. macOS without CGO)
//
// Call sites use these function variables instead of the systray package
// directly so the agent main file stays portable.

var (
	startSystemTray = func(_ *Agent) {}
	setTrayTooltip  = func(_ string) {}
	quitTray        = func() {}
)
