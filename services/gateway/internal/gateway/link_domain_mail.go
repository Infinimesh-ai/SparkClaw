package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mailsync"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// The mail domain over the link layer: the desktop's mail window and its
// composer. Each method parses its typed arguments and calls the mail
// services the host WebChat's handlers call, answering with the domain
// result {status, body} the original screens read. Files (an original
// message, an attachment) are delivered as domain objects; attachments a
// draft sends are domain objects the device uploaded.

const mailFileMaxBytes = 64 << 20

// Arguments naming one record.
type (
	mailArgs struct {
		Mail string `json:"mail"`
	}
	mailConversationArgs struct {
		Conversation string `json:"conversation"`
	}
	mailProviderArgs struct {
		Provider string `json:"provider"`
	}
	mailDraftArgs struct {
		Draft string `json:"draft"`
	}
)

type mailConversationMessagesArgs struct {
	emailListArgs
	Conversation string `json:"conversation"`
}

type mailRoutedArgs struct {
	emailListArgs
	Subtype  string `json:"subtype,omitempty"`
	Validity string `json:"validity,omitempty"`
}

type mailDraftsListArgs struct {
	emailListArgs
	Draft string `json:"draft,omitempty"`
}

type mailSyncArgs struct {
	Mailbox string  `json:"mailbox"`
	Cursor  *string `json:"cursor"`
	Limit   *int    `json:"limit"`
}

type mailAttachmentArgs struct {
	Mailbox string `json:"mailbox"`
	Mail    string `json:"mail"`
	Part    string `json:"part"`
}

type mailFileArgs struct {
	Mail   string `json:"mail"`
	PartID string `json:"part_id,omitempty"`
}

type mailPresentationsArgs struct {
	TargetKind string   `json:"target_kind,omitempty"`
	TargetIDs  []string `json:"target_ids,omitempty"`
	Language   string   `json:"language,omitempty"`
	Retry      bool     `json:"retry,omitempty"`
}

type mailWarningsArgs struct {
	MailboxID string `json:"mailbox_id,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type mailProviderUpdateArgs struct {
	Provider        string `json:"provider"`
	Enabled         *bool  `json:"enabled"`
	Default         *bool  `json:"default"`
	ExpectedVersion *int64 `json:"expected_version"`
}

type mailIntakeArgs struct {
	Provider               string `json:"provider"`
	IntakeEnabled          *bool  `json:"intake_enabled"`
	ExpectedMailboxVersion *int64 `json:"expected_mailbox_version"`
}

type mailDraftSaveArgs struct {
	Draft           string `json:"draft,omitempty"`
	ID              string `json:"id,omitempty"`
	ExpectedVersion int64  `json:"expected_version"`
	Attachments     []struct {
		LocalFileID string                    `json:"local_file_id"`
		Object      app.EmailAttachmentObject `json:"object"`
	} `json:"attachments"`
	MailboxID   string   `json:"mailbox_id"`
	Mode        string   `json:"mode"`
	ReplyMailID string   `json:"reply_mail_id"`
	To          []string `json:"to"`
	CC          []string `json:"cc"`
	Subject     string   `json:"subject"`
	Body        string   `json:"body"`
}

type mailDraftSendArgs struct {
	Draft           string `json:"draft"`
	ExpectedVersion int64  `json:"expected_version"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type mailDraftReconcileArgs struct {
	Draft      string `json:"draft"`
	SentMailID string `json:"sent_mail_id,omitempty"`
}

type mailDraftResolveArgs struct {
	Draft           string `json:"draft"`
	ExpectedVersion int64  `json:"expected_version"`
	Resolution      string `json:"resolution"`
}

type mailCommandArgs struct {
	ExpectedVersion int64  `json:"expected_version"`
	CommandKey      string `json:"command_key"`
}

type mailRenameArgs struct {
	mailCommandArgs
	Conversation string `json:"conversation"`
	Title        string `json:"title"`
}

type mailDeleteArgs struct {
	mailCommandArgs
	Conversation string `json:"conversation"`
}

type mailClassificationArgs struct {
	mailCommandArgs
	Mail                string `json:"mail"`
	Entry               string `json:"entry"`
	RememberSender      bool   `json:"remember_sender"`
	ExpectedRuleVersion int64  `json:"expected_rule_version"`
}

type mailAssignmentArgs struct {
	mailCommandArgs
	Mail           string `json:"mail"`
	ConversationID string `json:"conversation_id"`
	Title          string `json:"title"`
}

type mailSenderRuleArgs struct {
	mailCommandArgs
	Rule    string `json:"rule"`
	Entry   string `json:"entry"`
	Enabled bool   `json:"enabled"`
}

type mailCleanupArgs struct {
	Scope      string `json:"scope"`
	MailID     string `json:"mail_id"`
	MailboxID  string `json:"mailbox_id"`
	Date       string `json:"date"`
	CommandKey string `json:"command_key"`
}

type mailPolishArgs struct {
	ID          string `json:"id"`
	MailID      string `json:"mail_id"`
	Instruction string `json:"instruction"`
	Language    string `json:"language"`
}

type mailWarningAckArgs struct {
	Warning   string `json:"warning"`
	MailboxID string `json:"mailbox_id"`
}

type mailboxArgs struct {
	MailboxID string `json:"mailbox_id"`
}

type mailViewedArgs struct {
	MailIDs []string `json:"mail_ids"`
}

// mailOutcome is a mail management answer: the value, or its failure.
func mailOutcome(status int, value any, err error) domainReply {
	if err != nil {
		return emailManagementErrorReply(err)
	}
	return reply(status, value)
}

func mailComposeOutcome(value any, err error) domainReply {
	if err != nil {
		return emailComposeErrorReply(err)
	}
	return reply(http.StatusOK, value)
}

// mailManagement runs one call against the mail management service, which
// a backend without mail does not have.
func mailManagement[T any](mutation bool, call func(ctx context.Context, s *Server, m *emailmanagement.Service, p requestPrincipal, args T) domainReply) linkDomainMethod {
	return linkReply(mutation, func(ctx context.Context, s *Server, p requestPrincipal, args T) domainReply {
		if s.emailManagement == nil {
			return reply(http.StatusServiceUnavailable, map[string]any{"error": "Email management is unavailable.", "code": "email_management_unavailable", "retryable": true})
		}
		return call(ctx, s, s.emailManagement, p, args)
	})
}

// mailList runs a list query.
func mailList[T any](query func(T) emailListArgs, call func(ctx context.Context, m *emailmanagement.Service, q store.EmailQuery, args T) (any, error)) linkDomainMethod {
	return mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args T) domainReply {
		q, err := query(args).query(p.OwnerID, 30)
		if err != nil {
			return emailManagementErrorReply(err)
		}
		out, err := call(ctx, m, q, args)
		return mailOutcome(http.StatusOK, out, err)
	})
}

// emailRoutedMessages lists notification mail by subtype and validity, or
// interaction mail.
func emailRoutedMessages(ctx context.Context, m *emailmanagement.Service, q store.EmailQuery, notifications bool, subtype, validity string) (emailmanagement.MessagesView, error) {
	if validity != "" && validity != "not_expired" && validity != "expired" && validity != "validity_unknown" {
		return emailmanagement.MessagesView{}, emailmanagement.ErrInvalidInput
	}
	switch subtype {
	case "", "verification", "promotion", "account_security", "general":
	default:
		return emailmanagement.MessagesView{}, emailmanagement.ErrInvalidInput
	}
	if !notifications {
		if subtype != "" || validity != "" {
			return emailmanagement.MessagesView{}, emailmanagement.ErrInvalidInput
		}
		return m.InteractionMails(ctx, q)
	}
	q.NotificationSubtype, q.Validity = subtype, validity
	return m.Notifications(ctx, q)
}

// mailFileReply delivers one stored part of a message (or the original)
// as a domain object.
func mailFileReply(ctx context.Context, m *emailmanagement.Service, owner, mail, part string) domainReply {
	file, name, err := m.OpenFile(ctx, owner, mail, part)
	if err != nil {
		return emailManagementErrorReply(err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, mailFileMaxBytes+1))
	if err != nil || len(raw) > mailFileMaxBytes {
		return replyCode(http.StatusRequestEntityTooLarge, "mail_attachment_too_large")
	}
	return domainReply{Status: http.StatusOK, Object: &domainReplyFile{Purpose: "mail_attachment", Name: filepath.Base(name), MediaType: "application/octet-stream", Data: raw}}
}

// mailIntakeOperation is the intent of one receiving change, keyed by the
// link attempt that carries it, so a lost reply is settled from the
// mailbox later (mail.intake.reconcile).
func mailIntakeOperation(attempt linkDomainAttempt, provider string, enabled bool, version int64) store.EmailIntakeOperation {
	return store.EmailIntakeOperation{Key: execution.Digest([]byte(attempt.Key)), Digest: attempt.Digest, Provider: provider, Enabled: enabled, ExpectedVersion: version}
}

// mailIntakeReply is the answer to a receiving change once its outcome is
// known from the mailbox.
func mailIntakeReply(resolved store.EmailIntakeResolution) (domainReply, bool) {
	switch resolved.State {
	case store.EmailIntakeCommitted:
		return reply(http.StatusOK, map[string]any{"mailbox": emailmanagement.ProjectMailbox(resolved.Mailbox)}), true
	case store.EmailIntakeNotApplied:
		return reply(http.StatusConflict, map[string]any{"code": "email_intake_not_applied", "error": "The previous receiving change was not applied. Review the current setting and try again.", "retryable": false, "provider": resolved.Operation.Provider, "provider_version": resolved.Version}), true
	}
	return domainReply{}, false
}

type mailIntakeReconcileArgs struct {
	// IdempotencyKey is the original change's key and Original its
	// arguments: the attempt is found again by both.
	IdempotencyKey string          `json:"idempotency_key"`
	Original       json.RawMessage `json:"original"`
}

// reconcileMailIntake settles a receiving change whose reply was lost and
// whose attempt never finished: the mailbox tells whether it was applied.
// The settled answer becomes the change's receipt.
func (s *Server) reconcileMailIntake(ctx context.Context, req *link.Request, raw json.RawMessage) (any, error) {
	args, err := linkArgs[mailIntakeReconcileArgs](raw)
	if err != nil {
		return nil, err
	}
	original, err := linkArgs[mailIntakeArgs](args.Original)
	if err != nil || !execution.UUID(args.IdempotencyKey) || !app.KnownEmailProvider(original.Provider) || original.IntakeEnabled == nil || original.ExpectedMailboxVersion == nil || *original.ExpectedMailboxVersion < 0 {
		return nil, link.Errorf(contract.CodeInvalidRequest, "the original receiving change is invalid")
	}
	if s.emailManagement == nil {
		return nil, &contract.Error{Code: contract.CodeBackendUnavailable, Message: "Email management is unavailable.", Retryable: true}
	}
	key, digest := linkDomainReceipt(req.Principal.DeviceID, contract.DomainCallParams{Domain: "mail", Method: "mail.intake.update", Args: args.Original, IdempotencyKey: args.IdempotencyKey})
	settled, err := s.link.receipts.Settle(key, digest, func() (any, error) {
		operation := mailIntakeOperation(linkDomainAttempt{Key: key, Digest: digest}, original.Provider, *original.IntakeEnabled, *original.ExpectedMailboxVersion)
		resolved, err := s.emailManagement.ResolveIntake(ctx, req.Principal.OwnerID, operation)
		if err != nil || ctx.Err() != nil {
			return nil, errLinkOutcomeUnknown()
		}
		answer, known := mailIntakeReply(resolved)
		if !known {
			return nil, errLinkOutcomeUnknown()
		}
		result, err := s.linkDomainResult(req.Principal.DeviceID, answer)
		if err != nil {
			return nil, err
		}
		return contract.DomainCallResult{Result: mustRaw(result)}, nil
	})
	if err != nil {
		return nil, err
	}
	var call contract.DomainCallResult
	var result linkDomainResult
	if json.Unmarshal(settled, &call) != nil || json.Unmarshal(call.Result, &result) != nil {
		return nil, errLinkOutcomeUnknown()
	}
	return map[string]any{"state": "completed", "idempotency_key": args.IdempotencyKey, "response": result}, nil
}

func init() {
	registerLinkDomain("mail", map[string]linkDomainMethod{
		// providers
		"mail.providers.list": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			if s.email == nil {
				return replyError(http.StatusServiceUnavailable, errors.New("browser email configuration is unavailable"))
			}
			providers, err := s.email.List(ctx, p.OwnerID)
			if err != nil {
				return emailErrorReply(err)
			}
			return reply(http.StatusOK, map[string]any{"providers": providers})
		}),
		// The sending provider's settings; receiving is mail.intake.update.
		"mail.providers.update": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args mailProviderUpdateArgs) domainReply {
			if s.email == nil {
				return replyCode(http.StatusServiceUnavailable, "mail_settings_unavailable")
			}
			if !app.KnownEmailProvider(args.Provider) {
				return replyCode(http.StatusBadRequest, "invalid_email_provider")
			}
			if args.ExpectedVersion == nil || *args.ExpectedVersion < 0 || args.Enabled == nil && args.Default == nil {
				return replyCode(http.StatusBadRequest, "invalid_input")
			}
			status, err := s.email.Update(ctx, p.OwnerID, p.ActorID, args.Provider, emailautomation.UpdateProviderInput{Enabled: args.Enabled, Default: args.Default, ExpectedVersion: *args.ExpectedVersion})
			if err != nil {
				return emailErrorReply(err)
			}
			return reply(http.StatusOK, status)
		}),
		"mail.providers.check": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args mailProviderArgs) domainReply {
			if s.email == nil {
				return replyCode(http.StatusServiceUnavailable, "mail_settings_unavailable")
			}
			if !app.KnownEmailProvider(args.Provider) {
				return replyCode(http.StatusBadRequest, "invalid_email_provider")
			}
			return s.emailProviderCheck(ctx, p, args.Provider)
		}),
		// Opening the login browser may have happened before a failure: a
		// failed attempt stays fenced, and the check action stays available.
		"mail.providers.login": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args mailProviderArgs) domainReply {
			if s.email == nil {
				return replyCode(http.StatusServiceUnavailable, "mail_settings_unavailable")
			}
			if !app.KnownEmailProvider(args.Provider) {
				return replyCode(http.StatusBadRequest, "invalid_email_provider")
			}
			status, err := s.email.OpenLoginBrowser(ctx, p.OwnerID, p.ActorID, args.Provider)
			if err != nil {
				answer := emailErrorReply(err)
				answer.Unknown = answer.Status > http.StatusBadRequest
				return answer
			}
			return reply(http.StatusOK, status)
		}),
		"mail.intake.update": {Mutation: true, Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			attempt, _ := linkDomainAttemptFrom(ctx)
			// Receiving is its own change: it never carries the sending
			// provider's settings along.
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			_, enabled := fields["enabled"]
			_, primary := fields["default"]
			_, version := fields["expected_version"]
			return linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args mailIntakeArgs) domainReply {
				if enabled || primary || version || !app.KnownEmailProvider(args.Provider) || args.IntakeEnabled == nil || args.ExpectedMailboxVersion == nil || *args.ExpectedMailboxVersion < 0 {
					return replyCode(http.StatusBadRequest, "invalid_input")
				}
				if s.emailManagement == nil {
					return reply(http.StatusServiceUnavailable, map[string]any{"error": "Email management is unavailable.", "code": "email_management_unavailable", "retryable": true})
				}
				ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
				mailbox, err := s.emailManagement.ConfigureOperation(ctx, p.OwnerID, mailIntakeOperation(attempt, args.Provider, *args.IntakeEnabled, *args.ExpectedMailboxVersion))
				if err != nil {
					// Storage can report an uncertain commit: the mailbox
					// settles it later.
					answer := emailManagementErrorReply(err)
					answer.Unknown = answer.Status >= http.StatusInternalServerError
					return answer
				}
				return reply(http.StatusOK, map[string]any{"mailbox": emailmanagement.ProjectMailbox(mailbox)})
			}).Call(ctx, s, req, raw)
		}},
		"mail.intake.reconcile": {Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			return s.reconcileMailIntake(ctx, req, raw)
		}},

		// mailboxes
		"mail.mailboxes": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			if s.mailSync == nil {
				return replyCode(http.StatusServiceUnavailable, "mail_sync_unavailable")
			}
			boxes, err := s.mailSync.Mailboxes(ctx, p.OwnerID)
			if err != nil {
				return replyCode(http.StatusServiceUnavailable, "mail_sync_unavailable")
			}
			return reply(http.StatusOK, map[string]any{"mailboxes": boxes})
		}),
		"mail.sync": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, args mailSyncArgs) domainReply {
			if s.mailSync == nil {
				return reply(http.StatusServiceUnavailable, map[string]any{"code": "mail_sync_unavailable"})
			}
			if args.Cursor == nil || args.Limit == nil {
				return mailSyncErrorReply(mailsync.ErrInvalid)
			}
			out, err := s.mailSync.Sync(ctx, p.OwnerID, args.Mailbox, *args.Cursor, *args.Limit)
			if err != nil {
				return mailSyncErrorReply(err)
			}
			return reply(http.StatusOK, out)
		}),
		"mail.attachment": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailAttachmentArgs) domainReply {
			mail, found, err := m.ClientSyncMail(ctx, p.OwnerID, args.Mail)
			if err != nil || !found || mail.MailboxID != args.Mailbox || args.Part == "" || args.Part == "original" {
				return replyCode(http.StatusNotFound, "mail_attachment_not_found")
			}
			answer := mailFileReply(ctx, m, p.OwnerID, args.Mail, args.Part)
			if answer.failed() && answer.Status != http.StatusRequestEntityTooLarge {
				return replyCode(http.StatusNotFound, "mail_attachment_unavailable")
			}
			return answer
		}),

		// the mail window's reads
		"mail.message": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailArgs) domainReply {
			out, err := m.Message(ctx, p.OwnerID, args.Mail)
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.conversations.list": mailList(func(a emailListArgs) emailListArgs { return a }, func(ctx context.Context, m *emailmanagement.Service, q store.EmailQuery, _ emailListArgs) (any, error) {
			return m.QueryConversations(ctx, q)
		}),
		"mail.conversations.get": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailConversationArgs) domainReply {
			out, err := m.Conversation(ctx, p.OwnerID, args.Conversation)
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.conversations.messages": mailList(func(a mailConversationMessagesArgs) emailListArgs { return a.emailListArgs }, func(ctx context.Context, m *emailmanagement.Service, q store.EmailQuery, args mailConversationMessagesArgs) (any, error) {
			q.ConversationID = args.Conversation
			return m.Messages(ctx, q)
		}),
		"mail.pending": mailList(func(a emailListArgs) emailListArgs { return a }, func(ctx context.Context, m *emailmanagement.Service, q store.EmailQuery, _ emailListArgs) (any, error) {
			return m.Pending(ctx, q)
		}),
		"mail.notifications": mailList(func(a mailRoutedArgs) emailListArgs { return a.emailListArgs }, func(ctx context.Context, m *emailmanagement.Service, q store.EmailQuery, args mailRoutedArgs) (any, error) {
			return emailRoutedMessages(ctx, m, q, true, args.Subtype, args.Validity)
		}),
		"mail.interaction": mailList(func(a mailRoutedArgs) emailListArgs { return a.emailListArgs }, func(ctx context.Context, m *emailmanagement.Service, q store.EmailQuery, args mailRoutedArgs) (any, error) {
			return emailRoutedMessages(ctx, m, q, false, args.Subtype, args.Validity)
		}),
		"mail.sent_sources": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args emailListArgs) domainReply {
			q, err := args.query(p.OwnerID, 30)
			if err != nil {
				return emailComposeErrorReply(err)
			}
			return mailComposeOutcome(m.SentSources(ctx, q))
		}),
		"mail.sender_rules.list": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args emailListArgs) domainReply {
			q, err := args.query(p.OwnerID, 30)
			if err != nil {
				return emailManagementErrorReply(err)
			}
			rules, err := m.SenderRules(ctx, q)
			return mailOutcome(http.StatusOK, map[string]any{"rules": rules, "next_cursor": emailRuleNextCursor(rules, q.Limit)}, err)
		}),
		"mail.verification": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailArgs) domainReply {
			out, err := m.Verification(ctx, p.OwnerID, args.Mail)
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.render_preview": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailArgs) domainReply {
			out, err := m.RenderPreview(ctx, p.OwnerID, args.Mail)
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.file": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailFileArgs) domainReply {
			if len(args.PartID) > 256 {
				return replyCode(http.StatusBadRequest, "invalid_input")
			}
			part := args.PartID
			if part == "" {
				part = "original"
			}
			return mailFileReply(ctx, m, p.OwnerID, args.Mail, part)
		}),
		"mail.presentations.get": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailPresentationsArgs) domainReply {
			if len(args.TargetIDs) > 100 {
				return replyCode(http.StatusBadRequest, "invalid_input")
			}
			out, err := m.Presentations(ctx, store.EmailPresentationQuery{OwnerID: p.OwnerID, TargetKind: args.TargetKind, TargetIDs: args.TargetIDs, Language: args.Language})
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.compose.capabilities": mailManagement(false, func(_ context.Context, _ *Server, m *emailmanagement.Service, _ requestPrincipal, _ struct{}) domainReply {
			return reply(http.StatusOK, m.ComposeCapabilities())
		}),
		"mail.sync.status": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, _ struct{}) domainReply {
			out, err := m.Status(ctx, p.OwnerID)
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.sync.warnings": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailWarningsArgs) domainReply {
			q := store.EmailQuery{OwnerID: p.OwnerID, MailboxID: strings.TrimSpace(args.MailboxID), After: args.Cursor, Limit: 30}
			if args.Limit != 0 {
				q.Limit = args.Limit
			}
			out, err := m.SyncWarnings(ctx, q)
			return mailOutcome(http.StatusOK, out, err)
		}),

		// the composer
		"mail.drafts.list": mailManagement(false, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailDraftsListArgs) domainReply {
			if args.Draft != "" {
				rows, err := m.Drafts(ctx, p.OwnerID, args.Draft)
				if err != nil {
					return emailManagementErrorReply(err)
				}
				return reply(http.StatusOK, rows[0])
			}
			q, err := args.emailListArgs.query(p.OwnerID, 50)
			if err != nil {
				return emailComposeErrorReply(err)
			}
			return mailComposeOutcome(m.DraftPage(ctx, q))
		}),
		"mail.drafts.save": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailDraftSaveArgs) domainReply {
			if args.Draft != "" {
				if args.ID != "" && args.ID != args.Draft {
					return emailManagementErrorReply(emailmanagement.ErrInvalidInput)
				}
				args.ID = args.Draft
			}
			attachments := make([]app.EmailSendAttachment, len(args.Attachments))
			for i, attachment := range args.Attachments {
				attachments[i] = app.EmailSendAttachment{LocalFileID: attachment.LocalFileID, Object: attachment.Object}
			}
			ctx = emailmanagement.WithAttachmentObjects(ctx, true, mailAttachmentObjects{})
			return mailComposeOutcome(m.SaveDraft(ctx, p.OwnerID, store.EmailDraft{Attachments: attachments, ID: args.ID, MailboxID: args.MailboxID, Mode: args.Mode, ReplyMailID: args.ReplyMailID, To: args.To, CC: args.CC, Subject: args.Subject, Body: args.Body}, args.ExpectedVersion))
		}),
		"mail.send":        mailManagement(true, mailDraftSend),
		"mail.drafts.send": mailManagement(true, mailDraftSend),
		"mail.drafts.reconcile": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailDraftReconcileArgs) domainReply {
			if args.SentMailID != "" {
				return mailComposeOutcome(m.ConfirmDraftSource(ctx, p.OwnerID, args.Draft, args.SentMailID))
			}
			return mailComposeOutcome(m.ReconcileDraft(ctx, p.OwnerID, args.Draft))
		}),
		"mail.drafts.resolve": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailDraftResolveArgs) domainReply {
			return mailComposeOutcome(m.ResolveDraft(ctx, p.OwnerID, args.Draft, args.ExpectedVersion, args.Resolution))
		}),
		"mail.replies.polish": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailPolishArgs) domainReply {
			return mailComposeOutcome(m.PolishReply(ctx, p.OwnerID, emailmanagement.ReplyPolishRequest{DraftID: args.ID, MailID: args.MailID, Instruction: args.Instruction, OutputLanguage: args.Language}))
		}),

		// the mail window's changes
		"mail.conversations.rename": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailRenameArgs) domainReply {
			out, err := m.RenameConversation(ctx, store.EmailConversationRename{EmailCommand: store.EmailCommand{OwnerID: p.OwnerID, CommandKey: args.CommandKey}, ConversationID: args.Conversation, Title: args.Title, ExpectedVersion: args.ExpectedVersion})
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.conversations.delete": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailDeleteArgs) domainReply {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			out, err := m.DeleteConversation(ctx, store.EmailConversationDelete{EmailCommand: store.EmailCommand{OwnerID: p.OwnerID, CommandKey: args.CommandKey}, ConversationID: args.Conversation, ExpectedVersion: args.ExpectedVersion})
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.source.cleanup": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailCleanupArgs) domainReply {
			// A mailbox-wide or owner-wide cleanup loops over bounded batches.
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			out, err := m.CleanupSource(ctx, p.OwnerID, emailmanagement.CleanupRequest{Scope: args.Scope, MailID: args.MailID, MailboxID: args.MailboxID, Date: args.Date, CommandKey: args.CommandKey})
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.classification": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailClassificationArgs) domainReply {
			out, err := m.ChangeClassification(ctx, store.EmailClassificationOverride{EmailCommand: store.EmailCommand{OwnerID: p.OwnerID, CommandKey: args.CommandKey}, MailID: args.Mail, Entry: args.Entry, ExpectedVersion: args.ExpectedVersion, RememberSender: args.RememberSender, ExpectedRuleVersion: args.ExpectedRuleVersion})
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.assignment": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailAssignmentArgs) domainReply {
			out, err := m.ChangeAssignment(ctx, store.EmailManualAssignment{EmailCommand: store.EmailCommand{OwnerID: p.OwnerID, CommandKey: args.CommandKey}, MailID: args.Mail, ConversationID: args.ConversationID, Title: args.Title, ExpectedVersion: args.ExpectedVersion})
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.sender_rules.update": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailSenderRuleArgs) domainReply {
			out, err := m.ChangeSenderRule(ctx, store.EmailSenderRuleCommand{EmailCommand: store.EmailCommand{OwnerID: p.OwnerID, CommandKey: args.CommandKey}, RuleID: args.Rule, Entry: args.Entry, Enabled: args.Enabled, ExpectedVersion: args.ExpectedVersion})
			return mailOutcome(http.StatusOK, map[string]any{"rule": out}, err)
		}),
		"mail.presentations.ensure": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailPresentationsArgs) domainReply {
			out, err := m.EnsurePresentations(ctx, store.EmailPresentationQuery{OwnerID: p.OwnerID, TargetKind: args.TargetKind, TargetIDs: args.TargetIDs, Language: args.Language}, args.Retry)
			return mailOutcome(http.StatusAccepted, out, err)
		}),
		"mail.sync.acknowledge": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailWarningAckArgs) domainReply {
			out, err := m.AcknowledgeSyncWarning(ctx, p.OwnerID, args.MailboxID, args.Warning)
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.sync.request": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailboxArgs) domainReply {
			out, err := m.Sync(ctx, p.OwnerID, args.MailboxID)
			return mailOutcome(http.StatusAccepted, out, err)
		}),
		"mail.viewed": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailViewedArgs) domainReply {
			out, err := m.View(ctx, p.OwnerID, args.MailIDs)
			return mailOutcome(http.StatusOK, out, err)
		}),
		"mail.reanalyze": mailManagement(true, func(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailArgs) domainReply {
			out, err := m.Reanalyze(ctx, p.OwnerID, args.Mail)
			return mailOutcome(http.StatusAccepted, out, err)
		}),
	})
}

// mailDraftSend sends a saved draft once: the draft's own idempotency key
// guards the provider send, and the link receipt guards the call.
func mailDraftSend(ctx context.Context, _ *Server, m *emailmanagement.Service, p requestPrincipal, args mailDraftSendArgs) domainReply {
	ctx = emailmanagement.WithAttachmentObjects(ctx, true, mailAttachmentObjects{})
	return mailComposeOutcome(m.SendDraft(ctx, p.OwnerID, args.Draft, args.ExpectedVersion, args.IdempotencyKey))
}

// The resolver is deliberately stateless: every read uses the current request's
// verified installation/authorization binding, including after a draft restart.
type mailAttachmentObjects struct{}

var _ emailmanagement.AttachmentObjectReader = mailAttachmentObjects{}

func (mailAttachmentObjects) ReadAttachmentObject(ctx context.Context, owner string, object app.EmailAttachmentObject, limit int64) (app.EmailAttachmentObject, []byte, error) {
	access, ok := linkDomainObjectsFrom(ctx)
	ref := domainObjectRef(object)
	if !ok || owner != access.objectOwner() || ref.Purpose != app.EmailSendAttachmentPurpose || ref.Validate() != nil {
		return app.EmailAttachmentObject{}, nil, errLinkDomainObjectUnavailable
	}
	actual, err := access.describeObject(ref.ObjectID, ref.Version)
	if err != nil || actual.Purpose != app.EmailSendAttachmentPurpose || actual.Size != ref.Size || actual.SHA256 != ref.SHA256 || (ref.Name != "" && ref.Name != actual.Name) || (ref.MediaType != "" && ref.MediaType != actual.MediaType) || (ref.ExpiresAt != "" && ref.ExpiresAt != actual.ExpiresAt) {
		return app.EmailAttachmentObject{}, nil, errLinkDomainObjectUnavailable
	}
	raw, err := access.readObject(actual, app.EmailSendAttachmentPurpose, limit)
	if err != nil {
		return app.EmailAttachmentObject{}, nil, errLinkDomainObjectUnavailable
	}
	// An expiry during the read invalidates admission too.
	current, err := access.describeObject(actual.ObjectID, actual.Version)
	if err != nil || current != actual {
		return app.EmailAttachmentObject{}, nil, errLinkDomainObjectUnavailable
	}
	return app.EmailAttachmentObject(actual), raw, nil
}
