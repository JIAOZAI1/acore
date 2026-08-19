package model

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// Provider is the vendor extension point. Implementations own authentication,
// transport, protocol translation, and resource cleanup.
type Provider interface {
	// ID returns a stable provider identifier, for example "openai".
	ID() string

	// Models returns provider-owned model descriptors. The returned slice must
	// be safe for the caller to modify.
	Models() []Model

	// Generate starts one generation for model. Errors returned directly mean
	// setup failed before a stream was established. Failures after setup are
	// yielded through Stream's error value.
	Generate(ctx context.Context, model Model, req Request) (Stream, error)
}

// ProviderRegistry is a concurrency-safe provider collection.
type ProviderRegistry struct {
	mu   sync.RWMutex
	byID map[string]Provider
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{byID: make(map[string]Provider)}
}

// Add registers a provider and rejects nil or duplicate registrations.
func (r *ProviderRegistry) Add(provider Provider) error {
	if provider == nil {
		return errors.New("model: nil provider")
	}
	id := provider.ID()
	if id == "" {
		return errors.New("model: empty provider ID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[id]; exists {
		return errors.New("model: provider already registered: " + id)
	}
	r.byID[id] = provider
	return nil
}

func (r *ProviderRegistry) Provider(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.byID[id]
	return provider, ok
}

// Providers returns providers ordered by ID for deterministic callers.
func (r *ProviderRegistry) Providers() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	providers := make([]Provider, 0, len(ids))
	for _, id := range ids {
		providers = append(providers, r.byID[id])
	}
	return providers
}

// LLM resolves and binds one provider/model pair.
func (r *ProviderRegistry) LLM(providerID, modelID string) (LLM, error) {
	provider, ok := r.Provider(providerID)
	if !ok {
		return nil, errors.New("model: provider not found: " + providerID)
	}
	for _, candidate := range provider.Models() {
		if candidate.ID == modelID {
			return Bind(provider, candidate)
		}
	}
	return nil, errors.New("model: model not found: " + providerID + "/" + modelID)
}
