package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (app.Session, error) {
	var session app.Session
	err := row.Scan(&session.ID, &session.OwnerID, &session.WorkspaceRoot, &session.Title, &session.Source, &session.Hidden, &session.CreatedAt, &session.UpdatedAt)
	return session, err
}

func scanClient(row scanner) (app.Client, error) {
	var client app.Client
	err := row.Scan(&client.ID, &client.OwnerID, &client.ActorID, &client.Name, &client.TokenHash, &client.CreatedAt, &client.LastSeenAt, &client.RevokedAt)
	return client, err
}

func scanOwnerProfile(row scanner) (app.OwnerProfile, error) {
	var profile app.OwnerProfile
	var preferences []byte
	err := row.Scan(&profile.ID, &profile.Source, &profile.ExternalRef, &profile.WorkspaceRoot,
		&profile.DefaultChannel, &profile.DefaultBindingID, &profile.DisplayName, &profile.Email,
		&preferences, &profile.CreatedAt, &profile.UpdatedAt)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	profile.Preferences = map[string]string{}
	if err := json.Unmarshal(preferences, &profile.Preferences); err != nil {
		return app.OwnerProfile{}, errors.Join(errOwnerPreferencesDecode, err)
	}
	if profile.Preferences == nil {
		profile.Preferences = map[string]string{}
	}
	return profile, nil
}

var errOwnerPreferencesDecode = errors.New("owner preferences decode failed")

func classifyPostgresOwnerReadError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errOwnerPreferencesDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}

func scanPairingCode(row scanner) (app.PairingCode, error) {
	var code app.PairingCode
	err := row.Scan(&code.ID, &code.CodeHash, &code.Status, &code.ExpiresAt, &code.CreatedAt, &code.ClaimedAt, &code.ClientID)
	return code, err
}

func scanMessage(row scanner) (app.Message, error) {
	var message app.Message
	var attachments, requestedMedia []byte
	err := row.Scan(&message.ID, &message.SessionID, &message.RunID, &message.Role, &message.Content, &attachments, &requestedMedia, &message.CreatedAt)
	if err != nil {
		return app.Message{}, err
	}
	if len(attachments) > 0 {
		if err := json.Unmarshal(attachments, &message.Attachments); err != nil {
			return app.Message{}, fmt.Errorf("%w: attachments: %v", errMessageJSONDecode, err)
		}
	}
	if len(requestedMedia) > 0 {
		if err := json.Unmarshal(requestedMedia, &message.RequestedMedia); err != nil {
			return app.Message{}, fmt.Errorf("%w: requested media: %v", errMessageJSONDecode, err)
		}
	}
	return cloneMessage(message), nil
}

func scanExternalChatSession(row scanner) (app.ExternalChatSession, error) {
	var session app.ExternalChatSession
	err := row.Scan(
		&session.ID,
		&session.OwnerID,
		&session.AuthorizedOwnerID,
		&session.AuthorizedActorID,
		&session.WorkspaceRoot,
		&session.BindingID,
		&session.Channel,
		&session.Provider,
		&session.ExternalUserID,
		&session.ExternalChatID,
		&session.ExternalThreadID,
		&session.DisplayName,
		&session.LinkedSessionID,
		&session.Status,
		&session.ProviderCursor,
		&session.LastContextToken,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	return session, err
}

func scanExternalChatMessage(row scanner) (app.ExternalChatMessage, error) {
	var message app.ExternalChatMessage
	err := row.Scan(
		&message.ID,
		&message.ChatSessionID,
		&message.BindingID,
		&message.Channel,
		&message.Direction,
		&message.Role,
		&message.ExternalMessageID,
		&message.Content,
		&message.ContextToken,
		&message.LinkedRunID,
		&message.Status,
		&message.Error,
		&message.PendingReplyKind,
		&message.PendingReply,
		&message.DispatchAttempts,
		&message.CreatedAt,
		&message.UpdatedAt,
	)
	return message, err
}

func scanChannelInboxUpdate(row scanner) (app.ChannelInboxUpdate, error) {
	var update app.ChannelInboxUpdate
	var payload []byte
	err := row.Scan(
		&update.ID,
		&update.BindingID,
		&update.Channel,
		&update.ExternalID,
		&update.ChatKey,
		&payload,
		&update.Status,
		&update.Attempts,
		&update.AvailableAt,
		&update.LastError,
		&update.CreatedAt,
		&update.UpdatedAt,
	)
	if err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	if !json.Valid(payload) {
		return app.ChannelInboxUpdate{}, errors.Join(errChannelInboxPayloadDecode, errors.New("persisted inbox payload is not valid JSON"))
	}
	if strings.TrimSpace(string(payload)) != "null" {
		update.Payload = append([]byte(nil), payload...)
	}
	return update, nil
}

func scanPassiveNotification(row scanner) (app.PassiveNotification, error) {
	var notification app.PassiveNotification
	err := row.Scan(
		&notification.ID,
		&notification.OwnerID,
		&notification.EndpointID,
		&notification.IdempotencyKey,
		&notification.Fingerprint,
		&notification.NotificationID,
		&notification.Source,
		&notification.Kind,
		&notification.DeepLink,
		&notification.OccurredAt,
		&notification.ReadAt,
		&notification.CreatedAt,
		&notification.UpdatedAt,
	)
	return notification, err
}

func scanPassiveNotificationReadResult(row scanner) (app.PassiveNotification, bool, error) {
	var notification app.PassiveNotification
	var changed bool
	err := row.Scan(
		&notification.ID,
		&notification.OwnerID,
		&notification.EndpointID,
		&notification.IdempotencyKey,
		&notification.Fingerprint,
		&notification.NotificationID,
		&notification.Source,
		&notification.Kind,
		&notification.DeepLink,
		&notification.OccurredAt,
		&notification.ReadAt,
		&notification.CreatedAt,
		&notification.UpdatedAt,
		&changed,
	)
	return notification, changed, err
}

func scanRunFeedback(row scanner) (app.RunFeedback, error) {
	var feedback app.RunFeedback
	err := row.Scan(
		&feedback.ID,
		&feedback.SessionID,
		&feedback.RunID,
		&feedback.MessageID,
		&feedback.Rating,
		&feedback.Note,
		&feedback.Correction,
		&feedback.CreatedAt,
		&feedback.UpdatedAt,
	)
	return feedback, err
}

func scanRun(row scanner) (app.AgentRun, error) {
	var run app.AgentRun
	var risk string
	var workflowState []byte
	var messageContext []byte
	err := row.Scan(&run.ID, &run.SessionID, &run.State, &run.ModelLane, &risk, &run.StartedAt, &run.CompletedAt, &run.Summary, &workflowState, &messageContext)
	if err != nil {
		return app.AgentRun{}, err
	}
	run.Risk = app.RiskLevel(risk)
	if len(workflowState) > 0 {
		var workflow app.WorkflowState
		if err := json.Unmarshal(workflowState, &workflow); err != nil {
			return app.AgentRun{}, fmt.Errorf("%w: workflow state: %v", errRunJSONDecode, err)
		}
		run.Workflow = &workflow
	}
	if len(messageContext) > 0 {
		var context app.MessageRunContext
		if err := json.Unmarshal(messageContext, &context); err != nil {
			return app.AgentRun{}, fmt.Errorf("%w: message context: %v", errRunJSONDecode, err)
		}
		run.MessageContext = &context
	}
	return run, nil
}

func scanModelCall(row scanner) (app.ModelCall, error) {
	var call app.ModelCall
	err := row.Scan(
		&call.ID,
		&call.SessionID,
		&call.RunID,
		&call.Lane,
		&call.Profile,
		&call.Model,
		&call.Operation,
		&call.Mock,
		&call.Fallback,
		&call.Status,
		&call.PromptTokens,
		&call.ResponseTokens,
		&call.TotalTokens,
		&call.LatencyMS,
		&call.Error,
		&call.StartedAt,
		&call.CompletedAt,
	)
	return call, err
}

func scanToolCall(row scanner) (app.ToolCall, error) {
	var call app.ToolCall
	var risk string
	var status string
	var args []byte
	var result []byte
	var policyContext []byte
	err := row.Scan(&call.ID, &call.SessionID, &call.RunID, &call.WorkflowID, &call.WorkflowNodeID, &call.ScopeRevision, &call.Capability,
		&call.Tool, &risk, &status, &args, &result, &call.Error, &call.ErrorCode, &call.ApprovalID, &call.StartedAt, &call.CompletedAt, &call.ObservationRef, &call.ObservationSummary, &policyContext)
	if err != nil {
		return app.ToolCall{}, err
	}
	call.Risk = app.RiskLevel(risk)
	call.Status = app.ToolCallStatus(status)
	call.Arguments = map[string]any{}
	if err := json.Unmarshal(args, &call.Arguments); err != nil {
		return app.ToolCall{}, fmt.Errorf("%w: tool arguments: %v", errRunJSONDecode, err)
	}
	if len(result) > 0 && string(result) != "null" {
		if err := json.Unmarshal(result, &call.Result); err != nil {
			return app.ToolCall{}, fmt.Errorf("%w: tool result: %v", errRunJSONDecode, err)
		}
	}
	if len(policyContext) > 0 && string(policyContext) != "null" {
		call.PolicyContext = &app.PolicyExecutionContext{}
		if err := json.Unmarshal(policyContext, call.PolicyContext); err != nil {
			return app.ToolCall{}, fmt.Errorf("%w: tool policy context: %v", errRunJSONDecode, err)
		}
	}
	return call, nil
}

func scanDocumentRecord(row scanner) (app.DocumentRecord, error) {
	var record app.DocumentRecord
	err := row.Scan(
		&record.ID,
		&record.OwnerID,
		&record.SessionID,
		&record.GovernedPath,
		&record.Name,
		&record.ContentType,
		&record.Format,
		&record.SizeBytes,
		&record.SHA256,
		&record.Status,
		&record.Source,
		&record.SourceMessageID,
		&record.SourceRunID,
		&record.SourceToolCallID,
		&record.ParentDocumentID,
		&record.LastActivity,
		&record.LastActivityID,
		&record.LastActivityAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	return normalizePersistedDocumentRecord(record), nil
}

func scanApproval(row scanner) (app.Approval, error) {
	var approval app.Approval
	var source string
	var risk string
	var status string
	var externalContext []byte
	var resources []byte
	var args []byte
	var policyContext []byte
	var presentation []byte
	err := row.Scan(&approval.ID, &source, &approval.ExternalID, &externalContext,
		&approval.SessionID, &approval.RunID, &approval.ToolCallID, &approval.Tool, &risk,
		&status, &approval.Summary, &approval.Reason, &resources, &args,
		&approval.CreatedAt, &approval.ResolvedAt, &approval.ResolutionNote, &policyContext, &presentation)
	if err != nil {
		return app.Approval{}, err
	}
	approval.Source = app.ApprovalSource(source)
	approval.Risk = app.RiskLevel(risk)
	approval.Status = app.ApprovalStatus(status)
	if len(externalContext) > 0 && string(externalContext) != "null" {
		approval.ExternalContext = &app.ExternalApprovalContext{}
		if err := json.Unmarshal(externalContext, approval.ExternalContext); err != nil {
			return app.Approval{}, fmt.Errorf("%w: external context: %v", errApprovalJSONDecode, err)
		}
	}
	approval.Resources = []string{}
	if err := json.Unmarshal(resources, &approval.Resources); err != nil {
		return app.Approval{}, fmt.Errorf("%w: resources: %v", errApprovalJSONDecode, err)
	}
	approval.Arguments = map[string]any{}
	if err := json.Unmarshal(args, &approval.Arguments); err != nil {
		return app.Approval{}, fmt.Errorf("%w: arguments: %v", errApprovalJSONDecode, err)
	}
	if len(policyContext) > 0 && string(policyContext) != "null" {
		approval.PolicyContext = &app.PolicyExecutionContext{}
		if err := json.Unmarshal(policyContext, approval.PolicyContext); err != nil {
			return app.Approval{}, fmt.Errorf("%w: policy context: %v", errApprovalJSONDecode, err)
		}
	}
	if len(presentation) > 0 && string(presentation) != "null" {
		approval.Presentation = &app.ApprovalPresentation{}
		if err := json.Unmarshal(presentation, approval.Presentation); err != nil {
			return app.Approval{}, fmt.Errorf("%w: presentation: %v", errApprovalJSONDecode, err)
		}
	}
	return normalizePersistedApproval(approval)
}

func scanReminder(row scanner) (app.Reminder, error) {
	var reminder app.Reminder
	var scheduleSpec []byte
	err := row.Scan(&reminder.ID, &reminder.SessionID, &reminder.RunID, &reminder.Text, &reminder.TextSummary,
		&reminder.DueTime, &reminder.Timezone, &reminder.Channel, &reminder.Recipient, &reminder.RecipientBinding,
		&reminder.BindingID, &reminder.CredentialRef, &reminder.BaseURL, &reminder.Recurrence,
		&reminder.DedupeKey, &reminder.Status, &reminder.LastDeliveryID, &reminder.LastError,
		&reminder.CreatedAt, &reminder.UpdatedAt, &reminder.SentAt, &reminder.CanceledAt, &reminder.DeliveryAttempt, &scheduleSpec)
	if err != nil {
		return app.Reminder{}, err
	}
	if len(scheduleSpec) > 0 && string(scheduleSpec) != "null" {
		var spec app.ScheduleSpec
		if err := json.Unmarshal(scheduleSpec, &spec); err != nil {
			return app.Reminder{}, errors.Join(errReminderScheduleSpecJSONDecode, err)
		}
		reminder.ScheduleSpec = &spec
	}
	return reminder, nil
}

func scanReminderDelivery(row scanner) (app.ReminderDelivery, error) {
	var delivery app.ReminderDelivery
	var sentAt *time.Time
	err := row.Scan(&delivery.ID, &delivery.ReminderID, &delivery.Channel, &delivery.Provider, &delivery.Recipient,
		&delivery.Status, &delivery.ProviderStatus, &delivery.Error, &delivery.RetryState, &delivery.Attempt,
		&sentAt, &delivery.CreatedAt)
	if sentAt != nil {
		delivery.SentAt = *sentAt
	}
	return delivery, err
}

func scanConnectorSetting(row scanner) (app.ConnectorSetting, error) {
	var setting app.ConnectorSetting
	err := row.Scan(&setting.OwnerID, &setting.Channel, &setting.Enabled, &setting.ISCPEnabled, &setting.LANAccessEnabled, &setting.Version, &setting.UpdatedBy, &setting.UpdatedAt)
	return setting, err
}

func scanNotificationBinding(row scanner) (app.NotificationBinding, error) {
	var binding app.NotificationBinding
	var scopes []byte
	err := row.Scan(&binding.ID, &binding.OwnerID, &binding.ActorID, &binding.Channel, &binding.Provider, &binding.Status,
		&binding.DisplayName, &binding.ExternalUserID, &binding.ExternalChatID, &binding.ExternalThreadID, &binding.AccountID, &binding.CredentialRef,
		&binding.BaseURL, &binding.ProviderSessionID, &binding.ProviderState, &binding.ContextToken,
		&binding.ProviderCursor, &binding.QRCodeURL, &binding.QRCodeImage, &binding.DefaultForChannel,
		&scopes, &binding.CreatedAt, &binding.UpdatedAt, &binding.ExpiresAt, &binding.RevokedAt,
		&binding.LastError, &binding.Version, &binding.CredentialKind)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	binding.Scopes = []string{}
	if err := json.Unmarshal(scopes, &binding.Scopes); err != nil {
		return app.NotificationBinding{}, errors.Join(errNotificationBindingScopesDecode, err)
	}
	return binding, nil
}

func scanCredentialSecret(row scanner) (app.CredentialSecret, error) {
	var secret app.CredentialSecret
	err := row.Scan(&secret.Ref, &secret.Kind, &secret.Value, &secret.CreatedAt, &secret.UpdatedAt)
	return secret, err
}

func scanBrowserAuthRecord(row scanner) (app.BrowserAuthRecord, error) {
	var record app.BrowserAuthRecord
	var lastVerifiedAt *time.Time
	err := row.Scan(
		&record.ID,
		&record.OwnerID,
		&record.BrowserProfileID,
		&record.SiteOrigin,
		&record.SiteRealm,
		&record.AccountHint,
		&record.AuthStrategy,
		&record.Status,
		&record.SessionRef,
		&record.CredentialRef,
		&record.CookieJarRef,
		&lastVerifiedAt,
		&record.ExpiresAt,
		&record.LastError,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.RevokedAt,
	)
	if lastVerifiedAt != nil {
		record.LastVerifiedAt = *lastVerifiedAt
	}
	return record, err
}

func scanBrowserLoginBlock(row scanner) (app.BrowserLoginBlock, error) {
	var block app.BrowserLoginBlock
	var args, target, visibleEvidence []byte
	err := row.Scan(
		&block.ID,
		&block.SessionID,
		&block.RunID,
		&block.SchemaVersion,
		&block.Version,
		&block.WorkflowID,
		&block.WorkflowRevision,
		&block.WorkflowNodeID,
		&block.SessionGeneration,
		&block.Status,
		&block.OriginalGoal,
		&block.ResumeTool,
		&args,
		&block.LastToolCallID,
		&block.LoginHandoffURL,
		&block.LoginHandoffPageID,
		&block.LastVisiblePageID,
		&block.OwnerID,
		&block.BrowserProfileID,
		&block.SiteOrigin,
		&block.SiteRealm,
		&block.AccountHint,
		&block.BrowserAuthStatus,
		&target,
		&visibleEvidence,
		&block.LastUserReply,
		&block.LastError,
		&block.TransitionOwnerID,
		&block.TransitionLeaseUntil,
		&block.CreatedAt,
		&block.UpdatedAt,
		&block.ResolvedAt,
	)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &block.ResumeArgs); err != nil {
			return app.BrowserLoginBlock{}, errors.Join(errBrowserLoginBlockJSONDecode, fmt.Errorf("resume_args: %w", err))
		}
	}
	if len(target) > 0 {
		if err := json.Unmarshal(target, &block.Target); err != nil {
			return app.BrowserLoginBlock{}, errors.Join(errBrowserLoginBlockJSONDecode, fmt.Errorf("target: %w", err))
		}
	}
	if len(visibleEvidence) > 0 && string(visibleEvidence) != "null" {
		if err := json.Unmarshal(visibleEvidence, &block.VisibleEvidence); err != nil {
			return app.BrowserLoginBlock{}, errors.Join(errBrowserLoginBlockJSONDecode, fmt.Errorf("visible_evidence: %w", err))
		}
	}
	if block.ResumeArgs == nil {
		block.ResumeArgs = map[string]any{}
	}
	return cloneBrowserLoginBlock(block), nil
}

func scanMemoryCandidate(row scanner) (app.MemoryCandidate, error) {
	var candidate app.MemoryCandidate
	err := row.Scan(&candidate.ID, &candidate.SessionID, &candidate.RunID, &candidate.Kind, &candidate.Content, &candidate.Sensitivity, &candidate.Status, &candidate.Reason, &candidate.CreatedAt, &candidate.ResolvedAt)
	return candidate, err
}

func scanMemory(row scanner) (app.Memory, error) {
	var memory app.Memory
	err := row.Scan(&memory.ID, &memory.Kind, &memory.Content, &memory.SourceID, &memory.CreatedAt)
	return memory, err
}

func scanMemoryWithSession(row scanner) (app.Memory, string, error) {
	var memory app.Memory
	var sessionID string
	err := row.Scan(&memory.ID, &memory.Kind, &memory.Content, &memory.SourceID, &memory.CreatedAt, &sessionID)
	return memory, sessionID, err
}

func scanAuditEvent(row scanner) (app.AuditEvent, error) {
	var event app.AuditEvent
	var fields []byte
	err := row.Scan(&event.ID, &event.Time, &event.Type, &event.SessionID, &event.RunID, &event.Actor, &event.Summary, &fields)
	if err != nil {
		return app.AuditEvent{}, err
	}
	if len(fields) > 0 {
		event.Fields = map[string]any{}
		if err := json.Unmarshal(fields, &event.Fields); err != nil {
			return app.AuditEvent{}, fmt.Errorf("%w: %v", errAuditFieldsJSONDecode, err)
		}
	}
	return event, nil
}

func scanEvent(row scanner) (app.Event, error) {
	var event app.Event
	var payload []byte
	err := row.Scan(&event.ID, &event.Time, &event.Type, &event.SessionID, &event.RunID, &payload)
	if err != nil {
		return app.Event{}, err
	}
	if len(payload) == 0 {
		return event, nil
	}
	event.Payload, err = decodeEventPayload(event.Type, payload)
	if err != nil {
		return app.Event{}, fmt.Errorf("%w: %v", errEventPayloadJSONDecode, err)
	}
	return event, nil
}

func scanEvalRun(row scanner) (app.EvalRun, error) {
	var run app.EvalRun
	var cases []byte
	var failureArchives []byte
	err := row.Scan(&run.ID, &run.Profile, &run.Status, &run.Summary, &cases, &failureArchives, &run.StartedAt, &run.CompletedAt)
	if err != nil {
		return app.EvalRun{}, err
	}
	if err := json.Unmarshal(cases, &run.Cases); err != nil {
		return app.EvalRun{}, fmt.Errorf("%w: cases: %v", errEvalRunJSONDecode, err)
	}
	if err := json.Unmarshal(failureArchives, &run.FailureArchives); err != nil {
		return app.EvalRun{}, fmt.Errorf("%w: failure archives: %v", errEvalRunJSONDecode, err)
	}
	run = prepareEvalRun(run, run.StartedAt)
	return run, nil
}

func scanArtifactObject(row scanner) (app.ArtifactObject, error) {
	var object app.ArtifactObject
	err := row.Scan(
		&object.ID,
		&object.Kind,
		&object.RunID,
		&object.EvalID,
		&object.SessionID,
		&object.Backend,
		&object.Bucket,
		&object.Key,
		&object.URI,
		&object.Path,
		&object.ContentType,
		&object.Bytes,
		&object.CreatedAt,
	)
	return object, err
}

func scanEpisodeSummary(row scanner) (app.EpisodeSummary, error) {
	var summary app.EpisodeSummary
	var risk string
	var tools []byte
	var approvals []byte
	var failures []byte
	err := row.Scan(&summary.ID, &summary.SessionID, &summary.RunID, &summary.Goal, &summary.Outcome, &risk, &summary.ModelLane, &tools, &approvals, &failures, &summary.RepairPerformed, &summary.Summary, &summary.CreatedAt)
	if err != nil {
		return app.EpisodeSummary{}, err
	}
	summary.Risk = app.RiskLevel(risk)
	if err := json.Unmarshal(tools, &summary.Tools); err != nil {
		return app.EpisodeSummary{}, fmt.Errorf("%w: episode tools: %v", errRunJSONDecode, err)
	}
	if err := json.Unmarshal(approvals, &summary.Approvals); err != nil {
		return app.EpisodeSummary{}, fmt.Errorf("%w: episode approvals: %v", errRunJSONDecode, err)
	}
	if err := json.Unmarshal(failures, &summary.Failures); err != nil {
		return app.EpisodeSummary{}, fmt.Errorf("%w: episode failures: %v", errRunJSONDecode, err)
	}
	return summary, nil
}
