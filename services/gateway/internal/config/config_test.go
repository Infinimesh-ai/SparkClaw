package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	if cfg.Tools.Web.Search.Enabled {
		t.Fatalf("Infinimesh web search should be disabled by default: %#v", cfg.Tools.Web.Search)
	}
	if cfg.Tools.Notifications.Channels["telegram"].Enabled {
		t.Fatalf("Telegram should be disabled by default: %#v", cfg.Tools.Notifications.Channels["telegram"])
	}
	if cfg.State.Backend != "file" {
		t.Fatalf("default state backend changed: %#v", cfg.State)
	}
}

func TestRepositoryDefaultConfigLeavesOptionalRemoteEndpointsEmpty(t *testing.T) {
	for _, name := range []string{
		"SPARKCLAW_MODEL_MODE",
		"SPARKCLAW_FAST_BASE_URL",
		"SPARKCLAW_DEEP_BASE_URL",
		"SPARKCLAW_EMBEDDING_BASE_URL",
		"SPARKCLAW_RERANKER_BASE_URL",
		"SPARKCLAW_SPEECH_ENABLED",
		"SPARKCLAW_SPEECH_BASE_URL",
		"SPARKCLAW_SPEECH_ALLOWED_HOSTS",
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
	for name, profile := range map[string]ModelProfile{
		"fast":      cfg.Model.Fast,
		"deep":      cfg.Model.Deep,
		"embedding": cfg.Model.Embedding,
		"reranker":  cfg.Model.Reranker,
	} {
		if profile.BaseURL != "" {
			t.Fatalf("%s remote endpoint should require explicit configuration: %q", name, profile.BaseURL)
		}
	}
	if cfg.Speech.Enabled || cfg.Speech.BaseURL != "" || len(cfg.Speech.AllowedHosts) != 0 {
		t.Fatalf("speech remote endpoint should require explicit configuration: %#v", cfg.Speech)
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
			t.Setenv("SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF", "test-entitlement")
			t.Setenv("SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION", "test-device")
			t.Setenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF", "test-license")
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
	if _, err := Load(""); err == nil {
		t.Fatal("expected insecure speech URL to be rejected")
	}

	t.Setenv("SPARKCLAW_SPEECH_BASE_URL", "https://speech.example.test/asr")
	if _, err := Load(""); err == nil {
		t.Fatal("expected speech host outside the allowlist to be rejected")
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
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_COMMAND", "/opt/test/agent-browser")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_TIMEOUT_MS", "31000")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_STARTUP_TIMEOUT_MS", "11000")
	t.Setenv("SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS", "61000")
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
		cfg.Adapters.BrowserAutomation.DaemonIdleTimeoutMS != 61000 {
		t.Fatalf("agent-browser adapter env did not apply: %#v", cfg.Adapters.BrowserAutomation)
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
