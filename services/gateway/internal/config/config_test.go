package config

import (
	"os"
	"path/filepath"
	"slices"
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
	if cfg.Model.Guard.BaseURL != "http://guard.example.test/v1" || cfg.Model.Guard.Model != "Qwen/TestGuard" {
		t.Fatalf("guard model env did not apply: %#v", cfg.Model.Guard)
	}
}

func TestLoadAppliesStateEncryptionEnvironment(t *testing.T) {
	root := t.TempDir()
	keyFile := filepath.Join(root, "state.key")
	t.Setenv("SPARKCLAW_STATE_ENCRYPT_AT_REST", "true")
	t.Setenv("SPARKCLAW_STATE_ENCRYPTION_KEY", "env-secret")
	t.Setenv("SPARKCLAW_STATE_ENCRYPTION_KEY_FILE", keyFile)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.State.EncryptAtRest || cfg.State.EncryptionKey != "env-secret" {
		t.Fatalf("state encryption env did not apply: %#v", cfg.State)
	}
	if cfg.State.EncryptionKeyFile != keyFile {
		t.Fatalf("state encryption key file was not normalized: %#v", cfg.State)
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
