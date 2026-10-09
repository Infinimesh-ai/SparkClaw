package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (a *iscpDomainAdapter) mail(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	session, _ := iscpworkbench.SessionFromContext(ctx)
	return a.mailWithSession(ctx, request, session)
}
func (a *iscpDomainAdapter) mailWithSession(ctx context.Context, request iscpworkbench.Request, session iscpworkbench.SessionInfo) iscpDomainResult {
	principal, _, err := a.executionIdentity(ctx, request)
	if err != nil {
		return domainError(403, "installation_required")
	}
	switch request.Operation {
	case iscpworkbench.OperationMailMailboxes:
		if a.server.mailSync == nil {
			return domainError(503, "mail_sync_unavailable")
		}
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		boxes, err := a.server.mailSync.Mailboxes(ctx, principal.OwnerID)
		if err != nil {
			return domainError(503, "mail_sync_unavailable")
		}
		return domainJSON(200, map[string]any{"mailboxes": boxes})
	case iscpworkbench.OperationMailSync:
		return domainHTTP(ctx, request, http.MethodPost, a.server.syncMail, map[string]string{"mailbox": request.Params["mailbox"]})
	case iscpworkbench.OperationMailMessage:
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodGet, a.server.getEmailSingleMessage, map[string]string{"mail": request.Params["mail"]})
	case iscpworkbench.OperationMailAttachment:
		if a.server.emailManagement == nil {
			return domainError(503, "mail_unavailable")
		}
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		mail, found, err := a.server.emailManagement.ClientSyncMail(ctx, principal.OwnerID, request.Params["mail"])
		if err != nil || !found || mail.MailboxID != request.Params["mailbox"] || request.Params["part"] == "" || request.Params["part"] == "original" {
			return domainError(404, "mail_attachment_not_found")
		}
		file, name, err := a.server.emailManagement.OpenFile(ctx, principal.OwnerID, request.Params["mail"], request.Params["part"])
		if err != nil {
			return domainError(404, "mail_attachment_unavailable")
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
		return iscpDomainResult{status: 200, object: &ref}
	case iscpworkbench.OperationMailDraftsList:
		if a.server.emailManagement == nil {
			return domainError(503, "mail_unavailable")
		}
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		if request.Params["draft"] != "" {
			return domainHTTP(ctx, request, http.MethodGet, a.server.listEmailDrafts, map[string]string{"draft": request.Params["draft"]})
		}
		limit := 50
		if raw := request.Params["limit"]; raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 100 {
				return domainError(400, "invalid_input")
			}
		}
		page, err := a.server.emailManagement.DraftPage(ctx, store.EmailQuery{OwnerID: principal.OwnerID, Limit: limit, After: request.Params["cursor"], MailboxID: request.Params["mailbox_id"], Search: request.Params["q"]})
		if err != nil {
			return domainError(503, "mail_unavailable")
		}
		return domainJSON(200, page)
	case iscpworkbench.OperationMailDraftsSave:
		var input struct {
			Attachments []json.RawMessage `json:"attachments"`
		}
		if json.Unmarshal(request.Body, &input) == nil && len(input.Attachments) > 0 && !slices.Contains(session.Scopes, "files.read") {
			return domainError(403, "permission_denied")
		}
		return domainHTTP(ctx, request, http.MethodPost, a.server.saveEmailDraft, map[string]string{"draft": request.Params["draft"]})
	case iscpworkbench.OperationMailSend, iscpworkbench.OperationMailDraftsSend:
		if a.server.emailManagement != nil {
			drafts, err := a.server.emailManagement.Drafts(ctx, principal.OwnerID, request.Params["draft"])
			if err == nil && len(drafts) > 0 && len(drafts[0].Attachments) > 0 && !slices.Contains(session.Scopes, "files.read") {
				return domainError(403, "permission_denied")
			}
		}
		return domainHTTP(ctx, request, http.MethodPost, a.server.sendEmailDraft, map[string]string{"draft": request.Params["draft"]})
	case iscpworkbench.OperationMailDraftsReconcile:
		return domainHTTP(ctx, request, http.MethodPost, a.server.reconcileEmailDraft, map[string]string{"draft": request.Params["draft"]})
	}
	return domainError(501, "capability_unavailable")
}
