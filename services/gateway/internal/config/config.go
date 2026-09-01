package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

const DefaultBrowserDaemonIdleTimeoutMS = 20 * 60 * 1000

const (
	LocalMindMCPServerKey          = "localmind"
	LocalMindMCPServerName         = "localmind-ai"
	LocalMindMCPProtocolVersion    = "2025-06-18"
	LocalMindMCPDefaultNamespace   = "localmind"
	LocalMindMCPDefaultMaxResponse = int64(16 << 20)
)

var (
	mcpServerNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Config struct {
	Gateway     GatewayConfig              `json:"gateway"`
	JingSiLAN   JingSiLANConfig            `json:"jingsi_lan"`
	Model       ModelConfig                `json:"model"`
	Speech      SpeechConfig               `json:"speech"`
	ISCPPairing ISCPPairingConfig          `json:"iscp_pairing"`
	MCPAccess   MCPAccessConfig            `json:"mcp_access"`
	MCPServers  map[string]MCPServerConfig `json:"mcp_servers,omitempty"`
	Plugins     PluginsConfig              `json:"plugins"`
	Tools       ToolsConfig                `json:"tools"`
	Security    SecurityConfig             `json:"security"`
	Sandbox     SandboxConfig              `json:"sandbox"`
	Adapters    AdapterConfig              `json:"adapters"`
	Memory      MemoryConfig               `json:"memory"`
	// PassiveNotifications bounds the durable ISCP notification inbox.
	PassiveNotifications PassiveNotificationsConfig `json:"passive_notifications"`
	Workspaces           WorkspaceConfig            `json:"workspaces"`
	Storage              StorageConfig              `json:"storage"`
	State                StateConfig                `json:"state"`
	Runtime              RuntimeConfig              `json:"runtime"`
	Logging              LoggingConfig              `json:"logging"`
}

type JingSiLANConfig struct {
	Enabled         bool   `json:"enabled"`
	SessionID       string `json:"session_id,omitempty"`
	MaxMessageBytes int    `json:"max_message_bytes"`
}

type GatewayConfig struct {
	Bind            string `json:"bind"`
	Port            int    `json:"port"`
	PairingRequired bool   `json:"pairing_required"`
	RemoteAccess    string `json:"remote_access"`
	APIToken        string `json:"api_token,omitempty"`
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
	CapacityProfile    string       `json:"capacity_profile"`
	CapacityCatalog    string       `json:"capacity_catalog"`
	Fast               ModelProfile `json:"fast"`
	Deep               ModelProfile `json:"deep"`
	Embedding          ModelProfile `json:"embedding"`
	Guard              ModelProfile `json:"guard"`
	Mock               bool         `json:"mock"`
	HTTPTimeoutSeconds int          `json:"http_timeout_seconds"`
	DisableThinking    bool         `json:"disable_thinking"`
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
	Command              string `json:"command"`
	TimeoutMS            int    `json:"timeoutMs"`
	StartupTimeoutMS     int    `json:"startupTimeoutMs"`
	DaemonIdleTimeoutMS  int    `json:"daemonIdleTimeoutMs"`
	SettleTimeoutMS      int    `json:"settleTimeoutMs"`
	SettleQuietPeriodMS  int    `json:"settleQuietPeriodMs"`
	SettlePollIntervalMS int    `json:"settlePollIntervalMs"`
	RouteRebindLimit     int    `json:"routeRebindLimit"`
	ChromiumExecutable   string `json:"chromiumExecutable"`
	ProfileDir           string `json:"profileDir"`
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

func Load(path string) (Config, error) {
	cfg := Default()
	cfg.Model.CapacityCatalog = defaultModelCapacityCatalogPath()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, err
		}
		if err := rejectLegacyModelCapacity(raw); err != nil {
			return Config{}, err
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if err := applySelectedModelCapacity(&cfg, path); err != nil {
		return Config{}, err
	}
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
	if err := normalizeStateConfig(&cfg.State); err != nil {
		return Config{}, err
	}
	cfg.JingSiLAN.SessionID = strings.TrimSpace(cfg.JingSiLAN.SessionID)
	if cfg.JingSiLAN.MaxMessageBytes <= 0 {
		cfg.JingSiLAN.MaxMessageBytes = 64 << 10
	}
	if cfg.JingSiLAN.MaxMessageBytes > 1<<20 {
		return Config{}, errors.New("jingsi_lan.max_message_bytes must not exceed 1048576")
	}
	if cfg.JingSiLAN.Enabled && cfg.JingSiLAN.SessionID == "" {
		return Config{}, errors.New("jingsi_lan.session_id is required when JingSi LAN is enabled")
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
	if strings.TrimSpace(cfg.Adapters.BrowserAutomation.Command) == "" {
		cfg.Adapters.BrowserAutomation.Command = "agent-browser"
	}
	if cfg.Adapters.BrowserAutomation.TimeoutMS <= 0 {
		cfg.Adapters.BrowserAutomation.TimeoutMS = 30000
	}
	if cfg.Adapters.BrowserAutomation.StartupTimeoutMS <= 0 {
		cfg.Adapters.BrowserAutomation.StartupTimeoutMS = 10000
	}
	if cfg.Adapters.BrowserAutomation.DaemonIdleTimeoutMS <= 0 {
		cfg.Adapters.BrowserAutomation.DaemonIdleTimeoutMS = DefaultBrowserDaemonIdleTimeoutMS
	}
	if cfg.Adapters.BrowserAutomation.SettleTimeoutMS <= 0 {
		cfg.Adapters.BrowserAutomation.SettleTimeoutMS = 15000
	}
	if cfg.Adapters.BrowserAutomation.SettleQuietPeriodMS <= 0 {
		cfg.Adapters.BrowserAutomation.SettleQuietPeriodMS = 500
	}
	if cfg.Adapters.BrowserAutomation.SettlePollIntervalMS <= 0 {
		cfg.Adapters.BrowserAutomation.SettlePollIntervalMS = 100
	}
	if cfg.Adapters.BrowserAutomation.RouteRebindLimit <= 0 {
		cfg.Adapters.BrowserAutomation.RouteRebindLimit = 2
	}
	if cfg.Adapters.BrowserAutomation.SettleTimeoutMS < 500 || cfg.Adapters.BrowserAutomation.SettleTimeoutMS > 120000 {
		return Config{}, errors.New("adapters.browserAutomation.settleTimeoutMs must be between 500 and 120000")
	}
	if cfg.Adapters.BrowserAutomation.SettleQuietPeriodMS < 100 || cfg.Adapters.BrowserAutomation.SettleQuietPeriodMS > 10000 {
		return Config{}, errors.New("adapters.browserAutomation.settleQuietPeriodMs must be between 100 and 10000")
	}
	if cfg.Adapters.BrowserAutomation.SettlePollIntervalMS < 25 || cfg.Adapters.BrowserAutomation.SettlePollIntervalMS > cfg.Adapters.BrowserAutomation.SettleQuietPeriodMS {
		return Config{}, errors.New("adapters.browserAutomation.settlePollIntervalMs must be between 25 and settleQuietPeriodMs")
	}
	if cfg.Adapters.BrowserAutomation.RouteRebindLimit < 1 || cfg.Adapters.BrowserAutomation.RouteRebindLimit > 5 {
		return Config{}, errors.New("adapters.browserAutomation.routeRebindLimit must be between 1 and 5")
	}
	profileDir, err := filepath.Abs(cfg.Adapters.BrowserAutomation.ProfileDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve browser profile directory: %w", err)
	}
	cfg.Adapters.BrowserAutomation.ProfileDir = profileDir
	if err := normalizeRuntimeLimits(&cfg.Runtime); err != nil {
		return Config{}, err
	}
	// The idle-timeout floor couples the browser daemon to the model and
	// workflow windows; a deployment with browser automation disabled must
	// not be refused boot over knobs its daemon will never use.
	if cfg.Tools.BrowserAutomation.Enabled {
		minimumBrowserIdleTimeoutMS, err := minimumBrowserDaemonIdleTimeoutMS(cfg)
		if err != nil {
			return Config{}, err
		}
		if cfg.Adapters.BrowserAutomation.DaemonIdleTimeoutMS < minimumBrowserIdleTimeoutMS {
			return Config{}, fmt.Errorf(
				"adapters.browserAutomation.daemonIdleTimeoutMs must be at least %d for the configured model and workflow timeouts",
				minimumBrowserIdleTimeoutMS,
			)
		}
	}
	if err := normalizeInfinimeshInfoConfig(&cfg.Plugins.Entries.InfinimeshInfo.Config); err != nil {
		return Config{}, err
	}
	if err := normalizeSpeechConfig(&cfg.Speech); err != nil {
		return Config{}, err
	}
	if err := normalizeISCPPairingConfig(&cfg.ISCPPairing); err != nil {
		return Config{}, err
	}
	if err := normalizeMCPAccessConfig(&cfg.MCPAccess); err != nil {
		return Config{}, err
	}
	if err := normalizePassiveNotificationsConfig(&cfg.PassiveNotifications); err != nil {
		return Config{}, err
	}
	if err := normalizeDocumentOCRConfig(&cfg.Adapters.DocumentOCR); err != nil {
		return Config{}, err
	}
	if err := normalizePPTXVisualQAConfig(&cfg.Adapters.PPTXVisualQA); err != nil {
		return Config{}, err
	}
	if err := validateModelConfig(&cfg.Model); err != nil {
		return Config{}, err
	}
	if err := normalizeNotificationChannels(&cfg.Tools.Notifications); err != nil {
		return Config{}, err
	}
	if err := normalizeRemindersConfig(&cfg.Tools.Reminders); err != nil {
		return Config{}, err
	}
	if err := normalizeMCPServers(&cfg.MCPServers); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadDefault resolves the default model-capacity profile without applying
// file or environment overrides. Runtime entrypoints should use Load.
func LoadDefault() (Config, error) {
	cfg := Default()
	cfg.Model.CapacityCatalog = defaultModelCapacityCatalogPath()
	if err := applySelectedModelCapacity(&cfg, ""); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func normalizeISCPPairingConfig(pairing *ISCPPairingConfig) error {
	defaults := Default().ISCPPairing
	if pairing.RequestTimeoutSeconds <= 0 {
		pairing.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if pairing.ResponseBodyMaxBytes <= 0 {
		pairing.ResponseBodyMaxBytes = defaults.ResponseBodyMaxBytes
	}
	if pairing.TicketTTLSeconds <= 0 {
		pairing.TicketTTLSeconds = defaults.TicketTTLSeconds
	}
	if strings.TrimSpace(pairing.ExpectedTicketType) == "" {
		pairing.ExpectedTicketType = defaults.ExpectedTicketType
	}
	pairing.DomainID = strings.TrimSpace(pairing.DomainID)
	pairing.AuthorityURL = strings.TrimSpace(pairing.AuthorityURL)
	pairing.TokenEnv = strings.TrimSpace(pairing.TokenEnv)
	pairing.TokenFile = strings.TrimSpace(pairing.TokenFile)
	pairing.ExpectedTicketType = strings.TrimSpace(pairing.ExpectedTicketType)
	if pairing.ExpectedTicketType != "iscp.pairing_ticket.v2" {
		return errors.New("iscp_pairing.expected_ticket_type must be iscp.pairing_ticket.v2")
	}
	if pairing.RequestTimeoutSeconds > 120 {
		return errors.New("iscp_pairing.request_timeout_seconds must not exceed 120")
	}
	if pairing.ResponseBodyMaxBytes < 1024 || pairing.ResponseBodyMaxBytes > 1<<20 {
		return errors.New("iscp_pairing.response_body_max_bytes must be between 1024 and 1048576")
	}
	if pairing.TicketTTLSeconds < 60 || pairing.TicketTTLSeconds > 1800 {
		return errors.New("iscp_pairing.ticket_ttl_seconds must be between 60 and 1800")
	}
	if !pairing.Enabled {
		return nil
	}
	if pairing.DomainID == "" {
		return errors.New("iscp_pairing.domain_id is required when enabled")
	}
	if pairing.TokenEnv == pairing.TokenFile || (pairing.TokenEnv != "" && pairing.TokenFile != "") {
		return errors.New("iscp_pairing must configure exactly one of token_env or token_file")
	}
	if pairing.TokenEnv != "" && !environmentNamePattern.MatchString(pairing.TokenEnv) {
		return errors.New("iscp_pairing.token_env is invalid")
	}
	endpoint, err := url.Parse(pairing.AuthorityURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("iscp_pairing.authority_url must be absolute HTTP(S) without credentials, query, or fragment")
	}
	if endpoint.Scheme == "http" && !isLocalHTTPHost(endpoint.Hostname()) {
		return errors.New("iscp_pairing.authority_url may use HTTP only for a local or private authority")
	}
	pairing.AuthorityURL = strings.TrimRight(endpoint.String(), "/")
	return nil
}

func normalizePassiveNotificationsConfig(notifications *PassiveNotificationsConfig) error {
	if notifications.MaxPerOwner < 0 || notifications.MaxPerOwner > 100000 {
		return errors.New("passive_notifications.max_per_owner must be between 0 (uncapped) and 100000")
	}
	if notifications.RetentionDays < 0 || notifications.RetentionDays > 3650 {
		return errors.New("passive_notifications.retention_days must be between 0 (no sweep) and 3650")
	}
	return nil
}

func normalizeMCPAccessConfig(access *MCPAccessConfig) error {
	access.LocalDomainID = strings.TrimSpace(access.LocalDomainID)
	if access.LocalDomainID == "" {
		return errors.New("mcp_access.local_domain_id is required")
	}
	normalized := make([]string, 0, len(access.AllowedOrigins))
	seen := make(map[string]bool, len(access.AllowedOrigins))
	for _, entry := range access.AllowedOrigins {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		origin, err := NormalizeOrigin(entry)
		if err != nil {
			return fmt.Errorf("mcp_access.allowed_origins entry %q must be an absolute HTTP(S) origin without credentials, path, query, or fragment", entry)
		}
		if !seen[origin] {
			seen[origin] = true
			normalized = append(normalized, origin)
		}
	}
	access.AllowedOrigins = normalized
	return nil
}

// NormalizeOrigin canonicalizes a web origin ("scheme://host[:port]") to its
// lowercase form. It rejects values that are not plain HTTP(S) origins, such
// as URLs carrying credentials, paths, queries, or fragments, and the opaque
// "null" origin.
func NormalizeOrigin(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse origin: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must be an absolute HTTP(S) origin")
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host), nil
}

func normalizeMCPServers(servers *map[string]MCPServerConfig) error {
	if *servers == nil {
		*servers = map[string]MCPServerConfig{}
		return nil
	}
	for name, server := range *servers {
		if strings.TrimSpace(name) != name || !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("MCP server name %q must match %s", name, mcpServerNamePattern.String())
		}
		var err error
		if name == LocalMindMCPServerKey {
			server, err = normalizeLocalMindMCPServer(name, server)
		} else {
			server, err = normalizeGenericMCPServer(name, server)
		}
		if err != nil {
			return err
		}
		(*servers)[name] = server
	}
	return nil
}

func normalizeGenericMCPServer(name string, server MCPServerConfig) (MCPServerConfig, error) {
	if hasLocalMindOnlyMCPSettings(server) {
		return MCPServerConfig{}, fmt.Errorf("unsupported MCP server %q with LocalMind-specific configuration", name)
	}
	server.URL = strings.TrimSpace(server.URL)
	endpoint, err := url.Parse(server.URL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.Fragment != "" {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q URL must be absolute HTTP(S) without credentials or fragment", name)
	}
	server.TokenEnv = strings.TrimSpace(server.TokenEnv)
	server.TokenFile = strings.TrimSpace(server.TokenFile)
	if server.TokenEnv != "" && server.TokenFile != "" {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q must use only one of token_env or token_file", name)
	}
	if server.TokenEnv != "" && !environmentNamePattern.MatchString(server.TokenEnv) {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q token_env is invalid", name)
	}
	server.Namespace = strings.Trim(strings.TrimSpace(server.Namespace), ".")
	if server.Namespace == "" {
		server.Namespace = "mcp." + name
	}
	server.ExpectedServerName = strings.TrimSpace(server.ExpectedServerName)
	server.ToolAllow = normalizeStringSet(server.ToolAllow)
	server.ToolDeny = normalizeStringSet(server.ToolDeny)
	for _, allowed := range server.ToolAllow {
		if slicesContains(server.ToolDeny, allowed) {
			return MCPServerConfig{}, fmt.Errorf("MCP server %q tool %q cannot be both allowed and denied", name, allowed)
		}
	}
	if server.RequestTimeoutSeconds <= 0 {
		server.RequestTimeoutSeconds = 30
	}
	if server.RequestTimeoutSeconds > 3600 {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q request_timeout_seconds must not exceed 3600", name)
	}
	if server.DiscoveryRefreshSeconds <= 0 {
		server.DiscoveryRefreshSeconds = 60
	}
	if server.DiscoveryRefreshSeconds > 86400 {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q discovery_refresh_seconds must not exceed 86400", name)
	}
	if server.ResponseBodyMaxBytes <= 0 {
		server.ResponseBodyMaxBytes = 4 << 20
	}
	if server.ResponseBodyMaxBytes > 32<<20 {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q response_body_max_bytes must not exceed 33554432", name)
	}
	return server, nil
}

func normalizeLocalMindMCPServer(name string, server MCPServerConfig) (MCPServerConfig, error) {
	if hasGenericMCPSettings(server) {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s must use url_env and bearer_token_env instead of generic MCP endpoint settings", name)
	}
	defaults := MCPServerConfig{
		Transport:              "streamable-http",
		Namespace:              LocalMindMCPDefaultNamespace,
		ExpectedServerName:     LocalMindMCPServerName,
		ProtocolVersion:        LocalMindMCPProtocolVersion,
		RequestTimeoutSeconds:  30,
		LongCallGraceSeconds:   10,
		MaxResponseBytes:       LocalMindMCPDefaultMaxResponse,
		StateOutputMaxBytes:    16 << 10,
		ArchiveOutputMaxBytes:  16 << 20,
		RefreshIntervalSeconds: 300,
	}
	server.Transport = strings.ToLower(strings.TrimSpace(server.Transport))
	if server.Transport == "" {
		server.Transport = defaults.Transport
	}
	if server.Transport != defaults.Transport {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.transport must be %q", name, defaults.Transport)
	}
	server.URLEnv = strings.TrimSpace(server.URLEnv)
	server.BearerTokenEnv = strings.TrimSpace(server.BearerTokenEnv)
	if !environmentNamePattern.MatchString(server.URLEnv) || !environmentNamePattern.MatchString(server.BearerTokenEnv) {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s url_env and bearer_token_env must be valid environment variable names", name)
	}
	server.Namespace = strings.Trim(strings.TrimSpace(server.Namespace), ".")
	if server.Namespace == "" {
		server.Namespace = defaults.Namespace
	}
	if server.Namespace != LocalMindMCPDefaultNamespace {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.namespace must be %q", name, LocalMindMCPDefaultNamespace)
	}
	server.ExpectedServerName = strings.TrimSpace(server.ExpectedServerName)
	if server.ExpectedServerName == "" {
		server.ExpectedServerName = defaults.ExpectedServerName
	}
	if server.ExpectedServerName != LocalMindMCPServerName {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.expected_server_name must be %q", name, LocalMindMCPServerName)
	}
	server.ProtocolVersion = strings.TrimSpace(server.ProtocolVersion)
	if server.ProtocolVersion == "" {
		server.ProtocolVersion = defaults.ProtocolVersion
	}
	if server.ProtocolVersion != LocalMindMCPProtocolVersion {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.protocol_version must be %q", name, LocalMindMCPProtocolVersion)
	}
	if len(server.ToolAllow) != 0 || len(server.ToolDeny) != 0 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s tool_allow and tool_deny are not supported by the fixed LocalMind task contract", name)
	}
	if server.AllowMutations {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s allow_mutations is not supported by the fixed LocalMind task contract", name)
	}
	if server.RequestTimeoutSeconds <= 0 {
		server.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if server.RequestTimeoutSeconds > 120 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.request_timeout_seconds must not exceed 120", name)
	}
	if server.LongCallGraceSeconds <= 0 {
		server.LongCallGraceSeconds = defaults.LongCallGraceSeconds
	}
	if server.LongCallGraceSeconds > 120 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.long_call_grace_seconds must not exceed 120", name)
	}
	if server.MaxResponseBytes <= 0 {
		server.MaxResponseBytes = defaults.MaxResponseBytes
	}
	if server.MaxResponseBytes < 1024 || server.MaxResponseBytes > 32<<20 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.max_response_bytes must be between 1024 and 33554432", name)
	}
	if server.StateOutputMaxBytes <= 0 {
		server.StateOutputMaxBytes = defaults.StateOutputMaxBytes
	}
	if server.StateOutputMaxBytes < 1024 || server.StateOutputMaxBytes > 64<<10 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.state_output_max_bytes must be between 1024 and 65536", name)
	}
	if server.ArchiveOutputMaxBytes <= 0 {
		server.ArchiveOutputMaxBytes = defaults.ArchiveOutputMaxBytes
	}
	if server.ArchiveOutputMaxBytes < server.StateOutputMaxBytes || server.ArchiveOutputMaxBytes > 32<<20 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.archive_output_max_bytes must be between state_output_max_bytes and 33554432", name)
	}
	if server.RefreshIntervalSeconds <= 0 {
		server.RefreshIntervalSeconds = defaults.RefreshIntervalSeconds
	}
	if server.RefreshIntervalSeconds < 30 || server.RefreshIntervalSeconds > 86400 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.refresh_interval_seconds must be between 30 and 86400", name)
	}
	return server, nil
}

func hasLocalMindOnlyMCPSettings(server MCPServerConfig) bool {
	return server.Transport != "" || server.URLEnv != "" || server.BearerTokenEnv != "" ||
		server.ProtocolVersion != "" || server.AllowPrivateHTTP || server.LongCallGraceSeconds != 0 ||
		server.MaxResponseBytes != 0 || server.StateOutputMaxBytes != 0 ||
		server.ArchiveOutputMaxBytes != 0 || server.RefreshIntervalSeconds != 0
}

func hasGenericMCPSettings(server MCPServerConfig) bool {
	return server.URL != "" || server.TokenEnv != "" || server.TokenFile != "" ||
		server.DiscoveryRefreshSeconds != 0 || server.ResponseBodyMaxBytes != 0
}

func normalizeStringSet(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slicesContains(out, value) {
			continue
		}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// normalizeRemindersConfig backfills a non-positive delivery-attempt cap with
// the default so a partial tools.reminders section cannot silently make the
// scheduler retry a failing publish forever.
func normalizeRemindersConfig(reminders *RemindersToolConfig) error {
	if reminders.MaxDeliveryAttempts <= 0 {
		reminders.MaxDeliveryAttempts = Default().Tools.Reminders.MaxDeliveryAttempts
	}
	if reminders.MaxDeliveryAttempts > 100 {
		return errors.New("reminders maxDeliveryAttempts must not exceed 100")
	}
	return nil
}

// validateModelConfig rejects a non-mock model configuration whose core chat
// profiles have no endpoint. Capacity itself has already been loaded and
// validated from the selected catalog profile.
func validateModelConfig(model *ModelConfig) error {
	if model.Mock {
		return nil
	}
	if strings.TrimSpace(model.Fast.BaseURL) == "" {
		return errors.New("model.fast.base_url is required when model.mock is false (set it in config or SPARKCLAW_FAST_BASE_URL)")
	}
	if strings.TrimSpace(model.Deep.BaseURL) == "" {
		return errors.New("model.deep.base_url is required when model.mock is false (set it in config or SPARKCLAW_DEEP_BASE_URL)")
	}
	return nil
}

// normalizeRuntimeLimits backfills non-positive workflow budgets with the
// defaults so a partial runtime section in JSON cannot silently disable the
// stage or run stop conditions.
func normalizeRuntimeLimits(rt *RuntimeConfig) error {
	defaults := Default().Runtime
	if rt.ObservationSummaryMaxBytes <= 0 {
		rt.ObservationSummaryMaxBytes = defaults.ObservationSummaryMaxBytes
	}
	if rt.StageEvidenceMaxBytes <= 0 {
		rt.StageEvidenceMaxBytes = defaults.StageEvidenceMaxBytes
	}
	if rt.StageMaxDurationSeconds <= 0 {
		rt.StageMaxDurationSeconds = defaults.StageMaxDurationSeconds
	}
	if rt.StageMaxNoProgressActions <= 0 {
		rt.StageMaxNoProgressActions = defaults.StageMaxNoProgressActions
	}
	if rt.StageMaxObservationReads <= 0 {
		rt.StageMaxObservationReads = defaults.StageMaxObservationReads
	}
	if rt.RunMaxDurationSeconds <= 0 {
		rt.RunMaxDurationSeconds = defaults.RunMaxDurationSeconds
	}
	if rt.RunMaxToolCalls <= 0 {
		rt.RunMaxToolCalls = defaults.RunMaxToolCalls
	}
	if rt.RunMaxObservationBytes <= 0 {
		rt.RunMaxObservationBytes = defaults.RunMaxObservationBytes
	}
	if !rt.runObservationCompactionExplicit {
		rt.RunObservationCompactionBytes = rt.RunMaxObservationBytes * 3 / 4
	} else if rt.RunObservationCompactionBytes <= 0 || rt.RunObservationCompactionBytes >= rt.RunMaxObservationBytes {
		return fmt.Errorf("runtime.workflow_run_observation_compaction_bytes must be greater than zero and lower than workflow_run_max_observation_bytes")
	}
	if rt.RunMaxRepeatedToolCalls <= 0 {
		rt.RunMaxRepeatedToolCalls = defaults.RunMaxRepeatedToolCalls
	}
	return nil
}

func minimumBrowserDaemonIdleTimeoutMS(cfg Config) (int, error) {
	modelWindowSeconds := cfg.Model.HTTPTimeoutSeconds
	if modelWindowSeconds <= 0 {
		modelWindowSeconds = Default().Model.HTTPTimeoutSeconds
	}
	workflowWindowSeconds := cfg.Runtime.StageMaxDurationSeconds
	if workflowWindowSeconds <= 0 {
		workflowWindowSeconds = Default().Runtime.StageMaxDurationSeconds
	}
	toolHeadroomMS := cfg.Adapters.BrowserAutomation.TimeoutMS
	if toolHeadroomMS <= 0 {
		toolHeadroomMS = Default().Adapters.BrowserAutomation.TimeoutMS
	}
	// Interaction can run goal assessment and action selection between two
	// Chromium commands. Each model stage may consume its workflow window and
	// finish one in-flight request before the next browser command resets idle.
	maxInt := int(^uint(0) >> 1)
	maxReasoningWindowSeconds := (maxInt - toolHeadroomMS) / 2000
	if modelWindowSeconds > maxReasoningWindowSeconds ||
		workflowWindowSeconds > maxReasoningWindowSeconds-modelWindowSeconds {
		return 0, errors.New("configured model and workflow timeouts exceed the supported browser daemon idle timeout range")
	}
	return 2*(modelWindowSeconds+workflowWindowSeconds)*1000 + toolHeadroomMS, nil
}

func normalizeInfinimeshInfoConfig(cfg *InfinimeshInfoConfig) error {
	defaults := Default().Plugins.Entries.InfinimeshInfo.Config
	cfg.LicenseID = strings.TrimSpace(cfg.LicenseID)
	cfg.LicenseKey = strings.TrimSpace(cfg.LicenseKey)
	if cfg.LicenseID != "" || cfg.LicenseKey != "" {
		if cfg.LicenseID == "" || cfg.LicenseKey == "" {
			return errors.New("infinimesh info license and key must be configured together")
		}
		keyLicenseID, ok := infinimeshinfo.ParseLicenseKeyLicenseID(cfg.LicenseKey)
		if !ok {
			return errors.New("infinimesh info license key must use the ilk_v1 wire format")
		}
		if keyLicenseID != cfg.LicenseID {
			return errors.New("infinimesh info license key does not match the configured license")
		}
	}
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
		return errors.New("speech.base_url must be an absolute http or https URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return errors.New("speech.base_url must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("speech.base_url cannot contain credentials, query parameters, or fragments")
	}
	if !containsFold(speech.AllowedHosts, parsed.Hostname()) {
		return fmt.Errorf("speech.base_url host %q is not listed in speech.allowed_hosts", parsed.Hostname())
	}
	if parsed.Scheme == "http" && !isLocalHTTPHost(parsed.Hostname()) {
		return errors.New("speech.base_url may use http only for loopback, private, or local container hosts")
	}
	speech.BaseURL = strings.TrimRight(parsed.String(), "/")
	return nil
}

func normalizeDocumentOCRConfig(ocr *DocumentOCRAdapterConfig) error {
	defaults := Default().Adapters.DocumentOCR
	if ocr.TimeoutSeconds <= 0 {
		ocr.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if ocr.MaxUploadBytes <= 0 {
		ocr.MaxUploadBytes = defaults.MaxUploadBytes
	}
	if ocr.MaxOutputBytes <= 0 {
		ocr.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if ocr.MaxTokens <= 0 || ocr.ContextTokens <= 0 || ocr.MaxTokens >= ocr.ContextTokens {
		return errors.New("document OCR capacity must come from a valid selected model capacity profile")
	}
	if ocr.MaxConcurrency <= 0 {
		ocr.MaxConcurrency = defaults.MaxConcurrency
	}
	if ocr.MaxPending < 0 {
		return errors.New("document OCR maxPending cannot be negative")
	}
	ocr.AllowedHosts = normalizeHostList(ocr.AllowedHosts)
	if !ocr.Enabled {
		ocr.Provider = "disabled"
		return nil
	}

	ocr.Provider = strings.ToLower(strings.TrimSpace(ocr.Provider))
	if ocr.Provider != "openai-http" {
		return fmt.Errorf("unsupported document OCR provider %q", ocr.Provider)
	}
	if strings.TrimSpace(ocr.Model) == "" {
		return errors.New("document OCR model is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(ocr.BaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("document OCR base URL must be an absolute http or https URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("document OCR base URL must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("document OCR base URL must not contain credentials, query, or fragment")
	}
	if !containsFold(ocr.AllowedHosts, parsed.Hostname()) {
		return fmt.Errorf("document OCR base URL host %q is not allowlisted", parsed.Hostname())
	}
	if parsed.Scheme == "http" && !isLocalHTTPHost(parsed.Hostname()) {
		return errors.New("document OCR base URL may use http only for loopback, private, or local container hosts")
	}
	ocr.BaseURL = strings.TrimRight(parsed.String(), "/")
	if ocr.TimeoutSeconds > 600 {
		return errors.New("document OCR timeout must not exceed 600 seconds")
	}
	if ocr.MaxUploadBytes > 32<<20 {
		return errors.New("document OCR upload limit must not exceed 33554432 bytes")
	}
	if ocr.MaxOutputBytes < 1024 || ocr.MaxOutputBytes > 2<<20 {
		return errors.New("document OCR output limit must be between 1024 and 2097152 bytes")
	}
	if ocr.MaxConcurrency > 8 {
		return errors.New("document OCR maxConcurrency must not exceed 8")
	}
	if ocr.MaxPending > 32 {
		return errors.New("document OCR maxPending must not exceed 32")
	}
	return nil
}

func normalizePPTXVisualQAConfig(visual *PPTXVisualQAAdapterConfig) error {
	defaults := Default().Adapters.PPTXVisualQA
	visual.Phase = strings.ToLower(strings.TrimSpace(visual.Phase))
	if visual.Phase == "" {
		visual.Phase = defaults.Phase
	}
	if visual.TimeoutSeconds <= 0 {
		visual.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if visual.MaxInputBytes <= 0 {
		visual.MaxInputBytes = defaults.MaxInputBytes
	}
	if visual.MaxPDFBytes <= 0 {
		visual.MaxPDFBytes = defaults.MaxPDFBytes
	}
	if visual.MaxPages <= 0 {
		visual.MaxPages = defaults.MaxPages
	}
	if visual.MaxChangedPages <= 0 {
		visual.MaxChangedPages = defaults.MaxChangedPages
	}
	if visual.RasterScale <= 0 {
		visual.RasterScale = defaults.RasterScale
	}
	if visual.MaxPagePixels <= 0 {
		visual.MaxPagePixels = defaults.MaxPagePixels
	}
	if visual.MaxPNGBytes <= 0 {
		visual.MaxPNGBytes = defaults.MaxPNGBytes
	}
	if visual.DiagnosticToleranceMilli <= 0 {
		visual.DiagnosticToleranceMilli = defaults.DiagnosticToleranceMilli
	}
	if visual.ReadinessTTLSeconds <= 0 {
		visual.ReadinessTTLSeconds = defaults.ReadinessTTLSeconds
	}
	if !slices.Contains([]string{"disabled", "shadow", "warning", "qualified_blocking", "default_on"}, visual.Phase) {
		return fmt.Errorf("unsupported PPTX visual QA phase %q", visual.Phase)
	}
	if visual.MaxRepairAttempts < 0 || visual.MaxRepairAttempts > 2 {
		return errors.New("PPTX visual QA maxRepairAttempts must be between 0 and 2")
	}
	repairable := []string{
		"text_clipped", "content_obscured", "element_off_canvas", "missing_glyph", "broken_layout", "low_contrast",
		"text_too_small", "overcrowded", "misaligned", "weak_hierarchy", "poor_whitespace", "unclear_focus", "inconsistent_style",
	}
	blocking := []string{"text_clipped", "content_obscured", "element_off_canvas", "missing_glyph"}
	repairOperations := []string{"rewrite_text", "set_geometry", "set_text_style", "set_shape_style", "place_above", "place_below", "delete_generated_shape"}
	var qualificationErr error
	visual.RepairQualifiedClasses, qualificationErr = normalizePPTXVisualQAClasses(visual.RepairQualifiedClasses, repairable, "repairQualifiedClasses")
	if qualificationErr != nil {
		return qualificationErr
	}
	visual.RepairQualifiedOperations, qualificationErr = normalizePPTXVisualQAClasses(visual.RepairQualifiedOperations, repairOperations, "repairQualifiedOperations")
	if qualificationErr != nil {
		return qualificationErr
	}
	visual.BlockingQualifiedClasses, qualificationErr = normalizePPTXVisualQAClasses(visual.BlockingQualifiedClasses, blocking, "blockingQualifiedClasses")
	if qualificationErr != nil {
		return qualificationErr
	}
	for _, class := range visual.BlockingQualifiedClasses {
		if !slices.Contains(visual.RepairQualifiedClasses, class) {
			return fmt.Errorf("PPTX visual QA blocking class %q must also be repair-qualified", class)
		}
	}
	if visual.MaxChangedPages > visual.MaxPages {
		return errors.New("PPTX visual QA maxChangedPages cannot exceed maxPages")
	}
	if visual.MaxPages > 200 || visual.MaxChangedPages > 64 {
		return errors.New("PPTX visual QA page limits exceed the implementation bounds")
	}
	if visual.RasterScale < 0.5 || visual.RasterScale > 4 {
		return errors.New("PPTX visual QA rasterScale must be between 0.5 and 4")
	}
	if visual.DiagnosticToleranceMilli > 25 {
		return errors.New("PPTX visual QA diagnosticToleranceMilli must not exceed 25")
	}
	visual.AllowedHosts = normalizeHostList(visual.AllowedHosts)
	if visual.Phase == "disabled" {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(visual.BaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("PPTX visual QA base URL must be an absolute http or https URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("PPTX visual QA base URL must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("PPTX visual QA base URL must not contain credentials, query, or fragment")
	}
	if !containsFold(visual.AllowedHosts, parsed.Hostname()) {
		return fmt.Errorf("PPTX visual QA base URL host %q is not allowlisted", parsed.Hostname())
	}
	if parsed.Scheme == "http" && !isLocalHTTPHost(parsed.Hostname()) {
		return errors.New("PPTX visual QA base URL may use http only for loopback, private, or local container hosts")
	}
	visual.BaseURL = strings.TrimRight(parsed.String(), "/")
	return nil
}

func normalizePPTXVisualQAClasses(values, allowed []string, field string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if !slices.Contains(allowed, value) {
			return nil, fmt.Errorf("unsupported PPTX visual QA %s value %q", field, value)
		}
		if slices.Contains(out, value) {
			return nil, fmt.Errorf("duplicate PPTX visual QA %s value %q", field, value)
		}
		out = append(out, value)
	}
	slices.Sort(out)
	return out, nil
}

func isLocalHTTPHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	return !strings.Contains(host, ".")
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
	cfg := Config{
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
		JingSiLAN: JingSiLANConfig{
			Enabled:         false,
			MaxMessageBytes: 64 << 10,
		},
		Model: ModelConfig{
			CapacityProfile:    "dgx-spark-dual-light-v1",
			CapacityCatalog:    defaultModelCapacityCatalog,
			Mock:               false,
			HTTPTimeoutSeconds: 300,
			// Matches configs/sparkclaw.default.json: the local model
			// lanes run without thinking so bounded max_tokens (e.g. the
			// guard lane's 128) are spent on the answer, not reasoning.
			DisableThinking: true,
			Fast: ModelProfile{
				Name:    "sparkclaw-fast",
				BaseURL: "http://127.0.0.1:8001/v1",
				Model:   "nvidia/Qwen3.6-35B-A3B-NVFP4",
				MTP:     false,
			},
			Deep: ModelProfile{
				Name:    "sparkclaw-deep",
				BaseURL: "http://127.0.0.1:8002/v1",
				Model:   "nvidia/Qwen3.6-35B-A3B-NVFP4",
				MTP:     false,
			},
			Embedding: ModelProfile{
				Name:    "sparkclaw-embedding",
				BaseURL: "http://127.0.0.1:8003/v1",
				Model:   "Qwen/Qwen3-Embedding-0.6B",
			},
			Guard: ModelProfile{
				Name:    "sparkclaw-guard",
				BaseURL: "http://127.0.0.1:8005/v1",
				Model:   "Qwen/Qwen3Guard-Gen-0.6B",
			},
		},
		Speech: SpeechConfig{
			Enabled:         false,
			Backend:         "openai-http",
			BaseURL:         "",
			AllowedHosts:    nil,
			Model:           "sparkclaw-asr",
			DefaultLanguage: "auto",
			TimeoutSeconds:  120,
			MaxAudioSeconds: 60,
			MaxUploadBytes:  3 << 20,
			MaxConcurrency:  1,
			MaxPending:      1,
			RetainAudio:     false,
		},
		ISCPPairing: ISCPPairingConfig{
			Enabled: false, RequestTimeoutSeconds: 15, ResponseBodyMaxBytes: 64 << 10,
			TicketTTLSeconds: 600, ExpectedTicketType: "iscp.pairing_ticket.v2",
		},
		MCPAccess: MCPAccessConfig{
			LocalDomainID: "sparkclaw-local",
		},
		Plugins: PluginsConfig{
			Entries: PluginEntriesConfig{
				InfinimeshInfo: InfinimeshInfoPluginConfig{
					Config: InfinimeshInfoConfig{
						BaseURL:               "https://info.infinimesh.cloud",
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
				Provider: "agent-browser",
				Profile:  "default",
			},
			Reminders: RemindersToolConfig{
				Enabled:             true,
				DefaultChannel:      "web",
				MaxDeliveryAttempts: 8,
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
						Provider:   weixinproto.QRProvider,
						BaseURL:    "https://ilinkai.weixin.qq.com",
						CDNBaseURL: "https://novac2c.cdn.weixin.qq.com/c2c",
					},
					"mcp": {
						Enabled:  false,
						Provider: "iscp-mcp",
					},
				},
			},
		},
		MCPServers: map[string]MCPServerConfig{},
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
				Command:              "agent-browser",
				TimeoutMS:            30000,
				StartupTimeoutMS:     10000,
				DaemonIdleTimeoutMS:  DefaultBrowserDaemonIdleTimeoutMS,
				SettleTimeoutMS:      15000,
				SettleQuietPeriodMS:  500,
				SettlePollIntervalMS: 100,
				RouteRebindLimit:     2,
				ProfileDir:           "./data/browser-profiles",
			},
			DocumentOCR: DocumentOCRAdapterConfig{
				Enabled:        false,
				Provider:       "openai-http",
				Model:          "sparkclaw-ocr",
				TimeoutSeconds: 120,
				MaxUploadBytes: 12 << 20,
				MaxOutputBytes: 1 << 20,
				MaxConcurrency: 2,
				MaxPending:     2,
			},
			PPTXVisualQA: PPTXVisualQAAdapterConfig{
				Phase:                     "disabled",
				RepairQualifiedClasses:    []string{},
				RepairQualifiedOperations: []string{},
				BlockingQualifiedClasses:  []string{},
				MaxRepairAttempts:         2,
				TimeoutSeconds:            120,
				MaxInputBytes:             64 << 20,
				MaxPDFBytes:               64 << 20,
				MaxPages:                  100,
				MaxChangedPages:           20,
				RasterScale:               1.5,
				MaxPagePixels:             20_000_000,
				MaxPNGBytes:               12 << 20,
				DiagnosticToleranceMilli:  2,
				ReadinessTTLSeconds:       300,
			},
		},
		Memory: MemoryConfig{
			Enabled:              true,
			WritePolicy:          "candidate_then_confirm",
			AllowSensitiveMemory: false,
			RetentionDays:        180,
			RedactPatterns:       []string{"api_key", "password", "token", "ssh_key"},
		},
		PassiveNotifications: PassiveNotificationsConfig{
			MaxPerOwner:   500,
			RetentionDays: 90,
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
			Backend:                   "file",
			Path:                      "./data/memory/gateway-state.json",
			DSN:                       "",
			StartupTimeoutSeconds:     180,
			ReadTimeoutSeconds:        10,
			WriteTimeoutSeconds:       30,
			TransactionTimeoutSeconds: 60,
			EncryptAtRest:             false,
			EncryptionKey:             "",
			EncryptionKeyFile:         "",
			CredentialKey:             "",
			CredentialKeyFile:         "./data/memory/gateway-credentials.key",
		},
		Runtime: RuntimeConfig{
			ObservationSummaryMaxBytes:    2400,
			StageEvidenceMaxBytes:         8000,
			StageMaxDurationSeconds:       180,
			StageMaxNoProgressActions:     3,
			StageMaxObservationReads:      2,
			RunMaxDurationSeconds:         1800,
			RunMaxToolCalls:               32,
			RunObservationCompactionBytes: 36000,
			RunMaxObservationBytes:        48000,
			RunMaxRepeatedToolCalls:       3,
		},
		Logging: LoggingConfig{
			Level:          "info",
			RedactPatterns: []string{"api_key", "password", "token", "ssh_key"},
		},
	}
	return cfg
}

func applyEnv(cfg *Config) error {
	if err := rejectLegacyModelCapacityEnv(); err != nil {
		return err
	}
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
	if v := os.Getenv("SPARKCLAW_BRIDGE_TOKEN"); v != "" {
		cfg.Gateway.BridgeToken = v
	}
	if v := os.Getenv("SPARKCLAW_JINGSI_LAN_ENABLED"); v != "" {
		cfg.JingSiLAN.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_JINGSI_SESSION_ID"); v != "" {
		cfg.JingSiLAN.SessionID = v
	}
	if v := os.Getenv("SPARKCLAW_JINGSI_MAX_MESSAGE_BYTES"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil {
			cfg.JingSiLAN.MaxMessageBytes = limit
		}
	}
	if v := os.Getenv("SPARKCLAW_PAIRING_REQUIRED"); v != "" {
		cfg.Gateway.PairingRequired = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_ISCP_PAIRING_ENABLED"); v != "" {
		cfg.ISCPPairing.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_ISCP_DOMAIN_ID"); v != "" {
		cfg.ISCPPairing.DomainID = v
	}
	if v := os.Getenv("SPARKCLAW_ISCP_AUTHORITY_URL"); v != "" {
		cfg.ISCPPairing.AuthorityURL = v
	}
	if v := os.Getenv("SPARKCLAW_ISCP_AUTHORITY_TOKEN_ENV"); v != "" {
		cfg.ISCPPairing.TokenEnv = v
	}
	if v := os.Getenv("SPARKCLAW_ISCP_AUTHORITY_TOKEN_FILE"); v != "" {
		cfg.ISCPPairing.TokenFile = v
	}
	if v := os.Getenv("SPARKCLAW_MCP_LOCAL_DOMAIN_ID"); v != "" {
		cfg.MCPAccess.LocalDomainID = v
	}
	if v := os.Getenv("SPARKCLAW_MCP_ALLOWED_ORIGINS"); v != "" {
		cfg.MCPAccess.AllowedOrigins = splitCSV(v)
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
	if v := os.Getenv("SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS"); v != "" {
		seconds, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS must be an integer: %w", err)
		}
		cfg.State.StartupTimeoutSeconds = seconds
	}
	if v := os.Getenv("SPARKCLAW_STATE_READ_TIMEOUT_SECONDS"); v != "" {
		seconds, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("SPARKCLAW_STATE_READ_TIMEOUT_SECONDS must be an integer: %w", err)
		}
		cfg.State.ReadTimeoutSeconds = seconds
	}
	if v := os.Getenv("SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS"); v != "" {
		seconds, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS must be an integer: %w", err)
		}
		cfg.State.WriteTimeoutSeconds = seconds
	}
	if v := os.Getenv("SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS"); v != "" {
		seconds, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS must be an integer: %w", err)
		}
		cfg.State.TransactionTimeoutSeconds = seconds
	}
	if v := os.Getenv("SPARKCLAW_STATE_ENCRYPT_AT_REST"); v != "" {
		enabled, err := parseStoreBoolOverride("SPARKCLAW_STATE_ENCRYPT_AT_REST", v)
		if err != nil {
			return err
		}
		cfg.State.EncryptAtRest = enabled
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
	if v := os.Getenv("SPARKCLAW_DEEP_BASE_URL"); v != "" {
		cfg.Model.Deep.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_DEEP_MODEL"); v != "" {
		cfg.Model.Deep.Model = v
	}
	if v := os.Getenv("SPARKCLAW_DEEP_SERVED_NAME"); v != "" {
		cfg.Model.Deep.Name = v
	}
	if v := os.Getenv("SPARKCLAW_EMBEDDING_BASE_URL"); v != "" {
		cfg.Model.Embedding.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_EMBEDDING_MODEL"); v != "" {
		cfg.Model.Embedding.Model = v
	}
	if v := os.Getenv("SPARKCLAW_GUARD_BASE_URL"); v != "" {
		cfg.Model.Guard.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_GUARD_MODEL"); v != "" {
		cfg.Model.Guard.Model = v
	}
	if v := os.Getenv("SPARKCLAW_MODEL_CAPACITY_PROFILE"); v != "" {
		cfg.Model.CapacityProfile = v
	}
	if v := os.Getenv("SPARKCLAW_MODEL_CAPACITY_CATALOG"); v != "" {
		cfg.Model.CapacityCatalog = v
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
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_COMMAND"); v != "" {
		cfg.Adapters.BrowserAutomation.Command = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_TIMEOUT_MS"); v != "" {
		if timeoutMS, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.BrowserAutomation.TimeoutMS = timeoutMS
		}
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_STARTUP_TIMEOUT_MS"); v != "" {
		if timeoutMS, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.BrowserAutomation.StartupTimeoutMS = timeoutMS
		}
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS"); v != "" {
		if timeoutMS, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.BrowserAutomation.DaemonIdleTimeoutMS = timeoutMS
		}
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_SETTLE_TIMEOUT_MS"); v != "" {
		if timeoutMS, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.BrowserAutomation.SettleTimeoutMS = timeoutMS
		}
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_SETTLE_QUIET_PERIOD_MS"); v != "" {
		if timeoutMS, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.BrowserAutomation.SettleQuietPeriodMS = timeoutMS
		}
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_SETTLE_POLL_INTERVAL_MS"); v != "" {
		if timeoutMS, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.BrowserAutomation.SettlePollIntervalMS = timeoutMS
		}
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_AUTOMATION_ROUTE_REBIND_LIMIT"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.BrowserAutomation.RouteRebindLimit = limit
		}
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE"); v != "" {
		cfg.Adapters.BrowserAutomation.ChromiumExecutable = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_PROFILE_DIR"); v != "" {
		cfg.Adapters.BrowserAutomation.ProfileDir = v
	}
	if v := os.Getenv("SPARKCLAW_OCR_ENABLED"); v != "" {
		cfg.Adapters.DocumentOCR.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_OCR_PROVIDER"); v != "" {
		cfg.Adapters.DocumentOCR.Provider = v
	}
	if v := os.Getenv("SPARKCLAW_OCR_BASE_URL"); v != "" {
		cfg.Adapters.DocumentOCR.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_OCR_ALLOWED_HOSTS"); v != "" {
		cfg.Adapters.DocumentOCR.AllowedHosts = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_OCR_MODEL"); v != "" {
		cfg.Adapters.DocumentOCR.Model = v
	}
	if v := os.Getenv("SPARKCLAW_OCR_TIMEOUT_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.DocumentOCR.TimeoutSeconds = seconds
		}
	}
	if v := os.Getenv("SPARKCLAW_OCR_MAX_UPLOAD_BYTES"); v != "" {
		if maxBytes, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Adapters.DocumentOCR.MaxUploadBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_OCR_MAX_OUTPUT_BYTES"); v != "" {
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.DocumentOCR.MaxOutputBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_OCR_MAX_CONCURRENCY"); v != "" {
		if maxConcurrency, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.DocumentOCR.MaxConcurrency = maxConcurrency
		}
	}
	if v := os.Getenv("SPARKCLAW_OCR_MAX_PENDING"); v != "" {
		if maxPending, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.DocumentOCR.MaxPending = maxPending
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_PHASE"); v != "" {
		cfg.Adapters.PPTXVisualQA.Phase = v
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_CLASSES"); v != "" {
		cfg.Adapters.PPTXVisualQA.RepairQualifiedClasses = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_OPERATIONS"); v != "" {
		cfg.Adapters.PPTXVisualQA.RepairQualifiedOperations = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_BLOCKING_QUALIFIED_CLASSES"); v != "" {
		cfg.Adapters.PPTXVisualQA.BlockingQualifiedClasses = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_REPAIR_ATTEMPTS"); v != "" {
		if attempts, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.PPTXVisualQA.MaxRepairAttempts = attempts
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_BASE_URL"); v != "" {
		cfg.Adapters.PPTXVisualQA.BaseURL = v
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_ALLOWED_HOSTS"); v != "" {
		cfg.Adapters.PPTXVisualQA.AllowedHosts = splitCSV(v)
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_TIMEOUT_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.PPTXVisualQA.TimeoutSeconds = seconds
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_INPUT_BYTES"); v != "" {
		if maxBytes, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Adapters.PPTXVisualQA.MaxInputBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_PDF_BYTES"); v != "" {
		if maxBytes, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Adapters.PPTXVisualQA.MaxPDFBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_PAGES"); v != "" {
		if maxPages, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.PPTXVisualQA.MaxPages = maxPages
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_CHANGED_PAGES"); v != "" {
		if maxPages, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.PPTXVisualQA.MaxChangedPages = maxPages
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_RASTER_SCALE"); v != "" {
		if scale, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Adapters.PPTXVisualQA.RasterScale = scale
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_PAGE_PIXELS"); v != "" {
		if maxPixels, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Adapters.PPTXVisualQA.MaxPagePixels = maxPixels
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_MAX_PNG_BYTES"); v != "" {
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.PPTXVisualQA.MaxPNGBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_DIAGNOSTIC_TOLERANCE_MILLI"); v != "" {
		if tolerance, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.PPTXVisualQA.DiagnosticToleranceMilli = tolerance
		}
	}
	if v := os.Getenv("SPARKCLAW_PPTX_VISUAL_QA_READINESS_TTL_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			cfg.Adapters.PPTXVisualQA.ReadinessTTLSeconds = seconds
		}
	}
	if v := os.Getenv("SPARKCLAW_REMINDERS_ENABLED"); v != "" {
		cfg.Tools.Reminders.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_REMINDERS_DEFAULT_CHANNEL"); v != "" {
		cfg.Tools.Reminders.DefaultChannel = v
	}
	if v := os.Getenv("SPARKCLAW_REMINDERS_MAX_DELIVERY_ATTEMPTS"); v != "" {
		if attempts, err := strconv.Atoi(v); err == nil {
			cfg.Tools.Reminders.MaxDeliveryAttempts = attempts
		}
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
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_ID"); v != "" {
		info.LicenseID = v
	}
	if v := os.Getenv("SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY"); v != "" {
		info.LicenseKey = v
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
	if v := os.Getenv("SPARKCLAW_OBSERVATION_SUMMARY_MAX_BYTES"); v != "" {
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.ObservationSummaryMaxBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES"); v != "" {
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.StageEvidenceMaxBytes = maxBytes
		}
	}
	if v := os.Getenv("SPARKCLAW_WORKFLOW_STAGE_MAX_OBSERVATION_READS"); v != "" {
		if maxReads, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.StageMaxObservationReads = maxReads
		}
	}
	if v := os.Getenv("SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES"); v != "" {
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.RunObservationCompactionBytes = maxBytes
			cfg.Runtime.runObservationCompactionExplicit = true
		}
	}
	if v := os.Getenv("SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES"); v != "" {
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.RunMaxObservationBytes = maxBytes
		}
	} else if v := os.Getenv("SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES"); v != "" {
		// Deprecated environment override, kept for pre-rename deployments.
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.RunMaxObservationBytes = maxBytes
		}
	} else if v := os.Getenv("SPARKCLAW_REACT_MAX_OBSERVATION_BYTES"); v != "" {
		// Deprecated environment override, kept for pre-workflow deployments.
		if maxBytes, err := strconv.Atoi(v); err == nil {
			cfg.Runtime.RunMaxObservationBytes = maxBytes
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
	return nil
}

func parseStoreBoolOverride(name, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "required":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be one of 1, true, yes, on, required, 0, false, no, or off", name)
	}
}

func normalizeStateConfig(state *StateConfig) error {
	state.Backend = strings.ToLower(strings.TrimSpace(state.Backend))
	state.DSN = strings.TrimSpace(state.DSN)
	state.Path = strings.TrimSpace(state.Path)
	state.EncryptionKeyFile = strings.TrimSpace(state.EncryptionKeyFile)
	if state.StartupTimeoutSeconds < 1 || state.StartupTimeoutSeconds > 900 {
		return errors.New("state.startup_timeout_seconds must be between 1 and 900")
	}
	if state.ReadTimeoutSeconds < 1 || state.ReadTimeoutSeconds > 900 {
		return errors.New("state.read_timeout_seconds must be between 1 and 900")
	}
	if state.WriteTimeoutSeconds < 1 || state.WriteTimeoutSeconds > 900 {
		return errors.New("state.write_timeout_seconds must be between 1 and 900")
	}
	if state.TransactionTimeoutSeconds < 1 || state.TransactionTimeoutSeconds > 900 {
		return errors.New("state.transaction_timeout_seconds must be between 1 and 900")
	}
	switch state.Backend {
	case "memory", "postgres":
		// Encryption at rest is only implemented by the file backend; accepting
		// the knobs here would silently store plaintext.
		if state.EncryptAtRest || strings.TrimSpace(state.EncryptionKey) != "" || state.EncryptionKeyFile != "" {
			return fmt.Errorf("state.encrypt_at_rest and encryption keys are only supported by the file backend, not %q", state.Backend)
		}
		if state.Backend == "postgres" && state.DSN == "" {
			return errors.New("state.dsn is required when state.backend is postgres")
		}
		return nil
	case "file":
		if state.Path == "" {
			return errors.New("state.path is required when state.backend is file")
		}
		path, err := filepath.Abs(state.Path)
		if err != nil {
			return fmt.Errorf("resolve state.path: %w", err)
		}
		state.Path = filepath.Clean(path)
		if !filepath.IsAbs(state.Path) {
			return errors.New("state.path must resolve to an absolute path")
		}
		if state.EncryptionKeyFile != "" {
			keyFile, err := filepath.Abs(state.EncryptionKeyFile)
			if err != nil {
				return fmt.Errorf("resolve state.encryption_key_file: %w", err)
			}
			state.EncryptionKeyFile = filepath.Clean(keyFile)
		}
		if !state.EncryptAtRest {
			return nil
		}
		directConfigured := strings.TrimSpace(state.EncryptionKey) != ""
		fileConfigured := state.EncryptionKeyFile != ""
		if directConfigured == fileConfigured {
			return errors.New("encrypted file state requires exactly one of state.encryption_key or state.encryption_key_file")
		}
		if !fileConfigured {
			return nil
		}
		raw, err := os.ReadFile(state.EncryptionKeyFile)
		if err != nil {
			return fmt.Errorf("read state.encryption_key_file: %w", err)
		}
		if strings.TrimSpace(string(raw)) == "" {
			return errors.New("state.encryption_key_file must not be empty")
		}
		return nil
	default:
		return errors.New("state.backend must be memory, file, or postgres")
	}
}

func applyInfinimeshInfoCredentials(cfg *Config) error {
	info := &cfg.Plugins.Entries.InfinimeshInfo.Config
	var err error
	info.LicenseKey, err = secretFromEnvOrFile(
		info.LicenseKey,
		"SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE",
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
		cfg.Channels["weixin"] = Default().Tools.Notifications.Channels["weixin"]
	}
	if _, ok := cfg.Channels["telegram"]; !ok {
		cfg.Channels["telegram"] = Default().Tools.Notifications.Channels["telegram"]
	}
	if _, ok := cfg.Channels["mcp"]; !ok {
		cfg.Channels["mcp"] = Default().Tools.Notifications.Channels["mcp"]
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
