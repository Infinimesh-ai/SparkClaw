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

func TestLoadAppliesWebSearchEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_WEB_SEARCH_ENABLED", "true")
	t.Setenv("SPARKCLAW_WEB_SEARCH_PROVIDER", "infinimesh-info")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_BASE_URL", "https://info.example.test")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_TOKEN_BATCH_SIZE", "7")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_MAX_ATTEMPTS", "2")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_MAX_SOURCES", "6")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF", "entitlement-env")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION", "attestation-env")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF", "license-env")

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
	t.Setenv("SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE", "/opt/test/chromium")
	t.Setenv("SPARKCLAW_BROWSER_PROFILE_DIR", profileDir)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Adapters.BrowserAutomation.ChromiumExecutable != "/opt/test/chromium" {
		t.Fatalf("shared Chromium env did not apply: %#v", cfg.Adapters.BrowserAutomation)
	}
	if cfg.Adapters.BrowserAutomation.ProfileDir != profileDir || !filepath.IsAbs(cfg.Adapters.BrowserAutomation.ProfileDir) {
		t.Fatalf("browser profile directory was not normalized: %#v", cfg.Adapters.BrowserAutomation)
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
	files := map[string]string{
		"SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE":  "entitlement-file",
		"SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION_FILE": "attestation-file",
		"SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE":      "license-file",
	}
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
	if info.EntitlementProof != "entitlement-file" || info.DeviceAttestation != "attestation-file" || info.LicenseProof != "license-file" {
		t.Fatal("infinimesh info credential files were not loaded")
	}
}

func TestLoadPrefersDirectInfinimeshInfoCredentialOverFile(t *testing.T) {
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF", "direct-proof")
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE", filepath.Join(t.TempDir(), "missing"))

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plugins.Entries.InfinimeshInfo.Config.EntitlementProof != "direct-proof" {
		t.Fatal("direct credential did not take precedence")
	}
}

func TestLoadRejectsUnreadableInfinimeshInfoCredentialFile(t *testing.T) {
	t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE", filepath.Join(t.TempDir(), "missing"))
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
          "entitlementProof": "json-entitlement",
          "deviceAttestation": "json-attestation",
          "licenseProof": "json-license"
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
	if info.EntitlementProof != "" || info.DeviceAttestation != "" || info.LicenseProof != "" {
		t.Fatal("infinimesh info credentials must not load from JSON")
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
		t.Fatalf("weixin notification channel should still be disabled until explicitly enabled")
	}
}

func TestLoadAppliesParallelAPIKeyEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_PARALLEL_API_KEY", "par-env")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plugins.Entries.Parallel.Config.WebSearch.APIKey != "par-env" {
		t.Fatalf("parallel api key env did not apply: %#v", cfg.Plugins.Entries.Parallel.Config.WebSearch)
	}
}

func TestLoadKeepsWebSearchDisabledWhenExplicitlyDisabled(t *testing.T) {
	t.Setenv("SPARKCLAW_WEB_SEARCH_ENABLED", "false")
	t.Setenv("SPARKCLAW_PARALLEL_API_KEY", "par-env")

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
	t.Setenv("SPARKCLAW_FAST_BASE_URL", "http://fast.example.test/v1")
	t.Setenv("SPARKCLAW_FAST_MODEL", "sparkclaw-fast")
	t.Setenv("SPARKCLAW_FAST_SERVED_NAME", "fast-lane")
	t.Setenv("SPARKCLAW_FAST_MAX_TOKENS", "333")
	t.Setenv("SPARKCLAW_FAST_CONTEXT_TOKENS", "12000")
	t.Setenv("SPARKCLAW_DEEP_BASE_URL", "http://deep.example.test/v1")
	t.Setenv("SPARKCLAW_DEEP_MODEL", "sparkclaw-deep")
	t.Setenv("SPARKCLAW_DEEP_SERVED_NAME", "deep-lane")
	t.Setenv("SPARKCLAW_DEEP_MAX_TOKENS", "444")
	t.Setenv("SPARKCLAW_DEEP_CONTEXT_TOKENS", "12288")
	t.Setenv("SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS", "555")
	t.Setenv("SPARKCLAW_MODEL_DISABLE_THINKING", "true")

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
	if cfg.Model.Fast.MaxTokens != 333 {
		t.Fatalf("fast max tokens env did not apply: %#v", cfg.Model.Fast)
	}
	if cfg.Model.Fast.ContextTokens != 12000 {
		t.Fatalf("fast context tokens env did not apply: %#v", cfg.Model.Fast)
	}
	if cfg.Model.Deep.BaseURL != "http://deep.example.test/v1" || cfg.Model.Deep.Model != "sparkclaw-deep" || cfg.Model.Deep.Name != "deep-lane" {
		t.Fatalf("deep model env did not apply: %#v", cfg.Model.Deep)
	}
	if cfg.Model.Deep.MaxTokens != 444 {
		t.Fatalf("deep max tokens env did not apply: %#v", cfg.Model.Deep)
	}
	if cfg.Model.Deep.ContextTokens != 12288 {
		t.Fatalf("deep context tokens env did not apply: %#v", cfg.Model.Deep)
	}
	if cfg.Model.HTTPTimeoutSeconds != 555 {
		t.Fatalf("model HTTP timeout env did not apply: %#v", cfg.Model)
	}
	if !cfg.Model.DisableThinking {
		t.Fatalf("model disable thinking env did not apply: %#v", cfg.Model)
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
