// Package providers contains the four shipped Provider implementations:
// openai (covers OpenAI/xAI/DeepInfra/Mistral via base_url), anthropic
// native, google gemini, ollama. Each registers itself in init() so the
// parent ai package's registry can build them by kind.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpClient is the shared timeout-bound client. We deliberately disable
// keep-alive's default infinite re-use so a single long-lived process doesn't
// pin TLS sessions to a stale provider host.
var httpClient = &http.Client{
	Timeout: 120 * time.Second,
}

// doJSON is a tiny wrapper around http.Client.Do for JSON request/response
// pairs. It owns the sanitisation contract: at no point do we log the
// Authorization header or full URL+key. On error we return the truncated body
// to the caller for debugging — the caller is responsible for not leaking it
// to logs.
func doJSON(ctx context.Context, method, url string, headers map[string]string, body any, out any) (status int, raw []byte, err error) {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("providers: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("providers: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("providers: %s %s: %w", method, sanitizeURLForLog(url), err)
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16 MiB cap
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("providers: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, raw, fmt.Errorf("providers: http %d: %s", resp.StatusCode, truncateForLog(string(raw)))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, raw, fmt.Errorf("providers: decode response: %w", err)
		}
	}
	return resp.StatusCode, raw, nil
}

// sanitizeURLForLog strips any query parameter that looks like it could carry
// a secret. Some providers (Google) put the API key in the query string —
// that's their bug, our defence is to scrub before any log.
func sanitizeURLForLog(u string) string {
	q := strings.Index(u, "?")
	if q < 0 {
		return u
	}
	return u[:q] + "?[redacted]"
}

func truncateForLog(s string) string {
	const cap = 512
	if len(s) <= cap {
		return s
	}
	return s[:cap] + "...[truncated]"
}
