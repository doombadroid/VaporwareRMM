//go:build darwin

package main

import (
	"strings"
)

// collectAvailablePatches uses softwareupdate -l. Output:
//   * Label: macOS Sonoma 14.4-23E214
//      Title: macOS Sonoma 14.4, Version: 14.4, Size: 12345K, Recommended: YES, ...
// Plus Homebrew outdated formulae.
func collectAvailablePatches() []AgentPatchEntry {
	out := []AgentPatchEntry{}
	out = append(out, collectSoftwareUpdate()...)
	if hasCommand("brew") {
		out = append(out, collectBrewOutdated()...)
	}
	return out
}

func collectSoftwareUpdate() []AgentPatchEntry {
	stdout, err := runWithTimeout("softwareupdate", "-l")
	if err != nil {
		reportPatchError("softwareupdate", err)
		return nil
	}
	out := []AgentPatchEntry{}
	var current *AgentPatchEntry
	for _, raw := range strings.Split(string(stdout), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "* Label:") {
			if current != nil {
				out = append(out, *current)
			}
			label := strings.TrimSpace(strings.TrimPrefix(line, "* Label:"))
			current = &AgentPatchEntry{
				KBID:     label,
				Source:   "macos",
				Title:    label,
				Severity: "medium",
			}
			continue
		}
		if current == nil {
			continue
		}
		// Title: ..., Version: ..., Recommended: ...
		if strings.HasPrefix(line, "Title:") {
			current.Description = line
			// Severity heuristic: "Recommended: YES" → high.
			if strings.Contains(line, "Recommended: YES") {
				current.Severity = "high"
			}
		}
	}
	if current != nil {
		out = append(out, *current)
	}
	return out
}

func collectBrewOutdated() []AgentPatchEntry {
	stdout, err := runWithTimeout("brew", "outdated", "--quiet")
	if err != nil {
		reportPatchError("brew-outdated", err)
		return nil
	}
	out := []AgentPatchEntry{}
	for _, line := range strings.Split(string(stdout), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		out = append(out, AgentPatchEntry{
			KBID:     name,
			Source:   "macos", // collapse macOS-side updates under one source
			Title:    "brew: " + name,
			Severity: "low",
		})
	}
	return out
}
