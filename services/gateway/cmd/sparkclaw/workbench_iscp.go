package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func startWorkbenchISCP(ctx context.Context, cfg config.Config, server *gateway.Server) (*gateway.WorkbenchISCP, error) {
	if cfg.Gateway.WorkbenchISCPConfig == "" {
		return nil, nil
	}
	if !cfg.Gateway.WorkbenchISCPLocalTest {
		return nil, errors.New("ISCP workbench local test issuer is not enabled")
	}
	transport, err := iscpworkbench.LoadConfig(cfg.Gateway.WorkbenchISCPConfig)
	if err != nil {
		return nil, errors.New("ISCP workbench responder configuration is invalid or unavailable")
	}
	return server.StartWorkbenchISCP(ctx, transport, func(state string) {
		slog.Info("ISCP workbench responder state", "state", state)
	})
}
