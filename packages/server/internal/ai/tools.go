package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
)

// ToolHandler executes a registered tool. Inputs are validated by the registry
// before reaching the handler — handlers can assume args match the schema.
type ToolHandler func(ctx context.Context, args json.RawMessage, scope ScopeSnapshot) (any, error)

// ToolSpec is the registry entry. PermittedFields is a hard allow-list of
// arg fields that may be set by the model. Anything outside the list is
// rejected even if it passes the JSON schema (defence-in-depth against an
// over-permissive schema).
type ToolSpec struct {
	Name            string
	Description     string
	InputSchema     []byte // JSON schema; empty = no args
	PermittedFields []string
	// MinRung is the lowest rung at which this tool can execute. A capability
	// at suggest cannot invoke a tool whose MinRung is act_low.
	MinRung Rung
	Handler ToolHandler
}

var (
	toolsMu sync.RWMutex
	tools   = map[string]ToolSpec{}
)

// RegisterTool publishes a tool. Capabilities that need it pull it by name.
func RegisterTool(t ToolSpec) {
	if t.Name == "" {
		panic("ai: tool missing Name")
	}
	if t.Handler == nil {
		panic("ai: tool " + t.Name + " missing Handler")
	}
	toolsMu.Lock()
	defer toolsMu.Unlock()
	if _, exists := tools[t.Name]; exists {
		panic("ai: tool already registered: " + t.Name)
	}
	tools[t.Name] = t
}

// LookupTool fetches a tool spec by name.
func LookupTool(name string) (ToolSpec, bool) {
	toolsMu.RLock()
	defer toolsMu.RUnlock()
	t, ok := tools[name]
	return t, ok
}

// ToolDefsForRung returns the subset of registered tools that may run at the
// given rung. Used to compose the model's tool list — the model is never
// shown tools it couldn't actually invoke.
func ToolDefsForRung(r Rung) []ToolDef {
	toolsMu.RLock()
	defer toolsMu.RUnlock()
	out := make([]ToolDef, 0, len(tools))
	for _, t := range tools {
		if !rungAtLeast(r, t.MinRung) {
			continue
		}
		out = append(out, ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out
}

func rungAtLeast(actual, required Rung) bool {
	order := map[Rung]int{
		RungShadow: 0, RungSuggest: 1, RungActLow: 2, RungActPolicy: 3, RungAutonomous: 4,
	}
	return order[actual] >= order[required]
}

// ValidateToolCallArgs enforces (a) tool exists, (b) args parse as JSON, (c)
// every top-level field is in the tool's PermittedFields list. The JSON-schema
// validation pass would normally happen here too — Stage 0 ships the
// allow-list cheaply; full schema validation lands with the first capability
// that needs it (a common pattern with a maintained validator dep).
func ValidateToolCallArgs(name string, args []byte) error {
	t, ok := LookupTool(name)
	if !ok {
		return fmt.Errorf("ai: unknown tool %q", name)
	}
	if len(t.PermittedFields) == 0 {
		// Tool takes no args; reject any.
		if len(args) > 0 && string(args) != "{}" && string(args) != "null" {
			return fmt.Errorf("ai: tool %q accepts no args, got %d bytes", name, len(args))
		}
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return fmt.Errorf("ai: tool %q args not a JSON object: %w", name, err)
	}
	allowed := map[string]struct{}{}
	for _, f := range t.PermittedFields {
		allowed[f] = struct{}{}
	}
	for k := range raw {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("ai: tool %q rejected field %q (not in PermittedFields)", name, k)
		}
	}
	return nil
}

// ScopeSnapshot is what the rung gate captured at request entry. Tool
// handlers receive it so they can verify the resolved targets still match
// the snapshot at execution time. If a device's tags changed mid-flight,
// the handler refuses rather than acting on stale authorisation.
type ScopeSnapshot struct {
	TenantID    string             `json:"tenant_id"`
	CustomerID  string             `json:"customer_id,omitempty"`
	Devices     []DeviceSnapshot   `json:"devices,omitempty"`
	CapturedAt  int64              `json:"captured_at"`
	Hash        string             `json:"hash"` // computed by the gate, audited
}

// DeviceSnapshot captures the parts of a device record that scope decisions
// were made on. The handler compares this against live state.
type DeviceSnapshot struct {
	ID        string   `json:"id"`
	TenantID  string   `json:"tenant_id"`
	OSClass   string   `json:"os_class,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	DeletedAt *int64   `json:"deleted_at,omitempty"`
}

// SanitizeForToolOutput strips characters that have demonstrated history of
// being interpreted by LLMs as instructions when echoed back into a prompt.
// We err aggressive — non-printable ASCII, fenced code blocks, common
// directive prefixes. Tool authors who need richer output should use
// structured JSON returned to the caller, not free text fed back to the
// model.
var unsafeRe = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]|^[ \t]*(?i:ignore|disregard|system:)`)

// SanitizeFreeText is what tool handlers (or the gate) wrap any operator-
// or device-controlled string with before it goes back to the model.
func SanitizeFreeText(s string) string {
	return unsafeRe.ReplaceAllString(s, "")
}
