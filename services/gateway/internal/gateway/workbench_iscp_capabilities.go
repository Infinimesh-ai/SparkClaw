package gateway

import (
	"context"
	"slices"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
)

type iscpDomainCapability struct {
	ID                string   `json:"id"`
	Supported         bool     `json:"supported"`
	Permitted         bool     `json:"permitted"`
	DependenciesReady bool     `json:"dependencies_ready"`
	Qualified         bool     `json:"qualified"`
	Enabled           bool     `json:"enabled"`
	Reason            string   `json:"reason,omitempty"`
	Operations        []string `json:"operations"`
}

func (a *iscpDomainAdapter) capabilities(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if !domainEmpty(request.Body) {
		return domainError(400, "invalid_input")
	}
	session, ok := iscpworkbench.SessionFromContext(ctx)
	if !ok {
		return domainError(401, "authenticated_session_required")
	}
	type family struct {
		id         string
		operations []string
		ready      bool
	}
	speechReady, speechStreaming := false, false
	if a.server.speech != nil && a.server.cfg.Speech.Enabled && a.server.cfg.Speech.Backend != "disabled" {
		status := a.server.speech.Status(ctx)
		speechReady = status.Ready
		speechStreaming = status.Ready && status.SupportsStreaming
	}
	mailSend := a.server.emailManagement != nil && a.server.emailManagement.ComposeCapabilities().Compose
	toolsReady := len(a.server.workbenchExecutionAuthorization(session).AllowedTools) > 0
	families := []family{
		{"tools", []string{iscpworkbench.OperationToolsList, iscpworkbench.OperationToolsInvoke}, toolsReady},
		{"files", []string{iscpworkbench.OperationTransferOpen, iscpworkbench.OperationTransferChunk, iscpworkbench.OperationTransferCommit, iscpworkbench.OperationTransferStatus, iscpworkbench.OperationTransferAbort, iscpworkbench.OperationObjectDescribe, iscpworkbench.OperationObjectRelease, iscpworkbench.OperationObjectRead, iscpworkbench.OperationExecutionInputPut, iscpworkbench.OperationExecutionFileGet}, true},
		{"events", []string{iscpworkbench.OperationEventsSnapshot, iscpworkbench.OperationEventsPull, iscpworkbench.OperationEventsAck}, true},
		{"mail_read", []string{iscpworkbench.OperationMailMailboxes, iscpworkbench.OperationMailSync, iscpworkbench.OperationMailMessage}, a.server.mailSync != nil && a.server.emailManagement != nil},
		{"mail_attachments", []string{iscpworkbench.OperationMailAttachment, iscpworkbench.OperationObjectRead}, a.server.emailManagement != nil},
		{"mail_send", []string{iscpworkbench.OperationMailDraftsList, iscpworkbench.OperationMailDraftsSave, iscpworkbench.OperationMailDraftsSend, iscpworkbench.OperationMailDraftsReconcile}, mailSend},
		{"browser", []string{iscpworkbench.OperationBrowserHostGrant, iscpworkbench.OperationBrowserHostRegister, iscpworkbench.OperationBrowserHostPoll, iscpworkbench.OperationBrowserHostReply, iscpworkbench.OperationBrowserHostHeartbeat, iscpworkbench.OperationBrowserHostClose, iscpworkbench.OperationBrowserHostRevoke, iscpworkbench.OperationBrowserReceipt, iscpworkbench.OperationBrowserReconcile}, true},
		{"speech_recording", []string{iscpworkbench.OperationSpeechStatus, iscpworkbench.OperationSpeechTranscribe, iscpworkbench.OperationTransferOpen, iscpworkbench.OperationTransferChunk, iscpworkbench.OperationTransferCommit, iscpworkbench.OperationTransferStatus, iscpworkbench.OperationTransferAbort, iscpworkbench.OperationObjectRelease}, speechReady},
		{"speech_realtime", []string{iscpworkbench.OperationSpeechSessionOpen, iscpworkbench.OperationSpeechSessionFrame, iscpworkbench.OperationSpeechSessionFinish, iscpworkbench.OperationSpeechSessionCancel, iscpworkbench.OperationSpeechSessionEvents}, speechStreaming},
		{"settings_owner", []string{iscpworkbench.OperationSettingsOwnerGet, iscpworkbench.OperationSettingsOwnerPatch}, true},
		{"settings_connectors", []string{iscpworkbench.OperationSettingsConnectorsList, iscpworkbench.OperationSettingsConnectorsPatch}, a.server.connectors != nil},
		{"settings_credentials", []string{iscpworkbench.OperationSettingsIntegrationsList, iscpworkbench.OperationSettingsCredentialsAdd, iscpworkbench.OperationSettingsCredentialsActivate, iscpworkbench.OperationSettingsCredentialsCheck, iscpworkbench.OperationSettingsCredentialsDelete}, a.server.integrations != nil},
		{"approvals", []string{iscpworkbench.OperationApprovalsList, iscpworkbench.OperationApprovalsGet, iscpworkbench.OperationApprovalsDecide}, true},
		{"notifications", []string{iscpworkbench.OperationNotificationsList, iscpworkbench.OperationNotificationsRead, iscpworkbench.OperationNotificationsReadAll}, true},
	}
	capabilities := make([]iscpDomainCapability, 0, len(families))
	operations := []string{}
	for _, family := range families {
		capability := iscpDomainCapability{ID: family.id, Operations: family.operations, Supported: true, Permitted: true, Qualified: true, DependenciesReady: family.ready}
		for _, name := range family.operations {
			spec, found := iscpworkbench.LookupOperation(name)
			if !found {
				capability.Supported = false
				continue
			}
			if !slices.Contains(session.Scopes, spec.Scope) {
				capability.Permitted = false
			}
			if !slices.Contains(a.config.QualifiedCapabilities, name) {
				capability.Qualified = false
			}
			operations = append(operations, name)
		}
		capability.Enabled = capability.Supported && capability.Permitted && capability.Qualified && capability.DependenciesReady
		switch {
		case !capability.Supported:
			capability.Reason = "capability_unavailable"
		case !capability.Permitted:
			capability.Reason = "permission_denied"
		case !capability.DependenciesReady:
			capability.Reason = "dependency_unavailable"
		case !capability.Qualified:
			capability.Reason = "qualification_required"
		}
		capabilities = append(capabilities, capability)
	}
	capabilities = append(capabilities, iscpDomainCapability{ID: "speech_playback", Supported: false, Reason: "provider_unsupported", Operations: []string{}})
	return domainJSON(200, map[string]any{"schema_version": 2, "profile": session.Profile, "session_id": session.SessionID, "deployment_id": a.config.Binding.DeploymentID, "revision": domainRevision(capabilities), "authorization_revision": session.GrantRevision, "expires_at": domainNow().Add(2 * time.Minute), "permissions": session.Scopes, "capabilities": capabilities, "operations": operations, "limits": a.capabilityLimits()})
}

func (a *iscpDomainAdapter) capabilityLimits() map[string]any {
	limits := iscpobjects.DefaultLimits()
	return map[string]any{
		"message_bytes": iscpworkbench.MaxMessageBytes, "notification_watermark_items": 500,
		"operation_receipt_retention": "until_manual_authorization_deletion", "operation_receipt_records": 65536,
		"chunk_bytes": iscpobjects.ChunkBytes, "transfer_window": iscpobjects.Window,
		"object_max_bytes": limits.MaxObjectBytes, "object_owner_bytes": limits.MaxOwnerBytes, "object_total_bytes": limits.MaxTotalBytes,
		"active_transfers": limits.MaxTransfers, "object_records": limits.MaxRecords,
		"upload_ttl_seconds": int64(limits.UploadTTL / time.Second), "object_retention_seconds": int64(limits.Retention / time.Second),
		"execution_retention_seconds": int64(execution.ResultRetention / time.Second), "execution_task_bytes": execution.TaskBytes, "execution_owner_bytes": execution.OwnerBytes,
		"purpose_bytes":        map[string]int64{"context": 1 << 20, "request_body": 8 << 20, "execution_request": execution.ContextBytes, "execution_input": execution.ResultBytes, "execution_result": execution.ResultBytes, "file": limits.MaxObjectBytes, "mail_attachment": limits.MaxObjectBytes, "event_snapshot": 8 << 20, "browser_capture": 8 << 20, "speech_recording": min(25<<20, a.server.cfg.Speech.MaxUploadBytes), "speech_audio": min(25<<20, a.server.cfg.Speech.MaxUploadBytes)},
		"speech_frame_samples": speech.RealtimeFrameSamples, "speech_frame_ms": speech.RealtimeFrameMS, "speech_unacked_ms": speech.RealtimeMaxUnackedMS,
		"browser_capture_effective_bytes": 64 << 10, "browser_reply_json_bytes": 96 << 10,
		"event_unacked_packets": 1, "event_packet_items": 100, "event_cursor_ttl_seconds": 86400,
	}
}
