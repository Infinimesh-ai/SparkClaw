package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Gateway    GatewayConfig   `json:"gateway"`
	Model      ModelConfig     `json:"model"`
	Speech     SpeechConfig    `json:"speech"`
	Plugins    PluginsConfig   `json:"plugins"`
	Tools      ToolsConfig     `json:"tools"`
	Security   SecurityConfig  `json:"security"`
	Sandbox    SandboxConfig   `json:"sandbox"`
	Adapters   AdapterConfig   `json:"adapters"`
	Memory     MemoryConfig    `json:"memory"`
	Workspaces WorkspaceConfig `json:"workspaces"`
	Storage    StorageConfig   `json:"storage"`
	State      StateConfig     `json:"state"`
	Skills     SkillsConfig    `json:"skills"`
	Runtime    RuntimeConfig   `json:"runtime"`
	Logging    LoggingConfig   `json:"logging"`
}

type GatewayConfig struct {
	Bind            string          `json:"bind"`
	Port            int             `json:"port"`
	PairingRequired bool            `json:"pairing_required"`
	RemoteAccess    string          `json:"remote_access"`
	APIToken        string          `json:"api_token,omitempty"`
	RateLimit       RateLimitConfig `json:"rate_limit"`
}

type RateLimitConfig struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute"`
	Burst             int  `json:"burst"`
}

type ModelConfig struct {
	Fast               ModelProfile `json:"fast"`
	Deep               ModelProfile `json:"deep"`
	Embedding          ModelProfile `json:"embedding"`
	Reranker           ModelProfile `json:"reranker"`
	Guard              ModelProfile `json:"guard"`
	Mock               bool         `json:"mock"`
	HTTPTimeoutSeconds int          `json:"http_timeout_seconds"`
	DisableThinking    bool         `json:"disable_thinking"`
}

type ModelProfile struct {
	Name          string `json:"name"`
	BaseURL       string `json:"base_url"`
	Model         string `json:"model"`
	ContextTokens int    `json:"context_tokens"`
	MTP           bool   `json:"mtp"`
	MaxTokens     int    `json:"max_tokens"`
}

type SpeechConfig struct {
	Enabled         bool     `json:"enabled"`
	Backend         string   `json:"backend"`
	BaseURL         string   `json:"base_url"`
	AllowedHosts    []string `json:"allowed_hosts"`
	Model           string   `json:"model"`
	DefaultLanguage string   `json:"default_language"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	MaxAudioSeconds int      `json:"max_audio_seconds"`
	MaxUploadBytes  int64    `json:"max_upload_bytes"`
	MaxConcurrency  int      `json:"max_concurrency"`
	MaxPending      int      `json:"max_pending"`
	RetainAudio     bool     `json:"retain_audio"`
}

type PluginsConfig struct {
	Entries PluginEntriesConfig `json:"entries"`
}

type PluginEntriesConfig struct {
	InfinimeshInfo InfinimeshInfoPluginConfig `json:"infinimeshInfo"`
}

type InfinimeshInfoPluginConfig struct {
	Config InfinimeshInfoConfig `json:"config"`
}

type InfinimeshInfoConfig struct {
	BaseURL               string `json:"baseUrl"`
	TokenBatchSize        int    `json:"tokenBatchSize"`
	MaxAttempts           int    `json:"maxAttempts"`
	RetryBaseDelayMS      int    `json:"retryBaseDelayMs"`
	RequestTimeoutSeconds int    `json:"requestTimeoutSeconds"`
	ResponseBodyMaxBytes  int64  `json:"responseBodyMaxBytes"`
	Language              string `json:"language"`
	MaxSources            int    `json:"maxSources"`
	EntitlementProof      string `json:"-"`
	DeviceAttestation     string `json:"-"`
	LicenseProof          string `json:"-"`
}

func (cfg InfinimeshInfoConfig) Configured() bool {
	return strings.TrimSpace(cfg.EntitlementProof) != "" &&
		strings.TrimSpace(cfg.DeviceAttestation) != "" &&
		strings.TrimSpace(cfg.LicenseProof) != ""
}

type ToolsConfig struct {
	Web               WebToolsConfig              `json:"web"`
	BrowserAutomation BrowserAutomationToolConfig `json:"browserAutomation"`
	Reminders         RemindersToolConfig         `json:"reminders"`
	Notifications     NotificationsToolConfig     `json:"notifications"`
}

type WebToolsConfig struct {
	Search WebSearchToolConfig `json:"search"`
}

type WebSearchToolConfig struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
}

type BrowserAutomationToolConfig struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
}

type RemindersToolConfig struct {
	Enabled        bool   `json:"enabled"`
	DefaultChannel string `json:"defaultChannel"`
}

type NotificationsToolConfig struct {
	Channels map[string]NotificationChannelConfig `json:"channels"`
}

type NotificationChannelConfig struct {
	Enabled            bool   `json:"enabled"`
	Provider           string `json:"provider"`
	BaseURL            string `json:"baseUrl"`
	CDNBaseURL         string `json:"cdnBaseUrl,omitempty"`
	Token              string `json:"token,omitempty"`
	Recipient          string `json:"recipient,omitempty"`
	UpdateMode         string `json:"updateMode,omitempty"`
	PollTimeoutSeconds int    `json:"pollTimeoutSeconds,omitempty"`
	PrivateChatsOnly   bool   `json:"privateChatsOnly,omitempty"`
	MaxDownloadBytes   int64  `json:"maxDownloadBytes,omitempty"`
	MaxAttachments     int    `json:"maxAttachments,omitempty"`
	MaxVoiceSeconds    int    `json:"maxVoiceSeconds,omitempty"`
	MaxConcurrency     int    `json:"maxConcurrency,omitempty"`
	MaxPending         int    `json:"maxPending,omitempty"`
}

type SecurityConfig struct {
	ExternalContentUntrusted              bool     `json:"external_content_untrusted"`
	ApprovalRequiredForDangerousTools     bool     `json:"approval_required_for_dangerous_tools"`
	SandboxRequiredForMutatingTools       bool     `json:"sandbox_required_for_mutating_tools"`
	DangerousToolsRequireDeepVerification bool     `json:"dangerous_tools_require_deep_verification"`
	DeniedTools                           []string `json:"denied_tools"`
	ApprovalRequiredTools                 []string `json:"approval_required_tools"`
	ToolPolicyPath                        string   `json:"tool_policy_path"`
	BrowserReadAllowHosts                 []string `json:"browser_read_allow_hosts"`
}

type MemoryConfig struct {
	Enabled              bool     `json:"enabled"`
	WritePolicy          string   `json:"write_policy"`
	AllowSensitiveMemory bool     `json:"allow_sensitive_memory"`
	RetentionDays        int      `json:"retention_days"`
	RedactPatterns       []string `json:"redact_patterns"`
}

type SandboxConfig struct {
	Enabled         bool   `json:"enabled"`
	Backend         string `json:"backend"`
	RunnerURL       string `json:"runner_url"`
	Image           string `json:"image"`
	Network         string `json:"network"`
	WorkspaceAccess string `json:"workspace_access"`
	HostAccess      string `json:"host_access"`
}

type AdapterConfig struct {
	BrowserAutomation BrowserAutomationAdapterConfig `json:"browserAutomation"`
}

type BrowserAutomationAdapterConfig struct {
	MCPCommand         string   `json:"mcpCommand"`
	MCPArgs            []string `json:"mcpArgs"`
	TimeoutMS          int      `json:"timeoutMs"`
	ChromiumExecutable string   `json:"chromiumExecutable"`
	ProfileDir         string   `json:"profileDir"`
}

type WorkspaceConfig struct {
	DefaultRoot string   `json:"default_root"`
	Allowlist   []string `json:"allowlist"`
}

type StorageConfig struct {
	TraceDir        string `json:"trace_dir"`
	LogDir          string `json:"log_dir"`
	ArtifactBackend string `json:"artifact_backend"`
	ArtifactDir     string `json:"artifact_dir"`
	ArtifactBucket  string `json:"artifact_bucket"`
	S3Endpoint      string `json:"s3_endpoint"`
	S3Region        string `json:"s3_region"`
	S3AccessKey     string `json:"s3_access_key,omitempty"`
	S3SecretKey     string `json:"s3_secret_key,omitempty"`
}

type StateConfig struct {
	Backend           string `json:"backend"`
	Path              string `json:"path"`
	DSN               string `json:"dsn"`
	EncryptAtRest     bool   `json:"encrypt_at_rest"`
	EncryptionKey     string `json:"encryption_key,omitempty"`
	EncryptionKeyFile string `json:"encryption_key_file,omitempty"`
	CredentialKey     string `json:"credential_key,omitempty"`
	CredentialKeyFile string `json:"credential_key_file,omitempty"`
}

type SkillsConfig struct {
	Dirs []string `json:"dirs"`
}

type RuntimeConfig struct {
	ObservationSummaryMaxBytes int `json:"observation_summary_max_bytes"`
	ReactMaxDurationSeconds    int `json:"react_max_duration_seconds"`
	ReactMaxToolCalls          int `json:"react_max_tool_calls"`
	ReactMaxObservationBytes   int `json:"react_max_observation_bytes"`
	ReactMaxNoProgressActions  int `json:"react_max_no_progress_actions"`
	ReactMaxRepeatedToolCalls  int `json:"react_max_repeated_tool_calls"`
}

type LoggingConfig struct {
	Level          string   `json:"level"`
	RedactPatterns []string `json:"redact_patterns"`
}

type toolPolicyFile struct {
	Deny             []string `json:"deny"`
	ApprovalRequired []string `json:"approval_required"`
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, err
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, err
		}
	}
	applyEnv(&cfg)
	if err := applyInfinimeshInfoCredentials(&cfg); err != nil {
		return Config{}, err
	}
	if err := applyToolPolicyFile(&cfg); err != nil {
		return Config{}, err
	}
	if cfg.Gateway.Bind == "" {
		return Config{}, errors.New("gateway.bind is required")
	}
	if cfg.Gateway.Port <= 0 {
		return Config{}, errors.New("gateway.port must be positive")
	}
	if cfg.Workspaces.DefaultRoot == "" {
		cfg.Workspaces.DefaultRoot = "./data/workspaces"
	}
	root, err := filepath.Abs(cfg.Workspaces.DefaultRoot)
	if err != nil {
		return Config{}, err
	}
	cfg.Workspaces.DefaultRoot = root
	if len(cfg.Workspaces.Allowlist) == 0 {
		cfg.Workspaces.Allowlist = []string{root}
	}
	for i, p := range cfg.Workspaces.Allowlist {
		abs, err := filepath.Abs(p)
		if err == nil {
			cfg.Workspaces.Allowlist[i] = abs
		}
	}
	if strings.TrimSpace(cfg.Adapters.BrowserAutomation.ProfileDir) == "" {
		cfg.Adapters.BrowserAutomation.ProfileDir = "./data/browser-profiles"
	}
	profileDir, err := filepath.Abs(cfg.Adapters.BrowserAutomation.ProfileDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve browser profile directory: %w", err)
	}
	cfg.Adapters.BrowserAutomation.ProfileDir = profileDir
	normalizeRuntimeLimits(&cfg.Runtime)
	if err := normalizeInfinimeshInfoConfig(&cfg.Plugins.Entries.InfinimeshInfo.Config); err != nil {
		return Config{}, err
	}
	if err := normalizeSpeechConfig(&cfg.Speech); err != nil {
		return Config{}, err
	}
	if err := normalizeNotificationChannels(&cfg.Tools.Notifications); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// normalizeRuntimeLimits backfills non-positive ReAct budgets with the
// defaults so a partial runtime section in JSON cannot silently disable the
// loop's stop conditions.
func normalizeRuntimeLimits(rt *RuntimeConfig) {
	defaults := Default().Runtime
	if rt.ReactMaxDurationSeconds <= 0 {
		rt.ReactMaxDurationSeconds = defaults.ReactMaxDurationSeconds
	}
	if rt.ReactMaxToolCalls <= 0 {
		rt.ReactMaxToolCalls = defaults.ReactMaxToolCalls
	}
	if rt.ReactMaxObservationBytes <= 0 {
		rt.ReactMaxObservationBytes = defaults.ReactMaxObservationBytes
	}
	if rt.ReactMaxNoProgressActions <= 0 {
		rt.ReactMaxNoProgressActions = defaults.ReactMaxNoProgressActions
	}
	if rt.ReactMaxRepeatedToolCalls <= 0 {
		rt.ReactMaxRepeatedToolCalls = defaults.ReactMaxRepeatedToolCalls
	}
}

func normalizeInfinimeshInfoConfig(cfg *InfinimeshInfoConfig) error {
	defaults := Default().Plugins.Entries.InfinimeshInfo.Config
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaults.BaseURL
	}
	parsedBaseURL, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsedBaseURL.Host == "" || (parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") {
		return errors.New("infinimesh info base URL must be an absolute HTTP(S) URL")
	}
	cfg.BaseURL = strings.TrimRight(parsedBaseURL.String(), "/")
	if cfg.TokenBatchSize <= 0 {
		cfg.TokenBatchSize = defaults.TokenBatchSize
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaults.MaxAttempts
	}
	if cfg.RetryBaseDelayMS <= 0 {
		cfg.RetryBaseDelayMS = defaults.RetryBaseDelayMS
	}
	if cfg.RequestTimeoutSeconds <= 0 {
		cfg.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if cfg.ResponseBodyMaxBytes <= 0 {
		cfg.ResponseBodyMaxBytes = defaults.ResponseBodyMaxBytes
	}
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = defaults.Language
	}
	if cfg.MaxSources <= 0 {
		cfg.MaxSources = defaults.MaxSources
	}
	if cfg.TokenBatchSize > 100 {
		return errors.New("infinimesh info token batch size must not exceed 100")
	}
	if cfg.MaxAttempts > 5 {
		return errors.New("infinimesh info max attempts must not exceed 5")
	}
	if cfg.RetryBaseDelayMS > 5000 {
		return errors.New("infinimesh info retry base delay must not exceed 5000ms")
	}
	if cfg.RequestTimeoutSeconds > 120 {
		return errors.New("infinimesh info request timeout must not exceed 120 seconds")
	}
	if cfg.ResponseBodyMaxBytes < 1024 || cfg.ResponseBodyMaxBytes > 8<<20 {
		return errors.New("infinimesh info response body limit must be between 1024 and 8388608 bytes")
	}
	if cfg.MaxSources > 40 {
		return errors.New("infinimesh info max sources must not exceed 40")
	}
	return nil
}

func normalizeSpeechConfig(speech *SpeechConfig) error {
	defaults := Default().Speech
	if speech.TimeoutSeconds <= 0 {
		speech.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if speech.MaxAudioSeconds <= 0 {
		speech.MaxAudioSeconds = defaults.MaxAudioSeconds
	}
	if speech.MaxUploadBytes <= 0 {
		speech.MaxUploadBytes = defaults.MaxUploadBytes
	}
	if speech.MaxConcurrency <= 0 {
		speech.MaxConcurrency = defaults.MaxConcurrency
	}
	if speech.MaxPending < 0 {
		return errors.New("speech.max_pending cannot be negative")
	}
	if strings.TrimSpace(speech.DefaultLanguage) == "" {
		speech.DefaultLanguage = defaults.DefaultLanguage
	}
	if len(speech.AllowedHosts) == 0 {
		speech.AllowedHosts = append([]string(nil), defaults.AllowedHosts...)
	}
	speech.AllowedHosts = normalizeHostList(speech.AllowedHosts)
	if speech.RetainAudio {
		return errors.New("speech.retain_audio is not supported")
	}
	if !speech.Enabled {
		speech.Backend = "disabled"
		return nil
	}

	speech.Backend = strings.ToLower(strings.TrimSpace(speech.Backend))
	if speech.Backend != "openai-http" {
		return fmt.Errorf("unsupported speech backend %q", speech.Backend)
	}
	if strings.TrimSpace(speech.Model) == "" {
		return errors.New("speech.model is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(speech.BaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("speech.base_url must be an absolute https URL")
	}
	if parsed.Scheme != "https" {
		return errors.New("speech.base_url must use https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("speech.base_url cannot contain credentials, query parameters, or fragments")
	}
	if !containsFold(speech.AllowedHosts, parsed.Hostname()) {
		return fmt.Errorf("speech.base_url host %q is not listed in speech.allowed_hosts", parsed.Hostname())
	}
	speech.BaseURL = strings.TrimRight(parsed.String(), "/")
	return nil
}

func normalizeHostList(values []string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || containsFold(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func Default() Config {
	return Config{
		Gateway: GatewayConfig{
			Bind:            "127.0.0.1",
			Port:            18789,
			PairingRequired: false,
			RemoteAccess:    "disabled",
			RateLimit: RateLimitConfig{
				Enabled:           true,
				RequestsPerMinute: 600,
				Burst:             120,
			},
		},
		Model: ModelConfig{
			Mock:               false,
			HTTPTimeoutSeconds: 300,
			DisableThinking:    false,
			Fast: ModelProfile{
				Name:          "sparkclaw-fast",
				BaseURL:       "http://127.0.0.1:8001/v1",
				Model:         "Qwen/Qwen3.6-35B-A3B-FP8",
				ContextTokens: 131072,
				MTP:           true,
				MaxTokens:     1024,
			},
			Deep: ModelProfile{
				Name:          "sparkclaw-deep",
				BaseURL:       "http://127.0.0.1:8002/v1",
				Model:         "Qwen/Qwen3.6-27B-FP8",
				ContextTokens: 131072,
				MTP:           true,
				MaxTokens:     2048,
			},
			Embedding: ModelProfile{
				Name:          "sparkclaw-embedding",
				BaseURL:       "http://127.0.0.1:8003/v1",
				Model:         "Qwen/Qwen3-Embedding-0.6B",
				ContextTokens: 32768,
			},
			Reranker: ModelProfile{
				Name:          "sparkclaw-reranker",
				BaseURL:       "http://127.0.0.1:8004/v1",
				Model:         "Qwen/Qwen3-Reranker-0.6B",
				ContextTokens: 2048,
			},
			Guard: ModelProfile{
				Name:          "sparkclaw-guard",
				BaseURL:       "http://127.0.0.1:8005/v1",
				Model:         "Qwen/Qwen3Guard-0.6B",
				ContextTokens: 32768,
			},
		},
		Speech: SpeechConfig{
			Enabled:         false,
			Backend:         "openai-http",
			BaseURL:         "https://sparkclaw.infinimesh.cloud/asr",
			AllowedHosts:    []string{"sparkclaw.infinimesh.cloud"},
			Model:           "sparkclaw-asr",
			DefaultLanguage: "auto",
			TimeoutSeconds:  120,
			MaxAudioSeconds: 60,
			MaxUploadBytes:  3 << 20,
			MaxConcurrency:  1,
			MaxPending:      1,
			RetainAudio:     false,
		},
		Plugins: PluginsConfig{
			Entries: PluginEntriesConfig{
				InfinimeshInfo: InfinimeshInfoPluginConfig{
					Config: InfinimeshInfoConfig{
						BaseURL:               "https://info.infinimesh.cn",
						TokenBatchSize:        10,
						MaxAttempts:           3,
						RetryBaseDelayMS:      200,
						RequestTimeoutSeconds: 30,
						ResponseBodyMaxBytes:  4 << 20,
						Language:              "zh-CN",
						MaxSources:            8,
					},
				},
			},
		},
		Tools: ToolsConfig{
			Web: WebToolsConfig{
				Search: WebSearchToolConfig{
					Enabled:  false,
					Provider: "infinimesh-info",
				},
			},
			BrowserAutomation: BrowserAutomationToolConfig{
				Enabled:  false,
				Provider: "chromium-devtools-mcp",
				Profile:  "default",
			},
			Reminders: RemindersToolConfig{
				Enabled:        true,
				DefaultChannel: "web",
			},
			Notifications: NotificationsToolConfig{
				Channels: map[string]NotificationChannelConfig{
					"telegram": {
						Enabled:            false,
						Provider:           "telegram-bot-api",
						BaseURL:            "https://api.telegram.org",
						UpdateMode:         "long-polling",
						PollTimeoutSeconds: 30,
						PrivateChatsOnly:   true,
						MaxDownloadBytes:   20 << 20,
						MaxAttachments:     5,
						MaxVoiceSeconds:    120,
						MaxConcurrency:     4,
						MaxPending:         32,
					},
					"weixin": {
						Enabled:    false,
						Provider:   "openclaw-weixin-qr",
						BaseURL:    "https://ilinkai.weixin.qq.com",
						CDNBaseURL: "https://novac2c.cdn.weixin.qq.com/c2c",
					},
				},
			},
		},
		Security: SecurityConfig{
			ExternalContentUntrusted:              true,
			ApprovalRequiredForDangerousTools:     true,
			SandboxRequiredForMutatingTools:       true,
			DangerousToolsRequireDeepVerification: true,
			DeniedTools: []string{
				"host_shell.exec",
				"file.delete.permanent",
				"browser.submit_form.auto",
			},
			ApprovalRequiredTools: []string{},
			ToolPolicyPath:        "./configs/tools.policy.json",
			BrowserReadAllowHosts: []string{},
		},
		Sandbox: SandboxConfig{
			Enabled:         true,
			Backend:         "local-docker",
			RunnerURL:       "",
			Image:           "alpine:3.22",
			Network:         "none",
			WorkspaceAccess: "rw",
			HostAccess:      "forbidden",
		},
		Adapters: AdapterConfig{
			BrowserAutomation: BrowserAutomationAdapterConfig{
				MCPCommand: "npx",
				MCPArgs:    []string{"-y", "chrome-devtools-mcp@latest"},
				TimeoutMS:  15000,
				ProfileDir: "./data/browser-profiles",
			},
		},
		Memory: MemoryConfig{
			Enabled:              true,
			WritePolicy:          "candidate_then_confirm",
			AllowSensitiveMemory: false,
			RetentionDays:        180,
			RedactPatterns:       []string{"api_key", "password", "token", "ssh_key"},
		},
		Workspaces: WorkspaceConfig{
			DefaultRoot: "./data/workspaces",
			Allowlist:   []string{"./data/workspaces"},
		},
		Storage: StorageConfig{
			TraceDir:        "./data/traces",
			LogDir:          "./data/logs",
			ArtifactBackend: "filesystem",
			ArtifactDir:     "./data/artifacts",
			ArtifactBucket:  "sparkclaw",
			S3Endpoint:      "",
			S3Region:        "us-east-1",
		},
		State: StateConfig{
			Backend:           "file",
			Path:              "./data/memory/gateway-state.json",
			DSN:               "",
			EncryptAtRest:     false,
			EncryptionKey:     "",
			EncryptionKeyFile: "",
			CredentialKey:     "",
			CredentialKeyFile: "./data/memory/gateway-credentials.key",
		},
		Skills: SkillsConfig{
			Dirs: []string{"./skills", "./data/skills"},
		},
		Runtime: RuntimeConfig{
			ObservationSummaryMaxBytes: 2400,
			ReactMaxDurationSeconds:    180,
			ReactMaxToolCalls:          16,
			ReactMaxObservationBytes:   48000,
			ReactMaxNoProgressActions:  3,
			ReactMaxRepeatedToolCalls:  3,
		},
		Logging: LoggingConfig{
			Level:          "info",
			RedactPatterns: []string{"api_key", "password", "token", "ssh_key"},
		},
	}
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("SPARKCLAW_BIND"); v != "" {
		cfg.Gateway.Bind = v
	}
	if v := os.Getenv("SPARKCLAW_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Gateway.Port = port
		}
	}
	if v := os.Getenv("SPARKCLAW_API_TOKEN"); v != "" {
		cfg.Gateway.APIToken = v
	}
	if v := os.Getenv("SPARKCLAW_PAIRING_REQUIRED"); v != "" {
		cfg.Gateway.PairingRequired = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_RATE_LIMIT_ENABLED"); v != "" {
		cfg.Gateway.RateLimit.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_RATE_LIMIT_PER_MINUTE"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil {
			cfg.Gateway.RateLimit.RequestsPerMinute = limit
		}
	}
	if v := os.Getenv("SPARKCLAW_RATE_LIMIT_BURST"); v != "" {
		if burst, err := strconv.Atoi(v); err == nil {
			cfg.Gateway.RateLimit.Burst = burst
		}
	}
	if v := os.Getenv("SPARKCLAW_WORKSPACE_ROOT"); v != "" {
		cfg.Workspaces.DefaultRoot = v
		cfg.Workspaces.Allowlist = []string{v}
	}
	if v := os.Getenv("SPARKCLAW_TRACE_DIR"); v != "" {
		cfg.Storage.TraceDir = v
	}
	if v := os.Getenv("SPARKCLAW_ARTIFACT_BACKEND"); v != "" {
		cfg.Storage.ArtifactBackend = v
	}
	if v := os.Getenv("SPARKCLAW_ARTIFACT_DIR"); v != "" {
		cfg.Storage.ArtifactDir = v
	}
	if v := os.Getenv("SPARKCLAW_ARTIFACT_BUCKET"); v != "" {
		cfg.Storage.ArtifactBucket = v
	}
	if v := os.Getenv("SPARKCLAW_S3_ENDPOINT"); v != "" {
		cfg.Storage.S3Endpoint = v
	}
	if v := os.Getenv("SPARKCLAW_S3_REGION"); v != "" {
		cfg.Storage.S3Region = v
	}
	if v := os.Getenv("SPARKCLAW_S3_ACCESS_KEY"); v != "" {
		cfg.Storage.S3AccessKey = v
	}
	if v := os.Getenv("SPARKCLAW_S3_SECRET_KEY"); v != "" {
		cfg.Storage.S3SecretKey = v
	}
	if v := os.Getenv("SPARKCLAW_STATE_BACKEND"); v != "" {
		cfg.State.Backend = v
	}
	if v := os.Getenv("SPARKCLAW_STATE_PATH"); v != "" {
		cfg.State.Path = v
	}
	if v := os.Getenv("SPARKCLAW_STATE_DSN"); v != "" {
		cfg.State.DSN = v
	}
	if v := os.Getenv("SPARKCLAW_POSTGRES_DSN"); v != "" {
		cfg.State.DSN = v
	}
	if v := os.Getenv("SPARKCLAW_STATE_ENCRYPT_AT_REST"); v != "" {
		cfg.State.EncryptAtRest = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_STATE_ENCRYPTION_KEY"); v != "" {
		cfg.State.EncryptionKey = v
	}
	if v := os.Getenv("SPARKCLAW_STATE_ENCRYPTION_KEY_FILE"); v != "" {
		cfg.State.EncryptionKeyFile = v
	}
	if v := os.Getenv("SPARKCLAW_CREDENTIAL_KEY"); v != "" {
		cfg.State.CredentialKey = v
	}
	if v := os.Getenv("SPARKCLAW_CREDENTIAL_KEY_FILE"); v != "" {
		cfg.State.CredentialKeyFile = v
	}
	if v := os.Getenv("SPARKCLAW_MODEL_MODE"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "external", "external-model", "real", "local", "dgx-spark-local":
			cfg.Model.Mock = false
		case "mock":
			cfg.Model.Mock = true
		}
	}
	if v := os.Getenv("SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			cfg.Model.HTTPTimeoutSeconds = seconds
		}
	}
	if v := os.Getenv("SPARKCLAW_MODEL_TIMEOUT_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			cfg.Model.HTTPTimeoutSeconds = seconds
		}
	}
	if v := os.Getenv("SPARKCLAW_MODEL_DISABLE_THINKING"); v != "" {
		cfg.Model.DisableThinking = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_ENABLED"); v != "" {
		cfg.Speech.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_BACKEND"); v != "" {
		cfg.Speech.Backend = v
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_BASE_URL"); v != "" {
		cfg.Speech.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_ALLOWED_HOSTS"); v != "" {
		cfg.Speech.AllowedHosts = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_MODEL"); v != "" {
		cfg.Speech.Model = v
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_DEFAULT_LANGUAGE"); v != "" {
		cfg.Speech.DefaultLanguage = v
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_TIMEOUT_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			cfg.Speech.TimeoutSeconds = seconds
		}
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_MAX_AUDIO_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			cfg.Speech.MaxAudioSeconds = seconds
		}
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_MAX_UPLOAD_BYTES"); v != "" {
		if maxBytes, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Speech.MaxUploadBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_MAX_CONCURRENCY"); v != "" {
		if maxConcurrency, err := strconv.Atoi(v); err == nil {
			cfg.Speech.MaxConcurrency = maxConcurrency
		}
	}
	if v := os.Getenv("SPARKCLAW_SPEECH_MAX_PENDING"); v != "" {
		if maxPending, err := strconv.Atoi(v); err == nil {
			cfg.Speech.MaxPending = maxPending
		}
	}
	if v := os.Getenv("SPARKCLAW_FAST_BASE_URL"); v != "" {
		cfg.Model.Fast.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_FAST_MODEL"); v != "" {
		cfg.Model.Fast.Model = v
	}
	if v := os.Getenv("SPARKCLAW_FAST_SERVED_NAME"); v != "" {
		cfg.Model.Fast.Name = v
	}
	if v := os.Getenv("SPARKCLAW_FAST_MAX_TOKENS"); v != "" {
		if tokens, err := strconv.Atoi(v); err == nil {
			cfg.Model.Fast.MaxTokens = tokens
		}
	}
	if v := os.Getenv("SPARKCLAW_FAST_CONTEXT_TOKENS"); v != "" {
		if tokens, err := strconv.Atoi(v); err == nil {
			cfg.Model.Fast.ContextTokens = tokens
		}
	}
	if v := os.Getenv("SPARKCLAW_DEEP_BASE_URL"); v != "" {
		cfg.Model.Deep.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_DEEP_MODEL"); v != "" {
		cfg.Model.Deep.Model = v
	}
	if v := os.Getenv("SPARKCLAW_DEEP_SERVED_NAME"); v != "" {
		cfg.Model.Deep.Name = v
	}
	if v := os.Getenv("SPARKCLAW_DEEP_MAX_TOKENS"); v != "" {
		if tokens, err := strconv.Atoi(v); err == nil {
			cfg.Model.Deep.MaxTokens = tokens
		}
	}
	if v := os.Getenv("SPARKCLAW_DEEP_CONTEXT_TOKENS"); v != "" {
		if tokens, err := strconv.Atoi(v); err == nil {
			cfg.Model.Deep.ContextTokens = tokens
		}
	}
	if v := os.Getenv("SPARKCLAW_EMBEDDING_BASE_URL"); v != "" {
		cfg.Model.Embedding.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_EMBEDDING_MODEL"); v != "" {
		cfg.Model.Embedding.Model = v
	}
	if v := os.Getenv("SPARKCLAW_RERANKER_BASE_URL"); v != "" {
		cfg.Model.Reranker.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_RERANKER_MODEL"); v != "" {
		cfg.Model.Reranker.Model = v
	}
	if v := os.Getenv("SPARKCLAW_GUARD_BASE_URL"); v != "" {
		cfg.Model.Guard.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_GUARD_MODEL"); v != "" {
		cfg.Model.Guard.Model = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_READ_ALLOW_HOSTS"); v != "" {
		cfg.Security.BrowserReadAllowHosts = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_WEB_SEARCH_ENABLED"); v != "" {
		cfg.Tools.Web.Search.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_WEB_SEARCH_PROVIDER"); v != "" {
		cfg.Tools.Web.Search.Provider = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_ENABLED"); v != "" {
		cfg.Tools.BrowserAutomation.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_PROVIDER"); v != "" {
		cfg.Tools.BrowserAutomation.Provider = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_PROFILE"); v != "" {
		cfg.Tools.BrowserAutomation.Profile = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_MCP_COMMAND"); v != "" {
		cfg.Adapters.BrowserAutomation.MCPCommand = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_MCP_ARGS"); v != "" {
		cfg.Adapters.BrowserAutomation.MCPArgs = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_TIMEOUT_MS"); v != "" {
		if timeoutMS, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.BrowserAutomation.TimeoutMS = timeoutMS
		}
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE"); v != "" {
		cfg.Adapters.BrowserAutomation.ChromiumExecutable = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_PROFILE_DIR"); v != "" {
		cfg.Adapters.BrowserAutomation.ProfileDir = v
	}
	if v := os.Getenv("SPARKCLAW_REMINDERS_ENABLED"); v != "" {
		cfg.Tools.Reminders.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_REMINDERS_DEFAULT_CHANNEL"); v != "" {
		cfg.Tools.Reminders.DefaultChannel = v
	}
	ensureNotificationChannels(&cfg.Tools.Notifications)
	if v := os.Getenv("SPARKCLAW_WEIXIN_NOTIFICATION_ENABLED"); v != "" {
		ch := cfg.Tools.Notifications.Channels["weixin"]
		ch.Enabled = parseBool(v)
		cfg.Tools.Notifications.Channels["weixin"] = ch
	}
	if v := os.Getenv("SPARKCLAW_WEIXIN_NOTIFICATION_PROVIDER"); v != "" {
		ch := cfg.Tools.Notifications.Channels["weixin"]
		ch.Provider = v
		cfg.Tools.Notifications.Channels["weixin"] = ch
	}
	if v := os.Getenv("SPARKCLAW_WEIXIN_NOTIFICATION_BASE_URL"); v != "" {
		ch := cfg.Tools.Notifications.Channels["weixin"]
		ch.BaseURL = v
		cfg.Tools.Notifications.Channels["weixin"] = ch
	}
	if v := os.Getenv("SPARKCLAW_WEIXIN_CDN_BASE_URL"); v != "" {
		ch := cfg.Tools.Notifications.Channels["weixin"]
		ch.CDNBaseURL = v
		cfg.Tools.Notifications.Channels["weixin"] = ch
	}
	if v := os.Getenv("SPARKCLAW_WEIXIN_NOTIFICATION_TOKEN"); v != "" {
		ch := cfg.Tools.Notifications.Channels["weixin"]
		ch.Token = v
		cfg.Tools.Notifications.Channels["weixin"] = ch
	}
	if v := os.Getenv("SPARKCLAW_WEIXIN_NOTIFICATION_RECIPIENT"); v != "" {
		ch := cfg.Tools.Notifications.Channels["weixin"]
		ch.Recipient = v
		cfg.Tools.Notifications.Channels["weixin"] = ch
	}
	if v := os.Getenv("SPARKCLAW_TELEGRAM_ENABLED"); v != "" {
		ch := cfg.Tools.Notifications.Channels["telegram"]
		ch.Enabled = parseBool(v)
		cfg.Tools.Notifications.Channels["telegram"] = ch
	}
	if v := os.Getenv("SPARKCLAW_TELEGRAM_BASE_URL"); v != "" {
		ch := cfg.Tools.Notifications.Channels["telegram"]
		ch.BaseURL = v
		cfg.Tools.Notifications.Channels["telegram"] = ch
	}
	if v := os.Getenv("SPARKCLAW_TELEGRAM_POLL_TIMEOUT_SECONDS"); v != "" {
		if value, err := strconv.Atoi(v); err == nil {
			ch := cfg.Tools.Notifications.Channels["telegram"]
			ch.PollTimeoutSeconds = value
			cfg.Tools.Notifications.Channels["telegram"] = ch
		}
	}
	if v := os.Getenv("SPARKCLAW_TELEGRAM_MAX_DOWNLOAD_BYTES"); v != "" {
		if value, err := strconv.ParseInt(v, 10, 64); err == nil {
			ch := cfg.Tools.Notifications.Channels["telegram"]
			ch.MaxDownloadBytes = value
			cfg.Tools.Notifications.Channels["telegram"] = ch
		}
	}
	if v := os.Getenv("SPARKCLAW_TELEGRAM_MAX_ATTACHMENTS"); v != "" {
		if value, err := strconv.Atoi(v); err == nil {
			ch := cfg.Tools.Notifications.Channels["telegram"]
			ch.MaxAttachments = value
			cfg.Tools.Notifications.Channels["telegram"] = ch
		}
	}
	if v := os.Getenv("SPARKCLAW_TELEGRAM_MAX_VOICE_SECONDS"); v != "" {
		if value, err := strconv.Atoi(v); err == nil {
			ch := cfg.Tools.Notifications.Channels["telegram"]
			ch.MaxVoiceSeconds = value
			cfg.Tools.Notifications.Channels["telegram"] = ch
		}
	}
	if v := os.Getenv("SPARKCLAW_TELEGRAM_MAX_CONCURRENCY"); v != "" {
		if value, err := strconv.Atoi(v); err == nil {
			ch := cfg.Tools.Notifications.Channels["telegram"]
			ch.MaxConcurrency = value
			cfg.Tools.Notifications.Channels["telegram"] = ch
		}
	}
	if v := os.Getenv("SPARKCLAW_TELEGRAM_MAX_PENDING"); v != "" {
		if value, err := strconv.Atoi(v); err == nil {
			ch := cfg.Tools.Notifications.Channels["telegram"]
			ch.MaxPending = value
			cfg.Tools.Notifications.Channels["telegram"] = ch
		}
	}
	info := &cfg.Plugins.Entries.InfinimeshInfo.Config
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_BASE_URL"); v != "" {
		info.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_TOKEN_BATCH_SIZE"); v != "" {
		if count, err := strconv.Atoi(v); err == nil {
			info.TokenBatchSize = count
		}
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_MAX_ATTEMPTS"); v != "" {
		if attempts, err := strconv.Atoi(v); err == nil {
			info.MaxAttempts = attempts
		}
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_RETRY_BASE_DELAY_MS"); v != "" {
		if delay, err := strconv.Atoi(v); err == nil {
			info.RetryBaseDelayMS = delay
		}
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_REQUEST_TIMEOUT_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			info.RequestTimeoutSeconds = seconds
		}
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_RESPONSE_BODY_MAX_BYTES"); v != "" {
		if maxBytes, err := strconv.ParseInt(v, 10, 64); err == nil {
			info.ResponseBodyMaxBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_LANGUAGE"); v != "" {
		info.Language = v
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_MAX_SOURCES"); v != "" {
		if count, err := strconv.Atoi(v); err == nil {
			info.MaxSources = count
		}
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF"); v != "" {
		info.EntitlementProof = v
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION"); v != "" {
		info.DeviceAttestation = v
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF"); v != "" {
		info.LicenseProof = v
	}
	if v := os.Getenv("SPARKCLAW_MEMORY_RETENTION_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil {
			cfg.Memory.RetentionDays = days
		}
	}
	if v := os.Getenv("SPARKCLAW_TOOLS_POLICY_PATH"); v != "" {
		cfg.Security.ToolPolicyPath = v
	}
	if v := os.Getenv("SPARKCLAW_SANDBOX_BACKEND"); v != "" {
		cfg.Sandbox.Backend = v
	}
	if v := os.Getenv("SPARKCLAW_SANDBOX_RUNNER_URL"); v != "" {
		cfg.Sandbox.RunnerURL = v
	}
	if v := os.Getenv("SPARKCLAW_SANDBOX_IMAGE"); v != "" {
		cfg.Sandbox.Image = v
	}
	if v := os.Getenv("SPARKCLAW_SANDBOX_NETWORK"); v != "" {
		cfg.Sandbox.Network = v
	}
	if v := os.Getenv("SPARKCLAW_SKILLS_DIRS"); v != "" {
		cfg.Skills.Dirs = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_OBSERVATION_SUMMARY_MAX_BYTES"); v != "" {
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.ObservationSummaryMaxBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_REACT_MAX_OBSERVATION_BYTES"); v != "" {
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.ReactMaxObservationBytes = maxBytes
		}
	}
	if cfg.State.Path != "" {
		if abs, err := filepath.Abs(cfg.State.Path); err == nil {
			cfg.State.Path = abs
		}
	}
	if cfg.State.EncryptionKeyFile != "" {
		if abs, err := filepath.Abs(cfg.State.EncryptionKeyFile); err == nil {
			cfg.State.EncryptionKeyFile = abs
		}
	}
	if cfg.State.CredentialKeyFile != "" {
		if abs, err := filepath.Abs(cfg.State.CredentialKeyFile); err == nil {
			cfg.State.CredentialKeyFile = abs
		}
	}
	if cfg.Storage.ArtifactDir != "" {
		if abs, err := filepath.Abs(cfg.Storage.ArtifactDir); err == nil {
			cfg.Storage.ArtifactDir = abs
		}
	}
	for i, p := range cfg.Skills.Dirs {
		if abs, err := filepath.Abs(p); err == nil {
			cfg.Skills.Dirs[i] = abs
		}
	}
}

func applyInfinimeshInfoCredentials(cfg *Config) error {
	info := &cfg.Plugins.Entries.InfinimeshInfo.Config
	var err error
	info.EntitlementProof, err = secretFromEnvOrFile(
		info.EntitlementProof,
		"SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE",
	)
	if err != nil {
		return err
	}
	info.DeviceAttestation, err = secretFromEnvOrFile(
		info.DeviceAttestation,
		"SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION_FILE",
	)
	if err != nil {
		return err
	}
	info.LicenseProof, err = secretFromEnvOrFile(
		info.LicenseProof,
		"SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE",
	)
	return err
}

func secretFromEnvOrFile(direct, fileEnv string) (string, error) {
	if value := strings.TrimSpace(direct); value != "" {
		return value, nil
	}
	path := strings.TrimSpace(os.Getenv(fileEnv))
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileEnv, err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", fmt.Errorf("read %s: credential file is empty", fileEnv)
	}
	return value, nil
}

func ensureNotificationChannels(cfg *NotificationsToolConfig) {
	if cfg.Channels == nil {
		cfg.Channels = map[string]NotificationChannelConfig{}
	}
	if _, ok := cfg.Channels["weixin"]; !ok {
		cfg.Channels["weixin"] = NotificationChannelConfig{
			Provider:   "openclaw-weixin-qr",
			BaseURL:    "https://ilinkai.weixin.qq.com",
			CDNBaseURL: "https://novac2c.cdn.weixin.qq.com/c2c",
		}
	}
	if _, ok := cfg.Channels["telegram"]; !ok {
		cfg.Channels["telegram"] = Default().Tools.Notifications.Channels["telegram"]
	}
}

func normalizeNotificationChannels(cfg *NotificationsToolConfig) error {
	ensureNotificationChannels(cfg)
	telegram := cfg.Channels["telegram"]
	defaults := Default().Tools.Notifications.Channels["telegram"]
	if strings.TrimSpace(telegram.Provider) == "" {
		telegram.Provider = defaults.Provider
	}
	if strings.TrimSpace(telegram.BaseURL) == "" {
		telegram.BaseURL = defaults.BaseURL
	}
	if strings.TrimSpace(telegram.UpdateMode) == "" {
		telegram.UpdateMode = defaults.UpdateMode
	}
	if telegram.PollTimeoutSeconds <= 0 {
		telegram.PollTimeoutSeconds = defaults.PollTimeoutSeconds
	}
	if telegram.MaxDownloadBytes <= 0 {
		telegram.MaxDownloadBytes = defaults.MaxDownloadBytes
	}
	if telegram.MaxAttachments <= 0 {
		telegram.MaxAttachments = defaults.MaxAttachments
	}
	if telegram.MaxVoiceSeconds <= 0 {
		telegram.MaxVoiceSeconds = defaults.MaxVoiceSeconds
	}
	if telegram.MaxConcurrency <= 0 {
		telegram.MaxConcurrency = defaults.MaxConcurrency
	}
	if telegram.MaxPending == 0 {
		telegram.MaxPending = defaults.MaxPending
	}
	telegram.PrivateChatsOnly = true
	telegram.Provider = strings.ToLower(strings.TrimSpace(telegram.Provider))
	telegram.BaseURL = strings.TrimRight(strings.TrimSpace(telegram.BaseURL), "/")
	telegram.UpdateMode = strings.ToLower(strings.TrimSpace(telegram.UpdateMode))
	if telegram.Provider != "telegram-bot-api" {
		return fmt.Errorf("unsupported Telegram provider %q", telegram.Provider)
	}
	endpoint, err := url.Parse(telegram.BaseURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("Telegram baseUrl must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if strings.EqualFold(endpoint.Hostname(), "api.telegram.org") && endpoint.Scheme != "https" {
		return errors.New("Telegram api.telegram.org baseUrl must use HTTPS")
	}
	if telegram.UpdateMode != "long-polling" {
		return fmt.Errorf("unsupported Telegram updateMode %q", telegram.UpdateMode)
	}
	if telegram.PollTimeoutSeconds < 1 || telegram.PollTimeoutSeconds > 50 {
		return errors.New("Telegram pollTimeoutSeconds must be between 1 and 50")
	}
	if telegram.MaxDownloadBytes < 1 || telegram.MaxDownloadBytes > 20<<20 {
		return errors.New("Telegram maxDownloadBytes must be between 1 and 20971520")
	}
	if telegram.MaxAttachments < 1 || telegram.MaxAttachments > 5 {
		return errors.New("Telegram maxAttachments must be between 1 and 5")
	}
	if telegram.MaxVoiceSeconds < 1 || telegram.MaxVoiceSeconds > 600 {
		return errors.New("Telegram maxVoiceSeconds must be between 1 and 600")
	}
	if telegram.MaxConcurrency < 1 || telegram.MaxConcurrency > 16 {
		return errors.New("Telegram maxConcurrency must be between 1 and 16")
	}
	if telegram.MaxPending < telegram.MaxConcurrency || telegram.MaxPending > 1024 {
		return errors.New("Telegram maxPending must be at least maxConcurrency and at most 1024")
	}
	cfg.Channels["telegram"] = telegram
	return nil
}

func applyToolPolicyFile(cfg *Config) error {
	path := strings.TrimSpace(cfg.Security.ToolPolicyPath)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var policy toolPolicyFile
	if err := json.Unmarshal(raw, &policy); err != nil {
		return err
	}
	cfg.Security.DeniedTools = appendUnique(cfg.Security.DeniedTools, policy.Deny...)
	cfg.Security.ApprovalRequiredTools = appendUnique(cfg.Security.ApprovalRequiredTools, policy.ApprovalRequired...)
	if abs, err := filepath.Abs(path); err == nil {
		cfg.Security.ToolPolicyPath = abs
	}
	return nil
}

func appendUnique(base []string, values ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range base {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "required":
		return true
	default:
		return false
	}
}
