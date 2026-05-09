package ai

import (
	"context"
	"fmt"
	"sync"
)

// Factory builds a Provider from a decrypted ProviderConfig. Each backend
// registers one factory under its `kind` string at init time.
type Factory func(cfg ProviderConfig) (Provider, error)

var (
	factoriesMu sync.RWMutex
	factories   = map[string]Factory{}
)

// RegisterFactory attaches a Factory to a provider kind. Called from
// init() in each providers/<kind>.go file. Re-registering panics — that
// would always be a programming bug.
func RegisterFactory(kind string, f Factory) {
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	if _, exists := factories[kind]; exists {
		panic("ai: provider factory already registered for kind " + kind)
	}
	factories[kind] = f
}

// KnownKinds is what the dashboard offers in the provider-config dropdown.
func KnownKinds() []string {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	return out
}

// Build instantiates a Provider from a config row. Returns an error (not a
// panic) for an unknown kind so a stale row doesn't crash the server after a
// provider package is removed.
func Build(cfg ProviderConfig) (Provider, error) {
	factoriesMu.RLock()
	f, ok := factories[cfg.Kind]
	factoriesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("ai: no factory for provider kind %q", cfg.Kind)
	}
	return f(cfg)
}

// noopProvider is what we return when a tenant has AI enabled but no
// provider configured yet. Calling Chat / Embed on it is a clear error
// distinct from "real provider returned an error".
type noopProvider struct{}

func (noopProvider) Kind() string       { return "noop" }
func (noopProvider) Caps() Capabilities { return Capabilities{} }
func (noopProvider) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, fmt.Errorf("ai: no provider configured for this tenant")
}
func (noopProvider) Embed(context.Context, EmbedRequest) (EmbedResponse, error) {
	return EmbedResponse{}, fmt.Errorf("ai: no provider configured for this tenant")
}

// Noop returns the no-op provider — used by the chokepoint when a tenant has
// AI enabled but no provider rows yet.
func Noop() Provider { return noopProvider{} }
