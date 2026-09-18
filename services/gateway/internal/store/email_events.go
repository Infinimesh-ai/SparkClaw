package store

import (
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"sort"
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
type EmailConversationDelete struct {
	EmailCommand
	ConversationID  string `json:"conversation_id"`
	ExpectedVersion int64  `json:"expected_version"`
}
type EmailConversationDeleteResult struct {
	ConversationID string   `json:"conversation_id"`
	DeletedMails   int      `json:"deleted_mails"`
	ArtifactPaths  []string `json:"artifact_paths,omitempty"`
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
	return kind == app.EmailJobRelationshipCheck
}

// Event conversations no longer have a generated overview. Language-specific
// presentations remain valid for individual mails so the conversation can
// render a summary in the owner's current UI language, including for mail that
// predates that language selection.
func emailEventDisablesJob(e *emailEngine, kind, targetID string) bool {
	if emailDisabledAnalysis(kind) {
		return true
	}
	if kind != app.EmailJobPresentation {
		return false
	}
	presentation, ok := emailGet[app.EmailLocalizedPresentation](e, "presentation", targetID)
	return !ok || presentation.TargetKind != "mail"
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
			if emailEventDisablesJob(e, j.Kind, j.TargetID) && j.State != app.EmailJobSucceeded && j.State != app.EmailJobFailed {
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
				} else if !emailPatternVerification(m) && m.ConversationID == "" {
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

func emailDeleteConversation(e *emailEngine, c EmailConversationDelete) (EmailConversationDeleteResult, error) {
	out := EmailConversationDeleteResult{ConversationID: c.ConversationID}
	conv, ok := emailGet[app.EmailConversation](e, "conversation", c.ConversationID)
	if !ok {
		return out, errEmailNotFound
	}
	if conv.InputVersion != c.ExpectedVersion {
		return out, errEmailConflict
	}

	members := []app.EmailMail{}
	q := emailRowsQuery{Kind: "mail", Related: conv.ID, Limit: 100, IncludeSuperseded: true}
	for {
		page := emailList[app.EmailMail](e, q)
		members = append(members, page...)
		if e.err != nil || len(page) < q.Limit {
			break
		}
		last := page[len(page)-1]
		q.After = emailOrder(last.SourceTime, last.ID)
	}
	if e.err != nil {
		return out, e.err
	}

	drafts := []EmailDraft{}
	draftQuery := emailRowsQuery{Kind: "draft", Related: conv.ID, Limit: 100}
	for {
		rows, err := e.db.list(draftQuery)
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			var draft EmailDraft
			if err := json.Unmarshal(row.Data, &draft); err != nil {
				return out, errors.Join(errEmailCorrupt, err)
			}
			if draft.State == "sending" || draft.State == "unknown" {
				return out, errEmailConflict
			}
			drafts = append(drafts, draft)
		}
		if len(rows) < draftQuery.Limit {
			break
		}
		draftQuery.After = rows[len(rows)-1].Sort
	}

	ids := map[string]bool{conv.ID: true}
	artifactPaths := map[string]bool{}
	collectArtifacts := func(data []byte) error {
		var artifact struct {
			InputPath  string `json:"input_path"`
			OutputPath string `json:"output_path"`
		}
		if err := json.Unmarshal(data, &artifact); err != nil {
			return errors.Join(errEmailCorrupt, err)
		}
		for _, artifactPath := range []string{artifact.InputPath, artifact.OutputPath} {
			if artifactPath != "" {
				artifactPaths[artifactPath] = true
			}
		}
		return nil
	}
	for _, mail := range members {
		ids[mail.ID] = true
		if mail.RepresentationID != "" {
			rows, err := emailCollectRows(e, emailRowsQuery{Kind: "render_preview", Parent: mail.RepresentationID, Limit: 100})
			if err != nil {
				return out, err
			}
			for _, row := range rows {
				var preview app.EmailRenderPreview
				if err := json.Unmarshal(row.Data, &preview); err != nil {
					return out, errors.Join(errEmailCorrupt, err)
				}
				if preview.HTMLPath != "" {
					artifactPaths[preview.HTMLPath] = true
				}
				if preview.ArtifactPath != "" {
					artifactPaths[preview.ArtifactPath] = true
				}
			}
		}
		if mail.Classification != nil {
			for _, artifactPath := range []string{mail.Classification.InputPath, mail.Classification.OutputPath} {
				if artifactPath != "" {
					artifactPaths[artifactPath] = true
				}
			}
		}
	}
	presentationIDs := map[string]bool{}
	concernIDs := map[string]bool{}
	analysisTargetIDs := map[string]bool{}
	for targetID := range ids {
		rows, err := emailCollectRows(e, emailRowsQuery{Kind: "presentation", Related: targetID, Limit: 100})
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			presentationIDs[row.ID] = true
		}
		for _, kind := range []string{"decision", "concern", "summary"} {
			for _, query := range []emailRowsQuery{{Kind: kind, Parent: targetID, Limit: 100}, {Kind: kind, Related: targetID, Limit: 100}} {
				rows, err := emailCollectRows(e, query)
				if err != nil {
					return out, err
				}
				for _, row := range rows {
					if err := collectArtifacts(row.Data); err != nil {
						return out, err
					}
				}
			}
		}
		for _, query := range []emailRowsQuery{{Kind: "concern", Parent: targetID, Limit: 100}, {Kind: "target", Related: targetID, Limit: 100}} {
			rows, err := emailCollectRows(e, query)
			if err != nil {
				return out, err
			}
			for _, row := range rows {
				if row.Kind == "concern" {
					concernIDs[row.ID] = true
				} else {
					analysisTargetIDs[row.ID] = true
				}
			}
		}
	}
	for concernID := range concernIDs {
		emailDeleteRows(e, emailRowsQuery{Kind: "concern_link", Related: concernID, Limit: 100})
	}
	for targetID := range analysisTargetIDs {
		emailDeleteRows(e, emailRowsQuery{Kind: "dependency", Related: targetID, Limit: 100})
	}

	// Drafts contain reply bodies and recipients, so they are part of the
	// conversation rather than durable global history.
	for _, draft := range drafts {
		emailDeleteRows(e, emailRowsQuery{Kind: "send_snapshot", Parent: draft.ID, Limit: 100})
		emailDelete(e, "draft", draft.ID)
	}

	for _, mail := range members {
		for _, pair := range [][2]string{{"capture", mail.CaptureID}, {"representation", mail.RepresentationID}, {"context", mail.ContextID}, {"view", mail.ID}} {
			if pair[1] != "" {
				emailDelete(e, pair[0], pair[1])
			}
		}
		for _, kind := range []string{"capture", "representation", "render_preview", "context", "sync_failure", "decision", "concern", "summary", "presentation", "target"} {
			emailDeleteRows(e, emailRowsQuery{Kind: kind, Parent: mail.ID, Limit: 100})
			emailDeleteRows(e, emailRowsQuery{Kind: kind, Related: mail.ID, Limit: 100})
		}
		emailDelete(e, "mail", mail.ID)
	}

	// Conversation projections and audit/analysis rows must not retain message
	// bodies or generated summaries after the user removes the conversation.
	for _, kind := range []string{"decision", "concern", "concern_link", "summary", "presentation", "target"} {
		emailDeleteRows(e, emailRowsQuery{Kind: kind, Parent: conv.ID, Limit: 100})
		emailDeleteRows(e, emailRowsQuery{Kind: kind, Related: conv.ID, Limit: 100})
	}

	// Jobs address source targets through their JSON payload instead of a row
	// index. Scan the owner-bounded job set and remove only jobs for this scope.
	jobQuery := emailRowsQuery{Kind: "job", Limit: 100}
	for {
		rows, err := e.db.list(jobQuery)
		if err != nil {
			return out, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			var job app.EmailJob
			if err := json.Unmarshal(row.Data, &job); err != nil {
				return out, errors.Join(errEmailCorrupt, err)
			}
			if ids[job.TargetID] || presentationIDs[job.TargetID] {
				emailDelete(e, "job", row.ID)
			}
		}
		if len(rows) < jobQuery.Limit {
			break
		}
		jobQuery.After = rows[len(rows)-1].Sort
	}

	// Delete analysis dependencies that point at removed target records, plus
	// pending refresh intents and version references for the removed sources.
	for targetID := range ids {
		for _, ref := range []string{"mail:" + targetID, "source:" + targetID, "mapping:" + targetID, "conversation:" + targetID, "members:" + targetID, "summary:" + app.EmailJobMessageSummary + ":" + targetID, "summary:" + app.EmailJobConversationSummary + ":" + targetID} {
			emailDeleteRows(e, emailRowsQuery{Kind: "dependency", Parent: ref, Limit: 100})
			emailDeleteRows(e, emailRowsQuery{Kind: "refresh", Parent: ref, Limit: 100})
			emailDelete(e, "reference", ref)
		}
	}
	for presentationID := range presentationIDs {
		emailDelete(e, "presentation", presentationID)
	}
	// Catch dependencies indexed by analysis target ID after the targets above
	// have been removed.
	for _, kind := range []string{app.EmailJobClassification, app.EmailJobMessageSummary, app.EmailJobConversationSummary, app.EmailJobAssignment, app.EmailJobRelationshipCheck} {
		for targetID := range ids {
			emailDeleteRows(e, emailRowsQuery{Kind: "dependency", Related: kind + ":" + targetID, Limit: 100})
		}
	}

	emailDelete(e, "conversation", conv.ID)
	if e.err != nil {
		return EmailConversationDeleteResult{}, e.err
	}
	out.DeletedMails = len(members)
	for artifactPath := range artifactPaths {
		out.ArtifactPaths = append(out.ArtifactPaths, artifactPath)
	}
	sort.Strings(out.ArtifactPaths)
	return out, nil
}

func emailDeleteRows(e *emailEngine, q emailRowsQuery) {
	if e.err != nil {
		return
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	for {
		rows, err := e.db.list(q)
		if err != nil {
			e.err = err
			return
		}
		for _, row := range rows {
			emailDelete(e, row.Kind, row.ID)
		}
		if e.err != nil || len(rows) < q.Limit {
			return
		}
	}
}

func emailCollectRows(e *emailEngine, q emailRowsQuery) ([]EmailRecord, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	out := []EmailRecord{}
	for {
		rows, err := e.db.list(q)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(rows) < q.Limit {
			return out, nil
		}
		q.After = rows[len(rows)-1].Sort
	}
}

func EmailProviderThreadID(mailboxID, providerThreadID string) string {
	return emailID(mailboxID, providerThreadID)
}
