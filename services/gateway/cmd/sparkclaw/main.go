package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/reminder"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/skills"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/telegram"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixin"
)

func main() {
	configPath := flag.String("config", "configs/sparkclaw.default.json", "path to SparkClaw config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	st, err := newStore(cfg)
	if err != nil {
		slog.Error("failed to initialize store", "error", err)
		os.Exit(1)
	}
	if closer, ok := st.(interface{ Close() }); ok {
		defer closer.Close()
	}
	artifactStore := artifact.NewStore(cfg.Storage)
	tools := toolhub.New(cfg, st).WithArtifactStore(artifactStore)
	defer tools.Close()
	policyEngine := policy.New(cfg)
	models := modelrouter.New(cfg)
	transcriber, err := speech.New(cfg.Speech)
	if err != nil {
		slog.Error("failed to initialize speech adapter", "error", err)
		os.Exit(1)
	}
	defer transcriber.Close()
	traces := trace.NewWriterFromConfig(cfg)
	runtime := agent.NewRuntimeWithSkills(st, tools, policyEngine, models, traces, skills.NewRegistry(cfg)).WithArtifactStore(artifactStore)
	telegramConfig := cfg.Tools.Notifications.Channels["telegram"]
	credentialVault := credential.New(st, credential.Options{
		Key:        cfg.State.CredentialKey,
		KeyFile:    cfg.State.CredentialKeyFile,
		AutoCreate: telegramConfig.Enabled,
	})
	if telegramConfig.Enabled {
		if err := credentialVault.Ready(); err != nil {
			slog.Warn("connector credential vault is unavailable", "code", credential.ErrorCode(err))
		}
	}
	telegramService := telegram.NewService(
		st,
		telegramConfig,
		credentialVault,
		telegram.NewDispatcher(st, runtime, cfg, telegramSpeechTranscriber{transcriber: transcriber}),
	)
	server := gateway.NewWithTrace(
		cfg,
		st,
		tools,
		runtime,
		traces,
		gateway.WithSpeechTranscriber(transcriber),
		gateway.WithCredentialVault(credentialVault),
		gateway.WithNotificationBindingCancellation(telegramService.CancelBinding),
	)

	serverCtx, cancelServerCtx := context.WithCancel(context.Background())
	if cfg.Tools.Reminders.Enabled {
		notificationRouter := notification.NewRouter(cfg, st)
		if telegramConfig.Enabled {
			notificationRouter = notificationRouter.WithAdapter("telegram", telegram.NewNotificationAdapter(st, credentialVault, telegramConfig))
		}
		startReminderScheduler(serverCtx, reminder.NewScheduler(st, notificationRouter))
	}
	startWeixinContextSyncer(serverCtx, weixin.NewSyncer(st).WithConfig(cfg).WithDispatcher(weixin.NewDispatcherWithConfig(st, runtime, cfg)))
	if telegramConfig.Enabled {
		startTelegramService(serverCtx, telegramService)
	}
	httpServer := &http.Server{
		Addr:              server.Addr(),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return serverCtx
		},
	}

	go func() {
		slog.Info("sparkclaw gateway listening", "addr", server.Addr())
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("gateway failed", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	cancelServerCtx()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("gateway shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("sparkclaw gateway stopped")
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

func startTelegramService(ctx context.Context, service *telegram.Service) {
	go func() {
		if err := service.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Telegram connector stopped", "code", "telegram_service_failed")
		}
	}()
}

func startWeixinContextSyncer(ctx context.Context, syncer *weixin.Syncer) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			syncer.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func startReminderScheduler(ctx context.Context, scheduler *reminder.Scheduler) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			for _, delivery := range scheduler.Tick(ctx) {
				slog.Info("reminder delivery completed", "reminder_id", delivery.ReminderID, "status", delivery.Status, "channel", delivery.Channel, "retry_state", delivery.RetryState)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func newStore(cfg config.Config) (store.Store, error) {
	switch cfg.State.Backend {
	case "", "file":
		return store.NewFileStoreWithOptions(store.FileStoreOptions{
			Path:              cfg.State.Path,
			EncryptAtRest:     cfg.State.EncryptAtRest,
			EncryptionKey:     cfg.State.EncryptionKey,
			EncryptionKeyFile: cfg.State.EncryptionKeyFile,
		})
	case "memory":
		return store.NewMemoryStore(), nil
	case "postgres":
		return store.NewPostgresStore(context.Background(), cfg.State.DSN)
	default:
		return nil, fmt.Errorf("unsupported state backend %q", cfg.State.Backend)
	}
}
