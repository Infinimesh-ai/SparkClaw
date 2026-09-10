package emailmanagement

import (
	"context"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrComposeUnavailable = errors.New("email_compose_unavailable")
var ErrNativeReplyUnavailable = errors.New("email_native_reply_unavailable")
var ErrMultipleRecipientsUnavailable = errors.New("email_multiple_recipients_unavailable")

type ComposeBrowser interface {
	Admit(context.Context, string, string) (emailautomation.AdmissionResult, error)
	SendForOwner(context.Context, string, app.EmailSendRequest) (app.EmailSendResult, error)
	ReconcileSendForOwner(context.Context, string, app.EmailSendRequest) (app.EmailSendResult, error)
}
type ComposeCapabilities struct {
	Compose     bool   `json:"compose"`
	Reply       bool   `json:"reply"`
	ReplyAll    bool   `json:"reply_all"`
	CC          bool   `json:"cc"`
	MaxTo       int    `json:"max_to"`
	ReplyReason string `json:"reply_reason"`
}

func (s *Service) ComposeCapabilities() ComposeCapabilities {
	_, browser := s.browser.(ComposeBrowser)
	return ComposeCapabilities{Compose: browser, Reply: browser, ReplyAll: browser, CC: browser, MaxTo: 100}
}
func (s *Service) Drafts(ctx context.Context, owner, id string) ([]store.EmailDraft, error) {
	r := s.repository
	return r.ListEmailDrafts(ctx, owner, id)
}
func composeAddress(value string) (string, error) {
	a, err := mail.ParseAddress(value)
	if err != nil {
		return "", ErrInvalidInput
	}
	p := strings.LastIndex(a.Address, "@")
	if p <= 0 {
		return "", ErrInvalidInput
	}
	return a.Address[:p+1] + strings.ToLower(a.Address[p+1:]), nil
}
func (s *Service) SaveDraft(ctx context.Context, owner string, d store.EmailDraft, expected int64) (store.EmailDraft, error) {
	r := s.repository
	if d.ID == "" {
		d.ID = app.NewID("email_draft")
	}
	if d.Mode == "" {
		d.Mode = "compose"
	}
	if d.Mode != "compose" && d.Mode != "reply" && d.Mode != "reply_all" || utf8.RuneCountInString(d.Subject) > 998 || len(d.Body) > 200<<10 || strings.ContainsAny(d.Subject, "\r\n\x00") || strings.ContainsRune(d.Body, 0) || len(d.To) > 100 || len(d.CC) > 100 {
		return d, ErrInvalidInput
	}
	if d.Mode != "compose" {
		original, exists, err := s.repository.GetEmailMail(ctx, owner, d.ReplyMailID)
		if err != nil {
			return d, err
		}
		if !exists {
			return d, ErrNotFound
		}
		d.ConversationID = original.ConversationID
		d.ReplyInputVersion = original.InputVersion
		originalBox, exists, err := s.repository.GetEmailMailbox(ctx, owner, original.MailboxID)
		if err != nil {
			return d, err
		}
		if !exists {
			return d, ErrNotFound
		}
		d.ReplyTarget = &app.EmailReplyTarget{EmailCaptureTarget: app.EmailCaptureTarget{AccountAddress: originalBox.Address, ProviderMessageID: original.ProviderMessageID, ProviderSelectionID: original.ProviderSelectionID, ProviderThreadID: original.ProviderThreadID, Folder: original.Folder}, Subject: original.Subject}
		if d.MailboxID == "" {
			d.MailboxID = original.MailboxID
		}
		if expected == 0 && len(d.To) == 0 {
			rep, exists, err := s.repository.GetEmailRepresentation(ctx, owner, original.RepresentationID)
			if err != nil {
				return d, err
			}
			if !exists {
				return d, ErrNotFound
			}
			recipients := rep.ReplyTo
			validReplyTo := len(recipients) > 0
			for _, value := range recipients {
				if _, err := composeAddress(value); err != nil {
					validReplyTo = false
					break
				}
			}
			if !validReplyTo {
				recipients = rep.From
			}
			d.To = append([]string{}, recipients...)
			if d.Mode == "reply_all" {
				d.To = append(d.To, rep.To...)
				d.CC = append([]string{}, rep.CC...)
			}
			if d.Subject == "" {
				d.Subject = rep.Subject
				if !strings.HasPrefix(strings.ToLower(d.Subject), "re:") {
					d.Subject = "Re: " + d.Subject
				}
			}
		}
	} else if d.ReplyMailID != "" || d.ConversationID != "" {
		return d, ErrInvalidInput
	}
	mailbox, exists, err := s.repository.GetEmailMailbox(ctx, owner, d.MailboxID)
	if err != nil {
		return d, err
	}
	if !exists || !mailbox.Active {
		return d, ErrNotFound
	}
	d.MailboxGeneration = mailbox.BindingGeneration
	// Reply-all excludes every known owner mailbox, using exact local-part identity.
	excluded := map[string]bool{}
	if d.Mode == "reply_all" {
		mailboxes, err := s.repository.ListEmailMailboxes(ctx, owner)
		if err != nil {
			return d, err
		}
		for _, m := range mailboxes {
			a, e := composeAddress(m.Address)
			if e == nil {
				excluded[a] = true
			}
		}
	}
	seen := map[string]bool{}
	clean := func(values []string) ([]string, error) {
		out := []string{}
		for _, value := range values {
			a, e := composeAddress(value)
			if e != nil {
				return nil, e
			}
			if !seen[a] && !excluded[a] {
				out = append(out, a)
				seen[a] = true
			}
		}
		return out, nil
	}
	d.To, err = clean(d.To)
	if err != nil {
		return d, err
	}
	d.CC, err = clean(d.CC)
	if err != nil {
		return d, err
	}
	out, err := r.ChangeEmailDraft(ctx, store.EmailDraftCommand{OwnerID: owner, Action: "save", Draft: d, ExpectedVersion: expected})
	return out.Draft, err
}
func (s *Service) SendDraft(ctx context.Context, owner, id string, expected int64, key string) (store.EmailDraft, error) {
	r := s.repository
	browser, ok := s.browser.(ComposeBrowser)
	if !ok {
		return store.EmailDraft{}, ErrComposeUnavailable
	}
	rows, err := r.ListEmailDrafts(ctx, owner, id)
	if err != nil {
		return store.EmailDraft{}, err
	}
	d := rows[0]
	// Replays observe durable state without another probe or browser side effect.
	if d.SendKey == key && key != "" {
		return d, nil
	}
	if len(d.To) == 0 || len(d.To)+len(d.CC) > 100 {
		return d, ErrInvalidInput
	}
	if d.Mode != "compose" {
		original, exists, err := s.repository.GetEmailMail(ctx, owner, d.ReplyMailID)
		if err != nil {
			return d, err
		}
		if !exists || original.MailboxID != d.MailboxID || original.InputVersion != d.ReplyInputVersion || d.ReplyTarget == nil || d.ReplyTarget.ProviderMessageID == "" || d.ReplyTarget.ProviderSelectionID == "" || original.CaptureID == "" {
			return d, ErrConflict
		}
	}
	if strings.TrimSpace(d.Subject) == "" || strings.TrimSpace(d.Body) == "" {
		return d, ErrInvalidInput
	}
	mailbox, exists, err := s.repository.GetEmailMailbox(ctx, owner, d.MailboxID)
	if err != nil {
		return d, err
	}
	if !exists || !mailbox.Active || mailbox.BindingGeneration != d.MailboxGeneration {
		return d, ErrConflict
	}
	binding, err := browser.Admit(ctx, owner, mailbox.Provider)
	if err != nil {
		return d, err
	}
	// Probe hints are masked; the send script proves this exact account address before effects.
	if binding.Provider != mailbox.Provider {
		return d, ErrConflict
	}
	claimed, err := r.ChangeEmailDraft(ctx, store.EmailDraftCommand{OwnerID: owner, Action: "begin", Draft: store.EmailDraft{ID: id}, ExpectedVersion: expected, SendKey: key})
	if err != nil {
		return d, err
	}
	if !claimed.Execute {
		return claimed.Draft, nil
	}
	receipt, sendErr := browser.SendForOwner(ctx, owner, app.EmailSendRequest{Provider: binding.Provider, Account: binding.Account, To: d.To, CC: d.CC, Mode: d.Mode, AccountAddress: mailbox.Address, ReplyTarget: d.ReplyTarget, Subject: d.Subject, Body: d.Body, InvocationID: claimed.Draft.Snapshot.InvocationID, BrowserCredentialGeneration: binding.BrowserCredentialGeneration, ProbeRevision: binding.ProbeRevision, ScriptRevision: binding.SendScriptRevision, SettingVersion: binding.SettingVersion})
	state, code := "sent", ""
	var proof *app.EmailSendResult
	if sendErr != nil {
		state = "unknown"
		code = "email_send_outcome_unknown"
		switch emailautomation.ErrorCode(sendErr) {
		case app.ToolErrorEmailPageContractChanged, app.ToolErrorEmailAdmissionStale, app.ToolErrorEmailNotConfigured, app.ToolErrorEmailInvalidInput, app.ToolErrorEmailDraftConflict, app.ToolErrorEmailDraftVerificationFailed, app.ToolErrorEmailSendControlUnverified:
			state = "failed"
			code = string(emailautomation.ErrorCode(sendErr))
		}
	} else {
		proof = &receipt
	}
	// Persist completion even if the HTTP client disconnected; an interrupted process leaves sending fenced.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	out, err := r.ChangeEmailDraft(finishCtx, store.EmailDraftCommand{OwnerID: owner, Action: "finish", Draft: store.EmailDraft{ID: id, State: state}, SendKey: key, ErrorCode: code, Receipt: proof})
	if err != nil {
		return claimed.Draft, err
	}
	return out.Draft, nil
}

// ReconcileDraft links an existing captured Sent source by proved provider identity only.
func (s *Service) ReconcileDraft(ctx context.Context, owner, id string) (store.EmailDraft, error) {
	rows, err := s.repository.ListEmailDrafts(ctx, owner, id)
	if err != nil {
		return store.EmailDraft{}, err
	}
	d := rows[0]
	if (d.State == "unknown" || d.State == "sending" || d.State == "sent" && d.Receipt != nil && d.Receipt.ProviderMessageID == "") && d.Snapshot != nil {
		browser, ok := s.browser.(ComposeBrowser)
		if !ok {
			return d, ErrComposeUnavailable
		}
		box, ok, err := s.repository.GetEmailMailbox(ctx, owner, d.MailboxID)
		if err != nil {
			return d, err
		}
		if !ok || !box.Active || box.BindingGeneration != d.MailboxGeneration {
			return d, ErrConflict
		}
		binding, err := browser.Admit(ctx, owner, box.Provider)
		if err != nil {
			return d, err
		}
		snap := d.Snapshot
		receipt, err := browser.ReconcileSendForOwner(ctx, owner, app.EmailSendRequest{Provider: binding.Provider, Account: binding.Account, Mode: "reconcile", AccountAddress: box.Address, To: snap.To, CC: snap.CC, Subject: snap.Subject, Body: snap.Body, ReplyTarget: snap.ReplyTarget, InvocationID: snap.InvocationID, BrowserCredentialGeneration: binding.BrowserCredentialGeneration, ProbeRevision: binding.ProbeRevision, ScriptRevision: binding.SendScriptRevision, SettingVersion: binding.SettingVersion})
		if err != nil {
			return d, err
		}
		if receipt.Status == "sent" {
			result, err := s.repository.ChangeEmailDraft(ctx, store.EmailDraftCommand{OwnerID: owner, Action: "resolve", Draft: store.EmailDraft{ID: id, State: "sent"}, SendKey: d.SendKey, Receipt: &receipt})
			if err != nil {
				return d, err
			}
			d = result.Draft
		}
	}
	out, err := s.repository.ChangeEmailDraft(ctx, store.EmailDraftCommand{OwnerID: owner, Action: "reconcile", Draft: store.EmailDraft{ID: id}})
	return out.Draft, err
}
func (s *Service) ConfirmDraftSource(ctx context.Context, owner, id, source string) (store.EmailDraft, error) {
	out, err := s.repository.ChangeEmailDraft(ctx, store.EmailDraftCommand{OwnerID: owner, Action: "confirm_source", Draft: store.EmailDraft{ID: id, SentMailID: source}})
	return out.Draft, err
}

func (s *Service) SentSources(ctx context.Context, q store.EmailQuery) (MessagesView, error) {
	q.Direction = "sent"
	q.RequireNativeCapture = true
	return s.Messages(ctx, q)
}

func (s *Service) DraftPage(ctx context.Context, q store.EmailQuery) (store.EmailDraftPage, error) {
	return s.repository.ListEmailDraftPage(ctx, q)
}
