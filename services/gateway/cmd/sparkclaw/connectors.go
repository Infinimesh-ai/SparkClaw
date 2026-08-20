package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connector"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/telegram"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixin"
)

type connectorAssembly struct {
	registry    *connector.Registry
	credentials credential.CredentialVault
	endpoints   *messagecontrol.EndpointRegistry
	delivery    *delivery.Gateway
}

func newConnectorAssembly(
	cfg config.Config,
	st store.Store,
	runtime connectorruntime.AgentRuntime,
	transcriber speech.Transcriber,
	endpoints *messagecontrol.EndpointRegistry,
) (*connectorAssembly, error) {
	if endpoints == nil {
		return nil, fmt.Errorf("assemble connectors: endpoint registry is required")
	}
	telegramConfig := cfg.Tools.Notifications.Channels["telegram"]
	vault := credential.New(st, credential.Options{
		Key:        cfg.State.CredentialKey,
		KeyFile:    cfg.State.CredentialKeyFile,
		AutoCreate: true,
	})
	// Warn unconditionally: since runtime channel control (0b75ce9) a channel
	// can be enabled later through the API with the config flag still false,
	// and that path is exactly the one that needs the vault.
	if err := vault.Ready(); err != nil {
		slog.Warn("connector credential vault is unavailable", "code", credential.ErrorCode(err))
	}

	registry := connector.NewRegistry(cfg, st)
	endpoints.WithChannelEnabled(registry.Enabled)
	providers, err := registry.ProviderRegistry()
	if err != nil {
		return nil, fmt.Errorf("assemble delivery providers: %w", err)
	}
	routes := messagecontrol.NewReturnRouteResolver(endpoints)
	deliveryGateway := delivery.NewGateway(endpoints, providers, delivery.NewPersistentWebDelivery(st))
	resultDeliverer := delivery.NewWorkflowResultDeliverer(routes, deliveryGateway)
	telegramNotifications := telegram.NewNotificationAdapter(st, vault, telegramConfig)
	telegramService := telegram.NewService(
		st,
		telegramConfig,
		vault,
		telegram.NewDispatcher(st, runtime, cfg, telegramSpeechTranscriber{transcriber: transcriber}).WithResultDeliverer(resultDeliverer),
	)
	if err := registry.Register(connector.Registration{
		Channel:   "telegram",
		SetupKind: app.ConnectorSetupSecret,
		Binding:   binding.NewTelegramAdapter("telegram", telegramConfig, vault),
		Provider:  telegramNotifications,
		Runtime:   telegramService,
		CancelBinding: func(record app.NotificationBinding) {
			telegramService.CancelBinding(record.ID)
		},
	}); err != nil {
		return nil, fmt.Errorf("register Telegram connector: %w", err)
	}
	if err := registry.Register(connector.Registration{
		Channel: "mcp", SetupKind: app.ConnectorSetupExternal, Binding: mcpaccess.ConnectorAdapter{},
		Provider: mcpaccess.NewProvider(st), ExternalManaged: true,
	}); err != nil {
		return nil, fmt.Errorf("register MCP connector: %w", err)
	}

	weixinConfig := cfg.Tools.Notifications.Channels["weixin"]
	weixinSyncer := weixin.NewSyncer(st).
		WithCredentialVault(vault).
		WithConfig(cfg).
		WithDispatcher(weixin.NewDispatcherWithConfig(st, runtime, cfg).WithCredentialVault(vault).WithResultDeliverer(resultDeliverer))
	weixinNotifications := notification.NewWeixinAdapter("weixin", weixinConfig, st, vault)
	if err := registry.Register(connector.Registration{
		Channel:   "weixin",
		SetupKind: app.ConnectorSetupQR,
		Binding:   binding.NewWeixinAdapter("weixin", weixinConfig),
		Provider:  weixinNotifications,
		Runtime:   weixinSyncer,
	}); err != nil {
		return nil, fmt.Errorf("register Weixin connector: %w", err)
	}

	return &connectorAssembly{
		registry: registry, credentials: vault, endpoints: endpoints,
		delivery: deliveryGateway,
	}, nil
}

type telegramSpeechTranscriber struct {
	transcriber speech.Transcriber
}

func (a telegramSpeechTranscriber) Available(ctx context.Context) error {
	if a.transcriber == nil {
		return speech.NewError(speech.CodeDisabled, "speech transcription is disabled", false, nil)
	}
	status := a.transcriber.Status(ctx)
	if !status.Enabled {
		return speech.NewError(speech.CodeDisabled, "speech transcription is disabled", false, nil)
	}
	if !status.Ready {
		return speech.NewError(speech.CodeUnavailable, "speech transcription is unavailable", true, nil)
	}
	return nil
}

func (a telegramSpeechTranscriber) Transcribe(ctx context.Context, request telegram.VoiceTranscriptionRequest) (string, error) {
	if a.transcriber == nil {
		return "", speech.NewError(speech.CodeDisabled, "speech transcription is disabled", false, nil)
	}
	result, err := a.transcriber.Transcribe(ctx, speech.Request{
		RequestID:  request.RequestID,
		SessionID:  request.SessionID,
		Language:   request.Language,
		PCM16WAV:   request.PCM16WAV,
		DurationMS: int64(request.DurationMS),
	})
	if err != nil {
		return "", err
	}
	return result.Text, nil
}
