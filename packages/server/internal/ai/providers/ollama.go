package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"vaporrmm/server/internal/ai"
)

// ollamaProvider talks Ollama's /api/chat and /api/embeddings endpoints.
// Tool calling support depends on the underlying model (Llama 3.1+, Qwen 2.5
// Coder, etc.); we advertise it as a capability and let validation catch
// model-by-model gaps. TrustLevel is forced to self_hosted at build time so
// the rung gate applies the extra approval requirement.
type ollamaProvider struct {
	cfg ai.ProviderConfig
}

func init() {
	ai.RegisterFactory("ollama", func(cfg ai.ProviderConfig) (ai.Provider, error) {
		// Ollama is always self-hosted.
		cfg.TrustLevel = ai.TrustSelfHosted
		return &ollamaProvider{cfg: cfg}, nil
	})
}

func (p *ollamaProvider) Kind() string { return "ollama" }

func (p *ollamaProvider) Caps() ai.Capabilities {
	return ai.Capabilities{
		Streaming:   true,
		ToolCalling: true,
		Embeddings:  true,
		JSONMode:    true,
		MaxContext:  32_000, // model-dependent; conservative default
	}
}

func (p *ollamaProvider) base() string {
	if p.cfg.BaseURL != "" {
		return strings.TrimRight(p.cfg.BaseURL, "/")
	}
	return "http://localhost:11434"
}

type ollMsg struct {
	Role      string        `json:"role"`
	Content   string        `json:"content,omitempty"`
	ToolCalls []ollToolCall `json:"tool_calls,omitempty"`
}
type ollToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}
type ollTool struct {
	Type     string `json:"type"` // always "function"
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}
type ollChatReq struct {
	Model    string    `json:"model"`
	Messages []ollMsg  `json:"messages"`
	Stream   bool      `json:"stream"`
	Tools    []ollTool `json:"tools,omitempty"`
	Format   string    `json:"format,omitempty"` // "json" for json mode
	Options  struct {
		NumPredict  int     `json:"num_predict,omitempty"`
		Temperature float32 `json:"temperature,omitempty"`
	} `json:"options,omitempty"`
}
type ollChatResp struct {
	Model           string `json:"model"`
	Message         ollMsg `json:"message"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

func (p *ollamaProvider) Chat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error) {
	body := ollChatReq{Model: req.Model, Stream: false}
	body.Options.NumPredict = req.MaxOutputTokens
	body.Options.Temperature = req.Temperature
	if len(req.JSONSchema) > 0 {
		body.Format = "json"
	}
	for _, m := range req.Messages {
		om := ollMsg{Role: m.Role, Content: m.Content}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, ollToolCall{
				Function: struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}{Name: tc.Name, Arguments: tc.Args},
			})
		}
		body.Messages = append(body.Messages, om)
	}
	for _, t := range req.Tools {
		var tool ollTool
		tool.Type = "function"
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		tool.Function.Parameters = t.InputSchema
		body.Tools = append(body.Tools, tool)
	}
	var resp ollChatResp
	if _, _, err := doJSON(ctx, "POST", p.base()+"/api/chat", nil, body, &resp); err != nil {
		return ai.ChatResponse{}, err
	}
	out := ai.ChatResponse{
		Content:      resp.Message.Content,
		FinishReason: resp.DoneReason,
		PromptTokens: resp.PromptEvalCount,
		OutputTokens: resp.EvalCount,
		ModelVersion: resp.Model,
	}
	for _, tc := range resp.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ai.ToolCall{
			Name: tc.Function.Name, Args: tc.Function.Arguments,
		})
	}
	return out, nil
}

type ollEmbedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}
type ollEmbedResp struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

func (p *ollamaProvider) Embed(ctx context.Context, req ai.EmbedRequest) (ai.EmbedResponse, error) {
	body := ollEmbedReq{Model: req.Model, Input: req.Inputs}
	var resp ollEmbedResp
	if _, _, err := doJSON(ctx, "POST", p.base()+"/api/embed", nil, body, &resp); err != nil {
		return ai.EmbedResponse{}, err
	}
	if len(resp.Embeddings) == 0 {
		return ai.EmbedResponse{}, fmt.Errorf("ollama: empty embeddings")
	}
	// Ollama doesn't report tokens; estimate from input length.
	tokens := 0
	for _, s := range req.Inputs {
		tokens += len(s) / 4
	}
	return ai.EmbedResponse{
		Vectors:      resp.Embeddings,
		PromptTokens: tokens,
		ModelVersion: resp.Model,
	}, nil
}
