package connector

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Registration struct {
	Channel       string
	Binding       binding.Adapter
	Notification  notification.Adapter
	Runtime       connectorruntime.Runtime
	CancelBinding func(app.NotificationBinding)
}

type Registry struct {
	cfg           config.Config
	store         store.Store
	registrations map[string]Registration
}

func NewRegistry(cfg config.Config, st store.Store) *Registry {
	return &Registry{
		cfg:           cfg,
		store:         st,
		registrations: map[string]Registration{},
	}
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
	registration.Channel = channel
	r.registrations[channel] = registration
	return nil
}

func (r *Registry) BindingRouter() binding.Router {
	router := binding.NewBaseRouter(r.cfg)
	for _, channel := range r.channels() {
		registration := r.registrations[channel]
		if registration.Binding != nil {
			router = router.WithAdapter(channel, registration.Binding)
		}
	}
	return router
}

func (r *Registry) NotificationRouter() notification.Router {
	router := notification.NewBaseRouter(r.store)
	for _, channel := range r.channels() {
		registration := r.registrations[channel]
		channelCfg := r.cfg.Tools.Notifications.Channels[channel]
		if channelCfg.Enabled && registration.Notification != nil {
			router = router.WithAdapter(channel, registration.Notification)
		}
	}
	return router
}

func (r *Registry) Start(ctx context.Context) {
	for _, channel := range r.channels() {
		registration := r.registrations[channel]
		channelCfg := r.cfg.Tools.Notifications.Channels[channel]
		if !channelCfg.Enabled || registration.Runtime == nil {
			continue
		}
		go func(channel string, runtime connectorruntime.Runtime) {
			if err := runtime.Run(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("connector runtime stopped", "channel", channel, "code", "connector_runtime_failed")
			}
		}(channel, registration.Runtime)
	}
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
