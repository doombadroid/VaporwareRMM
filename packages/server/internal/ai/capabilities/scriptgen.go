package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/prompt"
)

// Script generator: tech describes the action in natural language, the model
// proposes a script (bash on Linux/mac, PowerShell on Windows), and the
// capability returns the script + a danger score for the tech to review.
//
// Critical contracts:
//   - We NEVER auto-execute the generated script. Output goes to the tech's
//     screen for explicit confirmation; running it is a separate API call
//     to the existing agent-command pipeline (which has its own gates).
//   - The generated script is validated against the same dangerousPatterns
//     used by the agent-side blocklist, plus a stricter server-side set
//     for things only meaningful at generation time (asks for credentials,
//     attempts to disable monitoring, etc.). A flagged script is returned
//     with danger_score=high; the dashboard surfaces a red banner.
//   - The model is told it CANNOT use sudo, su, doas, or chmod 777. Those
//     commands trip the agent blocklist and would be rejected anyway, so
//     having the model emit them is wasted budget.

const scriptGenCapName = "script_gen"

func init() {
	ai.Register(ai.Capability{
		Name:              scriptGenCapName,
		Category:          ai.CategoryAssistance,
		Description:       "Generate a bash or PowerShell script from a natural-language description. Output is validated and shown to the tech for review; never auto-run.",
		Stage:             2,
		PreferredTaskType: ai.TaskGenerate,
		RequiredCaps:      ai.Capabilities{JSONMode: true},
	})
}

// ScriptOut is what the capability returns to the dashboard. Language is
// "bash" | "powershell"; danger_score is "low" | "medium" | "high".
type ScriptOut struct {
	Language    string   `json:"language"`
	Code        string   `json:"code"`
	Explanation string   `json:"explanation"`
	DangerScore string   `json:"danger_score"`
	DangerHits  []string `json:"danger_hits,omitempty"` // patterns the validator flagged
	Warnings    []string `json:"warnings,omitempty"`
}

// GenerateScript runs the capability. `language` is one of "bash" |
// "powershell"; `query` is the tech's NL description.
func GenerateScript(ctx context.Context, tenantID, customerID, language, query string) (ScriptOut, error) {
	if tenantID == "" {
		return ScriptOut{}, errors.New("script_gen: empty TenantID")
	}
	if query == "" {
		return ScriptOut{}, errors.New("script_gen: empty query")
	}
	if language != "bash" && language != "powershell" {
		return ScriptOut{}, errors.New("script_gen: language must be 'bash' or 'powershell'")
	}

	pb := prompt.New(prompt.Scope{TenantID: tenantID, CustomerID: customerID}).
		SystemRules(`Generate a script that performs the operator's request. Strict rules:

- Output language must be exactly the requested language (bash or powershell).
- Do NOT use: sudo, su, doas, chmod 777, rm -rf /, mkfs, dd if=/dev/zero, curl ... | sh, wget ... | bash. Find a safer alternative or refuse.
- Prefer idempotent operations. Detect-then-act, not blind apply.
- Include a one-paragraph explanation of what the script does and what it doesn't.
- Output strict JSON: {"language":"...","code":"...","explanation":"..."}.
- If the request is impossible, ambiguous, or would require any banned command, set code to "" and explain why in the explanation field.`).
		TrustedContext("requested language: " + language).
		UntrustedInput("operator_request", query)

	var out ScriptOut
	out.Language = language
	_, err := ai.Run(ctx, ai.Input{
		TenantID:     tenantID,
		CustomerID:   customerID,
		CapabilityID: scriptGenCapName,
		RunType:      ai.RunTypeChat,
		Estimate:     3_000,
	}, func(ctx context.Context, p ai.Provider, modelName string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		req, _, perr := pb.Render(modelName, 1024)
		if perr != nil {
			return nil, nil, nil, perr
		}
		resp, cerr := p.Chat(ctx, req)
		if cerr != nil {
			return nil, nil, nil, cerr
		}
		_ = json.Unmarshal([]byte(resp.Content), &out)
		// Server-side validation: never trust the model not to slip a banned
		// command into the code. Score is high if any pattern matches; the
		// dashboard surfaces a red banner and disables the one-click "send
		// to agent" button until a tech edits the script.
		out.DangerHits = scanDangerousPatterns(out.Code)
		switch {
		case len(out.DangerHits) > 0:
			out.DangerScore = "high"
		case out.Language != language:
			// Model returned the wrong language — treat as medium so the tech
			// notices something's off.
			out.DangerScore = "medium"
			out.Warnings = append(out.Warnings, "model returned a different language than requested")
		default:
			out.DangerScore = "low"
		}
		payload, _ := json.Marshal(out)
		return &resp, nil, payload, nil
	})
	if err != nil {
		return ScriptOut{}, err
	}
	return out, nil
}

// dangerousPatterns mirrors the agent-side blocklist plus a few server-only
// rules that catch generation-time mistakes (the model writing literal
// password placeholders, asking for credentials, etc.).
var (
	scriptDangerSubstrings = []string{
		"rm -rf /", "rm -rf /*", "mkfs", "dd if=/dev/zero",
		"> /dev/sda", ":(){ :|:& };:", "chmod 000 /", "chmod 777 /",
		"mkfs.ext", "mkfs.xfs", "format c:", "del /f /s /q c:",
		"sudo ", " su ", "doas ", "Stop-Service WinDefend", "Set-MpPreference -DisableRealtimeMonitoring",
		"echo password", "PASSWORD=", "API_KEY=",
	}
	scriptDangerRegexps = []*regexp.Regexp{
		regexp.MustCompile(`curl\s+.*\|\s*sh`),
		regexp.MustCompile(`curl\s+.*\|\s*bash`),
		regexp.MustCompile(`wget\s+.*\|\s*sh`),
		regexp.MustCompile(`wget\s+.*\|\s*bash`),
		regexp.MustCompile(`(?i)Invoke-WebRequest\s+.*-OutFile.*\.exe`),
		regexp.MustCompile(`(?i)Invoke-Expression\s*\(\s*New-Object`),
		regexp.MustCompile(`(?i)bypass\b.*executionpolicy`),
	}
)

// scanDangerousPatterns returns the names of every pattern that matched.
// Empty slice means clean. We return the names rather than a bool so the
// dashboard can show the tech which patterns triggered.
func scanDangerousPatterns(code string) []string {
	if code == "" {
		return nil
	}
	hits := []string{}
	lower := strings.ToLower(code)
	for _, p := range scriptDangerSubstrings {
		if strings.Contains(lower, strings.ToLower(p)) {
			hits = append(hits, p)
		}
	}
	for _, re := range scriptDangerRegexps {
		if re.MatchString(code) {
			hits = append(hits, re.String())
		}
	}
	return hits
}
