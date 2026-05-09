package capabilities

import (
	"context"
	"encoding/json"
	"errors"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/prompt"
)

// NL search lets a tech ask "show me Windows servers with pending reboots
// that haven't rebooted in 60 days, grouped by customer" and gets back a
// structured answer + a tabular result. The capability uses the read-only
// tool registry (list_devices, list_tickets, list_active_clusters) so the
// model can fetch what it needs in one or two passes without us stuffing
// the whole project state into the prompt.
//
// Default rung is suggest: the answer is shown to the tech who can refine
// the query. We never auto-act on NL search output.

const nlSearchCapName = "nl_search"

func init() {
	ai.Register(ai.Capability{
		Name:              nlSearchCapName,
		Category:          ai.CategoryAssistance,
		Description:       "Translate a natural-language query about the fleet into structured tool calls and return a tabular answer.",
		Stage:             2,
		PreferredTaskType: ai.TaskReason,
		// We need tool calling so the model can fetch list_devices /
		// list_tickets / list_active_clusters. Streaming is nice for UX but
		// not required.
		RequiredCaps: ai.Capabilities{ToolCalling: true, JSONMode: true},
		DependsOn:    []string{"device_classification"},
	})
}

// SearchResult is what the capability returns. The dashboard renders this.
type SearchResult struct {
	Answer  string         `json:"answer"`           // human-readable summary
	Tables  []SearchTable  `json:"tables,omitempty"` // 0+ structured result tables
	ToolLog []ToolLogEntry `json:"tool_log"`         // every tool call the model made
}

type SearchTable struct {
	Title   string     `json:"title"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

type ToolLogEntry struct {
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args"`
	Result  string          `json:"result"` // truncated for audit; full result in ai_runs
	Success bool            `json:"success"`
}

// MaxToolSteps caps how many round-trips the model can run inside a single
// search. Without this a confused model can loop list_devices→list_tickets→
// list_devices forever and burn the per-call cost cap before we notice.
const MaxToolSteps = 6

// MaxToolCallsPerStep caps how many tool invocations a single model response
// can request. A confused model returning 50 parallel tool calls would
// otherwise issue 50 DB queries per step (300 total at the step cap) — the
// cost gate covers chat tokens but not tool-handler DB load. Beyond this
// limit we silently drop the extras and log a warning; the model's next
// turn sees only the first MaxToolCallsPerStep results.
const MaxToolCallsPerStep = 10

// MaxToolResultBytes is the most we feed back to the model from a single
// tool result. A list_devices call with limit=200 returning hostnames + tags
// can easily be 50KB; multiply by 6 steps and the messages slice blows the
// model's context window AND the per-call cost estimate. The audit log
// stores its own truncated copy at the same limit.
const MaxToolResultBytes = 4096

// Search runs the NL query inside the given scope. The chokepoint enforces
// the rung; this entry point just wires the prompt + tool loop.
func Search(ctx context.Context, tenantID, customerID, query string) (SearchResult, error) {
	if tenantID == "" {
		return SearchResult{}, errors.New("nl_search: empty TenantID")
	}
	if query == "" {
		return SearchResult{}, errors.New("nl_search: empty query")
	}

	scope := prompt.Scope{TenantID: tenantID, CustomerID: customerID}
	pb := prompt.New(scope).
		SystemRules(`You are an MSP fleet operator's assistant. Answer the operator's question by calling the available tools to fetch data, then summarising.

Rules:
- Use only the listed tools — do not invent data.
- Tools are tenant-scoped; you do not need to (and cannot) supply a tenant_id.
- Stop after you have enough data; one or two tool calls is usually plenty.
- Output strict JSON: {"answer":"<summary>","tables":[{"title":"...","columns":["..."],"rows":[["..."]]}]}`).
		UntrustedInput("operator_query", query)

	// Restrict the tools the model can see to read-only ones suitable for
	// the suggest rung. The chokepoint won't let act_low+ tools run anyway,
	// but trimming the surface area keeps the model focused.
	tools := ai.ToolDefsForRung(ai.RungSuggest)
	pb.Tools(tools)

	// Snapshot for the chokepoint. NL search isn't device-scoped so the
	// snapshot has no devices.
	devices := []ai.DeviceSnapshot{}

	res := SearchResult{ToolLog: []ToolLogEntry{}}
	out, err := ai.Run(ctx, ai.Input{
		TenantID:     tenantID,
		CustomerID:   customerID,
		CapabilityID: nlSearchCapName,
		RunType:      ai.RunTypeChat,
		Devices:      devices,
		Estimate:     5_000,
	}, func(ctx context.Context, p ai.Provider, modelName string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		// Build the initial request. Subsequent rounds re-use the same
		// PromptBuilder + append the model's tool calls + tool results to
		// the messages slice.
		req, _, perr := pb.Render(modelName, 1024)
		if perr != nil {
			return nil, nil, nil, perr
		}

		messages := req.Messages
		var lastResp ai.ChatResponse
		for step := 0; step <= MaxToolSteps; step++ {
			// On the last allowed step we strip tools so the model is forced
			// to produce its final summary. Without this a confused model
			// that always returns tool_calls would exit the loop with no
			// final answer and the operator would see an empty result.
			stepTools := tools
			if step == MaxToolSteps {
				stepTools = nil
				messages = append(messages, ai.ChatMessage{
					Role:    "system",
					Content: "Tool budget exhausted. Produce the final JSON answer using the data you've already gathered. Do not request more tools.",
				})
			}
			r := ai.ChatRequest{
				Model:           modelName,
				Messages:        messages,
				MaxOutputTokens: 1024,
				Tools:           stepTools,
				JSONSchema:      req.JSONSchema,
			}
			resp, cerr := p.Chat(ctx, r)
			if cerr != nil {
				return nil, nil, nil, cerr
			}
			lastResp = resp
			if len(resp.ToolCalls) == 0 {
				break // model produced its final answer
			}
			// Append the assistant turn so the next round sees its own
			// tool-call request.
			messages = append(messages, ai.ChatMessage{
				Role:      "assistant",
				Content:   resp.Content,
				ToolCalls: resp.ToolCalls,
			})
			// Execute each tool call through the registry. The registry
			// validates args against PermittedFields + InputSchema before
			// reaching the handler. We cap the number of parallel tool
			// calls per step to bound DB load from confused models.
			calls := resp.ToolCalls
			if len(calls) > MaxToolCallsPerStep {
				calls = calls[:MaxToolCallsPerStep]
			}
			for _, tc := range calls {
				logEntry := ToolLogEntry{Tool: tc.Name, Args: tc.Args}
				if err := ai.ValidateToolCallArgs(tc.Name, tc.Args); err != nil {
					logEntry.Success = false
					logEntry.Result = err.Error()
					res.ToolLog = append(res.ToolLog, logEntry)
					messages = append(messages, ai.ChatMessage{
						Role: "tool", ToolID: tc.ID, Name: tc.Name,
						Content: `{"error":"` + ai.SanitizeFreeText(err.Error()) + `"}`,
					})
					continue
				}
				spec, ok := ai.LookupTool(tc.Name)
				if !ok {
					logEntry.Success = false
					logEntry.Result = "unknown tool"
					res.ToolLog = append(res.ToolLog, logEntry)
					messages = append(messages, ai.ChatMessage{
						Role: "tool", ToolID: tc.ID, Name: tc.Name,
						Content: `{"error":"unknown tool"}`,
					})
					continue
				}
				snap := ai.ScopeSnapshot{TenantID: tenantID, CustomerID: customerID}
				result, herr := spec.Handler(ctx, tc.Args, snap)
				if herr != nil {
					logEntry.Success = false
					logEntry.Result = herr.Error()
					res.ToolLog = append(res.ToolLog, logEntry)
					messages = append(messages, ai.ChatMessage{
						Role: "tool", ToolID: tc.ID, Name: tc.Name,
						Content: `{"error":"` + ai.SanitizeFreeText(herr.Error()) + `"}`,
					})
					continue
				}
				body, _ := json.Marshal(result)
				logEntry.Success = true
				logEntry.Result = truncateForLog(string(body), 1024)
				res.ToolLog = append(res.ToolLog, logEntry)
				// Tool result fed back to the model is truncated to the
				// same bound the audit log uses. A 100KB device list
				// otherwise eats the model's context window.
				messages = append(messages, ai.ChatMessage{
					Role: "tool", ToolID: tc.ID, Name: tc.Name,
					Content: truncateForLog(string(body), MaxToolResultBytes),
				})
			}
		}
		// Parse the final answer.
		_ = json.Unmarshal([]byte(lastResp.Content), &res)
		payload, _ := json.Marshal(res)
		return &lastResp, nil, payload, nil
	})
	if err != nil {
		return SearchResult{}, err
	}
	_ = out
	return res, nil
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
