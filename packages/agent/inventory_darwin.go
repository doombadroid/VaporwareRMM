//go:build darwin

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
)

// collectSoftware on macOS reads installed apps via system_profiler
// (the same data source as About This Mac → Software) and merges with
// Homebrew formulae. system_profiler -json is stable across recent macOS.
func collectSoftware() []InventorySoftware {
	out := []InventorySoftware{}
	out = append(out, collectMacApplications()...)
	out = append(out, collectHomebrew()...)
	return dedupSoftware(out)
}

func dedupSoftware(in []InventorySoftware) []InventorySoftware {
	seen := make(map[string]struct{}, len(in))
	res := make([]InventorySoftware, 0, len(in))
	for _, s := range in {
		key := s.Name + "\x00" + s.Version
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		res = append(res, s)
	}
	return res
}

func collectMacApplications() []InventorySoftware {
	out, err := exec.Command("system_profiler", "-json", "SPApplicationsDataType").Output()
	if err != nil {
		reportSoftwareError("system_profiler", err)
		return nil
	}
	var parsed struct {
		Apps []struct {
			Name    string `json:"_name"`
			Version string `json:"version"`
			Vendor  string `json:"obtained_from"`
		} `json:"SPApplicationsDataType"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		reportSoftwareError("system_profiler-parse", err)
		return nil
	}
	res := make([]InventorySoftware, 0, len(parsed.Apps))
	for _, app := range parsed.Apps {
		if app.Name == "" {
			continue
		}
		res = append(res, InventorySoftware{Name: app.Name, Version: app.Version, Vendor: app.Vendor})
	}
	return res
}

func collectHomebrew() []InventorySoftware {
	out, err := exec.Command("brew", "list", "--versions").Output()
	if err != nil {
		reportSoftwareError("brew", err)
		return nil
	}
	res := []InventorySoftware{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		res = append(res, InventorySoftware{Name: fields[0], Version: fields[1], Vendor: "homebrew"})
	}
	return res
}
