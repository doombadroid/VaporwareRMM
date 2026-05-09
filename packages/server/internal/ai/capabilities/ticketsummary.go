package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/prompt"
	"vaporrmm/server/internal/ai/rag"
)

// Ticket summarisation runs on intake. The capability:
//   - Produces a 1-2 sentence summary of the ticket body (tech-facing).
//   - Suggests a priority (low/medium/high/critical) the tech can accept.
//   - Suggests up to 3 KB / past-ticket references via RAG (tenant-scoped).
//   - Writes results to tickets.ai_triage as JSON for the dashboard.
//
// Default rung is suggest. The triage card shows in the ticket detail view;
// the tech can accept the suggested priority with one click.

const ticketSummaryCapName = "ticket_summary"

func init() {
	ai.Register(ai.Capability{
		Name:              ticketSummaryCapName,
		Category:          ai.CategoryAssistance,
		Description:       "Summarise a new ticket body, suggest a priority, and link related KB / past tickets via RAG. Runs on ticket-create.",
		Stage:             2,
		PreferredTaskType: ai.TaskSummarize,
		RequiredCaps:      ai.Capabilities{JSONMode: true},
	})
}

// TriageOut is what we persist to tickets.ai_triage and surface in the UI.
type TriageOut struct {
	Summary           string         `json:"summary"`
	SuggestedPriority string         `json:"suggested_priority"` // low|medium|high|critical
	SuggestedTags     []string       `json:"suggested_tags,omitempty"`
	RelatedHits       []rag.Hit      `json:"related_hits,omitempty"`
}

// SummariseTicket is the intake entry point. Synchronous so the dashboard
// can render the triage card immediately; for slow-path async behaviour the
// caller can run this in a goroutine and update the row later.
func SummariseTicket(ctx context.Context, tenantID, customerID, ticketID, title, body string) (TriageOut, error) {
	if tenantID == "" {
		return TriageOut{}, errors.New("ticket_summary: empty TenantID")
	}
	if title == "" && body == "" {
		return TriageOut{}, errors.New("ticket_summary: empty title and body")
	}

	// Best-effort RAG lookup. If the tenant has no embeddings index yet (no
	// closed tickets, no KB) the retrieval returns nothing — the capability
	// degrades gracefully.
	rs := rag.New()
	hits, _ := rs.Retrieve(ctx, rag.Scope{TenantID: tenantID, CustomerID: customerID}, rag.SourceTicket, title+"\n"+body, 3)

	pb := prompt.New(prompt.Scope{TenantID: tenantID, CustomerID: customerID}).
		SystemRules(`You are a ticket-triage assistant. Read the new ticket and produce strict JSON:
{"summary":"<one or two sentences>","suggested_priority":"low|medium|high|critical","suggested_tags":["..."]}

Use a higher priority for: business-impacting outages, security incidents, multiple users affected, executive-tier devices. Use a lower priority for: cosmetic, single-user inconvenience, training questions.`).
		UntrustedInput("ticket_title", title).
		UntrustedInput("ticket_body", body)

	for _, h := range hits {
		// Hits' Text field is empty by design (rag doesn't store content);
		// we attach the source id so the model knows what's been seen
		// before. Re-fetching the source body is the dashboard's job.
		pb = pb.RAGSnippet(tenantID, customerID, string(h.SourceKind), h.SourceID,
			fmt.Sprintf("(closed-ticket reference: id=%s)", h.SourceID))
	}

	var out TriageOut
	out.RelatedHits = hits
	_, err := ai.Run(ctx, ai.Input{
		TenantID:     tenantID,
		CustomerID:   customerID,
		TicketID:     ticketID,
		CapabilityID: ticketSummaryCapName,
		RunType:      ai.RunTypeChat,
		Estimate:     2_500,
	}, func(ctx context.Context, p ai.Provider, modelName string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		req, _, perr := pb.Render(modelName, 512)
		if perr != nil {
			return nil, nil, nil, perr
		}
		resp, cerr := p.Chat(ctx, req)
		if cerr != nil {
			return nil, nil, nil, cerr
		}
		// Parse + clamp model output. Bad/missing priority defaults to medium.
		_ = json.Unmarshal([]byte(resp.Content), &out)
		switch out.SuggestedPriority {
		case "low", "medium", "high", "critical":
		default:
			out.SuggestedPriority = "medium"
		}
		// Sanitise free-text fields the dashboard renders + the next call
		// might see if the operator re-summarises.
		out.Summary = ai.SanitizeFreeText(out.Summary)
		for i, t := range out.SuggestedTags {
			out.SuggestedTags[i] = ai.SanitizeFreeText(t)
		}
		payload, _ := json.Marshal(out)
		return &resp, nil, payload, nil
	})
	if err != nil {
		return TriageOut{}, err
	}
	return out, nil
}
