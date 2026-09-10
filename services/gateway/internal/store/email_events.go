package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"strings"
	"time"
)

const EmailEventPolicyVersion = "email-management-v4-source-events"
const emailEventSuspended = "email_event_policy_suspended"

type EmailEventPolicy struct {
	Version     string    `json:"version"`
	ActivatedAt time.Time `json:"activated_at"`
}
type EmailManualAssignment struct {
	EmailCommand
	MailID, ConversationID, Title string
	ExpectedVersion               int64
}
type EmailConversationRename struct {
	EmailCommand
	ConversationID, Title string
	ExpectedVersion       int64
}

func emailEvents(e *emailEngine) bool {
	p, ok := emailGet[EmailEventPolicy](e, "counter", "event_policy")
	if ok && p.Version != EmailEventPolicyVersion {
		e.err = errEmailConflict
	}
	return ok
}
func emailActivateEvents(e *emailEngine, c EmailCommand) (EmailEventPolicy, error) {
	p, ok := emailGet[EmailEventPolicy](e, "counter", "event_policy")
	if ok {
		if p.Version != EmailEventPolicyVersion {
			return p, errEmailConflict
		}
		return p, e.err
	}
	p = EmailEventPolicy{Version: EmailEventPolicyVersion, ActivatedAt: e.now}
	emailPut(e, "counter", "event_policy", "", "", "", "", "event_policy", p)
	return p, e.err
}
func emailDisabledAnalysis(kind string) bool {
	return containsEmail([]string{app.EmailJobMessageSummary, app.EmailJobConversationSummary, app.EmailJobRelationshipCheck, app.EmailJobPresentation}, kind)
}
func EmailThreadCursor(t app.EmailProviderThread) string { return emailOrder(t.LastCheckedAt, t.ID) }

// A durable cursor rewrites only bounded pages; lease/policy fences apply from
// activation, including while historical queues are still being migrated.
func emailMigrateEvents(e *emailEngine, limit int) (int, bool, error) {
	type progress struct {
		Cursor string
		Done   bool
		Phase  string
	}
	p, _ := emailGet[progress](e, "counter", "event_migration")
	if p.Done {
		return 0, false, e.err
	}
	if p.Phase == "" {
		p.Phase = "job"
	}
	rows, err := e.db.list(emailRowsQuery{Kind: p.Phase, After: p.Cursor, Limit: limit})
	if err != nil {
		return 0, false, err
	}
	for _, row := range rows {
		switch p.Phase {
		case "job":
			j, _ := emailGet[app.EmailJob](e, "job", row.ID)
			if emailDisabledAnalysis(j.Kind) && j.State != app.EmailJobSucceeded && j.State != app.EmailJobFailed {
				j.State = app.EmailJobPaused
				j.ErrorCode = emailEventSuspended
				j.LeaseToken = ""
				j.LeaseExpiresAt = time.Time{}
				emailSaveJob(e, j)
			}
		case "mail":
			m, _ := emailGet[app.EmailMail](e, "mail", row.ID)
			emailSaveMail(e, m)
			if m.RepresentationID != "" {
				if m.Classification == nil {
					_, err = emailRequest(e, EmailJobRequest{Kind: app.EmailJobClassification, TargetID: m.ID, Dependencies: []string{}})
				} else if m.ConversationID == "" {
					_, err = emailRequest(e, EmailJobRequest{Kind: app.EmailJobAssignment, TargetID: m.ID, Dependencies: []string{}})
				}
			}
		case "conversation":
			c, _ := emailGet[app.EmailConversation](e, "conversation", row.ID)
			emailRecountEvent(e, &c)
			emailSaveConversation(e, c)
		}
		if err != nil {
			return 0, false, err
		}
		p.Cursor = row.Sort
	}
	if len(rows) < limit {
		p.Cursor = ""
		switch p.Phase {
		case "job":
			p.Phase = "mail"
		case "mail":
			p.Phase = "conversation"
		default:
			p.Done = true
		}
	}
	emailPut(e, "counter", "event_migration", "", "", "", "", "event_migration", p)
	return len(rows), !p.Done, e.err
}
func emailRecountEvent(e *emailEngine, c *app.EmailConversation) {
	q := emailRowsQuery{Kind: "mail", Related: c.ID, Limit: 100}
	c.MemberCount = 0
	c.UnseenCount = 0
	c.Participants = nil
	c.MailboxIDs = nil
	for {
		rows := emailList[app.EmailMail](e, q)
		for _, m := range rows {
			c.MemberCount++
			if _, ok := emailGet[app.EmailViewReceipt](e, "view", m.ID); !ok {
				c.UnseenCount++
			}
			c.Participants = append(c.Participants, m.Participants...)
			c.MailboxIDs = append(c.MailboxIDs, m.MailboxID)
		}
		if len(rows) < q.Limit || e.err != nil {
			break
		}
		q.After = emailOrder(rows[len(rows)-1].SourceTime, rows[len(rows)-1].ID)
	}
	c.Participants = emailUnique(c.Participants)
	c.MailboxIDs = emailUnique(c.MailboxIDs)
}
func emailManualAssignment(e *emailEngine, c EmailManualAssignment) (app.EmailMail, error) {
	m, err := emailMail(e, c.MailID)
	if err != nil {
		return m, err
	}
	if !emailEvents(e) || m.InputVersion != c.ExpectedVersion {
		return m, errEmailConflict
	}
	if m.RepresentationID == "" && m.LocalSendID == "" {
		return m, errEmailInvalid
	}
	old := m.ConversationID
	var target app.EmailConversation
	if c.ConversationID == "" {
		title := strings.TrimSpace(c.Title)
		if title == "" || len(title) > 512 {
			return m, errEmailInvalid
		}
		target = app.EmailConversation{ID: emailID(e.owner, "event", c.CommandKey), OwnerID: e.owner, Title: title, TitleState: "ready", CreatedAt: e.now}
	} else {
		var ok bool
		target, ok = emailGet[app.EmailConversation](e, "conversation", c.ConversationID)
		if !ok {
			return m, errEmailNotFound
		}
	}
	if m.Verification != nil && m.Verification.Code != "" && strings.Contains(target.Title, m.Verification.Code) {
		return m, errEmailInvalid
	}
	m.ConversationID = target.ID
	m.AssignmentSource = "manual"
	m.AssignmentRevision++
	m.InputVersion++
	m.AssignmentState = app.EmailAssignmentAssigned
	emailSaveMail(e, m)
	for _, id := range emailUnique([]string{old, target.ID}) {
		var conv app.EmailConversation
		if id == target.ID {
			conv = target
		} else {
			conv, _ = emailGet[app.EmailConversation](e, "conversation", id)
		}
		emailRecountEvent(e, &conv)
		conv.MembershipVersion++
		conv.InputVersion++
		conv.UpdatedAt = e.now
		emailSaveConversation(e, conv)
		emailTouch(e, "members:"+id)
	}
	auditID := emailID("manual_assignment", c.CommandKey)
	emailPut(e, "decision", auditID, m.ID, m.ConversationID, "manual", "", emailOrder(e.now, auditID), struct {
		ID, MailID, PreviousConversationID, ConversationID, Source string
		Revision                                                   int64
		CreatedAt                                                  time.Time
	}{auditID, m.ID, old, m.ConversationID, "manual", m.AssignmentRevision, e.now})
	emailCounter(e, "assignment_epoch", true)
	emailTouch(e, "mapping:"+m.ID)
	emailTouch(e, "search")
	if m.MessageID != "" {
		emailTouch(e, "reply:"+m.MessageID)
	}
	return m, e.err
}
func emailRenameConversation(e *emailEngine, c EmailConversationRename) (app.EmailConversation, error) {
	conv, ok := emailGet[app.EmailConversation](e, "conversation", c.ConversationID)
	if !ok {
		return conv, errEmailNotFound
	}
	title := strings.TrimSpace(c.Title)
	if title == "" || len(title) > 512 {
		return conv, errEmailInvalid
	}
	if conv.InputVersion != c.ExpectedVersion {
		return conv, errEmailConflict
	}
	if title == conv.Title {
		return conv, e.err
	}
	q := emailRowsQuery{Kind: "mail", Related: conv.ID, Limit: 100}
	for {
		members := emailList[app.EmailMail](e, q)
		for _, m := range members {
			if m.Verification != nil && m.Verification.Code != "" && strings.Contains(title, m.Verification.Code) {
				return conv, errEmailInvalid
			}
		}
		if len(members) < q.Limit || e.err != nil {
			break
		}
		last := members[len(members)-1]
		q.After = emailOrder(last.SourceTime, last.ID)
	}
	if e.err != nil {
		return conv, e.err
	}
	before := conv.Title
	conv.Title = title
	conv.TitleState = "ready"
	conv.InputVersion++
	emailSaveConversation(e, conv)
	id := emailID("rename", c.CommandKey)
	emailPut(e, "decision", id, conv.ID, conv.ID, "manual", "", emailOrder(e.now, id), struct {
		ID, Before, After string
		CreatedAt         time.Time
	}{id, before, title, e.now})
	return conv, e.err
}

func EmailProviderThreadID(mailboxID, providerThreadID string) string {
	return emailID(mailboxID, providerThreadID)
}
