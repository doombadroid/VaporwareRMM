package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// findRepoRoot walks upward from the test's working directory until
// it finds the .github folder; the workflow file is at
// <root>/.github/workflows/ci.yml. Falling back to a relative path
// would tie the test to the package's nested location.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "ci.yml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find repo root from %q", dir)
	return ""
}

// TestPRWorkflowDoesNotPushToGHCR is the Codex #5 attack-path
// regression. The docker-build job authenticates to GHCR with
// GITHUB_TOKEN. Before this fix it called docker/build-push-action
// with `push: true` unconditionally; any PR opened against main
// could publish images and overwrite the `latest` tag. After the
// fix the push field is gated on the workflow being a `push` event
// on `refs/heads/main` (or a manual workflow_dispatch).
//
// Structural assertion against the YAML — does not require a real
// GitHub Actions runner. Three checks:
//   - No `push: true` literal anywhere in the workflow.
//   - github.event_name + github.ref are both referenced (the
//     two-half guard for a push-context conditional).
//   - Every `packages: write` permission is reachable only behind
//     a guard (release tag, or the event_name/ref expression).
func TestPRWorkflowDoesNotPushToGHCR(t *testing.T) {
	root := findRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	src := string(raw)

	if matched, _ := regexp.MatchString(`(?m)^\s*push:\s*true\s*$`, src); matched {
		t.Error("Codex #5 regressed: workflow contains literal `push: true`; PR builds will publish")
	}

	if !strings.Contains(src, `github.event_name == 'push'`) {
		t.Error("workflow does not check github.event_name == 'push'; PR-context publishes still possible")
	}
	if !strings.Contains(src, `github.ref == 'refs/heads/main'`) {
		t.Error("workflow does not check github.ref == 'refs/heads/main'; PR-context publishes still possible")
	}

	// Every effective `packages: write` (as a YAML value, not a
	// comment substring) must sit under a guard. The release job is
	// tag-gated (refs/tags/) and OK. The docker-build job uses an
	// inline expression in its permissions block.
	pwRe := regexp.MustCompile(`(?m)^\s*packages:\s*write\s*(#.*)?$`)
	pwExprRe := regexp.MustCompile(`(?m)^\s*packages:\s*\${{`)
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if !pwRe.MatchString(line) {
			continue
		}
		// Inline expression on the same line counts as guarded
		// regardless of preceding context.
		if pwExprRe.MatchString(line) {
			continue
		}
		// Walk both directions for a guard: tag-gate (refs/tags/) on
		// the enclosing job's `if:`, or an event_name/ref check on
		// the permissions expression itself.
		guarded := false
		for j := i - 1; j >= 0 && j > i-30 && !guarded; j-- {
			if strings.Contains(lines[j], "refs/tags/") || strings.Contains(lines[j], "github.event_name") {
				guarded = true
			}
		}
		if !guarded {
			t.Errorf("line %d unconditional `packages: write` — Codex #5 surface", i+1)
		}
	}
}
