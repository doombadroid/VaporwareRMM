// Package playbooks is the action-tier registry. A Playbook is a server-side
// piece of code that mutates a managed device's state. The AI layer never
// emits shell from the LLM — it picks a registered playbook by name, the
// chokepoint validates the choice + scope + rung, and this package executes
// the playbook against the agent-command pipeline.
//
// Three contracts every playbook here upholds:
//
//  1. Apply MUST be idempotent. Use ensure_X (idempotent verbs) instead of X
//     (mutative verbs). A playbook that runs twice in a row produces the
//     same end state both times.
//
//  2. Plan returns a description of what Apply WOULD do without doing it.
//     The dashboard surfaces this when the operator manually triggers a
//     run; AI-driven runs use it for the audit log's "intended action"
//     field.
//
//  3. Rollback re-checks PRECONDITIONS before reverting. If the world
//     changed between Apply and Rollback (a human admin already restarted
//     the service, the disk is no longer full), Rollback returns nil
//     without acting and the orchestrator records "skipped — preconditions
//     no longer met". A playbook that auto-reverts blindly into a state
//     that's no longer correct is worse than no rollback.
package playbooks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"vaporrmm/server/internal/ai"
)

// Severity classifies a playbook's blast potential. The chokepoint refuses
// to run a "high" severity playbook at any rung below act_policy unless the
// per-tenant scope filter explicitly opted in for that device class.
type Severity string

const (
	SeverityLow      Severity = "low"      // restart a service, clear a queue
	SeverityMedium   Severity = "medium"   // free disk space, kill processes
	SeverityHigh     Severity = "high"     // reboot, patch deploy, network reconfig
	SeverityCritical Severity = "critical" // anything that touches a hypervisor or DC; manual-only
)

// Target is the per-call scope a playbook acts on. Builder code populates
// this from the capability's ScopeSnapshot; the playbook itself never
// resolves devices from the database.
type Target struct {
	TenantID   string
	CustomerID string
	DeviceID   string
	OSClass    string // populated from devices.os_class
	Tags       []string
}

// PlanResult describes what Apply would do without doing it. The dashboard
// renders this as an operator confirmation sheet.
type PlanResult struct {
	Description string   // human-readable
	Steps       []string // ordered list of agent commands or API calls
	WillModify  []string // service names, file paths, registry keys, etc.
	RollbackOK  bool     // false = irreversible
}

// ApplyResult is what Apply returned. The orchestrator schedules a regression
// check at OutcomeCheckAt and attempts Rollback if the alert that triggered
// the playbook re-fires within RollbackWindow.
type ApplyResult struct {
	Success               bool
	Detail                string
	RollbackToken         string        // opaque blob the playbook needs to reverse the change
	RollbackPreconditions string        // human-readable description; orchestrator stores it
	RollbackWindow        time.Duration // how long to watch for regression
}

// Playbook is the contract. Implementations live in this package alongside
// the framework so they share the agent-command client + tenant scoping
// helpers.
type Playbook interface {
	// Name is the registry key. Must match across all callsites — the AI
	// layer references playbooks by name in tool calls and audit rows.
	Name() string

	// Description is the one-line summary the model sees when deciding
	// whether to suggest this playbook for an alert.
	Description() string

	// Severity gates the rung at which the playbook is allowed to run.
	Severity() Severity

	// AppliesTo returns true if this playbook is relevant for the given
	// target. The capability layer pre-filters candidate playbooks via this
	// before asking the model to choose. Conservative implementation —
	// returning false silently excludes the playbook from the model's
	// choices, which is the safe default.
	AppliesTo(target Target) bool

	// Plan computes intent without mutating. Errors here are programming
	// bugs — they should never depend on agent connectivity.
	Plan(ctx context.Context, target Target, args map[string]any) (PlanResult, error)

	// Apply executes against the agent-command pipeline. MUST be idempotent.
	Apply(ctx context.Context, target Target, args map[string]any) (ApplyResult, error)

	// Rollback reverts a previous Apply. The token came from ApplyResult.
	// Implementations re-check preconditions and return nil (no-op) if the
	// world has moved on. Errors here trigger an alert, never an automatic
	// retry.
	Rollback(ctx context.Context, target Target, token string) error
}

// ErrPreconditionsNotMet is returned by Rollback when state has drifted
// (the service it was about to stop is already stopped, the file it was
// about to restore from backup is no longer there, etc.). Orchestrator
// treats this as a successful "no-op rollback" outcome.
var ErrPreconditionsNotMet = errors.New("playbooks: rollback preconditions no longer met; skipping")

var (
	pbMu     sync.RWMutex
	registry = map[string]Playbook{}
)

// Register adds a playbook. Called from init() in each playbook file.
func Register(p Playbook) {
	if p == nil {
		panic("playbooks: Register(nil)")
	}
	pbMu.Lock()
	defer pbMu.Unlock()
	if _, exists := registry[p.Name()]; exists {
		panic("playbooks: duplicate registration: " + p.Name())
	}
	registry[p.Name()] = p
}

// Lookup returns a playbook by name. Returns false for unknown — handlers
// must distinguish "unknown" (programming error) from "not applicable to
// this target" (operator-config decision).
func Lookup(name string) (Playbook, bool) {
	pbMu.RLock()
	defer pbMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// All returns every registered playbook, sorted by name. Used by the
// dashboard catalog and the auto-remediation capability when proposing
// candidates to the model.
func All() []Playbook {
	pbMu.RLock()
	defer pbMu.RUnlock()
	out := make([]Playbook, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// CandidatesFor returns only the playbooks whose AppliesTo returns true for
// the target. The auto-remediation capability uses this to bound the
// candidate list shown to the model — fewer choices means cleaner
// reasoning + lower token cost.
func CandidatesFor(target Target) []Playbook {
	out := []Playbook{}
	for _, p := range All() {
		if p.AppliesTo(target) {
			out = append(out, p)
		}
	}
	return out
}

// SeverityForRung is the lookup the chokepoint uses to gate a playbook at
// a given rung. Returns nil if allowed; an error explaining the gap if not.
func SeverityForRung(s Severity, r ai.Rung) error {
	allowed := map[Severity]ai.Rung{
		SeverityLow:      ai.RungActLow,
		SeverityMedium:   ai.RungActLow,
		SeverityHigh:     ai.RungActPolicy,
		SeverityCritical: ai.RungAutonomous, // realistically: never auto. Documented.
	}
	min, ok := allowed[s]
	if !ok {
		return fmt.Errorf("playbooks: unknown severity %q", s)
	}
	if !rungAtLeast(r, min) {
		return fmt.Errorf("playbooks: severity=%s requires rung %s; capability is at %s", s, min, r)
	}
	return nil
}

func rungAtLeast(actual, required ai.Rung) bool {
	order := map[ai.Rung]int{
		ai.RungShadow: 0, ai.RungSuggest: 1, ai.RungActLow: 2, ai.RungActPolicy: 3, ai.RungAutonomous: 4,
	}
	return order[actual] >= order[required]
}
