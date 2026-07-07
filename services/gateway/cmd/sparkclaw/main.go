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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/reminder"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/skills"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
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
	policyEngine := policy.New(cfg)
	models := modelrouter.New(cfg)
	traces := trace.NewWriterFromConfig(cfg)
	runtime := agent.NewRuntimeWithSkills(st, tools, policyEngine, models, traces, skills.NewRegistry(cfg)).WithArtifactStore(artifactStore)
	server := gateway.NewWithTrace(cfg, st, tools, runtime, traces)

	serverCtx, cancelServerCtx := context.WithCancel(context.Background())
	if cfg.Tools.Reminders.Enabled && !cfg.Tools.Reminders.Disabled {
		startReminderScheduler(serverCtx, reminder.NewScheduler(st, notification.NewRouter(cfg, st)))
	}
	startWeixinContextSyncer(serverCtx, weixin.NewSyncer(st).WithConfig(cfg).WithDispatcher(weixin.NewDispatcherWithConfig(st, runtime, cfg)))
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
