package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/prompt"
	"vaporrmm/server/internal/db"
)

// auto_route is a Stage 4 assistance/action capability that picks a tech to
// assign a new ticket to. Default rung suggest — the dashboard surfaces the
// suggestion in the ticket detail view; promotion to act_low (auto-assign
// without click) requires super_admin + clean metrics.
//
// Scoring is hybrid:
//   - Deterministic part (load + customer affinity + skill tags) computes a
//     base score in code; cheap, consistent, no LLM cost.
//   - LLM tiebreaker only when the top two deterministic candidates are
//     within 10% of each other; the model reads the ticket text and ranks
//     them. This keeps the cost low for the common case where one tech is
//     obviously right.
//
// The capability output is structured: {tech_user_id, reason, confidence,
// alternates: []}. The dashboard renders the top pick + alternates; the
// tech can override.

const autoRouteCapName = "auto_route"

func init() {
	ai.Register(ai.Capability{
		Name:              autoRouteCapName,
		Category:          ai.CategoryAction,
		Description:       "Suggest which tech should own a new ticket. Scoring blends current load, customer affinity (last 30d resolved), and skill tags. LLM tiebreaker for close calls.",
		Stage:             4,
		PreferredTaskType: ai.TaskClassify,
		RequiredCaps:      ai.Capabilities{JSONMode: true},
		// Defaults are conservative — operator must consciously promote.
		DefaultPromotion: ai.PromotionCriteria{
			PrecisionMin:         0.90,
			FalsePositiveRateMax: 0.05,
			LabelingRateMin:      0.30,
			WeeksCleanRequired:   4,
			MinSamples:           100,
		},
	})
}

// RouteCandidate is what the deterministic scorer produces, one per tech.
type RouteCandidate struct {
	UserID         string  `json:"user_id"`
	Name           string  `json:"name"`
	Score          float64 `json:"score"`
	OpenLoad       int     `json:"open_load"`
	CustomerWins30 int     `json:"customer_wins_30d"`
	SkillMatches   int     `json:"skill_matches"`
}

// RouteResult is what the capability returns.
type RouteResult struct {
	TechUserID string           `json:"tech_user_id"`
	Reason     string           `json:"reason"`
	Confidence float64          `json:"confidence"`
	Alternates []RouteCandidate `json:"alternates"`
	Method     string           `json:"method"` // "deterministic" | "llm_tiebreaker"
}

// Route is the capability entry point. The ticket-create handler can call
// this fire-and-forget after intake; the result is stored in tickets.ai_route
// for the dashboard to render.
func Route(ctx context.Context, tenantID, customerID, ticketID, title, body string) (RouteResult, error) {
	if tenantID == "" {
		return RouteResult{}, errors.New("auto_route: empty TenantID")
	}
	cands, err := scoreCandidates(ctx, tenantID, customerID, title, body)
	if err != nil {
		return RouteResult{}, err
	}
	if len(cands) == 0 {
		// No tenant_admin/user rows → no candidates. Log a no-op so metrics
		// reflect that the capability fired without a path forward.
		_, _ = ai.Run(ctx, ai.Input{
			TenantID: tenantID, CustomerID: customerID, TicketID: ticketID,
			CapabilityID: autoRouteCapName, RunType: ai.RunTypeChat,
		}, func(_ context.Context, _ ai.Provider, _ string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
			payload, _ := json.Marshal(RouteResult{Reason: "no candidate techs in tenant"})
			return &ai.ChatResponse{
				Content: string(payload), ModelVersion: "local",
				Synthetic: true, SyntheticSource: "auto_route_no_candidates",
			}, nil, payload, nil
		})
		return RouteResult{Reason: "no candidate techs"}, nil
	}

	// Sort descending by score; the top one is our pick unless we need to
	// LLM-tiebreak.
	sort.Slice(cands, func(i, j int) bool { return cands[i].Score > cands[j].Score })
	top := cands[0]
	res := RouteResult{
		TechUserID: top.UserID,
		Reason:     formatReason(top),
		Confidence: top.Score,
		Method:     "deterministic",
	}
	// Up to 3 alternates surfaced to the dashboard.
	if len(cands) > 1 {
		end := 4
		if end > len(cands) {
			end = len(cands)
		}
		res.Alternates = append(res.Alternates, cands[1:end]...)
	}

	// Tiebreaker: top two within 10% AND top score < 0.8 (clear winners
	// stay deterministic). The LLM call is bounded — single ~2k token
	// reasoning, no tools.
	if needsTiebreaker(cands) {
		llmPick, llmReason, err := llmTiebreaker(ctx, tenantID, customerID, ticketID, title, body, cands[0], cands[1])
		if err == nil && llmPick != "" {
			res.Method = "llm_tiebreaker"
			if llmPick == cands[0].UserID || llmPick == cands[1].UserID {
				res.TechUserID = llmPick
				res.Reason = llmReason
				// Confidence stays at the deterministic top score; the LLM
				// only resolved a close call, not asserted high confidence.
			}
		}
	}

	// Fire the audit-only ai.Run so the capability's call counter ticks
	// even on the deterministic path. Cost stays near $0.
	_, _ = ai.Run(ctx, ai.Input{
		TenantID: tenantID, CustomerID: customerID, TicketID: ticketID,
		CapabilityID: autoRouteCapName, RunType: ai.RunTypeChat, Estimate: 0,
	}, func(_ context.Context, _ ai.Provider, _ string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		payload, _ := json.Marshal(res)
		return &ai.ChatResponse{
			Content: string(payload), ModelVersion: "local",
			Synthetic: true, SyntheticSource: "auto_route_" + res.Method,
		}, nil, payload, nil
	})
	return res, nil
}

// scoreCandidates runs the deterministic scorer over every tech in the
// tenant. Score = 0.4 * load_inverse + 0.4 * customer_affinity + 0.2 *
// skill_match — tunable weights, hardcoded here for v1.
func scoreCandidates(ctx context.Context, tenantID, customerID, title, body string) ([]RouteCandidate, error) {
	// N+1 caveat: we issue 2 inner queries per user (load count + customer
	// affinity). For 100 techs that's 201 round-trips per Route() call.
	// Acceptable for v1 (ticket-create is low-frequency); v2 should batch
	// into a single LEFT JOIN per metric.
	rows, err := db.DB.Query(`
		SELECT id, COALESCE(name,email), COALESCE(skill_tags,''), COALESCE(routing_weight,100)
		  FROM users
		 WHERE tenant_id = ? AND role IN ('admin','user') AND COALESCE(routing_weight,100) > 0`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("auto_route: list users: %w", err)
	}
	defer rows.Close()

	thirtyDaysAgo := time.Now().Add(-30 * 24 * time.Hour).Unix()
	cands := []RouteCandidate{}
	for rows.Next() {
		var c RouteCandidate
		var skillCSV string
		var weight int
		if err := rows.Scan(&c.UserID, &c.Name, &skillCSV, &weight); err != nil {
			continue
		}
		// Open ticket load (fewer is better).
		var openLoad int
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tickets WHERE tenant_id = ? AND assigned_to = ? AND status NOT IN ('resolved','closed')`,
			tenantID, c.UserID).Scan(&openLoad)
		c.OpenLoad = openLoad

		// Customer affinity (resolved tickets in last 30d for this customer).
		if customerID != "" {
			_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tickets WHERE tenant_id = ? AND assigned_to = ? AND customer_id = ? AND status IN ('resolved','closed') AND updated_at >= ?`,
				tenantID, c.UserID, customerID, thirtyDaysAgo).Scan(&c.CustomerWins30)
		}

		// Skill match: count of skill tags that appear in the ticket text.
		// Cheap bag-of-words; a future iteration could embed.
		titleBody := strings.ToLower(title + " " + body)
		for _, t := range strings.Split(skillCSV, ",") {
			t = strings.TrimSpace(strings.ToLower(t))
			if t == "" {
				continue
			}
			if strings.Contains(titleBody, t) {
				c.SkillMatches++
			}
		}

		c.Score = computeScore(c, weight)
		cands = append(cands, c)
	}
	return cands, nil
}

// computeScore blends load + affinity + skills. routing_weight (per-user)
// scales the final score so an MSP can dial a tech down (training period,
// part-time, on PTO) without setting their workload to zero.
func computeScore(c RouteCandidate, weight int) float64 {
	loadInv := 1.0 / (1.0 + float64(c.OpenLoad)) // 1.0 with no load, 0.5 with 1 ticket, 0.33 with 2, etc.
	affinity := float64(c.CustomerWins30) / 10.0  // saturates at 1.0 with 10+ wins
	if affinity > 1.0 {
		affinity = 1.0
	}
	skill := float64(c.SkillMatches) / 5.0 // saturates at 1.0 with 5+ tag matches
	if skill > 1.0 {
		skill = 1.0
	}
	score := 0.4*loadInv + 0.4*affinity + 0.2*skill
	// Apply per-user weight (default 100).
	score *= float64(weight) / 100.0
	return score
}

func needsTiebreaker(cands []RouteCandidate) bool {
	if len(cands) < 2 {
		return false
	}
	a, b := cands[0].Score, cands[1].Score
	if a >= 0.8 {
		return false // clear winner
	}
	if b == 0 {
		return false
	}
	return (a-b)/a < 0.10 // within 10%
}

// llmTiebreaker asks the model to pick between two close candidates based on
// the ticket text. Returns the chosen user_id + a one-sentence reason.
// Bounded cost — 1 round-trip, no tools.
func llmTiebreaker(ctx context.Context, tenantID, customerID, ticketID, title, body string, a, b RouteCandidate) (string, string, error) {
	pb := prompt.New(prompt.Scope{TenantID: tenantID, CustomerID: customerID}).
		SystemRules(`Pick the better tech for this ticket. Strict JSON output:
{"user_id":"<one-of-the-two-ids>","reason":"<one-sentence>"}.

Only the two listed candidates are valid choices.`).
		TrustedContext(fmt.Sprintf("candidate A: id=%s name=%q open_load=%d customer_wins_30d=%d skill_matches=%d",
			a.UserID, a.Name, a.OpenLoad, a.CustomerWins30, a.SkillMatches)).
		TrustedContext(fmt.Sprintf("candidate B: id=%s name=%q open_load=%d customer_wins_30d=%d skill_matches=%d",
			b.UserID, b.Name, b.OpenLoad, b.CustomerWins30, b.SkillMatches)).
		UntrustedInput("ticket_title", title).
		UntrustedInput("ticket_body", body)

	var pick struct {
		UserID string `json:"user_id"`
		Reason string `json:"reason"`
	}
	_, err := ai.Run(ctx, ai.Input{
		TenantID: tenantID, CustomerID: customerID, TicketID: ticketID,
		CapabilityID: autoRouteCapName, RunType: ai.RunTypeChat,
		Estimate: 1500,
	}, func(ctx context.Context, p ai.Provider, modelName string) (*ai.ChatResponse, *ai.EmbedResponse, []byte, error) {
		req, _, perr := pb.Render(modelName, 256)
		if perr != nil {
			return nil, nil, nil, perr
		}
		resp, cerr := p.Chat(ctx, req)
		if cerr != nil {
			return nil, nil, nil, cerr
		}
		_ = json.Unmarshal([]byte(resp.Content), &pick)
		// Defensive: refuse if the model picked someone outside the pair.
		// We persist the FULL response text to action_taken so the audit
		// row carries the model's raw output (including the bad id) for
		// diagnostics; the caller still gets "" back and falls through to
		// the deterministic pick.
		valid := pick.UserID == a.UserID || pick.UserID == b.UserID
		if !valid {
			pick.UserID = ""
			pick.Reason = "model picked an invalid id; falling back to deterministic"
		}
		// Audit payload includes a validity flag so dashboard reviewers can
		// see "LLM tiebreaker fired but produced an invalid pick" without
		// mistaking it for a parse failure.
		auditPayload := map[string]any{
			"user_id":   pick.UserID,
			"reason":    pick.Reason,
			"valid":     valid,
			"raw_model": resp.Content,
		}
		payload, _ := json.Marshal(auditPayload)
		return &resp, nil, payload, nil
	})
	if err != nil {
		return "", "", err
	}
	return pick.UserID, ai.SanitizeFreeText(pick.Reason), nil
}

func formatReason(c RouteCandidate) string {
	parts := []string{fmt.Sprintf("%s has open_load=%d", c.Name, c.OpenLoad)}
	if c.CustomerWins30 > 0 {
		parts = append(parts, fmt.Sprintf("recently resolved %d tickets for this customer", c.CustomerWins30))
	}
	if c.SkillMatches > 0 {
		parts = append(parts, fmt.Sprintf("matched %d skill tag(s)", c.SkillMatches))
	}
	return strings.Join(parts, "; ")
}

// SaveRouteToTicket persists the route decision so the dashboard can render
// it. Tenant-scoped UPDATE — same pattern used by ticket triage.
func SaveRouteToTicket(ctx context.Context, tenantID, ticketID string, res RouteResult) error {
	payload, err := json.Marshal(res)
	if err != nil {
		return err
	}
	if _, err := db.DB.Exec(`UPDATE tickets SET ai_route = ? WHERE id = ? AND tenant_id = ?`,
		string(payload), ticketID, tenantID); err != nil {
		return fmt.Errorf("auto_route: persist to ticket: %w", err)
	}
	return nil
}

