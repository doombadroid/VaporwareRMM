//go:build darwin

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runPatchInstall on macOS shells out to softwareupdate -i for system
// updates and brew upgrade for Homebrew. We pass the label/formula name
// as a separate exec arg, never via shell concat.
func runPatchInstall(source, kbID string) ([]byte, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	kbID = strings.TrimSpace(kbID)
	if !validMacName(kbID) {
		return nil, fmt.Errorf("rejected kb_id %q", kbID)
	}
	ctx, cancel := newPatchInstallCtx()
	defer cancel()
	switch source {
	case "macos":
		// macos source is overloaded — could be softwareupdate label or
		// homebrew formula. Try brew first if it's installed AND the
		// formula matches; otherwise softwareupdate.
		if _, err := exec.LookPath("brew"); err == nil {
			// brew formulas don't contain spaces — rough heuristic.
			if !strings.Contains(kbID, " ") {
				if out, err := exec.CommandContext(ctx, "brew", "upgrade", kbID).CombinedOutput(); err == nil {
					return out, nil
				}
			}
		}
		return exec.CommandContext(ctx, "softwareupdate", "-i", kbID).CombinedOutput()
	default:
		return nil, fmt.Errorf("unsupported source on darwin: %q", source)
	}
}

func validMacName(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '+' || r == ':' || r == '-' || r == ' ':
		default:
			return false
		}
	}
	return true
}

func newPatchInstallCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), patchInstallTimeout)
}
