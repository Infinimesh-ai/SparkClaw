package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/sandbox"
)

func main() {
	addr := getenv("SPARKCLAW_SANDBOX_RUNNER_ADDR", "0.0.0.0:18889")
	runner := sandbox.LocalDockerRunner{
		HostWorkspaceRoot:      getenv("SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT", ""),
		ContainerWorkspaceRoot: getenv("SPARKCLAW_SANDBOX_CONTAINER_WORKSPACE_ROOT", ""),
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           sandbox.Handler(runner),
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("sparkclaw sandbox-runner listening", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("sandbox-runner failed", "error", err)
		os.Exit(1)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
