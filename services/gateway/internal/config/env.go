package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// envApply writes one environment value onto the config. Implementations
// that tolerate malformed input leave the config untouched and return nil;
// implementations that reject malformed input return an error naming the
// variable.
type envApply func(cfg *Config, value string) error

// envBinding is one row of the environment override table: the variable
// name, the config it overrides, and a one-line description. Adding a knob
// means adding a row; the table order is the application order, so a later
// row that targets the same field wins over an earlier one.
type envBinding struct {
	name string
	// aliases lists deprecated names consulted in order only when name is
	// unset or empty, so the new name always wins.
	aliases []string
	doc     string
	apply   envApply
}

// retiredEnvironment lists variables whose feature was removed; a set value
// fails config load so a stale deployment cannot silently lose the knob.
var retiredEnvironment = []retiredEnv{
	{name: "SPARKCLAW_MODEL_MODE", reason: "mock routing comes from the selected capacity profile (SPARKCLAW_MODEL_CAPACITY_PROFILE)"},
	{name: "SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_PROFILE_DIR", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_DISPLAY", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_XAUTHORITY", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_COMMAND", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_TRANSPORT", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_CDP_PROFILE_ID", reason: retiredBrowserReason},
	{name: "SPARKCLAW_BROWSER_CDP_CONNECT_TIMEOUT_MS", reason: retiredBrowserReason},
}

const retiredBrowserReason = "remove it and use the SparkClaw Browser Bridge controller"

type retiredEnv struct {
	name   string
	reason string
}

func envString(field func(*Config) *string) envApply {
	return func(cfg *Config, value string) error {
		*field(cfg) = value
		return nil
	}
}

func envBool(field func(*Config) *bool) envApply {
	return func(cfg *Config, value string) error {
		*field(cfg) = parseBool(value)
		return nil
	}
}

func envCSV(field func(*Config) *[]string) envApply {
	return func(cfg *Config, value string) error {
		*field(cfg) = splitCSV(value)
		return nil
	}
}

// envInt ignores unparsable values so a typo keeps the default instead of
// failing load; use envIntStrict for knobs whose silence would be dangerous.
func envInt(field func(*Config) *int) envApply {
	return func(cfg *Config, value string) error {
		if parsed, err := strconv.Atoi(value); err == nil {
			*field(cfg) = parsed
		}
		return nil
	}
}

func envInt64(field func(*Config) *int64) envApply {
	return func(cfg *Config, value string) error {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			*field(cfg) = parsed
		}
		return nil
	}
}

func envFloat64(field func(*Config) *float64) envApply {
	return func(cfg *Config, value string) error {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			*field(cfg) = parsed
		}
		return nil
	}
}

// envIntStrict trims and rejects a value that is not an integer.
func envIntStrict(name string, field func(*Config) *int) envApply {
	return func(cfg *Config, value string) error {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("%s must be an integer: %w", name, err)
		}
		*field(cfg) = parsed
		return nil
	}
}

// envChannel edits one notification channel entry in place; the channel is
// guaranteed to exist because applyEnvBindings ensures the built-in
// channels before the table runs.
func envChannel(channel string, set func(ch *NotificationChannelConfig, value string)) envApply {
	return func(cfg *Config, value string) error {
		ch := cfg.Tools.Notifications.Channels[channel]
		set(&ch, value)
		cfg.Tools.Notifications.Channels[channel] = ch
		return nil
	}
}

func setInt(target *int, value string) {
	if parsed, err := strconv.Atoi(value); err == nil {
		*target = parsed
	}
}

func setInt64(target *int64, value string) {
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		*target = parsed
	}
}

func infinimeshInfo(cfg *Config) *InfinimeshInfoConfig {
	return &cfg.Plugins.Entries.InfinimeshInfo.Config
}

// envBindings is the single registry of gateway environment overrides.
var envBindings = []envBinding{
	{name: "SPARKCLAW_BIND", doc: "Gateway listen address.", apply: envString(func(c *Config) *string { return &c.Gateway.Bind })},
	{name: "SPARKCLAW_PORT", doc: "Gateway listen port.", apply: envInt(func(c *Config) *int { return &c.Gateway.Port })},
	{name: "SPARKCLAW_API_TOKEN", doc: "Static gateway bearer token; empty means pairing-only auth.", apply: envString(func(c *Config) *string { return &c.Gateway.APIToken })},
	{name: "SPARKCLAW_WEBCHAT_PROXY_TOKEN", doc: "Private token that authenticates the WebChat reverse proxy for pairing bootstrap.", apply: envString(func(c *Config) *string { return &c.Gateway.WebChatProxyToken })},
	{name: "SPARKCLAW_BRIDGE_TOKEN", doc: "Dedicated bearer for the loopback ISCP bridge dispatch routes.", apply: envString(func(c *Config) *string { return &c.Gateway.BridgeToken })},
	{name: "SPARKCLAW_JINGSI_LAN_ENABLED", doc: "Enable the JingSi LAN listener.", apply: envBool(func(c *Config) *bool { return &c.JingSiLAN.Enabled })},
	{name: "SPARKCLAW_JINGSI_SESSION_ID", doc: "JingSi LAN session identifier (required when enabled).", apply: envString(func(c *Config) *string { return &c.JingSiLAN.SessionID })},
	{name: "SPARKCLAW_JINGSI_MAX_MESSAGE_BYTES", doc: "Maximum JingSi LAN message size in bytes.", apply: envInt(func(c *Config) *int { return &c.JingSiLAN.MaxMessageBytes })},
	{name: "SPARKCLAW_JINGSI_RUNTIME_V1_ENABLED", doc: "Enable the JingSi Runtime v1 routes.", apply: envBool(func(c *Config) *bool { return &c.JingSiRuntime.Enabled })},
	{name: "SPARKCLAW_JINGSI_RUNTIME_V1_STATE_DIR", doc: "Directory for JingSi Runtime v1 execution state.", apply: envString(func(c *Config) *string { return &c.JingSiRuntime.StateDir })},
	{name: "SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN", doc: "JingSi Runtime v1 bearer token (exclusive with the token file).", apply: envString(func(c *Config) *string { return &c.JingSiRuntime.BearerToken })},
	{name: "SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN_FILE", doc: "Owner-only file holding the JingSi Runtime v1 bearer token.", apply: envString(func(c *Config) *string { return &c.JingSiRuntime.BearerTokenFile })},
	{name: "SPARKCLAW_JINGSI_RUNTIME_V1_MAX_CONCURRENT", doc: "Maximum concurrent JingSi Runtime v1 executions.", apply: envInt(func(c *Config) *int { return &c.JingSiRuntime.MaxConcurrent })},
	{name: "SPARKCLAW_JINGSI_RUNTIME_V1_ENFORCE_EFFECT_SCOPES", doc: "Admit new JingSi executions under effect-scope enforcement instead of the legacy tool_scope-only rule (pending InfiniCenter decision 0034).", apply: envBool(func(c *Config) *bool { return &c.JingSiRuntime.EnforceEffectScopes })},
	{name: "SPARKCLAW_JINGSI_RUNTIME_V1_RETENTION_DAYS", doc: "Days to keep terminal JingSi Runtime v1 execution records and negative fences (0 keeps them forever).", apply: envInt(func(c *Config) *int { return &c.JingSiRuntime.RetentionDays })},
	{name: "SPARKCLAW_PAIRING_REQUIRED", doc: "Require device pairing before serving authenticated routes.", apply: envBool(func(c *Config) *bool { return &c.Gateway.PairingRequired })},
	{name: "SPARKCLAW_ISCP_PAIRING_ENABLED", doc: "Enable ISCP pairing against the authority.", apply: envBool(func(c *Config) *bool { return &c.ISCPPairing.Enabled })},
	{name: "SPARKCLAW_ISCP_DOMAIN_ID", doc: "ISCP domain identifier of this gateway.", apply: envString(func(c *Config) *string { return &c.ISCPPairing.DomainID })},
	{name: "SPARKCLAW_ISCP_AUTHORITY_URL", doc: "ISCP pairing authority base URL.", apply: envString(func(c *Config) *string { return &c.ISCPPairing.AuthorityURL })},
	{name: "SPARKCLAW_ISCP_AUTHORITY_TOKEN_ENV", doc: "Name of the variable holding the ISCP authority token.", apply: envString(func(c *Config) *string { return &c.ISCPPairing.TokenEnv })},
	{name: "SPARKCLAW_ISCP_AUTHORITY_TOKEN_FILE", doc: "File holding the ISCP authority token.", apply: envString(func(c *Config) *string { return &c.ISCPPairing.TokenFile })},
	{name: "SPARKCLAW_MCP_LOCAL_DOMAIN_ID", doc: "Domain identifier presented on the /mcp endpoint.", apply: envString(func(c *Config) *string { return &c.MCPAccess.LocalDomainID })},
	{name: "SPARKCLAW_MCP_ALLOWED_ORIGINS", doc: "Comma-separated extra browser origins allowed on /mcp.", apply: envCSV(func(c *Config) *[]string { return &c.MCPAccess.AllowedOrigins })},
	{name: "SPARKCLAW_RATE_LIMIT_ENABLED", doc: "Enable the per-client gateway rate limiter.", apply: envBool(func(c *Config) *bool { return &c.Gateway.RateLimit.Enabled })},
	{name: "SPARKCLAW_RATE_LIMIT_PER_MINUTE", doc: "Sustained requests per minute per client.", apply: envInt(func(c *Config) *int { return &c.Gateway.RateLimit.RequestsPerMinute })},
	{name: "SPARKCLAW_RATE_LIMIT_BURST", doc: "Rate limiter burst allowance.", apply: envInt(func(c *Config) *int { return &c.Gateway.RateLimit.Burst })},
	{name: "SPARKCLAW_WORKSPACE_ROOT", doc: "Default workspace root; also resets the workspace allowlist to this root.", apply: func(cfg *Config, value string) error {
		cfg.Workspaces.DefaultRoot = value
		cfg.Workspaces.Allowlist = []string{value}
		return nil
	}},
	{name: "SPARKCLAW_TRACE_DIR", doc: "Directory for run traces.", apply: envString(func(c *Config) *string { return &c.Storage.TraceDir })},
	{name: "SPARKCLAW_ARTIFACT_BACKEND", doc: "Artifact storage backend (filesystem or s3).", apply: envString(func(c *Config) *string { return &c.Storage.ArtifactBackend })},
	{name: "SPARKCLAW_ARTIFACT_DIR", doc: "Filesystem artifact directory.", apply: envString(func(c *Config) *string { return &c.Storage.ArtifactDir })},
	{name: "SPARKCLAW_ARTIFACT_BUCKET", doc: "S3 artifact bucket name.", apply: envString(func(c *Config) *string { return &c.Storage.ArtifactBucket })},
	{name: "SPARKCLAW_S3_ENDPOINT", doc: "S3-compatible endpoint URL.", apply: envString(func(c *Config) *string { return &c.Storage.S3Endpoint })},
	{name: "SPARKCLAW_S3_REGION", doc: "S3 region.", apply: envString(func(c *Config) *string { return &c.Storage.S3Region })},
	{name: "SPARKCLAW_S3_ACCESS_KEY", doc: "S3 access key.", apply: envString(func(c *Config) *string { return &c.Storage.S3AccessKey })},
	{name: "SPARKCLAW_S3_SECRET_KEY", doc: "S3 secret key.", apply: envString(func(c *Config) *string { return &c.Storage.S3SecretKey })},
	{name: "SPARKCLAW_STATE_BACKEND", doc: "State store backend (memory, file, or postgres).", apply: envString(func(c *Config) *string { return &c.State.Backend })},
	{name: "SPARKCLAW_STATE_PATH", doc: "File backend state path.", apply: envString(func(c *Config) *string { return &c.State.Path })},
	{name: "SPARKCLAW_STATE_DSN", doc: "Postgres backend DSN.", apply: envString(func(c *Config) *string { return &c.State.DSN })},
	{name: "SPARKCLAW_POSTGRES_DSN", doc: "Postgres DSN; overrides SPARKCLAW_STATE_DSN when both are set.", apply: envString(func(c *Config) *string { return &c.State.DSN })},
	{name: "SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS", doc: "State store startup timeout (rejects non-integers).", apply: envIntStrict("SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS", func(c *Config) *int { return &c.State.StartupTimeoutSeconds })},
	{name: "SPARKCLAW_STATE_READ_TIMEOUT_SECONDS", doc: "State store read timeout (rejects non-integers).", apply: envIntStrict("SPARKCLAW_STATE_READ_TIMEOUT_SECONDS", func(c *Config) *int { return &c.State.ReadTimeoutSeconds })},
	{name: "SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS", doc: "State store write timeout (rejects non-integers).", apply: envIntStrict("SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS", func(c *Config) *int { return &c.State.WriteTimeoutSeconds })},
	{name: "SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS", doc: "State store transaction timeout (rejects non-integers).", apply: envIntStrict("SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS", func(c *Config) *int { return &c.State.TransactionTimeoutSeconds })},
	{name: "SPARKCLAW_STATE_ENCRYPT_AT_REST", doc: "Encrypt the file state store (rejects unrecognized booleans).", apply: func(cfg *Config, value string) error {
		enabled, err := parseStoreBoolOverride("SPARKCLAW_STATE_ENCRYPT_AT_REST", value)
		if err != nil {
			return err
		}
		cfg.State.EncryptAtRest = enabled
		return nil
	}},
	{name: "SPARKCLAW_STATE_ENCRYPTION_KEY", doc: "File state encryption key (exclusive with the key file).", apply: envString(func(c *Config) *string { return &c.State.EncryptionKey })},
	{name: "SPARKCLAW_STATE_ENCRYPTION_KEY_FILE", doc: "File holding the state encryption key.", apply: envString(func(c *Config) *string { return &c.State.EncryptionKeyFile })},
	{name: "SPARKCLAW_CREDENTIAL_KEY", doc: "Key protecting stored integration credentials.", apply: envString(func(c *Config) *string { return &c.State.CredentialKey })},
	{name: "SPARKCLAW_CREDENTIAL_KEY_FILE", doc: "File holding the credential key; resolved to an absolute path.", apply: envString(func(c *Config) *string { return &c.State.CredentialKeyFile })},
	{name: "SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS", doc: "HTTP timeout for model server calls.", apply: envInt(func(c *Config) *int { return &c.Model.HTTPTimeoutSeconds })},
	{name: "SPARKCLAW_MODEL_TIMEOUT_SECONDS", doc: "Alias of SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS; wins when both are set.", apply: envInt(func(c *Config) *int { return &c.Model.HTTPTimeoutSeconds })},
	{name: "SPARKCLAW_MODEL_DISABLE_THINKING", doc: "Send chat requests with thinking disabled.", apply: envBool(func(c *Config) *bool { return &c.Model.DisableThinking })},
	{name: "SPARKCLAW_SPEECH_ENABLED", doc: "Enable the speech (ASR) adapter.", apply: envBool(func(c *Config) *bool { return &c.Speech.Enabled })},
	{name: "SPARKCLAW_SPEECH_BACKEND", doc: "Speech backend (openai-http).", apply: envString(func(c *Config) *string { return &c.Speech.Backend })},
	{name: "SPARKCLAW_SPEECH_BASE_URL", doc: "Speech server base URL.", apply: envString(func(c *Config) *string { return &c.Speech.BaseURL })},
	{name: "SPARKCLAW_SPEECH_ALLOWED_HOSTS", doc: "Comma-separated non-local hosts the speech adapter may call.", apply: envCSV(func(c *Config) *[]string { return &c.Speech.AllowedHosts })},
	{name: "SPARKCLAW_SPEECH_MODEL", doc: "Speech model name.", apply: envString(func(c *Config) *string { return &c.Speech.Model })},
	{name: "SPARKCLAW_SPEECH_DEFAULT_LANGUAGE", doc: "Default transcription language (auto detects).", apply: envString(func(c *Config) *string { return &c.Speech.DefaultLanguage })},
	{name: "SPARKCLAW_SPEECH_TIMEOUT_SECONDS", doc: "Speech request timeout.", apply: envInt(func(c *Config) *int { return &c.Speech.TimeoutSeconds })},
	{name: "SPARKCLAW_SPEECH_MAX_AUDIO_SECONDS", doc: "Maximum accepted audio duration.", apply: envInt(func(c *Config) *int { return &c.Speech.MaxAudioSeconds })},
	{name: "SPARKCLAW_SPEECH_MAX_UPLOAD_BYTES", doc: "Maximum accepted audio upload size.", apply: envInt64(func(c *Config) *int64 { return &c.Speech.MaxUploadBytes })},
	{name: "SPARKCLAW_SPEECH_MAX_CONCURRENCY", doc: "Concurrent speech requests.", apply: envInt(func(c *Config) *int { return &c.Speech.MaxConcurrency })},
	{name: "SPARKCLAW_SPEECH_MAX_PENDING", doc: "Queued speech requests beyond the concurrency limit.", apply: envInt(func(c *Config) *int { return &c.Speech.MaxPending })},
	{name: "SPARKCLAW_FAST_BASE_URL", doc: "Fast lane model server base URL.", apply: envString(func(c *Config) *string { return &c.Model.Fast.BaseURL })},
	{name: "SPARKCLAW_FAST_MODEL", doc: "Fast lane model identifier sent to the server.", apply: envString(func(c *Config) *string { return &c.Model.Fast.Model })},
	{name: "SPARKCLAW_FAST_SERVED_NAME", doc: "Fast lane profile name used in traces and lane matching.", apply: envString(func(c *Config) *string { return &c.Model.Fast.Name })},
	{name: "SPARKCLAW_DEEP_BASE_URL", doc: "Deep lane model server base URL.", apply: envString(func(c *Config) *string { return &c.Model.Deep.BaseURL })},
	{name: "SPARKCLAW_DEEP_MODEL", doc: "Deep lane model identifier sent to the server.", apply: envString(func(c *Config) *string { return &c.Model.Deep.Model })},
	{name: "SPARKCLAW_DEEP_SERVED_NAME", doc: "Deep lane profile name used in traces and lane matching.", apply: envString(func(c *Config) *string { return &c.Model.Deep.Name })},
	{name: "SPARKCLAW_EMBEDDING_BASE_URL", doc: "Embedding lane model server base URL.", apply: envString(func(c *Config) *string { return &c.Model.Embedding.BaseURL })},
	{name: "SPARKCLAW_EMBEDDING_MODEL", doc: "Embedding lane model identifier.", apply: envString(func(c *Config) *string { return &c.Model.Embedding.Model })},
	{name: "SPARKCLAW_GUARD_BASE_URL", doc: "Guard lane model server base URL.", apply: envString(func(c *Config) *string { return &c.Model.Guard.BaseURL })},
	{name: "SPARKCLAW_GUARD_MODEL", doc: "Guard lane model identifier.", apply: envString(func(c *Config) *string { return &c.Model.Guard.Model })},
	{name: "SPARKCLAW_MODEL_CAPACITY_PROFILE", doc: "Capacity profile selected from the catalog; sets mock routing and token budgets.", apply: envString(func(c *Config) *string { return &c.Model.CapacityProfile })},
	{name: "OPENAI_API_KEY", doc: "Bearer token sent to the fast, deep, embedding, and guard model servers; leave empty for unauthenticated local servers.", apply: func(cfg *Config, value string) error {
		cfg.Model.APIKey = strings.TrimSpace(value)
		return nil
	}},
	{name: "SPARKCLAW_MODEL_CAPACITY_CATALOG", doc: "Path to the model capacity catalog JSON.", apply: envString(func(c *Config) *string { return &c.Model.CapacityCatalog })},
	{name: "SPARKCLAW_BROWSER_READ_ALLOW_HOSTS", doc: "Comma-separated hosts the browser read tool may fetch.", apply: envCSV(func(c *Config) *[]string { return &c.Security.BrowserReadAllowHosts })},
	{name: "SPARKCLAW_WEB_SEARCH_ENABLED", doc: "Enable the web search tool.", apply: envBool(func(c *Config) *bool { return &c.Tools.Web.Search.Enabled })},
	{name: "SPARKCLAW_WEB_SEARCH_PROVIDER", doc: "Web search provider (infinimesh-info).", apply: envString(func(c *Config) *string { return &c.Tools.Web.Search.Provider })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_ENABLED", doc: "Enable the browser automation tool.", apply: envBool(func(c *Config) *bool { return &c.Tools.BrowserAutomation.Enabled })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_PROVIDER", doc: "Browser automation provider (playwright-extension).", apply: envString(func(c *Config) *string { return &c.Tools.BrowserAutomation.Provider })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_PROFILE", doc: "Browser automation profile name.", apply: envString(func(c *Config) *string { return &c.Tools.BrowserAutomation.Profile })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_TIMEOUT_MS", doc: "Per-action browser automation timeout.", apply: envInt(func(c *Config) *int { return &c.Adapters.BrowserAutomation.TimeoutMS })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_STARTUP_TIMEOUT_MS", doc: "Browser controller startup timeout.", apply: envInt(func(c *Config) *int { return &c.Adapters.BrowserAutomation.StartupTimeoutMS })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_SETTLE_TIMEOUT_MS", doc: "Maximum wait for a page to settle.", apply: envInt(func(c *Config) *int { return &c.Adapters.BrowserAutomation.SettleTimeoutMS })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_SETTLE_QUIET_PERIOD_MS", doc: "Quiet period that counts as settled.", apply: envInt(func(c *Config) *int { return &c.Adapters.BrowserAutomation.SettleQuietPeriodMS })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_SETTLE_POLL_INTERVAL_MS", doc: "Settle polling interval.", apply: envInt(func(c *Config) *int { return &c.Adapters.BrowserAutomation.SettlePollIntervalMS })},
	{name: "SPARKCLAW_BROWSER_AUTOMATION_ROUTE_REBIND_LIMIT", doc: "Maximum route rebinds per navigation.", apply: envInt(func(c *Config) *int { return &c.Adapters.BrowserAutomation.RouteRebindLimit })},
	{name: "SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET", doc: "Absolute path of the Browser Bridge controller socket.", apply: envString(func(c *Config) *string { return &c.Adapters.BrowserAutomation.PlaywrightExtension.ControllerSocket })},
	{name: "SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID", doc: "Browser Bridge profile identifier (must be default).", apply: envString(func(c *Config) *string { return &c.Adapters.BrowserAutomation.PlaywrightExtension.ProfileID })},
	{name: "SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS", doc: "Browser Bridge socket connect timeout (rejects non-integers).", apply: func(cfg *Config, value string) error {
		timeoutMS, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS must be an integer: %w", err)
		}
		cfg.Adapters.BrowserAutomation.PlaywrightExtension.ConnectTimeoutMS = timeoutMS
		return nil
	}},
	{name: "SPARKCLAW_OCR_ENABLED", doc: "Enable the document OCR adapter.", apply: envBool(func(c *Config) *bool { return &c.Adapters.DocumentOCR.Enabled })},
	{name: "SPARKCLAW_OCR_PROVIDER", doc: "OCR provider (openai-http).", apply: envString(func(c *Config) *string { return &c.Adapters.DocumentOCR.Provider })},
	{name: "SPARKCLAW_OCR_BASE_URL", doc: "OCR model server base URL.", apply: envString(func(c *Config) *string { return &c.Adapters.DocumentOCR.BaseURL })},
	{name: "SPARKCLAW_OCR_ALLOWED_HOSTS", doc: "Comma-separated non-local hosts the OCR adapter may call.", apply: envCSV(func(c *Config) *[]string { return &c.Adapters.DocumentOCR.AllowedHosts })},
	{name: "SPARKCLAW_OCR_MODEL", doc: "OCR model name.", apply: envString(func(c *Config) *string { return &c.Adapters.DocumentOCR.Model })},
	{name: "SPARKCLAW_OCR_TIMEOUT_SECONDS", doc: "OCR request timeout.", apply: envInt(func(c *Config) *int { return &c.Adapters.DocumentOCR.TimeoutSeconds })},
	{name: "SPARKCLAW_OCR_MAX_UPLOAD_BYTES", doc: "Maximum OCR input size.", apply: envInt64(func(c *Config) *int64 { return &c.Adapters.DocumentOCR.MaxUploadBytes })},
	{name: "SPARKCLAW_OCR_MAX_OUTPUT_BYTES", doc: "Maximum OCR output size.", apply: envInt(func(c *Config) *int { return &c.Adapters.DocumentOCR.MaxOutputBytes })},
	{name: "SPARKCLAW_OCR_MAX_CONCURRENCY", doc: "Concurrent OCR requests.", apply: envInt(func(c *Config) *int { return &c.Adapters.DocumentOCR.MaxConcurrency })},
	{name: "SPARKCLAW_OCR_MAX_PENDING", doc: "Queued OCR requests beyond the concurrency limit.", apply: envInt(func(c *Config) *int { return &c.Adapters.DocumentOCR.MaxPending })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_PHASE", doc: "PPTX visual QA rollout phase (disabled, shadow, repair, blocking).", apply: envString(func(c *Config) *string { return &c.Adapters.PPTXVisualQA.Phase })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_CLASSES", doc: "Comma-separated finding classes eligible for automatic repair.", apply: envCSV(func(c *Config) *[]string { return &c.Adapters.PPTXVisualQA.RepairQualifiedClasses })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_REPAIR_QUALIFIED_OPERATIONS", doc: "Comma-separated operations eligible for automatic repair.", apply: envCSV(func(c *Config) *[]string { return &c.Adapters.PPTXVisualQA.RepairQualifiedOperations })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_BLOCKING_QUALIFIED_CLASSES", doc: "Comma-separated finding classes that block delivery.", apply: envCSV(func(c *Config) *[]string { return &c.Adapters.PPTXVisualQA.BlockingQualifiedClasses })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_MAX_REPAIR_ATTEMPTS", doc: "Maximum repair rounds per deck.", apply: envInt(func(c *Config) *int { return &c.Adapters.PPTXVisualQA.MaxRepairAttempts })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_BASE_URL", doc: "PDF renderer (Gotenberg) base URL.", apply: envString(func(c *Config) *string { return &c.Adapters.PPTXVisualQA.BaseURL })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_ALLOWED_HOSTS", doc: "Comma-separated non-local hosts the renderer client may call.", apply: envCSV(func(c *Config) *[]string { return &c.Adapters.PPTXVisualQA.AllowedHosts })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_TIMEOUT_SECONDS", doc: "Renderer request timeout.", apply: envInt(func(c *Config) *int { return &c.Adapters.PPTXVisualQA.TimeoutSeconds })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_MAX_INPUT_BYTES", doc: "Maximum PPTX input size.", apply: envInt64(func(c *Config) *int64 { return &c.Adapters.PPTXVisualQA.MaxInputBytes })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_MAX_PDF_BYTES", doc: "Maximum rendered PDF size.", apply: envInt64(func(c *Config) *int64 { return &c.Adapters.PPTXVisualQA.MaxPDFBytes })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_MAX_PAGES", doc: "Maximum pages inspected per deck.", apply: envInt(func(c *Config) *int { return &c.Adapters.PPTXVisualQA.MaxPages })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_MAX_CHANGED_PAGES", doc: "Maximum changed pages re-rendered per repair.", apply: envInt(func(c *Config) *int { return &c.Adapters.PPTXVisualQA.MaxChangedPages })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_RASTER_SCALE", doc: "Raster scale for page images.", apply: envFloat64(func(c *Config) *float64 { return &c.Adapters.PPTXVisualQA.RasterScale })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_MAX_PAGE_PIXELS", doc: "Maximum pixels per rasterized page.", apply: envInt64(func(c *Config) *int64 { return &c.Adapters.PPTXVisualQA.MaxPagePixels })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_MAX_PNG_BYTES", doc: "Maximum PNG size per page.", apply: envInt(func(c *Config) *int { return &c.Adapters.PPTXVisualQA.MaxPNGBytes })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_DIAGNOSTIC_TOLERANCE_MILLI", doc: "Diagnostic tolerance in thousandths.", apply: envInt(func(c *Config) *int { return &c.Adapters.PPTXVisualQA.DiagnosticToleranceMilli })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_READINESS_TTL_SECONDS", doc: "Cache lifetime of the renderer readiness probe.", apply: envInt(func(c *Config) *int { return &c.Adapters.PPTXVisualQA.ReadinessTTLSeconds })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_GOTENBERG_VERSION", doc: "Pinned Gotenberg version the sealed manifest attests to.", apply: envString(func(c *Config) *string { return &c.Adapters.PPTXVisualQA.GotenbergVersion })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_LIBREOFFICE_VERSION", doc: "Pinned LibreOffice version bundled in the Gotenberg image.", apply: envString(func(c *Config) *string { return &c.Adapters.PPTXVisualQA.LibreOfficeVersion })},
	{name: "SPARKCLAW_PPTX_VISUAL_QA_PDFIUM_VERSION", doc: "Pinned pypdfium2 version used for rasterization.", apply: envString(func(c *Config) *string { return &c.Adapters.PPTXVisualQA.PDFiumVersion })},
	{name: "SPARKCLAW_REMINDERS_ENABLED", doc: "Enable the reminders tool.", apply: envBool(func(c *Config) *bool { return &c.Tools.Reminders.Enabled })},
	{name: "SPARKCLAW_REMINDERS_DEFAULT_CHANNEL", doc: "Default reminder delivery channel.", apply: envString(func(c *Config) *string { return &c.Tools.Reminders.DefaultChannel })},
	{name: "SPARKCLAW_REMINDERS_MAX_DELIVERY_ATTEMPTS", doc: "Maximum reminder delivery attempts.", apply: envInt(func(c *Config) *int { return &c.Tools.Reminders.MaxDeliveryAttempts })},
	{name: "SPARKCLAW_WEIXIN_NOTIFICATION_ENABLED", doc: "Enable the WeChat notification channel.", apply: envChannel("weixin", func(ch *NotificationChannelConfig, v string) { ch.Enabled = parseBool(v) })},
	{name: "SPARKCLAW_WEIXIN_NOTIFICATION_PROVIDER", doc: "WeChat channel provider.", apply: envChannel("weixin", func(ch *NotificationChannelConfig, v string) { ch.Provider = v })},
	{name: "SPARKCLAW_WEIXIN_NOTIFICATION_BASE_URL", doc: "WeChat iLink API base URL.", apply: envChannel("weixin", func(ch *NotificationChannelConfig, v string) { ch.BaseURL = v })},
	{name: "SPARKCLAW_WEIXIN_CDN_BASE_URL", doc: "WeChat media CDN base URL.", apply: envChannel("weixin", func(ch *NotificationChannelConfig, v string) { ch.CDNBaseURL = v })},
	{name: "SPARKCLAW_WEIXIN_NOTIFICATION_TOKEN", doc: "WeChat channel token.", apply: envChannel("weixin", func(ch *NotificationChannelConfig, v string) { ch.Token = v })},
	{name: "SPARKCLAW_WEIXIN_NOTIFICATION_RECIPIENT", doc: "WeChat channel default recipient.", apply: envChannel("weixin", func(ch *NotificationChannelConfig, v string) { ch.Recipient = v })},
	{name: "SPARKCLAW_TELEGRAM_ENABLED", doc: "Enable the Telegram notification channel.", apply: envChannel("telegram", func(ch *NotificationChannelConfig, v string) { ch.Enabled = parseBool(v) })},
	{name: "SPARKCLAW_TELEGRAM_BASE_URL", doc: "Telegram Bot API base URL.", apply: envChannel("telegram", func(ch *NotificationChannelConfig, v string) { ch.BaseURL = v })},
	{name: "SPARKCLAW_TELEGRAM_POLL_TIMEOUT_SECONDS", doc: "Telegram long-polling timeout.", apply: envChannel("telegram", func(ch *NotificationChannelConfig, v string) { setInt(&ch.PollTimeoutSeconds, v) })},
	{name: "SPARKCLAW_TELEGRAM_MAX_DOWNLOAD_BYTES", doc: "Maximum Telegram attachment download size.", apply: envChannel("telegram", func(ch *NotificationChannelConfig, v string) { setInt64(&ch.MaxDownloadBytes, v) })},
	{name: "SPARKCLAW_TELEGRAM_MAX_ATTACHMENTS", doc: "Maximum attachments per Telegram message.", apply: envChannel("telegram", func(ch *NotificationChannelConfig, v string) { setInt(&ch.MaxAttachments, v) })},
	{name: "SPARKCLAW_TELEGRAM_MAX_VOICE_SECONDS", doc: "Maximum Telegram voice note duration.", apply: envChannel("telegram", func(ch *NotificationChannelConfig, v string) { setInt(&ch.MaxVoiceSeconds, v) })},
	{name: "SPARKCLAW_TELEGRAM_MAX_CONCURRENCY", doc: "Concurrent Telegram update handlers.", apply: envChannel("telegram", func(ch *NotificationChannelConfig, v string) { setInt(&ch.MaxConcurrency, v) })},
	{name: "SPARKCLAW_TELEGRAM_MAX_PENDING", doc: "Queued Telegram updates beyond the concurrency limit.", apply: envChannel("telegram", func(ch *NotificationChannelConfig, v string) { setInt(&ch.MaxPending, v) })},
	{name: "SPARKCLAW_INFINIMESH_INFO_BASE_URL", doc: "Infinimesh Info search API base URL.", apply: envString(func(c *Config) *string { return &infinimeshInfo(c).BaseURL })},
	{name: "SPARKCLAW_INFINIMESH_INFO_TOKEN_BATCH_SIZE", doc: "Infinimesh Info token batch size.", apply: envInt(func(c *Config) *int { return &infinimeshInfo(c).TokenBatchSize })},
	{name: "SPARKCLAW_INFINIMESH_INFO_MAX_ATTEMPTS", doc: "Infinimesh Info retry attempts.", apply: envInt(func(c *Config) *int { return &infinimeshInfo(c).MaxAttempts })},
	{name: "SPARKCLAW_INFINIMESH_INFO_RETRY_BASE_DELAY_MS", doc: "Infinimesh Info retry base delay.", apply: envInt(func(c *Config) *int { return &infinimeshInfo(c).RetryBaseDelayMS })},
	{name: "SPARKCLAW_INFINIMESH_INFO_REQUEST_TIMEOUT_SECONDS", doc: "Infinimesh Info request timeout.", apply: envInt(func(c *Config) *int { return &infinimeshInfo(c).RequestTimeoutSeconds })},
	{name: "SPARKCLAW_INFINIMESH_INFO_RESPONSE_BODY_MAX_BYTES", doc: "Maximum Infinimesh Info response size.", apply: envInt64(func(c *Config) *int64 { return &infinimeshInfo(c).ResponseBodyMaxBytes })},
	{name: "SPARKCLAW_INFINIMESH_INFO_LANGUAGE", doc: "Infinimesh Info result language.", apply: envString(func(c *Config) *string { return &infinimeshInfo(c).Language })},
	{name: "SPARKCLAW_INFINIMESH_INFO_MAX_SOURCES", doc: "Maximum Infinimesh Info sources per query.", apply: envInt(func(c *Config) *int { return &infinimeshInfo(c).MaxSources })},
	{name: "SPARKCLAW_INFINIMESH_INFO_LICENSE_ID", doc: "Infinimesh Info license identifier.", apply: envString(func(c *Config) *string { return &infinimeshInfo(c).LicenseID })},
	{name: "SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY", doc: "Infinimesh Info license key (or use the _FILE variant).", apply: envString(func(c *Config) *string { return &infinimeshInfo(c).LicenseKey })},
	{name: "SPARKCLAW_MEMORY_RETENTION_DAYS", doc: "Days to retain memories.", apply: envInt(func(c *Config) *int { return &c.Memory.RetentionDays })},
	{name: "SPARKCLAW_TOOLS_POLICY_PATH", doc: "Path to the tool deny/approval policy JSON.", apply: envString(func(c *Config) *string { return &c.Security.ToolPolicyPath })},
	{name: "SPARKCLAW_SANDBOX_BACKEND", doc: "Sandbox backend (local-docker or http).", apply: envString(func(c *Config) *string { return &c.Sandbox.Backend })},
	{name: "SPARKCLAW_SANDBOX_RUNNER_URL", doc: "HTTP sandbox runner URL.", apply: envString(func(c *Config) *string { return &c.Sandbox.RunnerURL })},
	{name: "SPARKCLAW_SANDBOX_IMAGE", doc: "Sandbox container image.", apply: envString(func(c *Config) *string { return &c.Sandbox.Image })},
	{name: "SPARKCLAW_SANDBOX_NETWORK", doc: "Sandbox container network mode.", apply: envString(func(c *Config) *string { return &c.Sandbox.Network })},
	{name: "SPARKCLAW_OBSERVATION_SUMMARY_MAX_BYTES", doc: "Maximum bytes of one observation summary.", apply: envInt(func(c *Config) *int { return &c.Runtime.ObservationSummaryMaxBytes })},
	{name: "SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES", doc: "Maximum persisted evidence bytes provisioned to one workflow stage.", apply: envInt(func(c *Config) *int { return &c.Runtime.StageEvidenceMaxBytes })},
	{name: "SPARKCLAW_WORKFLOW_STAGE_MAX_OBSERVATION_READS", doc: "Maximum observation.read calls per workflow stage.", apply: envInt(func(c *Config) *int { return &c.Runtime.StageMaxObservationReads })},
	{name: "SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES", doc: "Observation bytes at which a workflow run compacts; marks the value explicit.", apply: func(cfg *Config, value string) error {
		if maxBytes, err := strconv.Atoi(value); err == nil {
			cfg.Runtime.RunObservationCompactionBytes = maxBytes
			cfg.Runtime.runObservationCompactionExplicit = true
		}
		return nil
	}},
	{
		name: "SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES",
		// Deprecated names kept for pre-rename and pre-workflow deployments.
		aliases: []string{"SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES", "SPARKCLAW_REACT_MAX_OBSERVATION_BYTES"},
		doc:     "Maximum observation bytes retained across one workflow run.",
		apply:   envInt(func(c *Config) *int { return &c.Runtime.RunMaxObservationBytes }),
	},
}

func applyEnv(cfg *Config) error {
	for _, retired := range retiredEnvironment {
		if strings.TrimSpace(os.Getenv(retired.name)) != "" {
			return fmt.Errorf("%s is retired; %s", retired.name, retired.reason)
		}
	}
	if err := rejectLegacyModelCapacityEnv(); err != nil {
		return err
	}
	return applyEnvBindings(cfg, os.Getenv)
}

// applyEnvBindings runs the override table against getenv; an empty value
// leaves the config untouched. It is separated from applyEnv so tests can
// feed a versioned env file through the same rows the process would use.
func applyEnvBindings(cfg *Config, getenv func(string) string) error {
	ensureNotificationChannels(&cfg.Tools.Notifications)
	for _, binding := range envBindings {
		value := getenv(binding.name)
		for i := 0; value == "" && i < len(binding.aliases); i++ {
			value = getenv(binding.aliases[i])
		}
		if value == "" {
			continue
		}
		if err := binding.apply(cfg, value); err != nil {
			return err
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
