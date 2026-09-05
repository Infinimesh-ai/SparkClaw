package config

import (
	"encoding/json"
	"regexp"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
)

const (
	LocalMindMCPServerKey          = "localmind"
	LocalMindMCPServerName         = "localmind-ai"
	LocalMindMCPProtocolVersion    = "2025-06-18"
	LocalMindMCPDefaultNamespace   = "localmind"
	LocalMindMCPDefaultMaxResponse = int64(16 << 20)
)

var (
	mcpServerNamePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	environmentNamePattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	webChatProxyTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43,128}$`)
)

type Config struct {
	Gateway       GatewayConfig              `json:"gateway"`
	JingSiLAN     JingSiLANConfig            `json:"jingsi_lan"`
	JingSiRuntime JingSiRuntimeConfig        `json:"jingsi_runtime_v1"`
	Model         ModelConfig                `json:"model"`
	Speech        SpeechConfig               `json:"speech"`
	ISCPPairing   ISCPPairingConfig          `json:"iscp_pairing"`
	MCPAccess     MCPAccessConfig            `json:"mcp_access"`
	MCPServers    map[string]MCPServerConfig `json:"mcp_servers,omitempty"`
	Plugins       PluginsConfig              `json:"plugins"`
	Tools         ToolsConfig                `json:"tools"`
	Security      SecurityConfig             `json:"security"`
	Sandbox       SandboxConfig              `json:"sandbox"`
	Adapters      AdapterConfig              `json:"adapters"`
	Memory        MemoryConfig               `json:"memory"`
	// PassiveNotifications bounds the durable ISCP notification inbox.
	PassiveNotifications PassiveNotificationsConfig `json:"passive_notifications"`
	Workspaces           WorkspaceConfig            `json:"workspaces"`
	Storage              StorageConfig              `json:"storage"`
	State                StateConfig                `json:"state"`
	Runtime              RuntimeConfig              `json:"runtime"`
	Logging              LoggingConfig              `json:"logging"`
	// Warnings collects load-time findings that do not block startup, such
	// as a remote model endpoint configured without an API key. Load fills
	// it and the entrypoint logs each entry.
	Warnings []string `json:"-"`
}

type JingSiLANConfig struct {
	Enabled         bool   `json:"enabled"`
	SessionID       string `json:"session_id,omitempty"`
	MaxMessageBytes int    `json:"max_message_bytes"`
}

// JingSiRuntimeConfig owns the accepted JingSi→SparkClaw Runtime v1 surface.
// The bearer itself is secret-only and can only enter through the environment
// or an owner-only file; it is never serialized back into public config.
type JingSiRuntimeConfig struct {
	Enabled         bool   `json:"enabled"`
	StateDir        string `json:"state_dir"`
	BearerTokenFile string `json:"bearer_token_file,omitempty"`
	MaxConcurrent   int    `json:"max_concurrent"`
	BearerToken     string `json:"-"`
}

type GatewayConfig struct {
	Bind            string `json:"bind"`
	Port            int    `json:"port"`
	PairingRequired bool   `json:"pairing_required"`
	RemoteAccess    string `json:"remote_access"`
	APIToken        string `json:"api_token,omitempty"`
	// WebChatProxyToken authenticates the private WebChat reverse proxy only
	// for the local pairing bootstrap. It is never a client or owner token.
	WebChatProxyToken string `json:"-"`
	// BridgeToken is the dedicated credential for the loopback ISCP bridge
	// dispatch routes. When set, bridge dispatch requires exactly this bearer
	// token; when empty, bridge dispatch requires gateway authentication and
	// fails closed (503) in the no-auth posture.
	BridgeToken string          `json:"bridge_token,omitempty"`
	RateLimit   RateLimitConfig `json:"rate_limit"`
}

type RateLimitConfig struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute"`
	Burst             int  `json:"burst"`
}

type ModelConfig struct {
	CapacityProfile string       `json:"capacity_profile"`
	CapacityCatalog string       `json:"capacity_catalog"`
	Fast            ModelProfile `json:"fast"`
	Deep            ModelProfile `json:"deep"`
	Embedding       ModelProfile `json:"embedding"`
	Guard           ModelProfile `json:"guard"`
	// Mock is resolved from the selected capacity profile; it is not a
	// file or environment knob.
	Mock               bool `json:"-"`
	HTTPTimeoutSeconds int  `json:"http_timeout_seconds"`
	DisableThinking    bool `json:"disable_thinking"`
	// APIKey is the bearer token sent to every OpenAI-compatible model
	// server. It is secret-only: OPENAI_API_KEY in the environment, never
	// the config file.
	APIKey string `json:"-"`
}

type ModelProfile struct {
	Name                  string                                  `json:"name"`
	BaseURL               string                                  `json:"base_url"`
	Model                 string                                  `json:"model"`
	MTP                   bool                                    `json:"mtp"`
	CapacityPhysicalModel string                                  `json:"-"`
	ContextTokens         int                                     `json:"-"`
	OutputBudgets         map[modelcapacity.OutputBudgetClass]int `json:"-"`
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

type ISCPPairingConfig struct {
	Enabled               bool   `json:"enabled"`
	DomainID              string `json:"domain_id,omitempty"`
	AuthorityURL          string `json:"authority_url,omitempty"`
	TokenEnv              string `json:"token_env,omitempty"`
	TokenFile             string `json:"token_file,omitempty"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
	ResponseBodyMaxBytes  int64  `json:"response_body_max_bytes"`
	TicketTTLSeconds      int    `json:"ticket_ttl_seconds"`
	ExpectedTicketType    string `json:"expected_ticket_type"`
}

type MCPAccessConfig struct {
	LocalDomainID string `json:"local_domain_id"`
	// AllowedOrigins lists additional web origins that may reach the /mcp
	// endpoint from a browser context. Loopback and gateway-bind origins are
	// always allowed; an empty list keeps the endpoint loopback/same-origin
	// only. Requests without an Origin header are unaffected.
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
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
	LicenseID             string `json:"-"`
	LicenseKey            string `json:"-"`
}

func (cfg InfinimeshInfoConfig) Configured() bool {
	return infinimeshinfo.Config{
		LicenseID:  cfg.LicenseID,
		LicenseKey: cfg.LicenseKey,
	}.Configured()
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
	Enabled             bool   `json:"enabled"`
	DefaultChannel      string `json:"defaultChannel"`
	MaxDeliveryAttempts int    `json:"maxDeliveryAttempts"`
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

type MCPServerConfig struct {
	URL                     string   `json:"url,omitempty"`
	TokenEnv                string   `json:"token_env,omitempty"`
	TokenFile               string   `json:"token_file,omitempty"`
	Transport               string   `json:"transport,omitempty"`
	URLEnv                  string   `json:"url_env,omitempty"`
	BearerTokenEnv          string   `json:"bearer_token_env,omitempty"`
	Namespace               string   `json:"namespace,omitempty"`
	ExpectedServerName      string   `json:"expected_server_name,omitempty"`
	ProtocolVersion         string   `json:"protocol_version,omitempty"`
	AllowMutations          bool     `json:"allow_mutations,omitempty"`
	AllowPrivateHTTP        bool     `json:"allow_private_http,omitempty"`
	ToolAllow               []string `json:"tool_allow,omitempty"`
	ToolDeny                []string `json:"tool_deny,omitempty"`
	RequestTimeoutSeconds   int      `json:"request_timeout_seconds,omitempty"`
	LongCallGraceSeconds    int      `json:"long_call_grace_seconds,omitempty"`
	MaxResponseBytes        int64    `json:"max_response_bytes,omitempty"`
	StateOutputMaxBytes     int      `json:"state_output_max_bytes,omitempty"`
	ArchiveOutputMaxBytes   int      `json:"archive_output_max_bytes,omitempty"`
	RefreshIntervalSeconds  int      `json:"refresh_interval_seconds,omitempty"`
	DiscoveryRefreshSeconds int      `json:"discovery_refresh_seconds,omitempty"`
	ResponseBodyMaxBytes    int64    `json:"response_body_max_bytes,omitempty"`
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

// PassiveNotificationsConfig bounds the durable passive-notification inbox
// fed by the ISCP bridge. MaxPerOwner caps stored records per owner (read
// records are evicted oldest-first before unread ones); RetentionDays expires
// records like memory.retention_days does for memories. Zero disables the
// respective bound; replaying an idempotency key whose record was pruned
// re-creates the notification.
type PassiveNotificationsConfig struct {
	MaxPerOwner   int `json:"max_per_owner"`
	RetentionDays int `json:"retention_days"`
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
	DocumentOCR       DocumentOCRAdapterConfig       `json:"documentOCR"`
	PPTXVisualQA      PPTXVisualQAAdapterConfig      `json:"pptxVisualQA"`
}

type BrowserAutomationAdapterConfig struct {
	TimeoutMS            int                       `json:"timeoutMs"`
	StartupTimeoutMS     int                       `json:"startupTimeoutMs"`
	SettleTimeoutMS      int                       `json:"settleTimeoutMs"`
	SettleQuietPeriodMS  int                       `json:"settleQuietPeriodMs"`
	SettlePollIntervalMS int                       `json:"settlePollIntervalMs"`
	RouteRebindLimit     int                       `json:"routeRebindLimit"`
	PlaywrightExtension  PlaywrightExtensionConfig `json:"playwrightExtension"`
}

type PlaywrightExtensionConfig struct {
	ControllerSocket string `json:"controllerSocket"`
	ProfileID        string `json:"profileID"`
	ConnectTimeoutMS int    `json:"connectTimeoutMs"`
}

type DocumentOCRAdapterConfig struct {
	Enabled        bool     `json:"enabled"`
	Provider       string   `json:"provider"`
	BaseURL        string   `json:"baseUrl"`
	AllowedHosts   []string `json:"allowedHosts"`
	Model          string   `json:"model"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	MaxUploadBytes int64    `json:"maxUploadBytes"`
	MaxOutputBytes int      `json:"maxOutputBytes"`
	MaxTokens      int      `json:"-"`
	ContextTokens  int      `json:"-"`
	MaxConcurrency int      `json:"maxConcurrency"`
	MaxPending     int      `json:"maxPending"`
}

type PPTXVisualQAAdapterConfig struct {
	Phase                     string   `json:"phase"`
	RepairQualifiedClasses    []string `json:"repairQualifiedClasses"`
	RepairQualifiedOperations []string `json:"repairQualifiedOperations"`
	BlockingQualifiedClasses  []string `json:"blockingQualifiedClasses"`
	MaxRepairAttempts         int      `json:"maxRepairAttempts"`
	BaseURL                   string   `json:"baseUrl"`
	AllowedHosts              []string `json:"allowedHosts"`
	TimeoutSeconds            int      `json:"timeoutSeconds"`
	MaxInputBytes             int64    `json:"maxInputBytes"`
	MaxPDFBytes               int64    `json:"maxPDFBytes"`
	MaxPages                  int      `json:"maxPages"`
	MaxChangedPages           int      `json:"maxChangedPages"`
	RasterScale               float64  `json:"rasterScale"`
	MaxPagePixels             int64    `json:"maxPagePixels"`
	MaxPNGBytes               int      `json:"maxPNGBytes"`
	DiagnosticToleranceMilli  int      `json:"diagnosticToleranceMilli"`
	ReadinessTTLSeconds       int      `json:"readinessTTLSeconds"`
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
	Backend                   string `json:"backend"`
	Path                      string `json:"path"`
	DSN                       string `json:"dsn"`
	StartupTimeoutSeconds     int    `json:"startup_timeout_seconds"`
	ReadTimeoutSeconds        int    `json:"read_timeout_seconds"`
	WriteTimeoutSeconds       int    `json:"write_timeout_seconds"`
	TransactionTimeoutSeconds int    `json:"transaction_timeout_seconds"`
	EncryptAtRest             bool   `json:"encrypt_at_rest"`
	EncryptionKey             string `json:"encryption_key,omitempty"`
	EncryptionKeyFile         string `json:"encryption_key_file,omitempty"`
	CredentialKey             string `json:"credential_key,omitempty"`
	CredentialKeyFile         string `json:"credential_key_file,omitempty"`
}

type RuntimeConfig struct {
	ObservationSummaryMaxBytes int `json:"observation_summary_max_bytes"`
	StageEvidenceMaxBytes      int `json:"workflow_stage_evidence_max_bytes"`

	// Stage budgets bound one workflow stage invocation (one model/tool
	// step-loop entry inside a workflow scope revision).
	StageMaxDurationSeconds   int `json:"workflow_stage_max_duration_seconds"`
	StageMaxNoProgressActions int `json:"workflow_stage_max_no_progress_actions"`
	StageMaxObservationReads  int `json:"workflow_stage_max_observation_reads"`

	// Run budgets bound one whole workflow run across all of its stages.
	RunMaxDurationSeconds            int `json:"workflow_run_max_duration_seconds"`
	RunMaxToolCalls                  int `json:"workflow_run_max_tool_calls"`
	RunObservationCompactionBytes    int `json:"workflow_run_observation_compaction_bytes"`
	RunMaxObservationBytes           int `json:"workflow_run_max_observation_bytes"`
	RunMaxRepeatedToolCalls          int `json:"workflow_run_max_repeated_tool_calls"`
	runObservationCompactionExplicit bool
}

// UnmarshalJSON accepts the workflow_stage_max_* / workflow_run_max_* keys
// plus the deprecated workflow_step_max_* and pre-workflow react_max_* names.
// A new-name key always wins; a deprecated key only fills a budget its new
// name did not set (workflow_step_* wins over react_*); unset budgets keep
// the values already present (the defaults pre-filled by Load).
// workflow_run_max_duration_seconds has no deprecated alias: the old step
// duration bounded a single stage and must not shrink the whole run.
func (rt *RuntimeConfig) UnmarshalJSON(raw []byte) error {
	var keys struct {
		ObservationSummaryMaxBytes *int `json:"observation_summary_max_bytes"`
		StageEvidenceMaxBytes      *int `json:"workflow_stage_evidence_max_bytes"`

		StageMaxDurationSeconds       *int `json:"workflow_stage_max_duration_seconds"`
		StageMaxNoProgressActions     *int `json:"workflow_stage_max_no_progress_actions"`
		StageMaxObservationReads      *int `json:"workflow_stage_max_observation_reads"`
		RunMaxDurationSeconds         *int `json:"workflow_run_max_duration_seconds"`
		RunMaxToolCalls               *int `json:"workflow_run_max_tool_calls"`
		RunObservationCompactionBytes *int `json:"workflow_run_observation_compaction_bytes"`
		RunMaxObservationBytes        *int `json:"workflow_run_max_observation_bytes"`
		RunMaxRepeatedToolCalls       *int `json:"workflow_run_max_repeated_tool_calls"`

		StepMaxDurationSeconds   *int `json:"workflow_step_max_duration_seconds"`
		StepMaxToolCalls         *int `json:"workflow_step_max_tool_calls"`
		StepMaxObservationBytes  *int `json:"workflow_step_max_observation_bytes"`
		StepMaxNoProgressActions *int `json:"workflow_step_max_no_progress_actions"`
		StepMaxRepeatedToolCalls *int `json:"workflow_step_max_repeated_tool_calls"`

		LegacyMaxDurationSeconds   *int `json:"react_max_duration_seconds"`
		LegacyMaxToolCalls         *int `json:"react_max_tool_calls"`
		LegacyMaxObservationBytes  *int `json:"react_max_observation_bytes"`
		LegacyMaxNoProgressActions *int `json:"react_max_no_progress_actions"`
		LegacyMaxRepeatedToolCalls *int `json:"react_max_repeated_tool_calls"`
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		return err
	}
	if keys.ObservationSummaryMaxBytes != nil {
		rt.ObservationSummaryMaxBytes = *keys.ObservationSummaryMaxBytes
	}
	if keys.StageEvidenceMaxBytes != nil {
		rt.StageEvidenceMaxBytes = *keys.StageEvidenceMaxBytes
	}
	applyBudget := func(target *int, candidates ...*int) {
		for _, candidate := range candidates {
			if candidate != nil {
				*target = *candidate
				return
			}
		}
	}
	applyBudget(&rt.StageMaxDurationSeconds, keys.StageMaxDurationSeconds, keys.StepMaxDurationSeconds, keys.LegacyMaxDurationSeconds)
	applyBudget(&rt.StageMaxNoProgressActions, keys.StageMaxNoProgressActions, keys.StepMaxNoProgressActions, keys.LegacyMaxNoProgressActions)
	applyBudget(&rt.StageMaxObservationReads, keys.StageMaxObservationReads)
	applyBudget(&rt.RunMaxDurationSeconds, keys.RunMaxDurationSeconds)
	applyBudget(&rt.RunMaxToolCalls, keys.RunMaxToolCalls, keys.StepMaxToolCalls, keys.LegacyMaxToolCalls)
	if keys.RunObservationCompactionBytes != nil {
		rt.RunObservationCompactionBytes = *keys.RunObservationCompactionBytes
		rt.runObservationCompactionExplicit = true
	}
	applyBudget(&rt.RunMaxObservationBytes, keys.RunMaxObservationBytes, keys.StepMaxObservationBytes, keys.LegacyMaxObservationBytes)
	applyBudget(&rt.RunMaxRepeatedToolCalls, keys.RunMaxRepeatedToolCalls, keys.StepMaxRepeatedToolCalls, keys.LegacyMaxRepeatedToolCalls)
	return nil
}

type LoggingConfig struct {
	Level          string   `json:"level"`
	RedactPatterns []string `json:"redact_patterns"`
}

type toolPolicyFile struct {
	Deny             []string `json:"deny"`
	ApprovalRequired []string `json:"approval_required"`
}
