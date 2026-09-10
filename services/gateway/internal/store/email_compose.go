package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"strings"
	"time"
)

// EmailDraft is owner-isolated. A frozen snapshot is never edited after Send.
type EmailDraft struct {
	ConfirmationSource string                `json:"confirmation_source,omitempty"`
	TimelineMailID     string                `json:"timeline_mail_id,omitempty"`
	ReplyTarget        *app.EmailReplyTarget `json:"reply_target,omitempty"`
	ReplyInputVersion  int64                 `json:"reply_input_version,omitempty"`
	SentMailID         string                `json:"sent_mail_id,omitempty"`
	ReconciledAt       *time.Time            `json:"reconciled_at,omitempty"`
	ID                 string                `json:"id"`
	OwnerID            string                `json:"owner_id"`
	Version            int64                 `json:"version"`
	MailboxID          string                `json:"mailbox_id"`
	MailboxGeneration  int64                 `json:"mailbox_generation"`
	Mode               string                `json:"mode"`
	ReplyMailID        string                `json:"reply_mail_id,omitempty"`
	ConversationID     string                `json:"conversation_id,omitempty"`
	To                 []string              `json:"to"`
	CC                 []string              `json:"cc"`
	Subject            string                `json:"subject"`
	Body               string                `json:"body"`
	State              string                `json:"state"`
	ErrorCode          string                `json:"error_code,omitempty"`
	SendKey            string                `json:"send_key,omitempty"`
	Snapshot           *EmailDraftSnapshot   `json:"snapshot,omitempty"`
	Receipt            *app.EmailSendResult  `json:"receipt,omitempty"`
	UpdatedAt          time.Time             `json:"updated_at"`
}
type EmailDraftSnapshot struct {
	ReplyTarget  *app.EmailReplyTarget `json:"reply_target,omitempty"`
	State        string                `json:"state"`
	ErrorCode    string                `json:"error_code,omitempty"`
	Receipt      *app.EmailSendResult  `json:"receipt,omitempty"`
	UpdatedAt    time.Time             `json:"updated_at"`
	InvocationID string                `json:"invocation_id"`
	Version      int64                 `json:"version"`
	MailboxID    string                `json:"mailbox_id"`
	To           []string              `json:"to"`
	CC           []string              `json:"cc"`
	Subject      string                `json:"subject"`
	Body         string                `json:"body"`
	Mode         string                `json:"mode"`
	ReplyMailID  string                `json:"reply_mail_id,omitempty"`
}
type EmailDraftCommand struct {
	OwnerID         string
	Action          string
	Draft           EmailDraft
	ExpectedVersion int64
	SendKey         string
	ErrorCode       string
	Receipt         *app.EmailSendResult
}
type EmailDraftResult struct {
	Draft   EmailDraft `json:"draft"`
	Execute bool       `json:"-"`
}
type EmailComposeRepository interface {
	ChangeEmailDraft(context.Context, EmailDraftCommand) (EmailDraftResult, error)
	ListEmailDrafts(context.Context, string, string) ([]EmailDraft, error)
}

func changeEmailDraft(e *emailEngine, c EmailDraftCommand) (EmailDraftResult, error) {
	d, ok := emailGet[EmailDraft](e, "draft", c.Draft.ID)
	if c.Draft.ID == "" || len(c.Draft.ID) > 128 || strings.ContainsAny(c.Draft.ID, "\x00\r\n") {
		return EmailDraftResult{}, errEmailInvalid
	}
	switch c.Action {
	case "save":
		if (!ok && c.ExpectedVersion != 0) || (ok && d.Version != c.ExpectedVersion) {
			return EmailDraftResult{}, errEmailConflict
		}
		if ok && d.State != "draft" && d.State != "failed" {
			return EmailDraftResult{}, errEmailConflict
		}
		d = c.Draft
		d.OwnerID = e.owner
		d.Version = c.ExpectedVersion + 1
		d.State = "draft"
		d.SendKey = ""
		d.Snapshot = nil
		d.Receipt = nil
		d.ErrorCode = ""
	case "begin":
		if !ok {
			return EmailDraftResult{}, errEmailNotFound
		}
		if c.SendKey == "" || len(c.SendKey) > 128 || strings.ContainsAny(c.SendKey, "\x00\r\n") {
			return EmailDraftResult{}, errEmailInvalid
		}
		if d.SendKey == c.SendKey {
			return EmailDraftResult{Draft: d}, nil
		}
		// A key can never be reused, even following a definite failure and draft edit.
		if _, used := emailGet[EmailDraftSnapshot](e, "send_snapshot", emailID(d.ID, c.SendKey)); used {
			return EmailDraftResult{}, errEmailConflict
		}
		if d.Version != c.ExpectedVersion || (d.State != "draft" && d.State != "failed") {
			return EmailDraftResult{}, errEmailConflict
		}
		snapshot := EmailDraftSnapshot{State: "sending", UpdatedAt: e.now, InvocationID: app.NewID("email_send"), Version: d.Version, MailboxID: d.MailboxID, To: d.To, CC: d.CC, Subject: d.Subject, Body: d.Body, Mode: d.Mode, ReplyMailID: d.ReplyMailID, ReplyTarget: d.ReplyTarget}
		emailPut(e, "send_snapshot", emailID(d.ID, c.SendKey), d.ID, "", "", "", e.now.String(), snapshot)
		d.Snapshot = &snapshot
		d.SendKey = c.SendKey
		d.State = "sending"
		d.Version++
		d.ErrorCode = ""
		d.Receipt = nil
	case "confirm_source":
		if ok && d.SentMailID != "" {
			if d.SentMailID == c.Draft.SentMailID {
				return EmailDraftResult{Draft: d}, nil
			}
			return EmailDraftResult{}, errEmailConflict
		}
		if !ok {
			return EmailDraftResult{}, errEmailNotFound
		}
		if err := emailConfirmSentSource(e, &d, c.Draft.SentMailID); err != nil {
			return EmailDraftResult{}, err
		}
	case "reconcile":
		if !ok {
			return EmailDraftResult{}, errEmailNotFound
		}
		if d.SentMailID != "" || d.Receipt == nil || d.Receipt.ProviderMessageID == "" {
			return EmailDraftResult{Draft: d}, nil
		}
		box, exists := emailGet[app.EmailMailbox](e, "mailbox", d.MailboxID)
		if !exists || box.Provider != d.Receipt.Provider {
			return EmailDraftResult{}, errEmailConflict
		}
		// Exact provider identity gives the canonical owner/mailbox source key.
		candidate, found := emailGet[app.EmailMail](e, "mail", emailID(e.owner, d.MailboxID, d.Receipt.ProviderMessageID))
		if !found || candidate.Direction != "sent" || candidate.CaptureState != app.EmailCaptureComplete || candidate.CaptureID == "" {
			return EmailDraftResult{Draft: d}, e.err
		}
		d.SentMailID = candidate.ID
		at := e.now
		d.ReconciledAt = &at
		d.Version++

	case "finish", "resolve":
		if ok && c.Action == "resolve" && d.State == "sent" && d.SendKey == c.SendKey && string(emailJSON(d.Receipt)) == string(emailJSON(c.Receipt)) {
			return EmailDraftResult{Draft: d}, nil
		}
		if !ok {
			return EmailDraftResult{}, errEmailNotFound
		}
		if d.SendKey != c.SendKey || (d.State != "sending" && !(c.Action == "resolve" && (d.State == "unknown" || d.State == "sent"))) {
			return EmailDraftResult{}, errEmailConflict
		}
		if c.Draft.State != "sent" && c.Draft.State != "failed" && c.Draft.State != "unknown" {
			return EmailDraftResult{}, errEmailInvalid
		}
		d.State = c.Draft.State
		d.ErrorCode = c.ErrorCode
		d.Receipt = c.Receipt
		if c.Receipt != nil {
			d.ConfirmationSource = "provider_receipt"
		}
		attempt, found := emailGet[EmailDraftSnapshot](e, "send_snapshot", emailID(d.ID, c.SendKey))
		if !found {
			return EmailDraftResult{}, errEmailCorrupt
		}
		attempt.State = d.State
		attempt.ErrorCode = d.ErrorCode
		attempt.Receipt = d.Receipt
		attempt.UpdatedAt = e.now
		emailPut(e, "send_snapshot", emailID(d.ID, c.SendKey), d.ID, "", attempt.State, "", emailOrder(e.now, d.ID), attempt)
		d.Snapshot = &attempt
		if err := emailPublishLocalSend(e, &d); err != nil {
			return EmailDraftResult{}, err
		}
		d.Version++
	default:
		return EmailDraftResult{}, errEmailInvalid
	}
	d.UpdatedAt = e.now
	emailPut(e, "draft", d.ID, d.MailboxID, d.ConversationID, d.State, d.Subject+" "+strings.Join(d.To, " "), emailOrder(d.UpdatedAt, d.ID), d)
	return EmailDraftResult{Draft: d, Execute: c.Action == "begin"}, e.err
}
func listEmailDrafts(e *emailEngine, id string) ([]EmailDraft, error) {
	if id != "" {
		d, ok := emailGet[EmailDraft](e, "draft", id)
		if !ok {
			return nil, errEmailNotFound
		}
		return []EmailDraft{d}, e.err
	}
	return emailList[EmailDraft](e, emailRowsQuery{Kind: "draft", Limit: 100}), e.err
}

type EmailDraftPage struct {
	Items      []EmailDraft `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

func emailDraftPage(e *emailEngine, q EmailQuery) (EmailDraftPage, error) {
	q.Entry = "draft"
	var err error
	q, err = emailScopedCursor(q, e.now)
	if err != nil {
		return EmailDraftPage{}, err
	}
	limit := emailLimit(q.Limit)
	items := emailList[EmailDraft](e, emailRowsQuery{Kind: "draft", Parent: q.MailboxID, Search: q.Search, After: q.After, Limit: limit + 1})
	out := EmailDraftPage{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = emailEncodeScopeCursor(q, emailOrder(last.UpdatedAt, last.ID))
	}
	return out, e.err
}
