package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, err
		}
		if err := rejectLegacyModelCapacity(raw); err != nil {
			return Config{}, err
		}
		if err := rejectLegacyBrowserAutomation(raw); err != nil {
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
	if token := cfg.Gateway.WebChatProxyToken; token != "" && !webChatProxyTokenPattern.MatchString(token) {
		return Config{}, errors.New("Gateway WebChat proxy token must be 43-128 base64url characters")
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
	if err := normalizeJingSiRuntimeConfig(&cfg); err != nil {
		return Config{}, err
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
	if cfg.Adapters.BrowserAutomation.TimeoutMS <= 0 {
		cfg.Adapters.BrowserAutomation.TimeoutMS = 30000
	}
	if cfg.Adapters.BrowserAutomation.StartupTimeoutMS <= 0 {
		cfg.Adapters.BrowserAutomation.StartupTimeoutMS = 10000
	}
	if cfg.Tools.BrowserAutomation.Provider == "" {
		cfg.Tools.BrowserAutomation.Provider = BrowserAutomationProvider
	}
	if cfg.Tools.BrowserAutomation.Provider != BrowserAutomationProvider {
		return Config{}, fmt.Errorf("tools.browserAutomation.provider must be %q", BrowserAutomationProvider)
	}
	// The startup timeout bounds browser session acquisition, which the
	// controller caps at 30 seconds of waiting.
	if cfg.Adapters.BrowserAutomation.StartupTimeoutMS < 500 || cfg.Adapters.BrowserAutomation.StartupTimeoutMS > 30000 {
		return Config{}, errors.New("adapters.browserAutomation.startupTimeoutMs must be between 500 and 30000")
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
	extension := &cfg.Adapters.BrowserAutomation.PlaywrightExtension
	extension.ControllerSocket = strings.TrimSpace(extension.ControllerSocket)
	if extension.ControllerSocket == "" {
		extension.ControllerSocket = "/run/sparkclaw/browser-controller/controller.sock"
	}
	if !filepath.IsAbs(extension.ControllerSocket) {
		return Config{}, errors.New("adapters.browserAutomation.playwrightExtension.controllerSocket must be absolute")
	}
	extension.ControllerSocket = filepath.Clean(extension.ControllerSocket)
	extension.ProfileID = strings.TrimSpace(extension.ProfileID)
	if extension.ProfileID == "" {
		extension.ProfileID = "default"
	}
	if extension.ProfileID != "default" {
		return Config{}, errors.New("adapters.browserAutomation.playwrightExtension.profileID must be default")
	}
	if extension.ConnectTimeoutMS <= 0 {
		extension.ConnectTimeoutMS = 20000
	}
	if extension.ConnectTimeoutMS < 1000 || extension.ConnectTimeoutMS > 120000 {
		return Config{}, errors.New("adapters.browserAutomation.playwrightExtension.connectTimeoutMs must be between 1000 and 120000")
	}
	if err := normalizeRuntimeLimits(&cfg.Runtime); err != nil {
		return Config{}, err
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
	cfg.Warnings = append(cfg.Warnings, modelConfigWarnings(cfg.Model)...)
	cfg.Warnings = append(cfg.Warnings, gatewayAuthWarnings(cfg.Gateway)...)
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

func rejectLegacyBrowserAutomation(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	var adapters map[string]json.RawMessage
	if value := root["adapters"]; len(value) > 0 {
		if err := json.Unmarshal(value, &adapters); err != nil {
			return err
		}
	}
	var browser map[string]json.RawMessage
	if value := adapters["browserAutomation"]; len(value) > 0 {
		if err := json.Unmarshal(value, &browser); err != nil {
			return err
		}
	}
	legacy := []string{
		"mode", "chromiumExecutable", "profileDir", "headed", "chromiumArgs",
		"executablePath", "display", "xauthority", "daemonIdleTimeoutMs",
	}
	for _, key := range legacy {
		if _, exists := browser[key]; exists {
			return fmt.Errorf(
				"adapters.browserAutomation.%s is retired; migrate to the sole playwrightExtension controller-socket configuration",
				key,
			)
		}
	}
	return nil
}

// ResolveDefault resolves the default model-capacity profile from an
// explicit catalog path without applying file or environment overrides.
// Tests use it through configtest; runtime entrypoints should use Load.
func ResolveDefault(catalogPath string) (Config, error) {
	cfg := Default()
	cfg.Model.CapacityCatalog = strings.TrimSpace(catalogPath)
	if cfg.Model.CapacityCatalog == "" {
		return Config{}, errors.New("ResolveDefault requires a model capacity catalog path")
	}
	if err := applySelectedModelCapacity(&cfg, ""); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
