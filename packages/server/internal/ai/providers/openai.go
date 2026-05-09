package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"vaporrmm/server/internal/ai"
)

// openaiProvider serves any OpenAI-compatible endpoint. That covers OpenAI
// itself (api.openai.com), xAI, DeepInfra, Mistral, Together AI, and any
// self-hosted vLLM / LocalAI / llama-cpp-server in OpenAI compat mode.
// The differentiator is BaseURL.
type openaiProvider struct {
	cfg ai.ProviderConfig
}

func init() {
	ai.RegisterFactory("openai", func(cfg ai.ProviderConfig) (ai.Provider, error) {
		return &openaiProvider{cfg: cfg}, nil
	})
	// "openai_compat" is the same factory under a different label so operators
	// can self-document a self-hosted endpoint as something other than OpenAI.
	ai.RegisterFactory("openai_compat", func(cfg ai.ProviderConfig) (ai.Provider, error) {
		return &openaiProvider{cfg: cfg}, nil
	})
}

func (p *openaiProvider) Kind() string { return "openai" }

func (p *openaiProvider) Caps() ai.Capabilities {
	return ai.Capabilities{
		Streaming:   true,
		ToolCalling: true,
		Embeddings:  true,
		JSONMode:    true,
		MaxContext:  128_000,
	}
}

func (p *openaiProvider) base() string {
	if p.cfg.BaseURL != "" {
		return strings.TrimRight(p.cfg.BaseURL, "/")
	}
	return "https://api.openai.com/v1"
}

type oaiChatReq struct {
	Model       string         `json:"model"`
	Messages    []oaiMsg       `json:"messages"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Temperature float32        `json:"temperature,omitempty"`
	Tools       []oaiTool      `json:"tools,omitempty"`
	ResponseFmt *oaiResponseFmt `json:"response_format,omitempty"`
}
type oaiMsg struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []oaiToolCall  `json:"tool_calls,omitempty"`
}
type oaiTool struct {
	Type     string         `json:"type"` // always "function"
	Function oaiFunctionDef `json:"function"`
}
type oaiFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}
type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type oaiResponseFmt struct {
	Type   string          `json:"type"` // "json_object" or "json_schema"
	Schema json.RawMessage `json:"schema,omitempty"`
}
type oaiChatResp struct {
	ID      string  `json:"id"`
	Model   string  `json:"model"`
	Choices []struct {
		Message      oaiMsg `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (p *openaiProvider) Chat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error) {
	body := oaiChatReq{
		Model:       req.Model,
		MaxTokens:   req.MaxOutputTokens,
		Temperature: req.Temperature,
	}
	for _, m := range req.Messages {
		om := oaiMsg{Role: m.Role, Content: m.Content, Name: m.Name, ToolCallID: m.ToolID}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, oaiToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: tc.Name, Arguments: string(tc.Args)},
			})
		}
		body.Messages = append(body.Messages, om)
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, oaiTool{
			Type: "function",
			Function: oaiFunctionDef{
				Name: t.Name, Description: t.Description, Parameters: t.InputSchema,
			},
		})
	}
	if len(req.JSONSchema) > 0 {
		body.ResponseFmt = &oaiResponseFmt{Type: "json_schema", Schema: req.JSONSchema}
	}
	headers := map[string]string{}
	if p.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + p.cfg.APIKey
	}
	var resp oaiChatResp
	if _, _, err := doJSON(ctx, "POST", p.base()+"/chat/completions", headers, body, &resp); err != nil {
		return ai.ChatResponse{}, err
	}
	if len(resp.Choices) == 0 {
		return ai.ChatResponse{}, fmt.Errorf("openai: empty choices")
	}
	c := resp.Choices[0]
	out := ai.ChatResponse{
		Content:      c.Message.Content,
		FinishReason: c.FinishReason,
		PromptTokens: resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		ModelVersion: resp.Model,
	}
	for _, tc := range c.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ai.ToolCall{
			ID: tc.ID, Name: tc.Function.Name, Args: []byte(tc.Function.Arguments),
		})
	}
	return out, nil
}

type oaiEmbedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}
type oaiEmbedResp struct {
	Model string `json:"model"`
	Data  []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}

func (p *openaiProvider) Embed(ctx context.Context, req ai.EmbedRequest) (ai.EmbedResponse, error) {
	body := oaiEmbedReq{Model: req.Model, Input: req.Inputs}
	headers := map[string]string{}
	if p.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + p.cfg.APIKey
	}
	var resp oaiEmbedResp
	if _, _, err := doJSON(ctx, "POST", p.base()+"/embeddings", headers, body, &resp); err != nil {
		return ai.EmbedResponse{}, err
	}
	out := ai.EmbedResponse{
		PromptTokens: resp.Usage.PromptTokens,
		ModelVersion: resp.Model,
	}
	for _, d := range resp.Data {
		out.Vectors = append(out.Vectors, d.Embedding)
	}
	return out, nil
}
