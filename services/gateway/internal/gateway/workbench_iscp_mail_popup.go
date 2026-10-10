package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// The original mail window uses the same owner-scoped services on both
// transports. Each operation selects one fixed handler; callers cannot provide
// a URL, HTTP method, principal, or arbitrary query field.
func (a *iscpDomainAdapter) mailPopup(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if _, _, err := a.executionIdentity(ctx, request); err != nil {
		return domainError(403, "installation_required")
	}
	if a.server.emailManagement == nil {
		return domainError(503, "mail_unavailable")
	}
	spec, found := iscpworkbench.LookupOperation(request.Operation)
	if !found || !spec.Mutation && !domainEmpty(request.Body) {
		return domainError(400, "invalid_input")
	}
	var handler http.HandlerFunc
	switch request.Operation {
	case iscpworkbench.OperationMailConversationsList:
		handler = a.server.listEmailConversations
	case iscpworkbench.OperationMailConversationsGet:
		handler = a.server.getEmailConversation
	case iscpworkbench.OperationMailConversationsMessages:
		handler = a.server.listEmailMessages
	case iscpworkbench.OperationMailConversationsRename:
		handler = a.server.renameEmailConversation
	case iscpworkbench.OperationMailConversationsDelete:
		handler = a.server.deleteEmailConversation
	case iscpworkbench.OperationMailPending:
		handler = a.server.listEmailPending
	case iscpworkbench.OperationMailNotifications, iscpworkbench.OperationMailInteraction:
		handler = a.server.listEmailRoutedMessages
	case iscpworkbench.OperationMailVerification:
		handler = a.server.revealEmailVerification
	case iscpworkbench.OperationMailRenderPreview:
		handler = a.server.getEmailMessageRenderPreview
	case iscpworkbench.OperationMailSourceCleanup:
		handler = a.server.cleanupEmailSource
	case iscpworkbench.OperationMailClassification:
		handler = a.server.changeEmailClassification
	case iscpworkbench.OperationMailAssignment:
		handler = a.server.changeEmailAssignment
	case iscpworkbench.OperationMailSenderRulesList:
		handler = a.server.emailSenderRules
	case iscpworkbench.OperationMailSenderRulesUpdate:
		handler = a.server.updateEmailSenderRule
	case iscpworkbench.OperationMailPresentationsGet:
		handler = a.server.getEmailPresentations
	case iscpworkbench.OperationMailPresentationsEnsure:
		handler = a.server.ensureEmailPresentations
	case iscpworkbench.OperationMailRepliesPolish:
		handler = a.server.polishEmailReply
	case iscpworkbench.OperationMailSentSources:
		handler = a.server.emailSentSources
	case iscpworkbench.OperationMailSyncStatus:
		handler = a.server.getEmailSyncStatus
	case iscpworkbench.OperationMailSyncWarnings:
		handler = a.server.listEmailSyncWarnings
	case iscpworkbench.OperationMailSyncAcknowledge:
		handler = a.server.acknowledgeEmailSyncWarning
	case iscpworkbench.OperationMailSyncRequest:
		handler = a.server.scheduleEmailSync
	case iscpworkbench.OperationMailViewed:
		handler = a.server.markEmailMessagesViewed
	case iscpworkbench.OperationMailReanalyze:
		handler = a.server.reanalyzeEmailMessage
	case iscpworkbench.OperationMailComposeCapabilities:
		capabilities := a.server.emailManagement.ComposeCapabilities()
		session, ok := iscpworkbench.SessionFromContext(ctx)
		capabilities.WorkspaceAttachments = capabilities.WorkspaceAttachments && ok && slices.Contains(session.Scopes, "files.read")
		return domainJSON(http.StatusOK, capabilities)
	case iscpworkbench.OperationMailFile:
		return a.mailPopupFile(ctx, request)
	case iscpworkbench.OperationMailIntakeUpdate:
		var input struct {
			Enabled *bool  `json:"intake_enabled"`
			Version *int64 `json:"expected_mailbox_version"`
		}
		if !app.KnownEmailProvider(request.Params["provider"]) || domainDecode(request.Body, &input) != nil || input.Enabled == nil || input.Version == nil || *input.Version < 0 {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPatch, a.server.updateEmailProvider, map[string]string{"provider": request.Params["provider"]})
	default:
		return domainError(501, "capability_unavailable")
	}
	return mailPopupHTTP(ctx, request, spec, handler)
}

func mailPopupHTTP(ctx context.Context, request iscpworkbench.Request, spec iscpworkbench.OperationSpec, handler http.HandlerFunc) iscpDomainResult {
	if spec.HTTP == nil {
		return domainError(501, "capability_unavailable")
	}
	query, paths := url.Values{}, map[string]string{}
	for key, value := range request.Params {
		if !slices.Contains(spec.Params, key) {
			return domainError(400, "invalid_input")
		}
		switch key {
		case "mail", "conversation", "rule", "warning":
			paths[key] = value
		case "target_ids":
			var ids []string
			if json.Unmarshal([]byte(value), &ids) != nil || len(ids) == 0 || len(ids) > 100 {
				return domainError(400, "invalid_input")
			}
			for _, id := range ids {
				if len(id) == 0 || len(id) > 256 {
					return domainError(400, "invalid_input")
				}
				query.Add("target_id", id)
			}
		default:
			query.Set(key, value)
		}
	}
	return domainHTTP(ctx, request, spec.HTTP.Method, func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path, r.URL.RawQuery = spec.HTTP.Path, query.Encode()
		handler(w, r)
	}, paths)
}

func (a *iscpDomainAdapter) mailPopupFile(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	part := request.Params["part_id"]
	if len(part) > 256 {
		return domainError(400, "invalid_input")
	}
	if part == "" {
		part = "original"
	}
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	file, name, err := a.server.emailManagement.OpenFile(ctx, principal.OwnerID, request.Params["mail"], part)
	if err != nil {
		return domainHTTP(ctx, request, http.MethodGet, func(w http.ResponseWriter, r *http.Request) { writeEmailManagementError(w, err) }, nil)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
	if err != nil || len(raw) > 64<<20 {
		return domainError(413, "mail_attachment_too_large")
	}
	ref, err := domainPutObject(ctx, "mail_attachment", filepath.Base(name), "application/octet-stream", raw)
	if err != nil {
		return domainError(503, "mail_attachment_object_unavailable")
	}
	return iscpDomainResult{status: http.StatusOK, object: &ref}
}

// The popup has its own qualification gate, independent of send-provider state.
// A logged-out account must not prevent opening the mailbox/login controls.
func mailPopupOperations() []string {
	return []string{
		iscpworkbench.OperationMailProvidersList, iscpworkbench.OperationMailMessage, iscpworkbench.OperationObjectRead,
		iscpworkbench.OperationMailConversationsList, iscpworkbench.OperationMailConversationsGet, iscpworkbench.OperationMailConversationsMessages,
		iscpworkbench.OperationMailConversationsRename, iscpworkbench.OperationMailConversationsDelete,
		iscpworkbench.OperationMailPending, iscpworkbench.OperationMailNotifications, iscpworkbench.OperationMailInteraction,
		iscpworkbench.OperationMailVerification, iscpworkbench.OperationMailRenderPreview, iscpworkbench.OperationMailSourceCleanup, iscpworkbench.OperationMailFile,
		iscpworkbench.OperationMailClassification, iscpworkbench.OperationMailAssignment, iscpworkbench.OperationMailSenderRulesList, iscpworkbench.OperationMailSenderRulesUpdate,
		iscpworkbench.OperationMailPresentationsGet, iscpworkbench.OperationMailPresentationsEnsure, iscpworkbench.OperationMailComposeCapabilities,
		iscpworkbench.OperationMailRepliesPolish, iscpworkbench.OperationMailSentSources,
		iscpworkbench.OperationMailSyncStatus, iscpworkbench.OperationMailSyncWarnings, iscpworkbench.OperationMailSyncAcknowledge, iscpworkbench.OperationMailSyncRequest,
		iscpworkbench.OperationMailViewed, iscpworkbench.OperationMailReanalyze, iscpworkbench.OperationMailIntakeUpdate,
	}
}
