package ai_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are static-analysis guard tests, not behaviour tests. They scan the
// ai package source for patterns that would put us at risk of leaking
// provider API keys: env-var fallbacks for keys, log statements that
// reference Authorization headers, and so on. If any of these fire, the
// developer needs to either delete the offending line or annotate it with
// // ai-policy:allow followed by a justification, which the test treats as
// an explicit waiver.
//
// The goal is to catch the "developer added an os.Getenv fallback for
// testing" mistake at PR time rather than at first incident.

const aiPkgRel = "." // tests run with cwd = packages/server/internal/ai
const providersRel = "providers"

var forbiddenPatterns = []string{
	"os.Getenv(\"OPENAI",
	"os.Getenv(\"ANTHROPIC",
	"os.Getenv(\"GOOGLE_API",
	"os.Getenv(\"GEMINI",
	"os.Getenv(\"OLLAMA_KEY",
	// Generic env fallback for keys is forbidden too — provider keys must come
	// from the encrypted DB column, not the environment, in production code.
	"_KEY\")",
	"_API_KEY\")",
}

func TestNoEnvAPIKeyFallbacks(t *testing.T) {
	for _, dir := range []string{aiPkgRel, providersRel} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			lines := strings.Split(string(body), "\n")
			for i, line := range lines {
				for _, pat := range forbiddenPatterns {
					if !strings.Contains(line, pat) {
						continue
					}
					if strings.Contains(line, "ai-policy:allow") ||
						(i+1 < len(lines) && strings.Contains(lines[i+1], "ai-policy:allow")) ||
						(i > 0 && strings.Contains(lines[i-1], "ai-policy:allow")) {
						continue
					}
					t.Errorf("%s:%d contains forbidden pattern %q (provider keys must come from the encrypted DB column, not env vars). If this is intentional, add `// ai-policy:allow: <reason>` on the line above.", path, i+1, pat)
				}
			}
		}
	}
}

// TestNoAuthHeaderInLogs is a heuristic scan: any log call in the ai package
// or its providers that mentions "Authorization" or "api_key" by string is a
// red flag. Producers must redact before logging. If the line is inside a
// comment, it's allowed.
func TestNoAuthHeaderInLogs(t *testing.T) {
	suspicious := []string{
		"\"Authorization\"",
		"\"X-API-Key\"",
		"\"x-api-key\"",
		"\"api_key\"",
	}
	for _, dir := range []string{aiPkgRel, providersRel} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			body, _ := os.ReadFile(path)
			lines := strings.Split(string(body), "\n")
			for i, line := range lines {
				trim := strings.TrimSpace(line)
				if strings.HasPrefix(trim, "//") {
					continue
				}
				logCall := strings.Contains(line, "slog.") || strings.Contains(line, "log.") || strings.Contains(line, "fmt.Print")
				if !logCall {
					continue
				}
				for _, pat := range suspicious {
					if strings.Contains(line, pat) {
						t.Errorf("%s:%d log call appears to reference an auth header by name (%q). Redact before logging.", path, i+1, pat)
					}
				}
			}
		}
	}
}
