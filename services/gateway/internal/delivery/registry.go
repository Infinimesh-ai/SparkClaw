package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func ValidateCapabilities(c app.DeliveryCapabilities, content app.MessageContent) error {
	if len(content.Parts) == 0 {
		return NewError(CodePartUnsupported, "delivery content requires at least one part", "blocked")
	}
	if c.MaxParts > 0 && len(content.Parts) > c.MaxParts {
		return NewError(CodePayloadTooLarge, "delivery contains too many parts", "blocked")
	}
	kinds := make(map[app.MessagePartKind]bool, len(c.Kinds))
	for _, kind := range c.Kinds {
		kinds[kind] = true
	}
	dispositions := make(map[app.MessagePartDisposition]bool, len(c.Dispositions))
	for _, disposition := range c.Dispositions {
		dispositions[disposition] = true
	}
	var total int64
	for _, part := range content.Parts {
		if !kinds[part.Kind] {
			return NewError(CodePartUnsupported, fmt.Sprintf("content kind %q is not supported", part.Kind), "blocked")
		}
		if strings.TrimSpace(part.ID) == "" {
			return NewError(CodePartUnsupported, "delivery part id is required", "blocked")
		}
		if part.Kind == app.MessagePartText && strings.TrimSpace(part.Text) == "" {
			return NewError(CodePartUnsupported, fmt.Sprintf("text part %q is empty", part.ID), "blocked")
		}
		if part.Kind != app.MessagePartText && strings.TrimSpace(part.ArtifactID) == "" && (part.Resource == nil || strings.TrimSpace(part.Resource.Ref) == "") {
			return NewError(CodeArtifactInvalid, fmt.Sprintf("binary part %q has no governed resource", part.ID), "blocked")
		}
		if len(dispositions) > 0 && !dispositions[part.Disposition] {
			return NewError(CodePartUnsupported, fmt.Sprintf("disposition %q is not supported", part.Disposition), "blocked")
		}
		bytes := int64(part.Bytes)
		total += bytes
		if limit := c.MaxBytesByKind[part.Kind]; limit > 0 && bytes > limit {
			return NewError(CodePayloadTooLarge, fmt.Sprintf("part %q exceeds the channel limit", part.ID), "blocked")
		}
	}
	if c.MaxTotalBytes > 0 && total > c.MaxTotalBytes {
		return NewError(CodePayloadTooLarge, "delivery payload exceeds the channel limit", "blocked")
	}
	return nil
}

type Provider interface {
	Key() string
	Capabilities() app.DeliveryCapabilities
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
	if err := ValidateCapabilities(provider.Capabilities(), request.Content); err != nil {
		return failedReceipt(endpoint, request, err.Error()), err
	}
	return provider.Deliver(ctx, endpoint, request)
}

func (r *ProviderRegistry) Capabilities(providerKey string) (app.DeliveryCapabilities, bool) {
	if r == nil {
		return app.DeliveryCapabilities{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[normalizeProviderKey(providerKey)]
	if !ok {
		return app.DeliveryCapabilities{}, false
	}
	return provider.Capabilities(), true
}

func normalizeProviderKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
