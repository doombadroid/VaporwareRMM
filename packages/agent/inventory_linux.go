//go:build linux

package main

import (
	"bufio"
	"os/exec"
	"strings"
)

// collectSoftware enumerates installed packages on Linux. We try multiple
// package managers and merge results — a system can have apt + snap +
// flatpak side by side. Each helper is best-effort; failure to find one
// is logged at debug and the others continue.
func collectSoftware() []InventorySoftware {
	out := []InventorySoftware{}
	out = append(out, collectDpkg()...)
	out = append(out, collectRpm()...)
	out = append(out, collectPacman()...)
	out = append(out, collectFlatpak()...)
	out = append(out, collectSnap()...)
	return dedupSoftware(out)
}

// dedupSoftware merges entries with the same (name, version). A package
// reported by both apt and dpkg-query — or any duplication source — would
// otherwise inflate fleet counts.
func dedupSoftware(in []InventorySoftware) []InventorySoftware {
	seen := make(map[string]struct{}, len(in))
	out := make([]InventorySoftware, 0, len(in))
	for _, s := range in {
		key := s.Name + "\x00" + s.Version
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

func collectDpkg() []InventorySoftware {
	cmd := exec.Command("dpkg-query", "-W", "-f=${Package}\t${Version}\t${Maintainer}\n")
	stdout, err := cmd.Output()
	if err != nil {
		reportSoftwareError("dpkg", err)
		return nil
	}
	out := []InventorySoftware{}
	scanner := bufio.NewScanner(strings.NewReader(string(stdout)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 3)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		entry := InventorySoftware{Name: parts[0], Version: parts[1]}
		if len(parts) >= 3 {
			entry.Vendor = parts[2]
		}
		out = append(out, entry)
	}
	return out
}

func collectRpm() []InventorySoftware {
	cmd := exec.Command("rpm", "-qa", "--queryformat", "%{NAME}\t%{VERSION}-%{RELEASE}\t%{VENDOR}\n")
	stdout, err := cmd.Output()
	if err != nil {
		reportSoftwareError("rpm", err)
		return nil
	}
	out := []InventorySoftware{}
	for _, line := range strings.Split(string(stdout), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		entry := InventorySoftware{Name: parts[0], Version: parts[1]}
		if len(parts) >= 3 && parts[2] != "(none)" {
			entry.Vendor = parts[2]
		}
		out = append(out, entry)
	}
	return out
}

func collectPacman() []InventorySoftware {
	cmd := exec.Command("pacman", "-Q")
	stdout, err := cmd.Output()
	if err != nil {
		reportSoftwareError("pacman", err)
		return nil
	}
	out := []InventorySoftware{}
	for _, line := range strings.Split(string(stdout), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		out = append(out, InventorySoftware{Name: parts[0], Version: parts[1]})
	}
	return out
}

func collectFlatpak() []InventorySoftware {
	cmd := exec.Command("flatpak", "list", "--app", "--columns=application,version")
	stdout, err := cmd.Output()
	if err != nil {
		reportSoftwareError("flatpak", err)
		return nil
	}
	out := []InventorySoftware{}
	for _, line := range strings.Split(string(stdout), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 || parts[0] == "" || parts[0] == "Application ID" {
			continue
		}
		out = append(out, InventorySoftware{Name: parts[0], Version: parts[1], Vendor: "flatpak"})
	}
	return out
}

func collectSnap() []InventorySoftware {
	cmd := exec.Command("snap", "list")
	stdout, err := cmd.Output()
	if err != nil {
		reportSoftwareError("snap", err)
		return nil
	}
	out := []InventorySoftware{}
	scanner := bufio.NewScanner(strings.NewReader(string(stdout)))
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue // header row
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		out = append(out, InventorySoftware{Name: fields[0], Version: fields[1], Vendor: "snap"})
	}
	return out
}
