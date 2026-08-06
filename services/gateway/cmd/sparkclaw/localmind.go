package main

import (
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/localmind"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func newLocalMindManager(cfg config.Config, tools *toolhub.ToolHub, httpClient *http.Client) (*localmind.Manager, error) {
	server, configured := cfg.MCPServers[config.LocalMindMCPServerKey]
	if !configured {
		return nil, nil
	}
	return localmind.New(server, tools, httpClient)
}
