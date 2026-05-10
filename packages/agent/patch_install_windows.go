//go:build windows

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runPatchInstall on Windows uses the WUA COM API to download + install
// a single update by KB number. We never shell-template the KB id —
// PowerShell receives it via -Command parameter and the script
// references it as a typed string.
func runPatchInstall(source, kbID string) ([]byte, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	kbID = strings.TrimSpace(kbID)
	if source != "winupdate" {
		return nil, fmt.Errorf("unsupported source on windows: %q", source)
	}
	if !validKBID(kbID) {
		return nil, fmt.Errorf("rejected kb_id %q: must match KB[0-9]+ or GUID", kbID)
	}
	ctx, cancel := newPatchInstallCtx()
	defer cancel()
	// Pass the KB through PowerShell's $args[0] so it's never inlined
	// into the script body — kbID is treated as data.
	const script = `
		param([string]$kb)
		$ErrorActionPreference = 'Stop'
		$session = New-Object -ComObject Microsoft.Update.Session
		$searcher = $session.CreateUpdateSearcher()
		# Strip "KB" prefix for KBArticleIDs match
		$kbNum = $kb -replace '^KB', ''
		$results = $searcher.Search("IsInstalled=0 and IsHidden=0")
		$collection = New-Object -ComObject Microsoft.Update.UpdateColl
		foreach ($u in $results.Updates) {
			if ($u.KBArticleIDs -contains $kbNum -or $u.Identity.UpdateID -eq $kb) {
				$collection.Add($u) | Out-Null
			}
		}
		if ($collection.Count -eq 0) { Write-Output "no matching update"; exit 1 }
		$dl = $session.CreateUpdateDownloader()
		$dl.Updates = $collection
		$dl.Download() | Out-Null
		$inst = $session.CreateUpdateInstaller()
		$inst.Updates = $collection
		$res = $inst.Install()
		Write-Output "ResultCode=$($res.ResultCode) RebootRequired=$($res.RebootRequired)"
		if ($res.ResultCode -ne 2) { exit 2 }
	`
	return exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive",
		"-Command", script, "-kb", kbID,
	).CombinedOutput()
}

// validKBID accepts "KB" + digits or a UUID (Update Identity GUID).
func validKBID(s string) bool {
	if strings.HasPrefix(s, "KB") {
		rest := s[2:]
		if rest == "" {
			return false
		}
		for _, r := range rest {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	// UUID-like fallback for direct UpdateID
	if len(s) >= 32 && len(s) <= 64 {
		for _, r := range s {
			switch {
			case r >= '0' && r <= '9':
			case r >= 'a' && r <= 'f':
			case r >= 'A' && r <= 'F':
			case r == '-':
			default:
				return false
			}
		}
		return true
	}
	return false
}

func newPatchInstallCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), patchInstallTimeout)
}
