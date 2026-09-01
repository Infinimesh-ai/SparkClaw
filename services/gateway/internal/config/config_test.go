package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestLoadMergesToolsPolicyFile(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "sparkclaw.json")
	policyPath := filepath.Join(root, "tools.policy.json")
	if err := os.WriteFile(policyPath, []byte(`{
  "deny": ["custom.blocked"],
  "approval_required": ["files.write_draft"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{
  "gateway": {"bind": "127.0.0.1", "port": 18789},
  "security": {"tool_policy_path": "`+escapeJSONPath(policyPath)+`", "denied_tools": ["base.denied"]},
  "workspaces": {"default_root": "`+escapeJSONPath(root)+`", "allowlist": ["`+escapeJSONPath(root)+`"]},
  "state": {"backend": "memory"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Security.DeniedTools, "base.denied") || !slices.Contains(cfg.Security.DeniedTools, "custom.blocked") {
		t.Fatalf("deny policy did not merge: %#v", cfg.Security.DeniedTools)
	}
	if !slices.Contains(cfg.Security.ApprovalRequiredTools, "files.write_draft") {
		t.Fatalf("approval policy did not merge: %#v", cfg.Security.ApprovalRequiredTools)
	}
	if !filepath.IsAbs(cfg.Security.ToolPolicyPath) {
		t.Fatalf("tool policy path was not normalized: %q", cfg.Security.ToolPolicyPath)
	}
	if !cfg.Gateway.RateLimit.Enabled || cfg.Gateway.RateLimit.RequestsPerMinute != 600 || cfg.Gateway.RateLimit.Burst != 120 {
		t.Fatalf("default rate limit missing: %#v", cfg.Gateway.RateLimit)
	}
}

func TestLoadAppliesRateLimitEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_RATE_LIMIT_ENABLED", "false")
	t.Setenv("SPARKCLAW_RATE_LIMIT_PER_MINUTE", "42")
	t.Setenv("SPARKCLAW_RATE_LIMIT_BURST", "7")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Gateway.RateLimit.Enabled || cfg.Gateway.RateLimit.RequestsPerMinute != 42 || cfg.Gateway.RateLimit.Burst != 7 {
		t.Fatalf("rate limit env did not apply: %#v", cfg.Gateway.RateLimit)
	}
}

func TestLoadAppliesJingSiLANEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_JINGSI_LAN_ENABLED", "true")
	t.Setenv("SPARKCLAW_JINGSI_SESSION_ID", " session-lan ")
	t.Setenv("SPARKCLAW_JINGSI_MAX_MESSAGE_BYTES", "4096")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.JingSiLAN.Enabled || cfg.JingSiLAN.SessionID != "session-lan" || cfg.JingSiLAN.MaxMessageBytes != 4096 {
		t.Fatalf("JingSi LAN env did not apply: %#v", cfg.JingSiLAN)
	}
}

func TestLoadRejectsEnabledJingSiLANWithoutSession(t *testing.T) {
	t.Setenv("SPARKCLAW_JINGSI_LAN_ENABLED", "true")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "jingsi_lan.session_id") {
		t.Fatalf("missing JingSi session error = %v", err)
	}
}

func TestLoadRejectsOversizedJingSiLANMessageLimit(t *testing.T) {
	t.Setenv("SPARKCLAW_JINGSI_MAX_MESSAGE_BYTES", strconv.Itoa((1<<20)+1))
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "max_message_bytes") {
		t.Fatalf("oversized JingSi message limit error = %v", err)
	}
}

func TestLoadAppliesMemoryRetentionEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_MEMORY_RETENTION_DAYS", "14")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Memory.RetentionDays != 14 {
		t.Fatalf("memory retention env did not apply: %#v", cfg.Memory)
	}
}

func TestLoadPassiveNotificationBounds(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PassiveNotifications.MaxPerOwner != 500 || cfg.PassiveNotifications.RetentionDays != 90 {
		t.Fatalf("passive notification defaults = %#v", cfg.PassiveNotifications)
	}

	root := t.TempDir()
	configPath := filepath.Join(root, "sparkclaw.json")
	if err := os.WriteFile(configPath, []byte(`{
  "gateway": {"bind": "127.0.0.1", "port": 18789},
  "passive_notifications": {"max_per_owner": 0, "retention_days": 0},
  "workspaces": {"default_root": "`+escapeJSONPath(root)+`"},
  "state": {"backend": "memory"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PassiveNotifications.MaxPerOwner != 0 || cfg.PassiveNotifications.RetentionDays != 0 {
		t.Fatalf("explicit zero (disabled) was not preserved: %#v", cfg.PassiveNotifications)
	}

	if err := os.WriteFile(configPath, []byte(`{
  "gateway": {"bind": "127.0.0.1", "port": 18789},
  "passive_notifications": {"max_per_owner": -1},
  "workspaces": {"default_root": "`+escapeJSONPath(root)+`"},
  "state": {"backend": "memory"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "passive_notifications.max_per_owner") {
		t.Fatalf("negative cap error = %v", err)
	}
}

func TestLoadAppliesObservationSummaryLimitEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_OBSERVATION_SUMMARY_MAX_BYTES", "256")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.ObservationSummaryMaxBytes != 256 {
		t.Fatalf("observation summary limit env did not apply: %#v", cfg.Runtime)
	}
}

func TestLoadAppliesGuardModelEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_GUARD_BASE_URL", "http://guard.example.test/v1")
	t.Setenv("SPARKCLAW_GUARD_MODEL", "Qwen/TestGuard")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Guard.BaseURL != "http://guard.example.test/v1" ||
		cfg.Model.Guard.Model != "Qwen/TestGuard" ||
		cfg.Model.Guard.ContextTokens != 16384 ||
		cfg.Model.Guard.OutputBudgets["guard"] != 256 {
		t.Fatalf("guard model env did not apply: %#v", cfg.Model.Guard)
	}
}

func TestDefaultChatProfilesMatchVLLMManagedNVFP4Checkpoint(t *testing.T) {
	cfg, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Model.Fast.Model != "nvidia/Qwen3.6-35B-A3B-NVFP4" ||
		cfg.Model.Fast.ContextTokens != 32768 || cfg.Model.Fast.MTP {
		t.Fatalf("fast profile does not match the vLLM-managed NVFP4 checkpoint default: %#v", cfg.Model.Fast)
	}
	if cfg.Model.Deep.Model != "nvidia/Qwen3.6-35B-A3B-NVFP4" ||
		cfg.Model.Deep.ContextTokens != 65536 || cfg.Model.Deep.MTP {
		t.Fatalf("deep profile does not match the vLLM-managed NVFP4 checkpoint default: %#v", cfg.Model.Deep)
	}
}

func TestLoadDefaultsOptionalFeaturesOff(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speech.Enabled || cfg.Speech.Backend != "disabled" {
		t.Fatalf("speech should be disabled by default: %#v", cfg.Speech)
	}
	if cfg.Speech.BaseURL != "" || len(cfg.Speech.AllowedHosts) != 0 || cfg.Speech.Model != "sparkclaw-asr" {
		t.Fatalf("speech endpoint should require explicit configuration: %#v", cfg.Speech)
	}
	if cfg.Speech.MaxAudioSeconds != 60 || cfg.Speech.MaxUploadBytes != 3<<20 || cfg.Speech.MaxConcurrency != 1 {
		t.Fatalf("speech limits missing: %#v", cfg.Speech)
	}
	if cfg.Adapters.DocumentOCR.Enabled || cfg.Adapters.DocumentOCR.Provider != "disabled" || cfg.Adapters.DocumentOCR.BaseURL != "" || len(cfg.Adapters.DocumentOCR.AllowedHosts) != 0 {
		t.Fatalf("document OCR should require explicit configuration: %#v", cfg.Adapters.DocumentOCR)
	}
	if cfg.Adapters.DocumentOCR.Model != "sparkclaw-ocr" || cfg.Adapters.DocumentOCR.MaxTokens != 16384 || cfg.Adapters.DocumentOCR.MaxUploadBytes != 12<<20 || cfg.Adapters.DocumentOCR.MaxOutputBytes != 1<<20 || cfg.Adapters.DocumentOCR.MaxConcurrency != 2 {
		t.Fatalf("document OCR limits missing: %#v", cfg.Adapters.DocumentOCR)
	}
	visual := cfg.Adapters.PPTXVisualQA
	if visual.Phase != "disabled" || visual.BaseURL != "" || len(visual.AllowedHosts) != 0 || len(visual.RepairQualifiedClasses) != 0 || len(visual.RepairQualifiedOperations) != 0 || len(visual.BlockingQualifiedClasses) != 0 || visual.MaxRepairAttempts != 2 {
		t.Fatalf("PPTX visual QA should require explicit rollout configuration: %#v", visual)
	}
	if visual.TimeoutSeconds != 120 || visual.MaxInputBytes != 64<<20 || visual.MaxPDFBytes != 64<<20 || visual.MaxPages != 100 || visual.MaxChangedPages != 20 || visual.RasterScale != 1.5 || visual.MaxPagePixels != 20_000_000 || visual.MaxPNGBytes != 12<<20 || visual.DiagnosticToleranceMilli != 2 || visual.ReadinessTTLSeconds != 300 {
		t.Fatalf("PPTX visual QA limits missing: %#v", visual)
	}
	if cfg.Tools.Web.Search.Enabled {
		t.Fatalf("Infinimesh web search should be disabled by default: %#v", cfg.Tools.Web.Search)
	}
	if cfg.Tools.Notifications.Channels["telegram"].Enabled {
		t.Fatalf("Telegram should be disabled by default: %#v", cfg.Tools.Notifications.Channels["telegram"])
	}
	if cfg.State.Backend != "file" {
		t.Fatalf("default state backend changed: %#v", cfg.State)
	}
	if len(cfg.MCPServers) != 0 {
		t.Fatalf("MCP servers should require explicit configuration: %#v", cfg.MCPServers)
	}
	if cfg.ISCPPairing.Enabled || cfg.ISCPPairing.ExpectedTicketType != "iscp.pairing_ticket.v2" || cfg.ISCPPairing.TicketTTLSeconds != 600 {
		t.Fatalf("ISCP pairing should be disabled with bounded defaults: %#v", cfg.ISCPPairing)
	}
	if cfg.MCPAccess.LocalDomainID != "sparkclaw-local" {
		t.Fatalf("MCP local domain default changed: %#v", cfg.MCPAccess)
	}
	if cfg.JingSiLAN.Enabled || cfg.JingSiLAN.SessionID != "" || cfg.JingSiLAN.MaxMessageBytes != 64<<10 {
		t.Fatalf("JingSi LAN should require explicit configuration: %#v", cfg.JingSiLAN)
	}
}

func TestLoadMCPAccessLocalDomain(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sparkclaw.json")
	raw := `{"model":{"mock":true},"workspaces":{"default_root":"` + escapeJSONPath(root) + `"},"mcp_access":{"local_domain_id":" local-domain "}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPAccess.LocalDomainID != "local-domain" {
		t.Fatalf("MCP local domain was not normalized: %#v", cfg.MCPAccess)
	}

	path = filepath.Join(root, "missing-domain.json")
	if err := os.WriteFile(path, []byte(`{"model":{"mock":true},"workspaces":{"default_root":"`+escapeJSONPath(root)+`"},"mcp_access":{"local_domain_id":""}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "local_domain_id") {
		t.Fatalf("MCP access accepted an empty local domain: %v", err)
	}
}

func TestLoadMCPAllowedOrigins(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sparkclaw.json")
	raw := `{"model":{"mock":true},"workspaces":{"default_root":"` + escapeJSONPath(root) + `"},"mcp_access":{"local_domain_id":"local-domain","allowed_origins":[" HTTPS://Panel.Example.COM "," https://panel.example.com/","","http://192.168.1.20:8443"]}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://panel.example.com", "http://192.168.1.20:8443"}
	if len(cfg.MCPAccess.AllowedOrigins) != len(want) {
		t.Fatalf("allowed origins were not normalized and deduplicated: %#v", cfg.MCPAccess.AllowedOrigins)
	}
	for index, origin := range want {
		if cfg.MCPAccess.AllowedOrigins[index] != origin {
			t.Fatalf("allowed origin %d = %q, want %q", index, cfg.MCPAccess.AllowedOrigins[index], origin)
		}
	}

	for name, entry := range map[string]string{
		"missing scheme": `"panel.example.com"`,
		"path":           `"https://panel.example.com/mcp"`,
		"credentials":    `"https://user@panel.example.com"`,
		"query":          `"https://panel.example.com?x=1"`,
		"null origin":    `"null"`,
	} {
		path := filepath.Join(root, "invalid-origin.json")
		invalid := `{"model":{"mock":true},"workspaces":{"default_root":"` + escapeJSONPath(root) + `"},"mcp_access":{"local_domain_id":"local-domain","allowed_origins":[` + entry + `]}}`
		if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "allowed_origins") {
			t.Fatalf("%s entry %s was accepted: %v", name, entry, err)
		}
	}
}

func TestLoadNormalizesEnabledISCPPairing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sparkclaw.json")
	raw := `{"model":{"mock":true},"workspaces":{"default_root":"` + escapeJSONPath(root) + `"},"iscp_pairing":{"enabled":true,"domain_id":" domain-a ","authority_url":"http://127.0.0.1:8090/v1/pairing/","token_env":"ISCP_TEST_TOKEN"}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ISCPPairing.DomainID != "domain-a" || cfg.ISCPPairing.AuthorityURL != "http://127.0.0.1:8090/v1/pairing" || cfg.ISCPPairing.RequestTimeoutSeconds != 15 {
		t.Fatalf("ISCP pairing configuration was not normalized: %#v", cfg.ISCPPairing)
	}
}

func TestLoadRejectsUnsafeISCPPairing(t *testing.T) {
	for _, test := range []struct{ name, config, want string }{
		{name: "missing domain", config: `"authority_url":"https://authority.test/pairing","token_env":"TOKEN"`, want: "domain_id"},
		{name: "missing token", config: `"domain_id":"domain-a","authority_url":"https://authority.test/pairing"`, want: "exactly one"},
		{name: "remote http", config: `"domain_id":"domain-a","authority_url":"http://authority.example/pairing","token_env":"TOKEN"`, want: "HTTP only"},
		{name: "wrong object", config: `"domain_id":"domain-a","authority_url":"https://authority.test/pairing","token_env":"TOKEN","expected_ticket_type":"private.ticket"`, want: "iscp.pairing_ticket.v2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "sparkclaw.json")
			raw := `{"model":{"mock":true},"workspaces":{"default_root":"` + escapeJSONPath(root) + `"},"iscp_pairing":{"enabled":true,` + test.config + `}}`
			if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unsafe ISCP pairing configuration was accepted: %v", err)
			}
		})
	}
}

func TestLoadNormalizesLocalMindMCPServer(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "sparkclaw.json")
	if err := os.WriteFile(configPath, []byte(`{
  "model": {"mock": true},
  "workspaces": {"default_root": "`+escapeJSONPath(root)+`"},
  "mcp_servers": {
    "localmind": {
      "url_env": "LOCALMIND_MCP_URL",
      "bearer_token_env": "LOCALMIND_MCP_TOKEN"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCPServers[LocalMindMCPServerKey]
	if server.Transport != "streamable-http" || server.Namespace != "localmind" ||
		server.ExpectedServerName != LocalMindMCPServerName || server.ProtocolVersion != LocalMindMCPProtocolVersion {
		t.Fatalf("LocalMind identity defaults missing: %#v", server)
	}
	if server.AllowMutations || server.AllowPrivateHTTP {
		t.Fatalf("LocalMind unsafe options should default off: %#v", server)
	}
	if server.RequestTimeoutSeconds != 30 || server.LongCallGraceSeconds != 10 ||
		server.MaxResponseBytes != 16<<20 || server.StateOutputMaxBytes != 16<<10 ||
		server.ArchiveOutputMaxBytes != 16<<20 || server.RefreshIntervalSeconds != 300 {
		t.Fatalf("LocalMind bounds missing: %#v", server)
	}
	if len(server.ToolAllow) != 0 || len(server.ToolDeny) != 0 {
		t.Fatalf("LocalMind retained obsolete tool filters: %#v", server)
	}
}

func TestLoadRejectsInvalidLocalMindMCPServer(t *testing.T) {
	for _, test := range []struct {
		name   string
		server string
		want   string
	}{
		{name: "unknown server", server: `"other":{"url_env":"URL","bearer_token_env":"TOKEN"}`, want: "unsupported MCP server"},
		{name: "transport", server: `"localmind":{"transport":"sse","url_env":"URL","bearer_token_env":"TOKEN"}`, want: "streamable-http"},
		{name: "environment reference", server: `"localmind":{"url_env":"https://localmind.test","bearer_token_env":"TOKEN"}`, want: "environment variable names"},
		{name: "protocol", server: `"localmind":{"url_env":"URL","bearer_token_env":"TOKEN","protocol_version":"2024-11-05"}`, want: LocalMindMCPProtocolVersion},
		{name: "server name", server: `"localmind":{"url_env":"URL","bearer_token_env":"TOKEN","expected_server_name":"wrong"}`, want: LocalMindMCPServerName},
		{name: "obsolete filters", server: `"localmind":{"url_env":"URL","bearer_token_env":"TOKEN","tool_allow":["read_document"]}`, want: "not supported"},
		{name: "obsolete mutation switch", server: `"localmind":{"url_env":"URL","bearer_token_env":"TOKEN","allow_mutations":true}`, want: "allow_mutations"},
		{name: "response bound", server: `"localmind":{"url_env":"URL","bearer_token_env":"TOKEN","max_response_bytes":33554433}`, want: "max_response_bytes"},
		{name: "refresh bound", server: `"localmind":{"url_env":"URL","bearer_token_env":"TOKEN","refresh_interval_seconds":29}`, want: "refresh_interval_seconds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "sparkclaw.json")
			raw := `{"model":{"mock":true},"workspaces":{"default_root":"` + escapeJSONPath(root) + `"},"mcp_servers":{` + test.server + `}}`
			if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid LocalMind MCP config was accepted: %v", err)
			}
		})
	}
}

func TestLoadNormalizesMixedMCPServerKinds(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "sparkclaw.json")
	if err := os.WriteFile(configPath, []byte(`{
  "model": {"mock": true},
  "workspaces": {"default_root": "`+escapeJSONPath(root)+`"},
  "mcp_servers": {
    "localmind": {
      "url_env": "LOCALMIND_MCP_URL",
      "bearer_token_env": "LOCALMIND_MCP_TOKEN"
    },
    "happy-tasks": {
      "url": "https://happy.example.test/mcp",
      "token_env": "HAPPY_TEAM_MCP_TOKEN"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPServers[LocalMindMCPServerKey].Namespace != LocalMindMCPDefaultNamespace {
		t.Fatalf("LocalMind server did not retain its dedicated defaults: %#v", cfg.MCPServers[LocalMindMCPServerKey])
	}
	if cfg.MCPServers["happy-tasks"].Namespace != "mcp.happy-tasks" {
		t.Fatalf("generic MCP server did not retain its defaults: %#v", cfg.MCPServers["happy-tasks"])
	}
}

func TestLoadNormalizesGenericMCPSafeguards(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "sparkclaw.json")
	if err := os.WriteFile(configPath, []byte(`{
  "model": {"mock": true},
  "workspaces": {"default_root": "`+escapeJSONPath(root)+`"},
  "mcp_servers": {
    "happy-tasks": {
      "url": "https://happy.example.test/mcp",
      "allow_mutations": true,
      "tool_allow": ["list_tasks", "create_task", "list_tasks", " create_task "],
      "tool_deny": ["approve_plan", "approve_plan"]
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCPServers["happy-tasks"]
	if !server.AllowMutations || !slices.Equal(server.ToolAllow, []string{"create_task", "list_tasks"}) ||
		!slices.Equal(server.ToolDeny, []string{"approve_plan"}) {
		t.Fatalf("generic MCP safeguards were not normalized: %#v", server)
	}
}

func TestLoadDefaultsGenericMCPMutationsOffAndRejectsFilterConflict(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "sparkclaw.json")
	base := `{"model":{"mock":true},"workspaces":{"default_root":"` + escapeJSONPath(root) + `"},"mcp_servers":%s}`
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(base, `{"happy":{"url":"https://happy.example.test/mcp"}}`)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPServers["happy"].AllowMutations {
		t.Fatal("generic MCP mutations did not default off")
	}
	conflict := `{"happy":{"url":"https://happy.example.test/mcp","tool_allow":["get_task"],"tool_deny":["get_task"]}}`
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(base, conflict)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "both allowed and denied") {
		t.Fatalf("generic MCP filter conflict was accepted: %v", err)
	}
}

func TestRepositoryDefaultConfigLeavesOptionalRemoteEndpointsEmpty(t *testing.T) {
	for _, name := range []string{
		"SPARKCLAW_MODEL_MODE",
		"SPARKCLAW_FAST_BASE_URL",
		"SPARKCLAW_DEEP_BASE_URL",
		"SPARKCLAW_EMBEDDING_BASE_URL",
		"SPARKCLAW_SPEECH_ENABLED",
		"SPARKCLAW_SPEECH_BASE_URL",
		"SPARKCLAW_SPEECH_ALLOWED_HOSTS",
		"SPARKCLAW_OCR_ENABLED",
		"SPARKCLAW_OCR_BASE_URL",
		"SPARKCLAW_OCR_ALLOWED_HOSTS",
	} {
		t.Setenv(name, "")
	}

	configPath := filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json")
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Model.Mock {
		t.Fatal("repository default config should use mock models")
	}
	if cfg.Model.Fast.ContextTokens != 32768 || cfg.Model.Fast.MTP ||
		cfg.Model.Deep.ContextTokens != 32768 || cfg.Model.Deep.MTP {
		t.Fatalf("repository chat profiles do not match the vLLM-managed NVFP4 checkpoint: %#v", cfg.Model)
	}
	for name, profile := range map[string]ModelProfile{
		"fast":      cfg.Model.Fast,
		"deep":      cfg.Model.Deep,
		"embedding": cfg.Model.Embedding,
	} {
		if profile.BaseURL != "" {
			t.Fatalf("%s remote endpoint should require explicit configuration: %q", name, profile.BaseURL)
		}
	}
	if cfg.Speech.Enabled || cfg.Speech.BaseURL != "" || len(cfg.Speech.AllowedHosts) != 0 {
		t.Fatalf("speech remote endpoint should require explicit configuration: %#v", cfg.Speech)
	}
	if cfg.Adapters.DocumentOCR.Enabled || cfg.Adapters.DocumentOCR.BaseURL != "" || len(cfg.Adapters.DocumentOCR.AllowedHosts) != 0 {
		t.Fatalf("document OCR remote endpoint should require explicit configuration: %#v", cfg.Adapters.DocumentOCR)
	}
}

func TestLoadAppliesDocumentOCREnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_OCR_ENABLED", "true")
	t.Setenv("SPARKCLAW_OCR_PROVIDER", "openai-http")
	t.Setenv("SPARKCLAW_OCR_BASE_URL", "https://ocr.example.test/v1/")
	t.Setenv("SPARKCLAW_OCR_ALLOWED_HOSTS", "ocr.example.test")
	t.Setenv("SPARKCLAW_OCR_MODEL", "ATH-MaaS/OvisOCR2")
	t.Setenv("SPARKCLAW_OCR_TIMEOUT_SECONDS", "90")
	t.Setenv("SPARKCLAW_OCR_MAX_UPLOAD_BYTES", "8388608")
	t.Setenv("SPARKCLAW_OCR_MAX_OUTPUT_BYTES", "524288")
	t.Setenv("SPARKCLAW_OCR_MAX_CONCURRENCY", "3")
	t.Setenv("SPARKCLAW_OCR_MAX_PENDING", "4")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	ocr := cfg.Adapters.DocumentOCR
	if !ocr.Enabled || ocr.Provider != "openai-http" || ocr.BaseURL != "https://ocr.example.test/v1" || ocr.Model != "ATH-MaaS/OvisOCR2" || ocr.TimeoutSeconds != 90 || ocr.MaxUploadBytes != 8388608 || ocr.MaxOutputBytes != 524288 || ocr.MaxTokens != 16384 || ocr.MaxConcurrency != 3 || ocr.MaxPending != 4 {
		t.Fatalf("document OCR environment did not apply: %#v", ocr)
	}
}

func TestLoadAppliesPPTXVisualQAEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_PHASE", "shadow")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_CLASSES", "text_clipped,missing_glyph")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_OPERATIONS", "set_text_style,set_geometry")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_BLOCKING_QUALIFIED_CLASSES", "text_clipped")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_REPAIR_ATTEMPTS", "1")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_BASE_URL", "http://gotenberg:3000/")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_ALLOWED_HOSTS", "gotenberg")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_TIMEOUT_SECONDS", "90")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_INPUT_BYTES", "33554432")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_PDF_BYTES", "50331648")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_PAGES", "40")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_CHANGED_PAGES", "12")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_RASTER_SCALE", "2")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_PAGE_PIXELS", "16000000")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_PNG_BYTES", "8388608")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_DIAGNOSTIC_TOLERANCE_MILLI", "3")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_READINESS_TTL_SECONDS", "600")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	visual := cfg.Adapters.PPTXVisualQA
	if visual.Phase != "shadow" || !slices.Equal(visual.RepairQualifiedClasses, []string{"missing_glyph", "text_clipped"}) || !slices.Equal(visual.RepairQualifiedOperations, []string{"set_geometry", "set_text_style"}) || !slices.Equal(visual.BlockingQualifiedClasses, []string{"text_clipped"}) || visual.MaxRepairAttempts != 1 || visual.BaseURL != "http://gotenberg:3000" || len(visual.AllowedHosts) != 1 || visual.AllowedHosts[0] != "gotenberg" || visual.TimeoutSeconds != 90 || visual.MaxInputBytes != 33554432 || visual.MaxPDFBytes != 50331648 || visual.MaxPages != 40 || visual.MaxChangedPages != 12 || visual.RasterScale != 2 || visual.MaxPagePixels != 16000000 || visual.MaxPNGBytes != 8388608 || visual.DiagnosticToleranceMilli != 3 || visual.ReadinessTTLSeconds != 600 {
		t.Fatalf("PPTX visual QA environment did not apply: %#v", visual)
	}
}

func TestLoadRejectsUnsafePPTXVisualQAEndpointAndUnsupportedPhase(t *testing.T) {
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_PHASE", "shadow")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_BASE_URL", "http://render.example.test")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_ALLOWED_HOSTS", "render.example.test")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "http only") {
		t.Fatalf("insecure public PPTX visual QA endpoint was accepted: %v", err)
	}

	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_BASE_URL", "https://render.example.test")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_ALLOWED_HOSTS", "other.example.test")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("PPTX visual QA endpoint outside the allowlist was accepted: %v", err)
	}

	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_PHASE", "repairing")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_ALLOWED_HOSTS", "render.example.test")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "unsupported PPTX visual QA phase") {
		t.Fatalf("unimplemented PPTX visual QA phase was accepted: %v", err)
	}
}

func TestLoadValidatesPPTXVisualQAQualificationPolicy(t *testing.T) {
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_PHASE", "warning")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_BASE_URL", "http://gotenberg:3000")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_ALLOWED_HOSTS", "gotenberg")
	if cfg, err := Load(""); err != nil || cfg.Adapters.PPTXVisualQA.Phase != "warning" {
		t.Fatalf("warning phase did not load: cfg=%#v err=%v", cfg.Adapters.PPTXVisualQA, err)
	}

	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_CLASSES", "unknown")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "repairQualifiedClasses") {
		t.Fatalf("unknown repair qualification was accepted: %v", err)
	}
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_CLASSES", "text_clipped")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_OPERATIONS", "arbitrary_ooxml")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "repairQualifiedOperations") {
		t.Fatalf("unknown repair operation qualification was accepted: %v", err)
	}
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_OPERATIONS", "set_geometry")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_BLOCKING_QUALIFIED_CLASSES", "missing_glyph")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "must also be repair-qualified") {
		t.Fatalf("blocking qualification without repair qualification was accepted: %v", err)
	}
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_BLOCKING_QUALIFIED_CLASSES", "text_clipped")
	t.Setenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_REPAIR_ATTEMPTS", "3")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "maxRepairAttempts") {
		t.Fatalf("excessive PPTX repair budget was accepted: %v", err)
	}
}

func TestLoadRejectsUnsafeDocumentOCREndpoint(t *testing.T) {
	t.Setenv("SPARKCLAW_OCR_ENABLED", "true")
	t.Setenv("SPARKCLAW_OCR_BASE_URL", "http://ocr.example.test/v1")
	t.Setenv("SPARKCLAW_OCR_ALLOWED_HOSTS", "ocr.example.test")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "http only") {
		t.Fatalf("insecure public OCR endpoint was accepted: %v", err)
	}

	t.Setenv("SPARKCLAW_OCR_BASE_URL", "https://ocr.example.test/v1")
	t.Setenv("SPARKCLAW_OCR_ALLOWED_HOSTS", "other.example.test")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("OCR endpoint outside the allowlist was accepted: %v", err)
	}
}

func TestLoadRejectsInvalidDocumentOCRConfiguration(t *testing.T) {
	for _, test := range []struct {
		name     string
		provider string
		baseURL  string
		want     string
	}{
		{name: "unsupported provider", provider: "custom-http", baseURL: "https://ocr.example.test/v1", want: "unsupported document OCR provider"},
		{name: "relative endpoint", provider: "openai-http", baseURL: "/v1", want: "absolute http or https URL"},
		{name: "endpoint credentials", provider: "openai-http", baseURL: "https://user@ocr.example.test/v1", want: "must not contain credentials"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SPARKCLAW_OCR_ENABLED", "true")
			t.Setenv("SPARKCLAW_OCR_PROVIDER", test.provider)
			t.Setenv("SPARKCLAW_OCR_BASE_URL", test.baseURL)
			t.Setenv("SPARKCLAW_OCR_ALLOWED_HOSTS", "ocr.example.test")
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid OCR configuration was accepted: %v", err)
			}
		})
	}
}

func TestLoadOptionalFeatureCompatibilityMatrix(t *testing.T) {
	tests := []struct {
		name      string
		webSearch string
		speech    string
		telegram  string
	}{
		{name: "all disabled", webSearch: "false", speech: "false", telegram: "false"},
		{name: "Telegram only", webSearch: "false", speech: "false", telegram: "true"},
		{name: "speech only", webSearch: "false", speech: "true", telegram: "false"},
		{name: "all enabled with file state", webSearch: "true", speech: "true", telegram: "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SPARKCLAW_WEB_SEARCH_ENABLED", test.webSearch)
			t.Setenv("SPARKCLAW_SPEECH_ENABLED", test.speech)
			t.Setenv("SPARKCLAW_TELEGRAM_ENABLED", test.telegram)
			t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_ID", "lic_test")
			t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY", "ilk_v1.lic_test.test-key")
			if test.speech == "true" {
				t.Setenv("SPARKCLAW_SPEECH_BASE_URL", "https://speech.example.test/asr")
				t.Setenv("SPARKCLAW_SPEECH_ALLOWED_HOSTS", "speech.example.test")
			}

			cfg, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Tools.Web.Search.Enabled != (test.webSearch == "true") || cfg.Speech.Enabled != (test.speech == "true") || cfg.Tools.Notifications.Channels["telegram"].Enabled != (test.telegram == "true") {
				t.Fatalf("feature matrix mismatch: web=%v speech=%v telegram=%v", cfg.Tools.Web.Search.Enabled, cfg.Speech.Enabled, cfg.Tools.Notifications.Channels["telegram"].Enabled)
			}
			if cfg.State.Backend != "file" {
				t.Fatalf("feature matrix left the default file backend: %#v", cfg.State)
			}
		})
	}
}

func TestLoadAppliesSpeechEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_SPEECH_ENABLED", "true")
	t.Setenv("SPARKCLAW_SPEECH_BACKEND", "openai-http")
	t.Setenv("SPARKCLAW_SPEECH_BASE_URL", "https://speech.example.test/asr/")
	t.Setenv("SPARKCLAW_SPEECH_ALLOWED_HOSTS", "speech.example.test")
	t.Setenv("SPARKCLAW_SPEECH_MODEL", "test-asr")
	t.Setenv("SPARKCLAW_SPEECH_DEFAULT_LANGUAGE", "zh-CN")
	t.Setenv("SPARKCLAW_SPEECH_TIMEOUT_SECONDS", "45")
	t.Setenv("SPARKCLAW_SPEECH_MAX_AUDIO_SECONDS", "30")
	t.Setenv("SPARKCLAW_SPEECH_MAX_UPLOAD_BYTES", "2097152")
	t.Setenv("SPARKCLAW_SPEECH_MAX_CONCURRENCY", "2")
	t.Setenv("SPARKCLAW_SPEECH_MAX_PENDING", "3")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speech.BaseURL != "https://speech.example.test/asr" || cfg.Speech.Model != "test-asr" {
		t.Fatalf("speech endpoint env did not apply: %#v", cfg.Speech)
	}
	if cfg.Speech.DefaultLanguage != "zh-CN" || cfg.Speech.TimeoutSeconds != 45 {
		t.Fatalf("speech model env did not apply: %#v", cfg.Speech)
	}
	if cfg.Speech.MaxAudioSeconds != 30 || cfg.Speech.MaxUploadBytes != 2097152 || cfg.Speech.MaxConcurrency != 2 || cfg.Speech.MaxPending != 3 {
		t.Fatalf("speech limits env did not apply: %#v", cfg.Speech)
	}
}

func TestLoadRejectsInsecureOrUnlistedSpeechEndpoint(t *testing.T) {
	t.Setenv("SPARKCLAW_SPEECH_ENABLED", "true")
	t.Setenv("SPARKCLAW_SPEECH_BASE_URL", "http://speech.example.test/asr")
	t.Setenv("SPARKCLAW_SPEECH_ALLOWED_HOSTS", "speech.example.test")
	if _, err := Load(""); err == nil {
		t.Fatal("expected insecure speech URL to be rejected")
	}

	t.Setenv("SPARKCLAW_SPEECH_BASE_URL", "https://speech.example.test/asr")
	t.Setenv("SPARKCLAW_SPEECH_ALLOWED_HOSTS", "")
	if _, err := Load(""); err == nil {
		t.Fatal("expected speech host outside the allowlist to be rejected")
	}
}

func TestLoadAllowsLocalHTTPSpeechEndpoint(t *testing.T) {
	tests := []struct {
		name string
		url  string
		host string
	}{
		{name: "loopback", url: "http://127.0.0.1:8006", host: "127.0.0.1"},
		{name: "private IP", url: "http://10.0.0.12:8006", host: "10.0.0.12"},
		{name: "compose service", url: "http://sparkclaw-asr:8006", host: "sparkclaw-asr"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SPARKCLAW_SPEECH_ENABLED", "true")
			t.Setenv("SPARKCLAW_SPEECH_BACKEND", "openai-http")
			t.Setenv("SPARKCLAW_SPEECH_BASE_URL", test.url)
			t.Setenv("SPARKCLAW_SPEECH_ALLOWED_HOSTS", test.host)

			cfg, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Speech.BaseURL != test.url {
				t.Fatalf("speech URL was not preserved: %#v", cfg.Speech)
			}
		})
	}
}

func TestLoadAppliesWebSearchEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_WEB_SEARCH_ENABLED", "true")
	t.Setenv("SPARKCLAW_WEB_SEARCH_PROVIDER", "infinimesh-info")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_BASE_URL", "https://info.example.test")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_TOKEN_BATCH_SIZE", "7")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_MAX_ATTEMPTS", "2")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_MAX_SOURCES", "6")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_ID", "lic_env")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY", "ilk_v1.lic_env.key-env")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tools.Web.Search.Enabled || cfg.Tools.Web.Search.Provider != "infinimesh-info" {
		t.Fatalf("web search env did not apply: %#v", cfg.Tools.Web.Search)
	}
	info := cfg.Plugins.Entries.InfinimeshInfo.Config
	if info.BaseURL != "https://info.example.test" || info.TokenBatchSize != 7 || info.MaxAttempts != 2 || info.MaxSources != 6 || !info.Configured() {
		t.Fatalf("infinimesh info env did not apply: %#v", info)
	}
}

func TestLoadAppliesSharedChromiumProfileEnvironment(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "browser-profile")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_COMMAND", "/opt/test/agent-browser")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_TIMEOUT_MS", "31000")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_STARTUP_TIMEOUT_MS", "11000")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS", "1210000")
	t.Setenv("SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE", "/opt/test/chromium")
	t.Setenv("SPARKCLAW_BROWSER_PROFILE_DIR", profileDir)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Adapters.BrowserAutomation.ChromiumExecutable != "/opt/test/chromium" {
		t.Fatalf("shared Chromium env did not apply: %#v", cfg.Adapters.BrowserAutomation)
	}
	if cfg.Adapters.BrowserAutomation.Command != "/opt/test/agent-browser" ||
		cfg.Adapters.BrowserAutomation.TimeoutMS != 31000 ||
		cfg.Adapters.BrowserAutomation.StartupTimeoutMS != 11000 ||
		cfg.Adapters.BrowserAutomation.DaemonIdleTimeoutMS != 1210000 {
		t.Fatalf("agent-browser adapter env did not apply: %#v", cfg.Adapters.BrowserAutomation)
	}
	if cfg.Adapters.BrowserAutomation.ProfileDir != profileDir || !filepath.IsAbs(cfg.Adapters.BrowserAutomation.ProfileDir) {
		t.Fatalf("browser profile directory was not normalized: %#v", cfg.Adapters.BrowserAutomation)
	}
}

func TestLoadRejectsBrowserDaemonIdleTimeoutShorterThanWorkflowGap(t *testing.T) {
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_ENABLED", "true")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS", "60000")

	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "daemonIdleTimeoutMs must be at least") {
		t.Fatalf("short browser daemon idle timeout error = %v", err)
	}
}

func TestLoadAllowsShortBrowserDaemonIdleTimeoutWhenDisabled(t *testing.T) {
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS", "60000")

	if _, err := Load(""); err != nil {
		t.Fatalf("disabled browser automation must not gate boot on the idle-timeout floor: %v", err)
	}
}

func TestLoadRejectsBrowserDaemonIdleTimeoutCalculationOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_ENABLED", "true")
	t.Setenv("SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS", strconv.Itoa(maxInt))
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS", strconv.Itoa(maxInt))

	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "timeouts exceed the supported browser daemon idle timeout range") {
		t.Fatalf("browser daemon idle timeout overflow error = %v", err)
	}
}

func TestLoadKeepsInfinimeshWebSearchDisabledByDefault(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.Web.Search.Enabled {
		t.Fatalf("web search should stay disabled until explicitly enabled: %#v", cfg.Tools.Web.Search)
	}
}

func TestLoadReadsInfinimeshInfoCredentialsFromFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_ID", "lic_file")
	files := map[string]string{"SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE": "ilk_v1.lic_file.key-file"}
	for envName, value := range files {
		path := filepath.Join(root, envName)
		if err := os.WriteFile(path, []byte("  "+value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(envName, path)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	info := cfg.Plugins.Entries.InfinimeshInfo.Config
	if info.LicenseID != "lic_file" || info.LicenseKey != "ilk_v1.lic_file.key-file" {
		t.Fatal("infinimesh info credential files were not loaded")
	}
}

func TestLoadPrefersDirectInfinimeshInfoCredentialOverFile(t *testing.T) {
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_ID", "lic_direct")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY", "ilk_v1.lic_direct.direct-key")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE", filepath.Join(t.TempDir(), "missing"))

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseKey != "ilk_v1.lic_direct.direct-key" {
		t.Fatal("direct credential did not take precedence")
	}
}

func TestLoadRejectsUnreadableInfinimeshInfoCredentialFile(t *testing.T) {
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE", filepath.Join(t.TempDir(), "missing"))
	if _, err := Load(""); err == nil {
		t.Fatal("expected unreadable credential file to fail config loading")
	}
}

func TestLoadDoesNotAcceptInfinimeshInfoCredentialsFromJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparkclaw.json")
	if err := os.WriteFile(path, []byte(`{
  "plugins": {
    "entries": {
      "infinimeshInfo": {
        "config": {
          "licenseId": "lic_json",
          "licenseKey": "ilk_v1.lic_json.json-key"
        }
      }
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	info := cfg.Plugins.Entries.InfinimeshInfo.Config
	if info.LicenseID != "" || info.LicenseKey != "" {
		t.Fatal("infinimesh info credentials must not load from JSON")
	}
}

func TestLoadRejectsInvalidInfinimeshInfoLicensePair(t *testing.T) {
	tests := []struct {
		name      string
		licenseID string
		key       string
	}{
		{name: "missing key", licenseID: "lic_test"},
		{name: "missing license", key: "ilk_v1.lic_test.key"},
		{name: "invalid key", licenseID: "lic_test", key: "legacy-proof"},
		{name: "mismatched license", licenseID: "lic_other", key: "ilk_v1.lic_test.key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_ID", test.licenseID)
			t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY", test.key)
			if _, err := Load(""); err == nil {
				t.Fatal("expected invalid license pair to fail config loading")
			}
		})
	}
}

func TestLoadBoundsReminderDeliveryAttempts(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.Reminders.MaxDeliveryAttempts != 8 {
		t.Fatalf("expected default reminder delivery attempt cap of 8, got %d", cfg.Tools.Reminders.MaxDeliveryAttempts)
	}

	t.Setenv("SPARKCLAW_REMINDERS_MAX_DELIVERY_ATTEMPTS", "-3")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.Reminders.MaxDeliveryAttempts != 8 {
		t.Fatalf("expected non-positive cap backfilled to 8, got %d", cfg.Tools.Reminders.MaxDeliveryAttempts)
	}

	t.Setenv("SPARKCLAW_REMINDERS_MAX_DELIVERY_ATTEMPTS", "101")
	if _, err := Load(""); err == nil {
		t.Fatal("expected oversized reminder delivery attempt cap to fail config loading")
	}
}

func TestLoadValidatesInfinimeshInfoLimits(t *testing.T) {
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_TOKEN_BATCH_SIZE", "101")
	if _, err := Load(""); err == nil {
		t.Fatal("expected oversized token batch to fail config loading")
	}
}

func TestLoadDefaultsWeixinNotificationToQRProvider(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	weixin := cfg.Tools.Notifications.Channels["weixin"]
	if weixin.Provider != "openclaw-weixin-qr" {
		t.Fatalf("expected openclaw-weixin-qr provider, got %#v", weixin)
	}
	if weixin.BaseURL != "https://ilinkai.weixin.qq.com" {
		t.Fatalf("expected default ilink base URL, got %#v", weixin)
	}
	if weixin.Enabled {
		t.Fatalf("weixin notification channel must stay opt-in by default")
	}
}

func TestLoadAllowsWeixinNotificationToBeExplicitlyEnabled(t *testing.T) {
	t.Setenv("SPARKCLAW_WEIXIN_NOTIFICATION_ENABLED", "true")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tools.Notifications.Channels["weixin"].Enabled {
		t.Fatalf("weixin notification channel should honor an explicit enable")
	}
}

func TestLoadRejectsExternalModelModeWithoutBaseURLs(t *testing.T) {
	// Mirrors the shipped sparkclaw.default.json after 7d0653f: mock mode
	// with the fast/deep endpoints blanked out.
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"model":{"mock":false,"fast":{"base_url":""},"deep":{"base_url":""}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "model.fast.base_url") {
		t.Fatalf("expected load-time error about missing fast base_url, got %v", err)
	}

	t.Setenv("SPARKCLAW_FAST_BASE_URL", "http://127.0.0.1:8000")
	_, err = Load(path)
	if err == nil || !strings.Contains(err.Error(), "model.deep.base_url") {
		t.Fatalf("expected load-time error about missing deep base_url, got %v", err)
	}

	t.Setenv("SPARKCLAW_DEEP_BASE_URL", "http://127.0.0.1:8001")
	if _, err = Load(path); err != nil {
		t.Fatalf("expected external mode with both base URLs to load, got %v", err)
	}
}

func TestLoadKeepsWebSearchDisabledWhenExplicitlyDisabled(t *testing.T) {
	t.Setenv("SPARKCLAW_WEB_SEARCH_ENABLED", "false")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.Web.Search.Enabled {
		t.Fatalf("web search should stay disabled when explicitly disabled: %#v", cfg.Tools.Web.Search)
	}
}

func TestLoadAppliesExternalModelEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_MODEL_MODE", "external-model")
	t.Setenv("SPARKCLAW_MODEL_CAPACITY_PROFILE", "dgx-spark-dual-light-v1")
	t.Setenv("SPARKCLAW_FAST_BASE_URL", "http://fast.example.test/v1")
	t.Setenv("SPARKCLAW_FAST_MODEL", "sparkclaw-fast")
	t.Setenv("SPARKCLAW_FAST_SERVED_NAME", "fast-lane")
	t.Setenv("SPARKCLAW_DEEP_BASE_URL", "http://deep.example.test/v1")
	t.Setenv("SPARKCLAW_DEEP_MODEL", "sparkclaw-deep")
	t.Setenv("SPARKCLAW_DEEP_SERVED_NAME", "deep-lane")
	t.Setenv("SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS", "555")
	t.Setenv("SPARKCLAW_MODEL_DISABLE_THINKING", "true")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS", "1600000")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Mock {
		t.Fatal("external-model mode should disable mock routing")
	}
	if cfg.Model.Fast.BaseURL != "http://fast.example.test/v1" || cfg.Model.Fast.Model != "sparkclaw-fast" || cfg.Model.Fast.Name != "fast-lane" {
		t.Fatalf("fast model env did not apply: %#v", cfg.Model.Fast)
	}
	if cfg.Model.Fast.ContextTokens != 32768 || cfg.Model.Fast.OutputBudgets["answer"] != 4096 {
		t.Fatalf("fast catalog capacity did not apply: %#v", cfg.Model.Fast)
	}
	if cfg.Model.Deep.BaseURL != "http://deep.example.test/v1" || cfg.Model.Deep.Model != "sparkclaw-deep" || cfg.Model.Deep.Name != "deep-lane" {
		t.Fatalf("deep model env did not apply: %#v", cfg.Model.Deep)
	}
	if cfg.Model.Deep.ContextTokens != 65536 || cfg.Model.Deep.OutputBudgets["answer"] != 8192 {
		t.Fatalf("deep catalog capacity did not apply: %#v", cfg.Model.Deep)
	}
	if cfg.Model.HTTPTimeoutSeconds != 555 {
		t.Fatalf("model HTTP timeout env did not apply: %#v", cfg.Model)
	}
	if !cfg.Model.DisableThinking {
		t.Fatalf("model disable thinking env did not apply: %#v", cfg.Model)
	}
}

func TestLoadRejectsLegacyModelCapacityEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_FAST_CONTEXT_TOKENS", "8192")

	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "model capacity comes from the selected capacity profile") {
		t.Fatalf("legacy capacity environment was accepted: %v", err)
	}
}

func TestLoadAppliesStateEncryptionEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", "true")
	t.Setenv("SPARKCLAW_STATE_ENCRYPTION_KEY", "env-secret")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.State.EncryptAtRest || cfg.State.EncryptionKey != "env-secret" {
		t.Fatalf("state encryption env did not apply: %#v", cfg.State)
	}
	if cfg.State.EncryptionKeyFile != "" {
		t.Fatalf("unexpected state encryption key file: %#v", cfg.State)
	}
}

func TestLoadValidatesStateConfiguration(t *testing.T) {
	t.Run("normalizes backend and default file path", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_BACKEND", " FiLe ")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.State.Backend != "file" || !filepath.IsAbs(cfg.State.Path) || cfg.State.StartupTimeoutSeconds != 180 ||
			cfg.State.ReadTimeoutSeconds != 10 || cfg.State.WriteTimeoutSeconds != 30 || cfg.State.TransactionTimeoutSeconds != 60 {
			t.Fatalf("normalized state config = %#v", cfg.State)
		}
	})

	t.Run("memory", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_BACKEND", " MEMORY ")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.State.Backend != "memory" {
			t.Fatalf("memory state config = %#v", cfg.State)
		}
	})

	t.Run("invalid backend", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_BACKEND", "sqlite")
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "state.backend") {
			t.Fatalf("invalid backend error = %v", err)
		}
	})

	t.Run("file path required", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_BACKEND", "file")
		t.Setenv("SPARKCLAW_STATE_PATH", "   ")
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "state.path") {
			t.Fatalf("missing file path error = %v", err)
		}
	})

	t.Run("postgres DSN required", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_BACKEND", "postgres")
		t.Setenv("SPARKCLAW_STATE_DSN", "")
		t.Setenv("SPARKCLAW_POSTGRES_DSN", "")
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "state.dsn") {
			t.Fatalf("missing postgres DSN error = %v", err)
		}
	})

	t.Run("legacy postgres DSN retains precedence", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_BACKEND", "postgres")
		t.Setenv("SPARKCLAW_STATE_DSN", "postgres://canonical")
		t.Setenv("SPARKCLAW_POSTGRES_DSN", " postgres://legacy ")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.State.DSN != "postgres://legacy" {
			t.Fatalf("postgres DSN precedence = %q", cfg.State.DSN)
		}
	})
}

func TestLoadValidatesStateStartupTimeoutOverride(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS", " 42 ")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.State.StartupTimeoutSeconds != 42 {
			t.Fatalf("startup timeout = %d", cfg.State.StartupTimeoutSeconds)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS", "soon")
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS") {
			t.Fatalf("malformed timeout error = %v", err)
		}
	})

	for _, value := range []string{"0", "901"} {
		t.Run("range "+value, func(t *testing.T) {
			t.Setenv("SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS", value)
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "state.startup_timeout_seconds") {
				t.Fatalf("timeout range error = %v", err)
			}
		})
	}
}

func TestLoadValidatesStateOperationTimeoutOverrides(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_READ_TIMEOUT_SECONDS", " 7 ")
		t.Setenv("SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS", " 19 ")
		t.Setenv("SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS", " 23 ")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.State.ReadTimeoutSeconds != 7 || cfg.State.WriteTimeoutSeconds != 19 || cfg.State.TransactionTimeoutSeconds != 23 {
			t.Fatalf("operation timeouts = read %d write %d transaction %d", cfg.State.ReadTimeoutSeconds, cfg.State.WriteTimeoutSeconds, cfg.State.TransactionTimeoutSeconds)
		}
	})

	for _, testCase := range []struct {
		name     string
		variable string
		field    string
	}{
		{"read", "SPARKCLAW_STATE_READ_TIMEOUT_SECONDS", "state.read_timeout_seconds"},
		{"write", "SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS", "state.write_timeout_seconds"},
		{"transaction", "SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS", "state.transaction_timeout_seconds"},
	} {
		t.Run(testCase.name+" malformed", func(t *testing.T) {
			t.Setenv(testCase.variable, "soon")
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), testCase.variable) {
				t.Fatalf("malformed timeout error = %v", err)
			}
		})
		for _, value := range []string{"0", "901"} {
			t.Run(testCase.name+" range "+value, func(t *testing.T) {
				t.Setenv(testCase.variable, value)
				if _, err := Load(""); err == nil || !strings.Contains(err.Error(), testCase.field) {
					t.Fatalf("timeout range error = %v", err)
				}
			})
			t.Run(testCase.name+" file range "+value, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "sparkclaw.json")
				field := strings.TrimPrefix(testCase.field, "state.")
				if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"state":{"%s":%s}}`, field, value)), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := Load(path); err == nil || !strings.Contains(err.Error(), testCase.field) {
					t.Fatalf("file timeout range error = %v", err)
				}
			})
		}
	}
}

func TestLoadValidatesStateEncryptionBooleanOverride(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off", "FALSE"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", value)
			cfg, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.State.EncryptAtRest {
				t.Fatalf("%q parsed true", value)
			}
		})
	}
	t.Run("malformed", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", "sometimes")
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "SPARKCLAW_STATE_ENCRYPT_AT_REST") {
			t.Fatalf("malformed encryption boolean error = %v", err)
		}
	})
}

func TestLoadValidatesEncryptedFileStateKeySource(t *testing.T) {
	t.Run("key file", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "state.key")
		if err := os.WriteFile(keyFile, []byte("file-secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", "true")
		t.Setenv("SPARKCLAW_STATE_ENCRYPTION_KEY_FILE", keyFile)
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.State.EncryptionKeyFile != keyFile || cfg.State.EncryptionKey != "" {
			t.Fatalf("key-file state config = %#v", cfg.State)
		}
	})

	t.Run("missing", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", "true")
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("missing key source error = %v", err)
		}
	})

	t.Run("both", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "state.key")
		if err := os.WriteFile(keyFile, []byte("file-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", "true")
		t.Setenv("SPARKCLAW_STATE_ENCRYPTION_KEY", "direct-secret")
		t.Setenv("SPARKCLAW_STATE_ENCRYPTION_KEY_FILE", keyFile)
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("dual key source error = %v", err)
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", "true")
		t.Setenv("SPARKCLAW_STATE_ENCRYPTION_KEY_FILE", filepath.Join(t.TempDir(), "missing.key"))
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "state.encryption_key_file") {
			t.Fatalf("unreadable key file error = %v", err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "state.key")
		if err := os.WriteFile(keyFile, []byte(" \n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", "true")
		t.Setenv("SPARKCLAW_STATE_ENCRYPTION_KEY_FILE", keyFile)
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Fatalf("empty key file error = %v", err)
		}
	})
}

func TestLoadAppliesCredentialKeyEnvironment(t *testing.T) {
	root := t.TempDir()
	keyFile := filepath.Join(root, "credential.key")
	t.Setenv("SPARKCLAW_CREDENTIAL_KEY", "01234567890123456789012345678901")
	t.Setenv("SPARKCLAW_CREDENTIAL_KEY_FILE", keyFile)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.State.CredentialKey != "01234567890123456789012345678901" {
		t.Fatalf("credential key env did not apply")
	}
	if cfg.State.CredentialKeyFile != keyFile {
		t.Fatalf("credential key file was not normalized: %#v", cfg.State)
	}
}

func TestLoadNormalizesTelegramConnectorDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	telegram := cfg.Tools.Notifications.Channels["telegram"]
	if telegram.Enabled || telegram.Provider != "telegram-bot-api" || telegram.BaseURL != "https://api.telegram.org" {
		t.Fatalf("unexpected Telegram defaults: %#v", telegram)
	}
	if telegram.UpdateMode != "long-polling" || telegram.PollTimeoutSeconds != 30 || !telegram.PrivateChatsOnly {
		t.Fatalf("Telegram polling defaults missing: %#v", telegram)
	}
	if telegram.MaxDownloadBytes != 20<<20 || telegram.MaxAttachments != 5 || telegram.MaxVoiceSeconds != 120 || telegram.MaxConcurrency != 4 || telegram.MaxPending != 32 {
		t.Fatalf("Telegram limits missing: %#v", telegram)
	}
}

func TestLoadAppliesTelegramEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_TELEGRAM_ENABLED", "true")
	t.Setenv("SPARKCLAW_TELEGRAM_BASE_URL", "http://127.0.0.1:18888")
	t.Setenv("SPARKCLAW_TELEGRAM_POLL_TIMEOUT_SECONDS", "20")
	t.Setenv("SPARKCLAW_TELEGRAM_MAX_DOWNLOAD_BYTES", "1048576")
	t.Setenv("SPARKCLAW_TELEGRAM_MAX_ATTACHMENTS", "3")
	t.Setenv("SPARKCLAW_TELEGRAM_MAX_VOICE_SECONDS", "90")
	t.Setenv("SPARKCLAW_TELEGRAM_MAX_CONCURRENCY", "2")
	t.Setenv("SPARKCLAW_TELEGRAM_MAX_PENDING", "8")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	telegram := cfg.Tools.Notifications.Channels["telegram"]
	if !telegram.Enabled || telegram.BaseURL != "http://127.0.0.1:18888" || telegram.PollTimeoutSeconds != 20 {
		t.Fatalf("Telegram environment did not apply: %#v", telegram)
	}
	if telegram.MaxDownloadBytes != 1048576 || telegram.MaxAttachments != 3 || telegram.MaxVoiceSeconds != 90 || telegram.MaxConcurrency != 2 || telegram.MaxPending != 8 {
		t.Fatalf("Telegram limits environment did not apply: %#v", telegram)
	}
}

func TestLoadRejectsInvalidTelegramConfiguration(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "credential in URL", mutate: func(cfg *Config) {
			channel := cfg.Tools.Notifications.Channels["telegram"]
			channel.BaseURL = "https://secret@example.test"
			cfg.Tools.Notifications.Channels["telegram"] = channel
		}},
		{name: "insecure cloud URL", mutate: func(cfg *Config) {
			channel := cfg.Tools.Notifications.Channels["telegram"]
			channel.BaseURL = "http://api.telegram.org"
			cfg.Tools.Notifications.Channels["telegram"] = channel
		}},
		{name: "unbounded pending", mutate: func(cfg *Config) {
			channel := cfg.Tools.Notifications.Channels["telegram"]
			channel.MaxPending = 2048
			cfg.Tools.Notifications.Channels["telegram"] = channel
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Model.CapacityCatalog = defaultModelCapacityCatalogPath()
			test.mutate(&cfg)
			path := filepath.Join(t.TempDir(), "config.json")
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("expected invalid Telegram config to fail")
			}
		})
	}
}

func TestLoadNormalizesMCPServersWithoutResolvingSecrets(t *testing.T) {
	t.Setenv("HAPPY_TEAM_MCP_TOKEN", "not-read-by-config")
	cfg := Default()
	cfg.Model.CapacityCatalog = defaultModelCapacityCatalogPath()
	cfg.MCPServers = map[string]MCPServerConfig{
		"happy-tasks": {
			URL: "https://happy.example.com/v1/team/mcp", TokenEnv: "HAPPY_TEAM_MCP_TOKEN", ExpectedServerName: "happy-team-tasks",
		},
		"happy-bridge": {
			URL: "http://127.0.0.1:8790/", TokenFile: "~/.happy/mcp.token", ExpectedServerName: "happy-bridge",
		},
	}
	path := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	tasks := loaded.MCPServers["happy-tasks"]
	bridge := loaded.MCPServers["happy-bridge"]
	if tasks.Namespace != "mcp.happy-tasks" || tasks.RequestTimeoutSeconds != 30 || tasks.DiscoveryRefreshSeconds != 60 || tasks.ResponseBodyMaxBytes != 4<<20 {
		t.Fatalf("task MCP defaults missing: %#v", tasks)
	}
	if bridge.Namespace != "mcp.happy-bridge" || bridge.TokenFile != "~/.happy/mcp.token" {
		t.Fatalf("bridge MCP normalization changed credential reference: %#v", bridge)
	}
}

func TestLoadRejectsInvalidMCPServerConfiguration(t *testing.T) {
	for _, test := range []struct {
		name   string
		server MCPServerConfig
	}{
		{name: "credential in URL", server: MCPServerConfig{URL: "https://token@example.test/mcp"}},
		{name: "two token sources", server: MCPServerConfig{URL: "https://example.test/mcp", TokenEnv: "TOKEN", TokenFile: "/tmp/token"}},
		{name: "invalid token env", server: MCPServerConfig{URL: "https://example.test/mcp", TokenEnv: "BAD-NAME"}},
		{name: "oversized response", server: MCPServerConfig{URL: "https://example.test/mcp", ResponseBodyMaxBytes: 33 << 20}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Model.CapacityCatalog = defaultModelCapacityCatalogPath()
			cfg.MCPServers = map[string]MCPServerConfig{"fixture": test.server}
			path := filepath.Join(t.TempDir(), "config.json")
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("expected invalid MCP config to fail")
			}
		})
	}
}

func escapeJSONPath(path string) string {
	out := ""
	for _, ch := range path {
		if ch == '\\' || ch == '"' {
			out += "\\"
		}
		out += string(ch)
	}
	return out
}
