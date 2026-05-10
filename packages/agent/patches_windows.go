//go:build windows

package main

import (
	"encoding/json"
	"strings"
)

// collectAvailablePatches uses Windows Update via PSWindowsUpdate or the
// COM API exposed through PowerShell. We avoid forcing the
// PSWindowsUpdate module install — instead we use the WUA COM object
// which is built-in. Returns title + KB number per pending update.
func collectAvailablePatches() []AgentPatchEntry {
	const script = `
		$ErrorActionPreference = 'Stop'
		try {
			$session = New-Object -ComObject Microsoft.Update.Session
			$searcher = $session.CreateUpdateSearcher()
			$results = $searcher.Search("IsInstalled=0 and IsHidden=0")
			$out = @()
			foreach ($u in $results.Updates) {
				$kb = ($u.KBArticleIDs | Select-Object -First 1)
				if ($kb) { $kb = "KB$kb" } else { $kb = $u.Identity.UpdateID }
				$out += [PSCustomObject]@{
					kb_id = $kb
					source = 'winupdate'
					title = $u.Title
					description = $u.Description
					severity = if ($u.MsrcSeverity) { $u.MsrcSeverity.ToLower() } else { 'medium' }
					cve = ''
				}
			}
			$out | ConvertTo-Json -Compress
		} catch {
			'[]'
		}
	`
	out, err := runWithTimeout("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		reportPatchError("winupdate", err)
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	// PowerShell may return a single object (no array brackets) when
	// only one update is available.
	var rows []AgentPatchEntry
	if err := json.Unmarshal(out, &rows); err != nil {
		var single AgentPatchEntry
		if err2 := json.Unmarshal(out, &single); err2 != nil {
			reportPatchError("winupdate-parse", err)
			return nil
		}
		rows = []AgentPatchEntry{single}
	}
	// Normalize severity casing — Microsoft returns "Critical", "Important",
	// "Moderate", "Low". Map to our enum.
	for i := range rows {
		switch strings.ToLower(rows[i].Severity) {
		case "critical":
			rows[i].Severity = "critical"
		case "important":
			rows[i].Severity = "high"
		case "moderate":
			rows[i].Severity = "medium"
		case "low":
			rows[i].Severity = "low"
		default:
			rows[i].Severity = "medium"
		}
	}
	return rows
}
