//go:build linux

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runPatchInstall installs a single package via the requested package
// manager. We never execute arbitrary shell — both source and kb_id are
// validated and passed as separate exec args (no shell concat) to
// neutralize meta-character abuse.
func runPatchInstall(source, kbID string) ([]byte, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	kbID = strings.TrimSpace(kbID)
	if !validPkgName(kbID) {
		return nil, fmt.Errorf("rejected kb_id %q: must match [a-zA-Z0-9._+:-]+", kbID)
	}
	ctx, cancel := newPatchInstallCtx()
	defer cancel()
	switch source {
	case "apt":
		return exec.CommandContext(ctx, "apt-get", "install", "-y", "--only-upgrade", kbID).CombinedOutput()
	case "dnf", "yum":
		return exec.CommandContext(ctx, source, "upgrade", "-y", kbID).CombinedOutput()
	case "pacman":
		return exec.CommandContext(ctx, "pacman", "-S", "--noconfirm", kbID).CombinedOutput()
	case "flatpak":
		return exec.CommandContext(ctx, "flatpak", "update", "-y", kbID).CombinedOutput()
	case "snap":
		return exec.CommandContext(ctx, "snap", "refresh", kbID).CombinedOutput()
	default:
		return nil, fmt.Errorf("unsupported source on linux: %q", source)
	}
}

// validPkgName allows the chars all major linux package managers use in
// names: alnum + . _ + : -. Rejects anything else, including shell metas.
// kbID never reaches a shell on this platform but defense-in-depth.
func validPkgName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '+' || r == ':' || r == '-':
		default:
			return false
		}
	}
	return true
}

func newPatchInstallCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), patchInstallTimeout)
}
