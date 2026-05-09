// Package ai is the AI agent layer for Vaporware RMM. It is opt-in,
// per-tenant, and gated by an explicit chokepoint (Run) that enforces the
// action ladder, scope filters, kill switches, and cost caps before any
// provider call leaves the process.
//
// Stage 0 (this package today) ships only the chassis: provider abstraction,
// capability registry, gates, and audit ledger. No user-visible AI features
// run until at least one capability is registered and an operator promotes
// it past the default `shadow` rung.
package ai

import (
	"context"
	"time"
)

// Rung is a position on the action ladder. Capabilities only ever execute at
// the rung snapshotted at request entry, so a mid-flight promotion does not
// retroactively widen blast radius.
type Rung string

const (
	RungShadow     Rung = "shadow"     // logs prediction, no surfacing
	RungSuggest    Rung = "suggest"    // surfaces in tech queue, requires explicit approve
	RungActLow     Rung = "act_low"    // auto-acts on low-blast-radius scope
	RungActPolicy  Rung = "act_policy" // auto-acts on operator-defined policies
	RungAutonomous Rung = "autonomous" // broadly autonomous within scope filter
)

// Category groups capabilities for UI + onboarding.
type Category string

const (
	CategoryObservation Category = "observation"
	CategoryAssistance  Category = "assistance"
	CategoryAction      Category = "action"
)

// TaskType maps a capability invocation to a routing rule, so operators can
// route cheap classification calls to a small/local model and reserve frontier
// calls for genuine reasoning.
type TaskType string

const (
	TaskClassify  TaskType = "classify"
	TaskReason    TaskType = "reason"
	TaskSummarize TaskType = "summarize"
	TaskEmbed     TaskType = "embed"
	TaskGenerate  TaskType = "generate"
)

// RunType is what was actually executed; embed and chat are billed under
// separate per-day caps so a runaway re-index cannot exhaust the chat budget.
type RunType string

const (
	RunTypeChat     RunType = "chat"
	RunTypeEmbed    RunType = "embed"
	RunTypeToolCall RunType = "tool_call"
)

// TrustLevel records whether the model endpoint is operator-controlled
// (`local`), a contracted SaaS provider (`external`), or a self-hosted
// endpoint we treat as potentially adversarial (`self_hosted`).
type TrustLevel string

const (
	TrustLocal      TrustLevel = "local"
	TrustExternal   TrustLevel = "external"
	TrustSelfHosted TrustLevel = "self_hosted"
)

// ScopeFilter constrains which targets a capability may act on. All four
// dimensions are AND-combined; excludes always win over includes.
type ScopeFilter struct {
	CustomerIDs         []string `json:"customer_ids,omitempty"`
	DeviceClassIncludes []string `json:"device_class_includes,omitempty"`
	DeviceClassExcludes []string `json:"device_class_excludes,omitempty"`
	DeviceTagExcludes   []string `json:"device_tag_excludes,omitempty"`
}

// PromotionCriteria is checked by the metrics layer before any rung promotion.
// Promotion is always manual; demotion is automatic.
type PromotionCriteria struct {
	PrecisionMin         float64 `json:"precision_min"`
	FalsePositiveRateMax float64 `json:"fpr_max"`
	LabelingRateMin      float64 `json:"labeling_rate_min"` // techs must label at least this fraction
	WeeksCleanRequired   int     `json:"weeks_clean_required"`
	MinSamples           int     `json:"min_samples"` // never promote on N<this
}

// Capabilities advertises what a Provider implementation supports. The
// capability registry refuses to route a capability whose required features
// aren't covered by the chosen provider — so a capability needing tool-calling
// won't silently degrade on a self-hosted model that can't do it.
type Capabilities struct {
	Streaming   bool
	ToolCalling bool
	Embeddings  bool
	JSONMode    bool // strict JSON-schema-constrained outputs
	MaxContext  int  // tokens
}

// ChatMessage is one turn in a chat exchange. Role is one of
// "system" | "user" | "assistant" | "tool".
type ChatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	Name      string     `json:"name,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolID    string     `json:"tool_call_id,omitempty"` // when role=tool
}

// ChatRequest is provider-neutral. Each Provider translates to its native
// schema. JSONSchema (when set) requests structured output; providers without
// JSONMode will reject the request.
type ChatRequest struct {
	Model           string
	Messages        []ChatMessage
	MaxOutputTokens int
	Temperature     float32
	Tools           []ToolDef
	JSONSchema      []byte // raw JSON schema; empty = unstructured
	Stream          bool
}

// ChatResponse carries the model's completion + telemetry needed to bill the
// run. PromptTokens / OutputTokens are authoritative (provider-reported); we
// use them for cost calculation, not our own estimate.
type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	PromptTokens int
	OutputTokens int
	ModelVersion string // e.g. "gpt-4o-2024-11-20" — pinned where the provider exposes it
	// Synthetic marks responses produced by capability fast-paths (e.g. an
	// exact-match cluster lookup that doesn't actually call the provider).
	// The chokepoint replaces model_name with "local:<source>" on the audit
	// row when this is set so reviewers can tell a real call from a cache hit.
	Synthetic       bool
	SyntheticSource string
}

// ToolDef is what the model is told it may call.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema []byte `json:"input_schema"` // JSON schema
}

// ToolCall is what the model emitted. The server validates Name against the
// tool registry, validates Args against the tool's InputSchema, then re-checks
// the rung gate before any side-effecting code runs.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args []byte `json:"args"` // JSON
}

// EmbedRequest / EmbedResponse — embeddings are billed and capped separately
// from chat. A single call may carry many inputs (batch).
type EmbedRequest struct {
	Model  string
	Inputs []string
}
type EmbedResponse struct {
	Vectors      [][]float32
	PromptTokens int
	ModelVersion string
}

// Provider is the vendor-neutral interface. Implementations live in
// providers/ subdirectory. Adding a new provider means: implement this
// interface, register it via providers.Register(kind, factory), and
// optionally add a routing-rule preset.
type Provider interface {
	Kind() string
	Caps() Capabilities
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error)
}

// ProviderConfig is the row from ai_providers, decrypted, ready to instantiate.
type ProviderConfig struct {
	ID         string
	TenantID   string
	Kind       string
	Name       string
	BaseURL    string
	APIKey     string // decrypted; never log
	Region     string
	TrustLevel TrustLevel
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
