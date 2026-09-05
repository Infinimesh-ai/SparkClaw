package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func applyEnv(cfg *Config) error {
	for _, name := range []string{
		"SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE",
		"SPARKCLAW_BROWSER_PROFILE_DIR",
		"SPARKCLAW_BROWSER_DISPLAY",
		"SPARKCLAW_BROWSER_XAUTHORITY",
		"SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS",
		"SPARKCLAW_BROWSER_AUTOMATION_COMMAND",
		"SPARKCLAW_BROWSER_AUTOMATION_TRANSPORT",
		"SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST",
		"SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE",
		"SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST",
		"SPARKCLAW_BROWSER_CDP_PROFILE_ID",
		"SPARKCLAW_BROWSER_CDP_CONNECT_TIMEOUT_MS",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return fmt.Errorf("%s is retired; remove it and use the SparkClaw Browser Bridge controller", name)
		}
	}
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
	if v := os.Getenv("SPARKCLAW_WEBCHAT_PROXY_TOKEN"); v != "" {
		cfg.Gateway.WebChatProxyToken = v
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
	if v := os.Getenv("SPARKCLAW_JINGSI_RUNTIME_V1_ENABLED"); v != "" {
		cfg.JingSiRuntime.Enabled = parseBool(v)
	}
	if v := os.Getenv("SPARKCLAW_JINGSI_RUNTIME_V1_STATE_DIR"); v != "" {
		cfg.JingSiRuntime.StateDir = v
	}
	if v := os.Getenv("SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN"); v != "" {
		cfg.JingSiRuntime.BearerToken = v
	}
	if v := os.Getenv("SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN_FILE"); v != "" {
		cfg.JingSiRuntime.BearerTokenFile = v
	}
	if v := os.Getenv("SPARKCLAW_JINGSI_RUNTIME_V1_MAX_CONCURRENT"); v != "" {
		if count, err := strconv.Atoi(v); err == nil {
			cfg.JingSiRuntime.MaxConcurrent = count
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
	if v := os.Getenv("SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET"); v != "" {
		cfg.Adapters.BrowserAutomation.PlaywrightExtension.ControllerSocket = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID"); v != "" {
		cfg.Adapters.BrowserAutomation.PlaywrightExtension.ProfileID = v
	}
	if v := os.Getenv("SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS"); v != "" {
		timeoutMS, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS must be an integer: %w", err)
		}
		cfg.Adapters.BrowserAutomation.PlaywrightExtension.ConnectTimeoutMS = timeoutMS
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
