package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type Capabilities struct {
	Parts             map[app.MessagePartKind]bool
	AudioDispositions map[app.MessagePartDisposition]bool
}

func (c Capabilities) Validate(content app.MessageContent) error {
	if len(content.Parts) == 0 {
		return errors.New("delivery content requires at least one part")
	}
	for _, part := range content.Parts {
		if !c.Parts[part.Kind] {
			return fmt.Errorf("content kind %q is not supported", part.Kind)
		}
		if part.Kind == app.MessagePartAudio && len(c.AudioDispositions) > 0 && !c.AudioDispositions[part.Disposition] {
			return fmt.Errorf("audio disposition %q is not supported", part.Disposition)
		}
	}
	return nil
}

type Provider interface {
	Key() string
	Capabilities() Capabilities
	Deliver(context.Context, app.MessageEndpoint, app.DeliveryRequest) (app.DeliveryReceipt, error)
}

type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: map[string]Provider{}}
}

func (r *ProviderRegistry) Register(provider Provider) error {
	if r == nil || provider == nil {
		return errors.New("delivery provider is required")
	}
	key := normalizeProviderKey(provider.Key())
	if key == "" {
		return errors.New("delivery provider key is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[key]; exists {
		return fmt.Errorf("delivery provider %q is already registered", key)
	}
	r.providers[key] = provider
	return nil
}

func (r *ProviderRegistry) Deliver(ctx context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	key := normalizeProviderKey(endpoint.ProviderKey)
	r.mu.RLock()
	provider, ok := r.providers[key]
	r.mu.RUnlock()
	if !ok {
		return failedReceipt(endpoint, request, "delivery provider is not registered"), fmt.Errorf("delivery provider %q is not registered", key)
	}
	// Capability negotiation covers the complete payload before the provider
	// can perform its first external send.
	if err := provider.Capabilities().Validate(request.Content); err != nil {
		return failedReceipt(endpoint, request, err.Error()), err
	}
	return provider.Deliver(ctx, endpoint, request)
}

func normalizeProviderKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
