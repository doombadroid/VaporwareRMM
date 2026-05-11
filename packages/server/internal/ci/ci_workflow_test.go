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
// regression. The docker-build/-publish jobs authenticate to GHCR
// with GITHUB_TOKEN. Before this fix any PR opened against main
// could publish images and overwrite the `latest` tag.
//
// Original Codex #5 design: one docker-build job whose
// `packages: write` permission and `push:` field both resolved via
// `${{ ... }}` expressions referencing github.event_name + github.ref.
// GitHub Actions does NOT evaluate github.* context inside the
// permissions block — the workflow failed validation with
// "Unrecognized named-value: 'github'". The post-fix shape splits
// into two jobs:
//
//   - docker-build: runs on every event, `permissions: contents:
//     read`, every build step has `push: false`. Verifies the
//     Dockerfiles compile on PR builds.
//   - docker-publish: gated by a job-level `if:` containing
//     github.event_name + github.ref, has `permissions: packages:
//     write`, and only this job carries `push: true` lines.
//
// Structural assertions against the YAML — no real runner required.
// Six checks:
//   - github.event_name + github.ref are both referenced (the
//     two-half guard for a push-context conditional).
//   - Every `push: true` literal sits inside a job whose `if:` (or
//     enclosing block's `if:`) contains a github.event_name guard.
//   - Every `packages: write` permission is reachable only behind
//     a guard (release tag, or the event_name/ref expression).
//   - No `permissions:` block contains an inline ${{ ... }}
//     expression — those don't validate at workflow load time and
//     re-introduce the very breakage this test exists to prevent.
func TestPRWorkflowDoesNotPushToGHCR(t *testing.T) {
	root := findRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	src := string(raw)
	lines := strings.Split(src, "\n")

	if !strings.Contains(src, `github.event_name == 'push'`) {
		t.Error("workflow does not check github.event_name == 'push'; PR-context publishes still possible")
	}
	if !strings.Contains(src, `github.ref == 'refs/heads/main'`) {
		t.Error("workflow does not check github.ref == 'refs/heads/main'; PR-context publishes still possible")
	}

	// Refuse any `permissions:` block whose value is an inline
	// expression. GitHub Actions rejects github.* references inside
	// permissions at workflow validation time; the failure mode is a
	// 100%-broken workflow rather than a silent privilege bug, but
	// it still bricks CI and previously shipped to main.
	permExprRe := regexp.MustCompile(`(?m)^\s*(?:contents|packages|id-token|actions|checks|deployments|issues|pull-requests|repository-projects|security-events|statuses):\s*\${{`)
	if permExprRe.MatchString(src) {
		t.Error("permissions: block uses ${{ ... }} expression — GitHub Actions does not evaluate github.* context inside permissions and the workflow will fail validation")
	}

	// Every `push: true` literal must sit inside a job whose `if:`
	// references github.event_name. Walk back from the push line
	// until we hit the start of a job (next `^  [a-z]:` at column 2
	// indent) or beginning of file. Within that range, a line
	// containing `if:` AND `github.event_name` proves the job is
	// gated.
	pushTrueRe := regexp.MustCompile(`(?m)^\s*push:\s*true\s*(#.*)?$`)
	jobStartRe := regexp.MustCompile(`^  [a-zA-Z][a-zA-Z0-9_-]*:\s*$`)
	for i, line := range lines {
		if !pushTrueRe.MatchString(line) {
			continue
		}
		guarded := false
		for j := i - 1; j >= 0; j-- {
			// Stop at the previous job's header — we only inspect
			// the enclosing job's `if:`.
			if jobStartRe.MatchString(lines[j]) && j != i {
				if !guarded {
					break
				}
			}
			if strings.Contains(lines[j], "if:") && strings.Contains(lines[j], "github.event_name") {
				guarded = true
				break
			}
		}
		if !guarded {
			t.Errorf("line %d `push: true` is not inside a job gated on github.event_name — PR builds would publish", i+1)
		}
	}

	// Every effective `packages: write` (as a YAML value, not a
	// comment substring) must sit under a guard. The release job is
	// tag-gated (refs/tags/) and OK. The docker-publish job uses a
	// job-level `if:` containing github.event_name.
	pwRe := regexp.MustCompile(`(?m)^\s*packages:\s*write\s*(#.*)?$`)
	for i, line := range lines {
		if !pwRe.MatchString(line) {
			continue
		}
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
