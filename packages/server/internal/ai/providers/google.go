package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"vaporrmm/server/internal/ai"
)

// googleProvider talks Google's Gemini generative-language v1beta API.
// Tool calling = "function declarations". Structured output via response
// MIME type. Embeddings supported via the embed-content endpoint.
type googleProvider struct {
	cfg ai.ProviderConfig
}

func init() {
	ai.RegisterFactory("google", func(cfg ai.ProviderConfig) (ai.Provider, error) {
		return &googleProvider{cfg: cfg}, nil
	})
}

func (p *googleProvider) Kind() string { return "google" }

func (p *googleProvider) Caps() ai.Capabilities {
	return ai.Capabilities{
		Streaming:   true,
		ToolCalling: true,
		Embeddings:  true,
		JSONMode:    true,
		MaxContext:  1_000_000,
	}
}

func (p *googleProvider) base() string {
	if p.cfg.BaseURL != "" {
		return strings.TrimRight(p.cfg.BaseURL, "/")
	}
	return "https://generativelanguage.googleapis.com/v1beta"
}

type gPart struct {
	Text         string          `json:"text,omitempty"`
	FunctionCall *gFunctionCall  `json:"functionCall,omitempty"`
	FunctionResp *gFunctionResp  `json:"functionResponse,omitempty"`
}
type gFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}
type gFunctionResp struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}
type gContent struct {
	Role  string  `json:"role"` // "user" | "model"
	Parts []gPart `json:"parts"`
}
type gFnDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}
type gTool struct {
	FunctionDeclarations []gFnDecl `json:"functionDeclarations"`
}
type gGenConfig struct {
	MaxOutputTokens  int     `json:"maxOutputTokens,omitempty"`
	Temperature      float32 `json:"temperature,omitempty"`
	ResponseMimeType string  `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
}
type gChatReq struct {
	Contents         []gContent  `json:"contents"`
	SystemInstruction *gContent  `json:"systemInstruction,omitempty"`
	Tools            []gTool     `json:"tools,omitempty"`
	GenerationConfig *gGenConfig `json:"generationConfig,omitempty"`
}
type gChatResp struct {
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content      gContent `json:"content"`
		FinishReason string   `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func (p *googleProvider) Chat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error) {
	body := gChatReq{}
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			body.SystemInstruction = &gContent{Role: "user", Parts: []gPart{{Text: m.Content}}}
		case "tool":
			body.Contents = append(body.Contents, gContent{
				Role: "user",
				Parts: []gPart{{FunctionResp: &gFunctionResp{
					Name: m.Name, Response: json.RawMessage(`{"result":` + jsonString(m.Content) + `}`),
				}}},
			})
		default:
			role := m.Role
			if role == "assistant" {
				role = "model"
			}
			parts := []gPart{}
			if m.Content != "" {
				parts = append(parts, gPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, gPart{FunctionCall: &gFunctionCall{Name: tc.Name, Args: tc.Args}})
			}
			body.Contents = append(body.Contents, gContent{Role: role, Parts: parts})
		}
	}
	if len(req.Tools) > 0 {
		decls := make([]gFnDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, gFnDecl{Name: t.Name, Description: t.Description, Parameters: t.InputSchema})
		}
		body.Tools = []gTool{{FunctionDeclarations: decls}}
	}
	if req.MaxOutputTokens > 0 || len(req.JSONSchema) > 0 {
		gc := &gGenConfig{MaxOutputTokens: req.MaxOutputTokens, Temperature: req.Temperature}
		if len(req.JSONSchema) > 0 {
			gc.ResponseMimeType = "application/json"
			gc.ResponseSchema = req.JSONSchema
		}
		body.GenerationConfig = gc
	}
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", p.base(), req.Model, p.cfg.APIKey)
	var resp gChatResp
	if _, _, err := doJSON(ctx, "POST", url, nil, body, &resp); err != nil {
		return ai.ChatResponse{}, err
	}
	if len(resp.Candidates) == 0 {
		return ai.ChatResponse{}, fmt.Errorf("google: empty candidates")
	}
	cand := resp.Candidates[0]
	out := ai.ChatResponse{
		FinishReason: cand.FinishReason,
		PromptTokens: resp.UsageMetadata.PromptTokenCount,
		OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
		ModelVersion: resp.ModelVersion,
	}
	var sb strings.Builder
	for _, part := range cand.Content.Parts {
		if part.Text != "" {
			sb.WriteString(part.Text)
		}
		if part.FunctionCall != nil {
			out.ToolCalls = append(out.ToolCalls, ai.ToolCall{
				Name: part.FunctionCall.Name, Args: part.FunctionCall.Args,
			})
		}
	}
	out.Content = sb.String()
	return out, nil
}

type gEmbedReq struct {
	Model   string   `json:"model"`
	Content gContent `json:"content"`
}
type gEmbedResp struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}

func (p *googleProvider) Embed(ctx context.Context, req ai.EmbedRequest) (ai.EmbedResponse, error) {
	out := ai.EmbedResponse{ModelVersion: req.Model}
	url := fmt.Sprintf("%s/models/%s:embedContent?key=%s", p.base(), req.Model, p.cfg.APIKey)
	for _, input := range req.Inputs {
		var resp gEmbedResp
		body := gEmbedReq{Model: "models/" + req.Model, Content: gContent{Parts: []gPart{{Text: input}}}}
		if _, _, err := doJSON(ctx, "POST", url, nil, body, &resp); err != nil {
			return ai.EmbedResponse{}, err
		}
		out.Vectors = append(out.Vectors, resp.Embedding.Values)
		// Google does not report token count in this endpoint; we estimate.
		out.PromptTokens += len(input) / 4
	}
	return out, nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
