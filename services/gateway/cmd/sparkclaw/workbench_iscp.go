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

	var started []*gateway.WorkbenchISCP
	type selected struct{ path, mode string }
	var profiles []selected
	if cfg.Gateway.WorkbenchISCPConfig != "" {
		mode := "local-test"
		if cfg.Gateway.WorkbenchISCPNodeConfig == "" && cfg.Gateway.WorkbenchISCPNodeManaged {
			mode = "node-managed"
		}
		profiles = append(profiles, selected{cfg.Gateway.WorkbenchISCPConfig, mode})
	}
	if cfg.Gateway.WorkbenchISCPNodeConfig != "" {
		profiles = append(profiles, selected{cfg.Gateway.WorkbenchISCPNodeConfig, "node-managed"})
	}
	for _, profile := range profiles {
		if profile.mode == "local-test" && !cfg.Gateway.WorkbenchISCPLocalTest || profile.mode == "node-managed" && !cfg.Gateway.WorkbenchISCPNodeManaged {
			for _, service := range started {
				_ = service.Close()
			}
			return nil, errors.New("ISCP workbench selected issuer is not enabled")
		}
		transport, err := iscpworkbench.LoadRuntimeConfig(ctx, profile.path)
		if err != nil {
			for _, service := range started {
				_ = service.Close()
			}
			return nil, errors.New("ISCP workbench responder configuration is invalid or unavailable")
		}
		if transport.Mode != profile.mode || profile.mode == "local-test" && !cfg.Gateway.WorkbenchISCPLocalTest || profile.mode == "node-managed" && !cfg.Gateway.WorkbenchISCPNodeManaged {
			for _, service := range started {
				_ = service.Close()
			}
			return nil, errors.New("ISCP workbench mode differs from its explicit Gateway opt-in")
		}
		service, err := server.StartWorkbenchISCP(ctx, transport, func(state string) { slog.Info("ISCP workbench responder state", "mode", profile.mode, "state", state) })
		if err != nil {
			for _, old := range started {
				_ = old.Close()
			}
			return nil, err
		}
		started = append(started, service)
	}
	return gateway.JoinWorkbenchISCP(started...), nil
}
