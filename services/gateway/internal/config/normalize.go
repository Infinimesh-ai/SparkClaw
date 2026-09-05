package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
)

func normalizeJingSiRuntimeConfig(cfg *Config) error {
	value := &cfg.JingSiRuntime
	value.StateDir = strings.TrimSpace(value.StateDir)
	value.BearerTokenFile = strings.TrimSpace(value.BearerTokenFile)
	value.BearerToken = strings.TrimSpace(value.BearerToken)
	if value.StateDir == "" {
		value.StateDir = "./data/jingsi-runtime-v1"
	}
	absoluteStateDir, err := filepath.Abs(value.StateDir)
	if err != nil {
		return fmt.Errorf("resolve jingsi_runtime_v1.state_dir: %w", err)
	}
	value.StateDir = absoluteStateDir
	if value.MaxConcurrent == 0 {
		value.MaxConcurrent = 4
	}
	if value.MaxConcurrent < 1 || value.MaxConcurrent > 64 {
		return errors.New("jingsi_runtime_v1.max_concurrent must be between 1 and 64")
	}
	if value.BearerToken != "" && value.BearerTokenFile != "" {
		return errors.New("JingSi Runtime bearer token must use exactly one of environment or file")
	}
	if value.BearerTokenFile != "" {
		info, err := os.Lstat(value.BearerTokenFile)
		if err != nil {
			return fmt.Errorf("inspect JingSi Runtime bearer token file: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("JingSi Runtime bearer token file must be a regular non-symlink file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return errors.New("JingSi Runtime bearer token file must not be accessible by group or other users")
		}
		if info.Size() <= 0 || info.Size() > 4096 {
			return errors.New("JingSi Runtime bearer token file must contain at most 4096 bytes")
		}
		raw, err := os.ReadFile(value.BearerTokenFile)
		if err != nil {
			return fmt.Errorf("read JingSi Runtime bearer token file: %w", err)
		}
		value.BearerToken = strings.TrimSpace(string(raw))
	}
	if !value.Enabled {
		value.BearerToken = ""
		return nil
	}
	bindIP := net.ParseIP(strings.TrimSpace(cfg.Gateway.Bind))
	if bindIP == nil || !bindIP.IsLoopback() {
		return errors.New("jingsi_runtime_v1 requires gateway.bind to be a literal loopback IP")
	}
	if len(value.BearerToken) < 16 {
		return errors.New("JingSi Runtime bearer token must contain at least 16 characters")
	}
	return nil
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

// modelConfigWarnings reports non-mock model lanes that point at a remote
// host while no API key is configured. It is a warning rather than an
// error because private-network servers legitimately run without auth;
// Docker service names and loopback addresses count as local.
func modelConfigWarnings(model ModelConfig) []string {
	if model.Mock || model.APIKey != "" {
		return nil
	}
	warnings := []string{}
	for _, lane := range []struct {
		name    string
		profile ModelProfile
	}{
		{"fast", model.Fast}, {"deep", model.Deep}, {"embedding", model.Embedding}, {"guard", model.Guard},
	} {
		endpoint, err := url.Parse(strings.TrimSpace(lane.profile.BaseURL))
		if err != nil || endpoint.Host == "" || isLocalHTTPHost(endpoint.Hostname()) {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("model.%s.base_url %s is a remote endpoint but OPENAI_API_KEY is empty; requests will be sent without a bearer token", lane.name, endpoint.Redacted()))
	}
	return warnings
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
