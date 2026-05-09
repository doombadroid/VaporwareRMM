package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"vaporrmm/server/internal/ai"
)

// anthropicProvider talks Anthropic's native Messages API. Tool calls and
// structured output are first-class; embeddings are NOT supported by
// Anthropic, so capability matching will route embed calls elsewhere.
type anthropicProvider struct {
	cfg ai.ProviderConfig
}

func init() {
	ai.RegisterFactory("anthropic", func(cfg ai.ProviderConfig) (ai.Provider, error) {
		return &anthropicProvider{cfg: cfg}, nil
	})
}

func (p *anthropicProvider) Kind() string { return "anthropic" }

func (p *anthropicProvider) Caps() ai.Capabilities {
	return ai.Capabilities{
		Streaming:   true,
		ToolCalling: true,
		Embeddings:  false, // route embed to OpenAI / Ollama
		JSONMode:    true,  // via "tool use" trick
		MaxContext:  200_000,
	}
}

func (p *anthropicProvider) base() string {
	if p.cfg.BaseURL != "" {
		return strings.TrimRight(p.cfg.BaseURL, "/")
	}
	return "https://api.anthropic.com/v1"
}

type anthMsg struct {
	Role    string        `json:"role"`
	Content []anthContent `json:"content"`
}
type anthContent struct {
	Type      string          `json:"type"`            // "text" | "tool_use" | "tool_result"
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`     // for tool_use
	Name      string          `json:"name,omitempty"`   // for tool_use
	Input     json.RawMessage `json:"input,omitempty"`  // for tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // for tool_result
}
type anthTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}
type anthChatReq struct {
	Model       string     `json:"model"`
	System      string     `json:"system,omitempty"`
	Messages    []anthMsg  `json:"messages"`
	MaxTokens   int        `json:"max_tokens"`
	Temperature float32    `json:"temperature,omitempty"`
	Tools       []anthTool `json:"tools,omitempty"`
}
type anthChatResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []anthContent `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (p *anthropicProvider) Chat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error) {
	// Anthropic's API splits the system prompt out of Messages.
	body := anthChatReq{
		Model:       req.Model,
		MaxTokens:   req.MaxOutputTokens,
		Temperature: req.Temperature,
	}
	if body.MaxTokens == 0 {
		body.MaxTokens = 1024 // required by Anthropic API
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			if body.System != "" {
				body.System += "\n\n"
			}
			body.System += m.Content
			continue
		}
		ac := anthContent{Type: "text", Text: m.Content}
		if m.Role == "tool" {
			ac = anthContent{Type: "tool_result", ToolUseID: m.ToolID, Text: m.Content}
		}
		body.Messages = append(body.Messages, anthMsg{Role: m.Role, Content: []anthContent{ac}})
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, anthTool{
			Name: t.Name, Description: t.Description, InputSchema: t.InputSchema,
		})
	}
	headers := map[string]string{
		"x-api-key":         p.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}
	var resp anthChatResp
	if _, _, err := doJSON(ctx, "POST", p.base()+"/messages", headers, body, &resp); err != nil {
		return ai.ChatResponse{}, err
	}
	out := ai.ChatResponse{
		FinishReason: resp.StopReason,
		PromptTokens: resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		ModelVersion: resp.Model,
	}
	var sb strings.Builder
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			sb.WriteString(c.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ai.ToolCall{
				ID: c.ID, Name: c.Name, Args: c.Input,
			})
		}
	}
	out.Content = sb.String()
	return out, nil
}

func (p *anthropicProvider) Embed(ctx context.Context, req ai.EmbedRequest) (ai.EmbedResponse, error) {
	return ai.EmbedResponse{}, fmt.Errorf("anthropic: embeddings not supported; route TaskEmbed to a different provider")
}
