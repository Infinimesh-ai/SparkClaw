package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/localmind"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func newLocalMindManager(cfg config.Config, tools *toolhub.ToolHub, httpClient *http.Client) (*localmind.Manager, error) {
	server, configured := cfg.MCPServers[config.LocalMindMCPServerKey]
	if !configured {
		return nil, nil
	}
	// The shipped default config carries the localmind block so the env-var
	// names are documented, but a deployment that never set them is not an
	// integration: constructing the manager anyway would register the
	// discovery tool and re-warn on every refresh interval forever.
	if strings.TrimSpace(os.Getenv(server.URLEnv)) == "" || strings.TrimSpace(os.Getenv(server.BearerTokenEnv)) == "" {
		slog.Info("LocalMind MCP integration is not configured; skipping", "url_env", server.URLEnv, "bearer_token_env", server.BearerTokenEnv)
		return nil, nil
	}
	return localmind.New(server, tools, httpClient)
}

func withoutLocalMindMCPServer(servers map[string]config.MCPServerConfig) map[string]config.MCPServerConfig {
	filtered := make(map[string]config.MCPServerConfig, len(servers))
	for name, server := range servers {
		if name != config.LocalMindMCPServerKey {
			filtered[name] = server
		}
	}
	return filtered
}
