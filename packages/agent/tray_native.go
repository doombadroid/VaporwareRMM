//go:build linux || windows

package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"fyne.io/systray"
)

func init() {
	startSystemTray = startSystemTrayNative
	setTrayTooltip = systray.SetTooltip
	quitTray = systray.Quit
}

// startSystemTrayNative is the linux/windows implementation of setupSystemTray.
// macOS uses the no-op version (see tray_noop.go) because fyne.io/systray
// requires CGO for the Cocoa backend.
func startSystemTrayNative(a *Agent) {
	headless := os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
	if runtime.GOOS != "windows" && headless {
		slog.Info("No display detected — running headless, system tray disabled")
		return
	}

	go func() {
		systray.Run(func() {
			systray.SetTooltip(fmt.Sprintf("%s - %s\nClick for support", a.companyName, a.hostname))

			if a.iconURL != "" {
				iconData, err := downloadIcon(a.iconURL)
				if err == nil {
					systray.SetIcon(iconData)
				} else {
					slog.Warn("could not download icon", "error", err)
				}
			}

			mHelp := systray.AddMenuItem("Request Help", "Contact IT support")
			mStatus := systray.AddMenuItem("Status: Connected", "Agent connection status")
			mStatus.Disable()

			systray.AddSeparator()

			mQuit := systray.AddMenuItem("Quit", "Stop the agent")

			go func() {
				for {
					select {
					case <-mHelp.ClickedCh:
						if err := a.handleRequestHelp(); err != nil {
							slog.Warn("error handling help request", "error", err)
						}
					case <-mQuit.ClickedCh:
						systray.Quit()
						return
					}
				}
			}()
		}, func() {
			slog.Info("System tray exited")
		})
	}()
}
