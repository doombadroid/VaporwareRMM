//go:build linux

package main

import (
	"strings"
)

// collectAvailablePatches probes whichever package managers are present
// and merges their available-update output. We try apt, dnf, then
// pacman; each collector skips silently if the binary is absent.
func collectAvailablePatches() []AgentPatchEntry {
	out := []AgentPatchEntry{}
	if hasCommand("apt") {
		out = append(out, collectApt()...)
	}
	if hasCommand("dnf") {
		out = append(out, collectDnf()...)
	} else if hasCommand("yum") {
		out = append(out, collectYum()...)
	}
	if hasCommand("pacman") {
		out = append(out, collectPacmanUpgrades()...)
	}
	return out
}

// collectApt runs `apt list --upgradable`. Output:
//   pkg/jammy-updates 1.2.3-2 amd64 [upgradable from: 1.2.3-1]
// The leading "Listing..." header is filtered.
func collectApt() []AgentPatchEntry {
	stdout, err := runWithTimeout("apt", "list", "--upgradable")
	if err != nil {
		reportPatchError("apt", err)
		return nil
	}
	out := []AgentPatchEntry{}
	for _, line := range trimEmpty(strings.Split(string(stdout), "\n")) {
		if strings.HasPrefix(line, "Listing") {
			continue
		}
		// Format: pkg/release version arch [upgradable from: oldver]
		slash := strings.Index(line, "/")
		if slash <= 0 {
			continue
		}
		name := line[:slash]
		fields := strings.Fields(line[slash+1:])
		version := ""
		if len(fields) >= 2 {
			version = fields[1]
		}
		out = append(out, AgentPatchEntry{
			KBID:        name,
			Source:      "apt",
			Title:       name + " " + version,
			Description: line,
			Severity:    "medium",
		})
	}
	return out
}

// collectDnf runs `dnf check-update --quiet`. Exit code 100 = updates
// available (not an error from our perspective). Output lines:
//   pkg.arch  version  repo
func collectDnf() []AgentPatchEntry {
	stdout, _ := runWithTimeout("dnf", "check-update", "--quiet")
	out := []AgentPatchEntry{}
	for _, line := range trimEmpty(strings.Split(string(stdout), "\n")) {
		// Skip blank lines + "Last metadata expiration" headers + obsolete sections.
		if strings.HasPrefix(line, "Last") || strings.HasPrefix(line, "Obsolet") || strings.HasPrefix(line, "Security:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		// Strip arch suffix for kb_id; keep full in title.
		if dot := strings.LastIndex(name, "."); dot > 0 {
			name = name[:dot]
		}
		out = append(out, AgentPatchEntry{
			KBID:        name,
			Source:      "dnf",
			Title:       fields[0] + " " + fields[1],
			Description: line,
			Severity:    "medium",
		})
	}
	return out
}

// collectYum is the legacy variant; same output shape as dnf.
func collectYum() []AgentPatchEntry {
	stdout, _ := runWithTimeout("yum", "check-update", "--quiet")
	out := []AgentPatchEntry{}
	for _, line := range trimEmpty(strings.Split(string(stdout), "\n")) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		if dot := strings.LastIndex(name, "."); dot > 0 {
			name = name[:dot]
		}
		out = append(out, AgentPatchEntry{
			KBID:        name,
			Source:      "yum",
			Title:       fields[0] + " " + fields[1],
			Description: line,
			Severity:    "medium",
		})
	}
	return out
}

// collectPacmanUpgrades uses checkupdates from pacman-contrib if
// present (preferred — doesn't sync the system DB), else falls back
// to `pacman -Qu`.
func collectPacmanUpgrades() []AgentPatchEntry {
	var stdout []byte
	var err error
	if hasCommand("checkupdates") {
		stdout, err = runWithTimeout("checkupdates")
	} else {
		stdout, err = runWithTimeout("pacman", "-Qu")
	}
	if err != nil {
		reportPatchError("pacman", err)
		return nil
	}
	out := []AgentPatchEntry{}
	for _, line := range trimEmpty(strings.Split(string(stdout), "\n")) {
		// Format: name oldver -> newver
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		newVer := ""
		if len(fields) >= 4 {
			newVer = fields[3]
		}
		out = append(out, AgentPatchEntry{
			KBID:        name,
			Source:      "pacman",
			Title:       name + " " + newVer,
			Description: line,
			Severity:    "medium",
		})
	}
	return out
}
