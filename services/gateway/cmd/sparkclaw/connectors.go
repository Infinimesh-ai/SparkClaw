package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connector"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
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
) (*connectorAssembly, error) {
	telegramConfig := cfg.Tools.Notifications.Channels["telegram"]
	vault := credential.New(st, credential.Options{
		Key:        cfg.State.CredentialKey,
		KeyFile:    cfg.State.CredentialKeyFile,
		AutoCreate: telegramConfig.Enabled,
	})
	if telegramConfig.Enabled {
		if err := vault.Ready(); err != nil {
			slog.Warn("connector credential vault is unavailable", "code", credential.ErrorCode(err))
		}
	}

	registry := connector.NewRegistry(cfg)
	providers, err := registry.ProviderRegistry()
	if err != nil {
		return nil, fmt.Errorf("assemble delivery providers: %w", err)
	}
	endpoints := messagecontrol.NewEndpointRegistry(st)
	routes := messagecontrol.NewReturnRouteResolver(endpoints)
	deliveryGateway := delivery.NewGateway(endpoints, providers, delivery.LocalWebDelivery{})
	resultDeliverer := delivery.NewWorkflowResultDeliverer(routes, deliveryGateway)
	telegramNotifications := telegram.NewNotificationAdapter(st, vault, telegramConfig)
	telegramService := telegram.NewService(
		st,
		telegramConfig,
		vault,
		telegram.NewDispatcher(st, runtime, cfg, telegramSpeechTranscriber{transcriber: transcriber}).WithResultDeliverer(resultDeliverer),
	)
	if err := registry.Register(connector.Registration{
		Channel:  "telegram",
		Binding:  binding.NewTelegramAdapter("telegram", telegramConfig, vault),
		Provider: telegramNotifications,
		Runtime:  telegramService,
		CancelBinding: func(record app.NotificationBinding) {
			telegramService.CancelBinding(record.ID)
		},
	}); err != nil {
		return nil, fmt.Errorf("register Telegram connector: %w", err)
	}

	weixinConfig := cfg.Tools.Notifications.Channels["weixin"]
	weixinSyncer := weixin.NewSyncer(st).
		WithConfig(cfg).
		WithDispatcher(weixin.NewDispatcherWithConfig(st, runtime, cfg).WithResultDeliverer(resultDeliverer))
	weixinNotifications := notification.NewWeixinAdapter("weixin", weixinConfig, st)
	if err := registry.Register(connector.Registration{
		Channel:  "weixin",
		Binding:  newWeixinBindingAdapter("weixin", weixinConfig),
		Provider: weixinNotifications,
		Runtime:  weixinSyncer,
	}); err != nil {
		return nil, fmt.Errorf("register Weixin connector: %w", err)
	}

	return &connectorAssembly{
		registry: registry, credentials: vault, endpoints: endpoints,
		delivery: deliveryGateway,
	}, nil
}

func newWeixinBindingAdapter(channel string, cfg config.NotificationChannelConfig) binding.Adapter {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "openclaw-weixin-qr", "openclaw-weixin-login-qr":
		return binding.NewWeixinQRAdapter(channel, cfg)
	default:
		return binding.NewManualWeixinAdapter(channel, cfg)
	}
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
