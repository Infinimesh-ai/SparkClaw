package connector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Registration struct {
	Channel         string
	SetupKind       string
	Binding         binding.Adapter
	BindingProvider string
	CredentialKind  string
	Provider        delivery.Provider
	Runtime         connectorruntime.Runtime
	CancelBinding   func(app.NotificationBinding)
	ExternalManaged bool
}

func (r *Registry) ProviderRegistry() (*delivery.ProviderRegistry, error) {
	if r == nil || r.providers == nil {
		return nil, errors.New("connector provider registry is unavailable")
	}
	return r.providers, nil
}

type Registry struct {
	cfg            config.Config
	store          store.ConnectorRepository
	registrations  map[string]Registration
	providers      *delivery.ProviderRegistry
	settingsMu     sync.RWMutex
	settings       map[string]connectorSettingCacheEntry
	settingsLoaded bool
	runtimeMu      sync.Mutex
	runtimeCtx     context.Context
	runtimeRuns    map[string]*runtimeRun
	runtimeErrors  map[string]string
	started        bool
	credentials    CredentialLifecycle
	bindingLocks   sync.Map
}

type CredentialLifecycle interface {
	Ready() error
	Seal(context.Context, string, string, []byte) (string, error)
	Delete(context.Context, string) error
	AbortSeal(context.Context, string, string) error
}

func NewRegistry(cfg config.Config, stores ...store.ConnectorRepository) *Registry {
	var st store.ConnectorRepository
	if len(stores) > 0 {
		st = stores[0]
	}
	return &Registry{
		cfg:           cfg,
		store:         st,
		registrations: map[string]Registration{},
		providers:     delivery.NewProviderRegistry(),
		settings:      map[string]connectorSettingCacheEntry{},
		runtimeRuns:   map[string]*runtimeRun{},
		runtimeErrors: map[string]string{},
	}
}

func (r *Registry) WithCredentialLifecycle(credentials CredentialLifecycle) *Registry {
	r.credentials = credentials
	return r
}

func (r *Registry) Register(registration Registration) error {
	channel := normalizeChannel(registration.Channel)
	if channel == "" {
		return errors.New("connector channel is required")
	}
	if _, configured := r.cfg.Tools.Notifications.Channels[channel]; !configured {
		return errors.New("connector channel is not configured")
	}
	if _, exists := r.registrations[channel]; exists {
		return errors.New("connector channel is already registered")
	}
	if registration.Provider != nil {
		if normalizeChannel(registration.Provider.Key()) != channel {
			return fmt.Errorf("connector channel %q does not match delivery provider key %q", channel, registration.Provider.Key())
		}
		if err := r.providers.Register(managedProvider{registry: r, provider: registration.Provider}); err != nil {
			return err
		}
	}
	registration.Channel = channel
	r.registrations[channel] = registration
	return nil
}

func (r *Registry) BindingRouter() binding.Router {
	router := binding.NewBaseRouter(r.cfg).WithChannelEnabled(r.Enabled)
	for _, channel := range r.channels() {
		registration := r.registrations[channel]
		if registration.Binding != nil {
			router = router.WithAdapter(channel, registration.Binding)
		}
	}
	return router
}

func (r *Registry) CancelBinding(bindingRecord app.NotificationBinding) {
	registration, ok := r.registrations[normalizeChannel(bindingRecord.Channel)]
	if ok && registration.CancelBinding != nil {
		registration.CancelBinding(bindingRecord)
	}
}

func (r *Registry) channels() []string {
	channels := make([]string, 0, len(r.registrations))
	for channel := range r.registrations {
		channels = append(channels, channel)
	}
	slices.Sort(channels)
	return channels
}

func normalizeChannel(channel string) string {
	return strings.ToLower(strings.TrimSpace(channel))
}
