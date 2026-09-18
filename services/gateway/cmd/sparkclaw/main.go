package main

import (
	"context"
	"encoding/json"
	"flag"
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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func main() {
	configPath := flag.String("config", "configs/sparkclaw.default.json", "path to SparkClaw config")
	migrateRenderPreviewOwner := flag.String("migrate-email-render-previews", "", "generate safe email render previews serially for this owner, then exit")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	for _, warning := range cfg.Warnings {
		slog.Warn("config warning", "warning", warning)
	}
	storeStartupCtx, cancelStoreStartup := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.State.StartupTimeoutSeconds)*time.Second,
	)
	storeRuntime, err := newStore(storeStartupCtx, cfg)
	cancelStoreStartup()
	if err != nil {
		slog.Error("failed to initialize store", "error", err)
		os.Exit(1)
	}
	st := backendFromRuntime(storeRuntime)
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
	runtime, err := agent.NewRuntimeWithContext(context.Background(), st, tools, policyEngine, models, traces)
	if err != nil {
		slog.Error("failed to initialize agent runtime", "error", err)
		os.Exit(1)
	}
	runtime = runtime.WithArtifactStore(artifactStore)
	services, err := newGatewayServices(cfg, st, tools, runtime, traces, transcriber, storeRuntime, models)
	if err != nil {
		slog.Error("failed to initialize gateway services", "error", err)
		os.Exit(1)
	}
	defer services.Close()
	server := services.server
	if *migrateRenderPreviewOwner != "" {
		migrationCtx, cancelMigration := context.WithTimeout(context.Background(), 30*time.Minute)
		report, migrationErr := services.emailManagement.MigrateRenderPreviews(migrationCtx, *migrateRenderPreviewOwner)
		cancelMigration()
		if encodeErr := json.NewEncoder(os.Stdout).Encode(report); encodeErr != nil && migrationErr == nil {
			migrationErr = encodeErr
		}
		storeCloseCtx, cancelStoreClose := context.WithTimeout(context.Background(), 10*time.Second)
		closeErr := storeRuntime.Close(storeCloseCtx)
		cancelStoreClose()
		if migrationErr != nil || closeErr != nil || report.Failed != 0 {
			services.Close()
			_ = transcriber.Close()
			_ = tools.Close()
			slog.Error("email render preview migration incomplete", "error", migrationErr, "close_error", closeErr, "failed", report.Failed)
			os.Exit(2)
		}
		return
	}

	serverCtx, cancelServerCtx := context.WithCancel(context.Background())
	if err := services.Start(serverCtx); err != nil {
		cancelServerCtx()
		slog.Error("failed to start gateway services", "error", err)
		os.Exit(1)
	}
	storeRuntime.StartRecovery(serverCtx)
	if failed, err := runtime.FailInterruptedRuns(serverCtx); err != nil {
		slog.Warn("could not fail runs interrupted by the previous process", "error", err)
	} else if failed > 0 {
		slog.Info("failed runs interrupted by the previous process", "count", failed)
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
	if err := server.WaitForBackgroundWork(shutdownCtx); err != nil {
		slog.Error("gateway background work shutdown failed", "error", err)
		os.Exit(1)
	}
	storeCloseCtx, cancelStoreClose := context.WithTimeout(context.Background(), 10*time.Second)
	if err := storeRuntime.Close(storeCloseCtx); err != nil {
		cancelStoreClose()
		slog.Error("store shutdown failed", "error", err)
		os.Exit(1)
	}
	cancelStoreClose()
	slog.Info("sparkclaw gateway stopped")
}

type backend struct {
	store.ISCPOnboardingRepository
	store.OwnerRepository
	store.EmailRepository
	store.EmailPresentationRepository
	store.ClientRepository
	store.CredentialRepository
	store.ConnectorRepository
	store.SessionRepository
	store.ConversationRepository
	store.RunRepository
	store.DocumentRepository
	store.ApprovalRepository
	store.AuditRepository
	store.EvaluationRepository
	store.ArtifactMetadataRepository
	store.BrowserStateRepository
	store.MemoryRepository
	store.ScheduleRepository
	store.PassiveNotificationRepository
	store.DeliveryRecordRepository
	store.ExternalChatRepository
	store.MCPRepository
}

func newStore(ctx context.Context, cfg config.Config) (*store.Runtime, error) {
	timeouts := store.OperationTimeouts{
		Read:        time.Duration(cfg.State.ReadTimeoutSeconds) * time.Second,
		Write:       time.Duration(cfg.State.WriteTimeoutSeconds) * time.Second,
		Transaction: time.Duration(cfg.State.TransactionTimeoutSeconds) * time.Second,
	}
	switch cfg.State.Backend {
	case "", "file":
		return store.NewRuntime(ctx, store.RuntimeOptions{
			Backend:  store.BackendFile,
			Timeouts: timeouts,
			File: store.FileStoreOptions{
				Path: cfg.State.Path, EncryptAtRest: cfg.State.EncryptAtRest,
				EncryptionKey: cfg.State.EncryptionKey, EncryptionKeyFile: cfg.State.EncryptionKeyFile,
			},
		})
	case "memory":
		return store.NewRuntime(ctx, store.RuntimeOptions{Backend: store.BackendMemory, Timeouts: timeouts})
	case "postgres":
		return store.NewRuntime(ctx, store.RuntimeOptions{Backend: store.BackendPostgres, Timeouts: timeouts, PostgresDSN: cfg.State.DSN})
	default:
		return store.NewRuntime(ctx, store.RuntimeOptions{Backend: store.BackendKind(cfg.State.Backend), Timeouts: timeouts})
	}
}

func backendFromRuntime(runtime *store.Runtime) backend {
	return backend{
		ISCPOnboardingRepository:      runtime.ISCPOnboardingRepository(),
		OwnerRepository:               runtime.OwnerRepository(),
		EmailRepository:               runtime.EmailRepository(),
		EmailPresentationRepository:   runtime.EmailPresentationRepository(),
		ClientRepository:              runtime.ClientRepository(),
		CredentialRepository:          runtime.CredentialRepository(),
		ConnectorRepository:           runtime.ConnectorRepository(),
		SessionRepository:             runtime.SessionRepository(),
		ConversationRepository:        runtime.ConversationRepository(),
		RunRepository:                 runtime.RunRepository(),
		DocumentRepository:            runtime.DocumentRepository(),
		ApprovalRepository:            runtime.ApprovalRepository(),
		AuditRepository:               runtime.AuditRepository(),
		EvaluationRepository:          runtime.EvaluationRepository(),
		ArtifactMetadataRepository:    runtime.ArtifactMetadataRepository(),
		BrowserStateRepository:        runtime.BrowserStateRepository(),
		MemoryRepository:              runtime.MemoryRepository(),
		ScheduleRepository:            runtime.ScheduleRepository(),
		PassiveNotificationRepository: runtime.PassiveNotificationRepository(),
		DeliveryRecordRepository:      runtime.DeliveryRecordRepository(),
		ExternalChatRepository:        runtime.ExternalChatRepository(),
		MCPRepository:                 runtime.MCPRepository(),
	}
}
