package gateway

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpintegration"
)

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	toolsConfig, err := s.publicToolsConfig(r.Context(), principal.OwnerID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"gateway":             publicGatewayConfig(s.cfg.Gateway),
		"model":               publicModelConfig(s.cfg.Model),
		"speech":              publicSpeechConfig(s.cfg.Speech),
		"iscp_pairing":        s.iscpPairing.Status(r.Context()),
		"mcp_servers":         publicMCPServersConfig(s.cfg.MCPServers),
		"workspaces":          s.cfg.Workspaces,
		"security":            s.cfg.Security,
		"sandbox":             s.cfg.Sandbox,
		"storage":             publicStorageConfig(s.cfg.Storage),
		"state":               publicStateConfig(s.cfg.State),
		"adapters":            publicAdapterConfig(s.cfg.Adapters, s.tools.DocumentOCRReadiness()),
		"tools":               toolsConfig,
		"mcp_server_statuses": s.mcpServerStatuses(),
		"memory":              s.cfg.Memory,
		"runtime":             s.cfg.Runtime,
		"tool_policy":         toolPolicySummary(s.cfg.Security, s.tools.Definitions()),
	})
}

func (s *Server) listMCPServers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": s.mcpServerStatuses()})
}

func (s *Server) refreshMCPServer(w http.ResponseWriter, r *http.Request) {
	if s.mcp == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP integration is unavailable"))
		return
	}
	status, err := s.mcp.Refresh(r.Context(), strings.TrimSpace(r.PathValue("name")))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"server": status, "error": status.ErrorCode})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": status})
}

func (s *Server) mcpServerStatuses() []mcpintegration.Status {
	if s.mcp == nil {
		return []mcpintegration.Status{}
	}
	return s.mcp.ListStatus()
}

func modelMode(cfg config.Config) string {
	if cfg.Model.Mock {
		return "mock"
	}
	return "external"
}

func artifactBackend(cfg config.Config) string {
	return strings.ToLower(strings.TrimSpace(cfg.Storage.ArtifactBackend))
}

func stateDSNStatus(cfg config.Config) string {
	if cfg.State.Backend != "postgres" {
		return ""
	}
	if strings.TrimSpace(cfg.State.DSN) == "" {
		return "missing"
	}
	return "configured"
}

func publicGatewayConfig(cfg config.GatewayConfig) config.GatewayConfig {
	cfg.APIToken = ""
	return cfg
}

func publicMCPServersConfig(servers map[string]config.MCPServerConfig) map[string]any {
	out := map[string]any{}
	for name, server := range servers {
		projected := map[string]any{
			"configured":           true,
			"transport":            server.Transport,
			"namespace":            server.Namespace,
			"expected_server_name": server.ExpectedServerName,
			"protocol_version":     server.ProtocolVersion,
			"allow_private_http":   server.AllowPrivateHTTP,
		}
		if name == config.LocalMindMCPServerKey {
			projected["task_contract"] = "localmind.task.v1"
		} else {
			projected["allow_mutations"] = server.AllowMutations
			projected["tool_allow"] = append([]string(nil), server.ToolAllow...)
			projected["tool_deny"] = append([]string(nil), server.ToolDeny...)
		}
		out[name] = projected
	}
	return out
}

func publicModelConfig(cfg config.ModelConfig) map[string]any {
	return map[string]any{
		"capacity_profile":     cfg.CapacityProfile,
		"mock":                 cfg.Mock,
		"http_timeout_seconds": cfg.HTTPTimeoutSeconds,
		"disable_thinking":     cfg.DisableThinking,
		"fast":                 publicModelProfile(cfg.Fast),
		"deep":                 publicModelProfile(cfg.Deep),
		"embedding":            publicModelProfile(cfg.Embedding),
		"guard":                publicModelProfile(cfg.Guard),
	}
}

func publicModelProfile(profile config.ModelProfile) map[string]any {
	return map[string]any{
		"name":                    profile.Name,
		"base_url":                profile.BaseURL,
		"model":                   profile.Model,
		"capacity_physical_model": profile.CapacityPhysicalModel,
		"context_tokens":          profile.ContextTokens,
		"output_budgets":          profile.OutputBudgets,
		"mtp":                     profile.MTP,
	}
}

func publicSpeechConfig(cfg config.SpeechConfig) map[string]any {
	return map[string]any{
		"enabled":           cfg.Enabled,
		"backend":           cfg.Backend,
		"model":             cfg.Model,
		"default_language":  cfg.DefaultLanguage,
		"max_audio_seconds": cfg.MaxAudioSeconds,
		"max_upload_bytes":  cfg.MaxUploadBytes,
		"retain_audio":      false,
	}
}

func publicStorageConfig(cfg config.StorageConfig) map[string]any {
	return map[string]any{
		"trace_dir":        cfg.TraceDir,
		"log_dir":          cfg.LogDir,
		"artifact_backend": cfg.ArtifactBackend,
		"artifact_dir":     cfg.ArtifactDir,
		"artifact_bucket":  cfg.ArtifactBucket,
		"s3_endpoint":      cfg.S3Endpoint,
		"s3_region":        cfg.S3Region,
		"s3_access_key":    configuredStatus(cfg.S3AccessKey),
		"s3_secret_key":    configuredStatus(cfg.S3SecretKey),
	}
}

func publicStateConfig(cfg config.StateConfig) map[string]any {
	return map[string]any{
		"backend":                     cfg.Backend,
		"path":                        cfg.Path,
		"dsn":                         configuredStatus(cfg.DSN),
		"startup_timeout_seconds":     cfg.StartupTimeoutSeconds,
		"read_timeout_seconds":        cfg.ReadTimeoutSeconds,
		"write_timeout_seconds":       cfg.WriteTimeoutSeconds,
		"transaction_timeout_seconds": cfg.TransactionTimeoutSeconds,
		"encrypt_at_rest":             cfg.EncryptAtRest,
		"encryption_key":              stateEncryptionStatus(cfg.EncryptionKey),
		"encryption_key_file":         stateEncryptionStatus(cfg.EncryptionKeyFile),
	}
}

func publicAdapterConfig(cfg config.AdapterConfig, ocrReadiness documentocr.RuntimeReadiness) map[string]any {
	return map[string]any{
		"browserAutomation": map[string]any{
			"timeout_ms":         cfg.BrowserAutomation.TimeoutMS,
			"startup_timeout_ms": cfg.BrowserAutomation.StartupTimeoutMS,
			"browser_bridge": map[string]any{
				"profile_id":         cfg.BrowserAutomation.PlaywrightExtension.ProfileID,
				"connect_timeout_ms": cfg.BrowserAutomation.PlaywrightExtension.ConnectTimeoutMS,
			},
		},
		"emailAutomation": map[string]any{
			"script_dir": cfg.EmailAutomation.ScriptDir,
		},
		"documentOCR": map[string]any{
			"configured_enabled":    ocrReadiness.ConfiguredEnabled,
			"adapter_ready":         ocrReadiness.AdapterReady,
			"runtime_status":        ocrReadiness.RuntimeStatus,
			"reason_code":           ocrReadiness.ReasonCode,
			"provider":              cfg.DocumentOCR.Provider,
			"model":                 cfg.DocumentOCR.Model,
			"last_call_status":      ocrReadiness.LastCallStatus,
			"last_call_reason_code": ocrReadiness.LastCallReason,
			"last_call_at":          ocrReadiness.LastCallAt,
			"timeout_seconds":       cfg.DocumentOCR.TimeoutSeconds,
			"max_upload_bytes":      cfg.DocumentOCR.MaxUploadBytes,
			"max_output_bytes":      cfg.DocumentOCR.MaxOutputBytes,
			"max_tokens":            cfg.DocumentOCR.MaxTokens,
			"max_concurrency":       cfg.DocumentOCR.MaxConcurrency,
			"max_pending":           cfg.DocumentOCR.MaxPending,
		},
		"pptxVisualQA": map[string]any{
			"phase":                       cfg.PPTXVisualQA.Phase,
			"repair_qualified_classes":    cfg.PPTXVisualQA.RepairQualifiedClasses,
			"repair_qualified_operations": cfg.PPTXVisualQA.RepairQualifiedOperations,
			"blocking_qualified_classes":  cfg.PPTXVisualQA.BlockingQualifiedClasses,
			"max_repair_attempts":         cfg.PPTXVisualQA.MaxRepairAttempts,
			"renderer":                    "gotenberg-libreoffice",
			"rasterizer":                  "pypdfium2",
			"timeout_seconds":             cfg.PPTXVisualQA.TimeoutSeconds,
			"max_input_bytes":             cfg.PPTXVisualQA.MaxInputBytes,
			"max_pdf_bytes":               cfg.PPTXVisualQA.MaxPDFBytes,
			"max_pages":                   cfg.PPTXVisualQA.MaxPages,
			"max_changed_pages":           cfg.PPTXVisualQA.MaxChangedPages,
			"raster_scale":                cfg.PPTXVisualQA.RasterScale,
			"max_page_pixels":             cfg.PPTXVisualQA.MaxPagePixels,
			"max_png_bytes":               cfg.PPTXVisualQA.MaxPNGBytes,
			"diagnostic_tolerance_milli":  cfg.PPTXVisualQA.DiagnosticToleranceMilli,
			"readiness_ttl_seconds":       cfg.PPTXVisualQA.ReadinessTTLSeconds,
		},
	}
}

func (s *Server) publicToolsConfig(ctx context.Context, ownerID string) (map[string]any, error) {
	cfg := s.cfg
	connectorStatuses := map[string]app.ConnectorStatus{}
	if s.connectors != nil {
		statuses, err := s.connectors.ListStatus(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		for _, status := range statuses {
			connectorStatuses[status.Channel] = status
		}
	}
	notificationChannels := map[string]any{}
	for name, channel := range cfg.Tools.Notifications.Channels {
		publicChannel := map[string]any{
			"enabled":          channel.Enabled,
			"provider":         channel.Provider,
			"base_url":         channel.BaseURL,
			"token_configured": strings.TrimSpace(channel.Token) != "",
			"recipient_set":    strings.TrimSpace(channel.Recipient) != "",
		}
		publicChannel["available"] = false
		publicChannel["operator_enabled"] = channel.Enabled
		publicChannel["binding_status"] = ""
		publicChannel["startable"] = false
		publicChannel["disabled_reason"] = binding.CodeConnectorUnavailable
		if status, ok := connectorStatuses[name]; ok {
			publicChannel["enabled"] = status.Enabled
			publicChannel["available"] = status.Available
			publicChannel["binding_status"] = status.BindingStatus
			publicChannel["startable"] = status.BindingStartable
			publicChannel["disabled_reason"] = status.DisabledReason
		}
		notificationChannels[name] = publicChannel
	}
	return map[string]any{
		"web": map[string]any{
			"search": map[string]any{
				"enabled":    cfg.Tools.Web.Search.Enabled,
				"provider":   cfg.Tools.Web.Search.Provider,
				"configured": webSearchConfigured(cfg),
			},
		},
		"browserAutomation": map[string]any{
			"enabled":  cfg.Tools.BrowserAutomation.Enabled,
			"provider": cfg.Tools.BrowserAutomation.Provider,
			"profile":  cfg.Tools.BrowserAutomation.Profile,
		},
		"reminders": map[string]any{
			"enabled":         cfg.Tools.Reminders.Enabled,
			"default_channel": cfg.Tools.Reminders.DefaultChannel,
		},
		"notifications": map[string]any{
			"channels": notificationChannels,
		},
	}, nil
}

func webSearchConfigured(cfg config.Config) bool {
	switch strings.ToLower(strings.TrimSpace(cfg.Tools.Web.Search.Provider)) {
	case "", "infinimesh-info":
		return cfg.Plugins.Entries.InfinimeshInfo.Config.Configured()
	default:
		return false
	}
}

func publicNotificationBindings(bindings []app.NotificationBinding) []map[string]any {
	out := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, publicNotificationBinding(binding))
	}
	return out
}

func publicNotificationBinding(binding app.NotificationBinding, includeActivation ...bool) map[string]any {
	qrCodeURL := binding.QRCodeURL
	if binding.Status == app.NotificationBindingWaitingConfirm && (len(includeActivation) == 0 || !includeActivation[0]) {
		qrCodeURL = ""
	}
	return map[string]any{
		"id":                  binding.ID,
		"owner_id":            binding.OwnerID,
		"channel":             binding.Channel,
		"provider":            binding.Provider,
		"status":              binding.Status,
		"display_name":        binding.DisplayName,
		"external_user_id":    redactExternalID(binding.ExternalUserID),
		"account_id":          redactExternalID(binding.AccountID),
		"credential_ref":      configuredStatus(binding.CredentialRef),
		"context_token":       configuredStatus(binding.ContextToken),
		"base_url":            binding.BaseURL,
		"qr_code_url":         qrCodeURL,
		"qr_code_image":       binding.QRCodeImage,
		"default_for_channel": binding.DefaultForChannel,
		"scopes":              binding.Scopes,
		"created_at":          binding.CreatedAt,
		"updated_at":          binding.UpdatedAt,
		"expires_at":          binding.ExpiresAt,
		"revoked_at":          binding.RevokedAt,
		"last_error":          binding.LastError,
	}
}

func redactExternalID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 6 {
		return value
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-2:])
}

func configuredStatus(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "configured"
}

func firstEndpointActor(binding app.NotificationBinding) string {
	if actorID := strings.TrimSpace(binding.ActorID); actorID != "" {
		return actorID
	}
	return strings.TrimSpace(binding.OwnerID)
}

func stateEncryptionStatus(value string) string {
	if strings.TrimSpace(value) == "" {
		return "missing"
	}
	return "configured"
}

func publicRateLimitConfig(cfg config.RateLimitConfig) map[string]any {
	return map[string]any{
		"enabled":             cfg.Enabled,
		"requests_per_minute": cfg.RequestsPerMinute,
		"burst":               cfg.Burst,
	}
}

func toolPolicySummary(security config.SecurityConfig, defs []app.ToolDefinition) map[string]any {
	riskCounts := map[string]int{}
	approvalRequired := []string{}
	for _, def := range defs {
		riskCounts[string(def.Risk)]++
		if def.RequiresApproval {
			approvalRequired = append(approvalRequired, def.Name)
		}
	}
	slices.Sort(approvalRequired)
	return map[string]any{
		"policy_path":                           security.ToolPolicyPath,
		"external_content_untrusted":            security.ExternalContentUntrusted,
		"approval_required_for_dangerous_tools": security.ApprovalRequiredForDangerousTools,
		"sandbox_required_for_mutating_tools":   security.SandboxRequiredForMutatingTools,
		"dangerous_tools_deep_verification":     security.DangerousToolsRequireDeepVerification,
		"definition_count":                      len(defs),
		"risk_counts":                           riskCounts,
		"definition_approval_required_tools":    approvalRequired,
		"configured_approval_required_tools":    security.ApprovalRequiredTools,
		"denied_tools":                          security.DeniedTools,
		"browser_read_allow_hosts":              security.BrowserReadAllowHosts,
	}
}
