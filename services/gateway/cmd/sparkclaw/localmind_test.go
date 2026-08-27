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
	// The manager must exist without operator environment variables so a
	// household credential can be activated later through settings.
	manager, err = newLocalMindManager(cfg, hub, nil)
	if err != nil || manager == nil {
		t.Fatalf("LocalMind manager was not created for household configuration: %#v %v", manager, err)
	}

	t.Setenv("LOCALMIND_MCP_URL", "https://localmind.example.test/mcp")
	t.Setenv("LOCALMIND_MCP_TOKEN", "token-a")
	manager, err = newLocalMindManager(cfg, hub, nil)
	if err != nil || manager == nil {
		t.Fatalf("configured LocalMind manager was not created: %#v %v", manager, err)
	}
}

func TestWithoutLocalMindMCPServerPreservesGenericServers(t *testing.T) {
	servers := map[string]config.MCPServerConfig{
		config.LocalMindMCPServerKey: {URLEnv: "LOCALMIND_MCP_URL"},
		"happy-tasks":                {URL: "https://happy.example.test/mcp"},
	}

	filtered := withoutLocalMindMCPServer(servers)
	if _, exists := filtered[config.LocalMindMCPServerKey]; exists {
		t.Fatal("generic MCP manager retained the dedicated LocalMind server")
	}
	if filtered["happy-tasks"].URL != servers["happy-tasks"].URL {
		t.Fatalf("generic MCP server was not preserved: %#v", filtered)
	}
}
