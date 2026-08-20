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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func main() {
	configPath := flag.String("config", "configs/sparkclaw.default.json", "path to SparkClaw config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	storeStartupCtx, cancelStoreStartup := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.State.StartupTimeoutSeconds)*time.Second,
	)
	st, err := newStore(storeStartupCtx, cfg)
	cancelStoreStartup()
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
	localMindManager, err := newLocalMindManager(cfg, tools, nil)
	if err != nil {
		slog.Error("failed to initialize LocalMind MCP integration", "error", err)
		os.Exit(1)
	}
	if localMindManager != nil {
		startupTimeout := time.Duration(cfg.MCPServers[config.LocalMindMCPServerKey].RequestTimeoutSeconds+5) * time.Second
		startupCtx, cancelStartup := context.WithTimeout(context.Background(), startupTimeout)
		if _, err := localMindManager.Refresh(startupCtx); err != nil {
			slog.Warn("LocalMind MCP startup refresh failed; discovery remains retryable", "error", err)
		}
		cancelStartup()
	}
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
	services, err := newGatewayServices(cfg, st, tools, runtime, traces, transcriber)
	if err != nil {
		slog.Error("failed to initialize gateway services", "error", err)
		os.Exit(1)
	}
	server := services.server

	serverCtx, cancelServerCtx := context.WithCancel(context.Background())
	if err := services.Start(serverCtx); err != nil {
		cancelServerCtx()
		slog.Error("failed to start gateway services", "error", err)
		os.Exit(1)
	}
	if localMindManager != nil {
		go localMindManager.Run(serverCtx)
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
	slog.Info("sparkclaw gateway stopped")
}

func newStore(ctx context.Context, cfg config.Config) (store.Store, error) {
	timeouts := store.OperationTimeouts{
		Read:  time.Duration(cfg.State.ReadTimeoutSeconds) * time.Second,
		Write: time.Duration(cfg.State.WriteTimeoutSeconds) * time.Second,
	}
	switch cfg.State.Backend {
	case "", "file":
		return store.NewFileStoreWithOptions(store.FileStoreOptions{
			Path:              cfg.State.Path,
			EncryptAtRest:     cfg.State.EncryptAtRest,
			EncryptionKey:     cfg.State.EncryptionKey,
			EncryptionKeyFile: cfg.State.EncryptionKeyFile,
			ReadTimeout:       timeouts.Read,
			WriteTimeout:      timeouts.Write,
		})
	case "memory":
		return store.NewMemoryStoreWithOptions(timeouts), nil
	case "postgres":
		return store.NewPostgresStoreWithOptions(ctx, cfg.State.DSN, timeouts)
	default:
		return nil, fmt.Errorf("unsupported state backend %q", cfg.State.Backend)
	}
}
