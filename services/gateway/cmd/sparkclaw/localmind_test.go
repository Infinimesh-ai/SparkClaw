package main

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestNewLocalMindManagerRequiresExplicitConfig(t *testing.T) {
	cfg := config.Default()
	hub := toolhub.New(cfg, store.NewMemoryStore())
	defer hub.Close()
	manager, err := newLocalMindManager(cfg, hub, nil)
	if err != nil || manager != nil {
		t.Fatalf("default config unexpectedly enabled LocalMind: %#v %v", manager, err)
	}

	cfg.MCPServers = map[string]config.MCPServerConfig{
		config.LocalMindMCPServerKey: {
			Transport: "streamable-http", URLEnv: "LOCALMIND_MCP_URL", BearerTokenEnv: "LOCALMIND_MCP_TOKEN",
			Namespace: "localmind", ExpectedServerName: config.LocalMindMCPServerName, ProtocolVersion: config.LocalMindMCPProtocolVersion,
			RequestTimeoutSeconds: 30, LongCallGraceSeconds: 10, MaxResponseBytes: 16 << 20,
			StateOutputMaxBytes: 16 << 10, ArchiveOutputMaxBytes: 16 << 20, RefreshIntervalSeconds: 300,
		},
	}
	manager, err = newLocalMindManager(cfg, hub, nil)
	if err != nil || manager == nil {
		t.Fatalf("configured LocalMind manager was not created: %#v %v", manager, err)
	}
}
