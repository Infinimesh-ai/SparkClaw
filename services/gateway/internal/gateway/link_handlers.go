package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

func (s *Server) registerLinkHandlers(svc *link.Service) {
	svc.Handle(contract.OpPing, func(context.Context, *link.Request) (any, error) {
		return map[string]string{"time": time.Now().UTC().Format(time.RFC3339Nano)}, nil
	})
	svc.Handle(contract.OpDeviceDescribe, s.linkDeviceDescribe)
	svc.Handle(contract.OpSyncChanges, s.linkSyncChanges)
	svc.Handle(contract.OpSessionCreate, s.linkSessionCreate)
	svc.Handle(contract.OpSessionRename, s.linkSessionRename)
	svc.Handle(contract.OpSessionArchive, s.linkSessionArchive)
	svc.Handle(contract.OpSessionDelete, s.linkSessionDelete)
	svc.Handle(contract.OpSessionMessages, s.linkSessionMessages)
	svc.Handle(contract.OpMessageSend, s.linkMessageSend)
	svc.Handle(contract.OpRunSubscribe, s.linkRunSubscribe)
	svc.Handle(contract.OpRunUnsubscribe, s.linkRunUnsubscribe)
	svc.Handle(contract.OpRunStatus, s.linkRunStatus)
	svc.Handle(contract.OpRunCancel, s.linkRunCancel)
	svc.Handle(contract.OpApprovalList, s.linkApprovalList)
	svc.Handle(contract.OpApprovalResolve, s.linkApprovalResolve)
	svc.Handle(contract.OpObjectUploadBegin, s.linkUploadBegin)
	svc.Handle(contract.OpObjectUploadChunk, s.linkUploadChunk)
	svc.Handle(contract.OpObjectUploadCommit, s.linkUploadCommit)
	svc.Handle(contract.OpObjectDescribe, s.linkObjectDescribe)
	svc.Handle(contract.OpObjectRead, s.linkObjectRead)
	svc.Handle(contract.OpDomainCall, s.linkDomainCall)
	svc.Handle(contract.OpProjectionSnapshot, s.linkProjectionSnapshot)
	svc.Handle(contract.OpProjectionActivity, s.linkProjectionActivity)
	s.registerLinkPresentation()
}

func linkTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func linkHeader(sess app.Session, running bool) contract.SessionHeader {
	return contract.SessionHeader{ID: sess.ID, Title: sess.Title, Revision: sess.Revision, LastSeq: sess.LastSeq, Preview: sess.Preview, Archived: sess.Archived, CreatedAt: linkTime(sess.CreatedAt), UpdatedAt: linkTime(sess.UpdatedAt), Running: running}
}

func linkMessage(m app.Message) contract.Message {
	out := contract.Message{ID: m.ID, SessionID: m.SessionID, Seq: m.Seq, Role: m.Role, Content: m.Content, RunID: m.RunID, ClientMessageID: m.ClientMessageID, CreatedAt: linkTime(m.CreatedAt)}
	for _, a := range m.Attachments {
		out.Attachments = append(out.Attachments, contract.AttachmentRef{ObjectID: linkAttachmentObjectID(m.SessionID, a), Name: a.Name, Size: int64(a.Bytes), ContentType: a.ContentType, SHA256: a.SHA256})
	}
	if m.Clarification != nil {
		if raw, err := json.Marshal(map[string]any{"clarification": m.Clarification}); err == nil {
			out.Extra = raw
		}
	}
	return out
}

// projectMessage applies the device projection (design 4.3): the watch
// receives trimmed plain text and only attachment names.
func projectMessage(class string, m contract.Message) contract.Message {
	if class != contract.ClassWatch {
		return m
	}
	m.Extra = nil
	for i := range m.Attachments {
		m.Attachments[i] = contract.AttachmentRef{Name: m.Attachments[i].Name}
	}
	m.Content = trimForWatch(m.Content, 2000)
	return m
}

func trimForWatch(text string, limit int) string {
	text = strings.TrimSpace(text)
	replacer := strings.NewReplacer("**", "", "__", "", "```", "", "`", "", "### ", "", "## ", "", "# ", "")
	text = replacer.Replace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	r := []rune(text)
	return string(r[:limit]) + "…"
}

func (s *Server) linkDeviceDescribe(ctx context.Context, req *link.Request) (any, error) {
	return contract.DeviceDescribeResult{Principal: req.Principal.Principal, Features: s.link.service.Features(req.Principal.Conditions, req.Principal.Profile), Domains: s.linkDomainNames(), Backend: "sparkclaw", DeploymentID: s.cfg.Gateway.DeploymentID}, nil
}

func (s *Server) linkSyncChanges(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.SyncChangesParams](req)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	changes, err := s.store.SessionChanges(ctx, req.Principal.OwnerID, p.Cursor, limit+1)
	if err != nil {
		return nil, linkStoreError(err)
	}
	out := contract.SyncChangesResult{Cursor: p.Cursor, Sessions: []contract.SessionHeader{}}
	if len(changes) > limit {
		changes = changes[:limit]
		out.More = true
	}
	for _, c := range changes {
		var h contract.SessionHeader
		if c.Deleted {
			h = contract.SessionHeader{ID: c.Session.ID, Revision: c.Session.Revision, Deleted: true, UpdatedAt: linkTime(c.DeletedAt)}
		} else {
			h = linkHeader(c.Session, s.link.runs.running(c.Session.ID))
			if req.Principal.Class == contract.ClassWatch {
				h.Preview = trimForWatch(h.Preview, 120)
			}
		}
		out.Sessions = append(out.Sessions, h)
		out.Cursor = c.Session.Revision
	}
	return out, nil
}

func linkStoreError(err error) error {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorNotFound:
		return link.Errorf(contract.CodeNotFound, "not found")
	case store.StoreErrorConflict:
		return link.Errorf(contract.CodeConflict, "conflict")
	case store.StoreErrorInvalid:
		return link.Errorf(contract.CodeInvalidRequest, "invalid")
	}
	return &contract.Error{Code: contract.CodeBackendUnavailable, Message: "storage unavailable", Retryable: true}
}

func (s *Server) linkSessionCreate(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.SessionCreateParams](req)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ClientRequestID) == "" {
		return nil, link.Errorf(contract.CodeInvalidRequest, "client_request_id is required")
	}
	key := req.Principal.DeviceID + "\x00session.create\x00" + p.ClientRequestID
	raw, err := s.link.receipts.Do(key, linkDigest(p), func() (any, error) {
		profile, found, err := s.store.GetOwnerProfileByID(ctx, req.Principal.OwnerID)
		if err != nil {
			return nil, linkStoreError(err)
		}
		workspace := ""
		if found {
			workspace = profile.WorkspaceRoot
		}
		title := strings.TrimSpace(p.Title)
		sess, err := s.store.CreateSessionWithScope(ctx, title, req.Principal.OwnerID, workspace, "webchat", false)
		if err != nil {
			return nil, linkStoreError(err)
		}
		header, _, err := s.store.SessionHeader(ctx, sess.ID)
		if err != nil {
			return nil, linkStoreError(err)
		}
		return linkHeader(header, false), nil
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func (s *Server) linkOwnedSession(ctx context.Context, req *link.Request, id string) (app.Session, error) {
	sess, ok, err := s.sessionForPrincipal(ctx, linkRequestPrincipal(req), id)
	if err != nil {
		return app.Session{}, linkStoreError(err)
	}
	if !ok || sess.Hidden {
		return app.Session{}, link.Errorf(contract.CodeNotFound, "conversation not found")
	}
	return sess, nil
}

func (s *Server) linkSessionRename(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.SessionRenameParams](req)
	if err != nil {
		return nil, err
	}
	if _, err := s.linkOwnedSession(ctx, req, p.SessionID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, link.Errorf(contract.CodeInvalidRequest, "title is required")
	}
	if _, err := s.store.UpdateSessionTitle(ctx, p.SessionID, p.Title); err != nil {
		return nil, linkStoreError(err)
	}
	header, _, err := s.store.SessionHeader(ctx, p.SessionID)
	if err != nil {
		return nil, linkStoreError(err)
	}
	return linkHeader(header, s.link.runs.running(p.SessionID)), nil
}

func (s *Server) linkSessionArchive(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.SessionArchiveParams](req)
	if err != nil {
		return nil, err
	}
	if _, err := s.linkOwnedSession(ctx, req, p.SessionID); err != nil {
		return nil, err
	}
	header, err := s.store.SetSessionArchived(ctx, p.SessionID, p.Archived)
	if err != nil {
		return nil, linkStoreError(err)
	}
	return linkHeader(header, s.link.runs.running(p.SessionID)), nil
}

func (s *Server) linkSessionDelete(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.SessionRef](req)
	if err != nil {
		return nil, err
	}
	sess, ok, err := s.sessionForPrincipal(ctx, linkRequestPrincipal(req), p.SessionID)
	if err != nil {
		return nil, linkStoreError(err)
	}
	if !ok {
		// Deleting twice is a success: the tombstone already exists.
		return map[string]any{"session_id": p.SessionID, "deleted": true}, nil
	}
	if s.link.runs.running(sess.ID) {
		return nil, link.Errorf(contract.CodeBusy, "a message is running in this conversation")
	}
	objects := s.linkSessionUploads(ctx, sess.ID)
	if _, err := s.store.DeleteSession(ctx, sess.ID); err != nil {
		return nil, linkStoreError(err)
	}
	// Uploaded attachments are deleted with the conversation (design 3.10).
	for _, path := range objects {
		_ = removeFileQuiet(path)
	}
	return map[string]any{"session_id": sess.ID, "deleted": true}, nil
}

func (s *Server) linkSessionMessages(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.SessionMessagesParams](req)
	if err != nil {
		return nil, err
	}
	sess, err := s.linkOwnedSession(ctx, req, p.SessionID)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if req.Principal.Class == contract.ClassWatch && limit > 20 {
		limit = 20
	}
	messages, more, err := s.store.ListMessagePage(ctx, sess.ID, p.BeforeSeq, p.AfterSeq, limit)
	if err != nil {
		return nil, linkStoreError(err)
	}
	header, _, err := s.store.SessionHeader(ctx, sess.ID)
	if err != nil {
		return nil, linkStoreError(err)
	}
	out := contract.SessionMessagesResult{Session: linkHeader(header, s.link.runs.running(sess.ID)), More: more, Messages: make([]contract.Message, 0, len(messages))}
	for _, m := range messages {
		out.Messages = append(out.Messages, projectMessage(req.Principal.Class, linkMessage(m)))
	}
	return out, nil
}

func linkApproval(a app.Approval) contract.Approval {
	out := contract.Approval{ID: a.ID, SessionID: a.SessionID, RunID: a.RunID, Title: a.Summary, Summary: a.Reason, Risk: string(a.Risk), Status: string(a.Status), ToolName: a.Tool, CreatedAt: linkTime(a.CreatedAt)}
	if out.Title == "" {
		out.Title = a.Tool
	}
	if raw, err := json.Marshal(a); err == nil {
		out.Detail = raw
	}
	return out
}

func (s *Server) linkApprovalList(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.ApprovalListParams](req)
	if err != nil {
		return nil, err
	}
	status := app.ApprovalStatus("")
	if p.Pending {
		status = app.ApprovalStatusPending
	}
	approvals, err := s.store.ListApprovals(ctx, status)
	if err != nil {
		return nil, linkStoreError(err)
	}
	principal := linkRequestPrincipal(req)
	out := contract.ApprovalListResult{Approvals: []contract.Approval{}}
	for _, a := range approvals {
		if p.SessionID != "" && a.SessionID != p.SessionID {
			continue
		}
		visible, err := s.approvalVisibleToPrincipal(ctx, principal, a)
		if err != nil || !visible {
			continue
		}
		la := linkApproval(a)
		if req.Principal.Class == contract.ClassWatch {
			la.Detail = nil
			la.Summary = trimForWatch(la.Summary, 300)
		}
		out.Approvals = append(out.Approvals, la)
	}
	return out, nil
}

func (s *Server) linkApprovalResolve(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.ApprovalResolveParams](req)
	if err != nil {
		return nil, err
	}
	var status app.ApprovalStatus
	switch p.Decision {
	case "approve":
		status = app.ApprovalStatusApproved
	case "reject":
		status = app.ApprovalStatusRejected
	default:
		return nil, link.Errorf(contract.CodeInvalidRequest, "decision must be approve or reject")
	}
	if strings.TrimSpace(p.IdempotencyKey) == "" {
		return nil, link.Errorf(contract.CodeInvalidRequest, "idempotency_key is required")
	}
	key := req.Principal.DeviceID + "\x00approval.resolve\x00" + p.IdempotencyKey
	raw, err := s.link.receipts.Do(key, linkDigest(p), func() (any, error) {
		var input approvalDecisionInput
		if len(p.Inputs) > 0 {
			if err := json.Unmarshal(p.Inputs, &input.ApprovalInputSubmission); err != nil {
				return nil, link.Errorf(contract.CodeInvalidRequest, "inputs: %v", err)
			}
		}
		outcome, err := s.resolveApprovalFor(ctx, linkRequestPrincipal(req), p.ApprovalID, status, input, nil)
		if err != nil {
			var happy *happyPlanApprovalPending
			if asHappy(err, &happy) {
				return nil, link.Errorf(contract.CodeUnsupportedCapability, "this approval is decided on the host")
			}
			return nil, linkConversationError(err)
		}
		decided := outcome.approval
		if a, ok := outcome.body["approval"].(app.Approval); ok {
			decided = a
		}
		if decided.SessionID != "" && decided.RunID != "" {
			if result, found, err := s.runtime.LookupRunResult(ctx, decided.SessionID, decided.RunID); err == nil && found {
				s.link.runs.finish(decided.SessionID, s.linkRunStatusFromResult(ctx, decided.SessionID, decided.RunID, result, nil))
			}
		}
		return contract.ApprovalResolveResult{Approval: linkApproval(decided)}, nil
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}
