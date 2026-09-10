package store

import (
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type EmailPresentationQuery struct {
	OwnerID    string
	TargetKind string   `json:"target_kind"`
	TargetIDs  []string `json:"target_ids"`
	Language   string   `json:"language"`
}
type EmailPresentationCommand struct {
	EmailCommand
	Query EmailPresentationQuery
	Retry bool
}
type EmailPresentationPublish struct {
	EmailCommand
	Lease        EmailJobLease
	Presentation app.EmailLocalizedPresentation
}

func emailPresentationValidate(q EmailPresentationQuery) error {
	if q.OwnerID == "" || (q.Language != "zh" && q.Language != "en") || (q.TargetKind != "mail" && q.TargetKind != "conversation") || len(q.TargetIDs) < 1 || len(q.TargetIDs) > 100 {
		return errEmailInvalid
	}
	seen := map[string]bool{}
	for _, id := range q.TargetIDs {
		if id == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n") || seen[id] {
			return errEmailInvalid
		}
		seen[id] = true
	}
	return nil
}

// Hash semantic/source inputs, excluding viewing and job activity. Language
// work can never mutate these inputs or invalidate another language's cache.
func emailPresentationRevision(e *emailEngine, kind, id string) (string, error) {
	if kind == "mail" {
		m, err := emailMail(e, id)
		if err != nil {
			return "", err
		}
		verificationRevision := int64(0)
		if m.Verification != nil {
			verificationRevision = m.Verification.Revision
		}
		return emailID(fmt.Sprint(m.InputVersion), m.RepresentationID, m.ContextID, m.ConversationID, string(emailJSON(m.Classification)), fmt.Sprint(verificationRevision), emailPresentationReplyRevision(e, m)), e.err
	}
	c, ok := emailGet[app.EmailConversation](e, "conversation", id)
	if !ok {
		return "", errEmailNotFound
	}
	concerns, err := emailConcerns(e, EmailQuery{ConversationID: id, Limit: 100})
	if err != nil {
		return "", err
	}
	replyInputs := []string{}
	for _, m := range emailList[app.EmailMail](e, emailRowsQuery{Kind: "mail", Related: id, Limit: 20}) {
		if m.ReplyMailID != "" {
			replyInputs = append(replyInputs, emailPresentationReplyRevision(e, m))
		}
	}
	return emailID(fmt.Sprint(c.InputVersion), fmt.Sprint(c.MembershipVersion), string(emailJSON(concerns)), strings.Join(replyInputs, "|")), e.err
}
func emailPresentationCurrent(e *emailEngine, q EmailPresentationQuery, id string) (app.EmailLocalizedPresentation, error) {
	revision, err := emailPresentationRevision(e, q.TargetKind, id)
	if err != nil {
		return app.EmailLocalizedPresentation{}, err
	}
	key := emailID(q.TargetKind, id, revision, q.Language, app.EmailPresentationPromptVersion)
	p, ok := emailGet[app.EmailLocalizedPresentation](e, "presentation", key)
	if !ok {
		p = app.EmailLocalizedPresentation{ID: key, TargetKind: q.TargetKind, TargetID: id, Language: q.Language, State: "missing", AnalysisRevision: revision, PromptVersion: app.EmailPresentationPromptVersion}
	}
	if emailEvents(e) {
		p.State = "suspended"
		p.ErrorCode = emailEventSuspended
		return p, e.err
	}
	if p.State != "ready" && p.State != "missing" {
		if j, ok := emailGet[app.EmailJob](e, "job", emailID(app.EmailJobPresentation, key)); ok {
			switch j.State {
			case app.EmailJobFailed:
				p.State = "failed"
				p.ErrorCode = "email_presentation_failed"
			case app.EmailJobRunning:
				p.State = "running"
			default:
				p.State = "queued"
			}
		}
	}
	return p, e.err
}
func emailPresentationRead(e *emailEngine, q EmailPresentationQuery) ([]app.EmailLocalizedPresentation, error) {
	if err := emailPresentationValidate(q); err != nil || q.OwnerID != e.owner {
		return nil, errEmailInvalid
	}
	out := make([]app.EmailLocalizedPresentation, 0, len(q.TargetIDs))
	for _, id := range q.TargetIDs {
		p, err := emailPresentationCurrent(e, q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, e.err
}
func emailPresentationEnsure(e *emailEngine, c EmailPresentationCommand) ([]app.EmailLocalizedPresentation, error) {
	if c.Query.OwnerID != c.OwnerID {
		return nil, errEmailInvalid
	}
	out, err := emailPresentationRead(e, c.Query)
	if err != nil {
		return nil, err
	}
	for i, p := range out {
		if p.State != "missing" && !(c.Retry && p.State == "failed") {
			continue
		}
		jID := emailID(app.EmailJobPresentation, p.ID)
		j, exists := emailGet[app.EmailJob](e, "job", jID)
		if !exists {
			j = app.EmailJob{ID: jID, OwnerID: e.owner, Kind: app.EmailJobPresentation, TargetID: p.ID, Generation: 1, InputFingerprint: p.AnalysisRevision, MaxAttempts: 3, CreatedAt: e.now}
		}
		j.State = app.EmailJobQueued
		j.Attempt = 0
		j.NextAttemptAt = e.now
		j.ErrorCode = ""
		j.LeaseToken = ""
		j.UpdatedAt = e.now
		p.State = "queued"
		p.ErrorCode = ""
		emailPut(e, "presentation", p.ID, p.TargetKind, p.TargetID, p.State, "", p.ID, p)
		emailSaveJob(e, j)
		out[i] = p
	}
	return out, e.err
}
func emailPresentationPublish(e *emailEngine, c EmailPresentationPublish) (app.EmailLocalizedPresentation, error) {
	p := c.Presentation
	if err := emailLeaseCheck(e, c.Lease, app.EmailJobPresentation, p.ID); err != nil {
		return p, err
	}
	prior, ok := emailGet[app.EmailLocalizedPresentation](e, "presentation", p.ID)
	if !ok {
		return p, errEmailNotFound
	}
	if prior.TargetKind != p.TargetKind || prior.TargetID != p.TargetID || prior.Language != p.Language || prior.AnalysisRevision != p.AnalysisRevision || prior.PromptVersion != p.PromptVersion {
		return p, errEmailConflict
	}
	current, err := emailPresentationRevision(e, p.TargetKind, p.TargetID)
	if err != nil {
		return p, err
	}
	if current != p.AnalysisRevision {
		return p, errEmailConflict
	}
	// A committed projection is immutable for its dependency/language key.
	// Lost-response retries cannot replace the already-published result.
	if prior.State == "ready" {
		return prior, e.err
	}
	if len(p.Title) > 512 || len(p.Summary) > 8000 || len(p.Explanation) > 2000 || strings.TrimSpace(p.Summary) == "" || len(p.ConcernExplanations) > 100 {
		return p, errEmailInvalid
	}
	for _, v := range p.ConcernExplanations {
		if len(v) > 2000 {
			return p, errEmailInvalid
		}
	}
	emailRedactPresentation(e, &p)
	p.State = "ready"
	p.ErrorCode = ""
	p.Revision = prior.Revision + 1
	emailPut(e, "presentation", p.ID, p.TargetKind, p.TargetID, p.State, "", p.ID, p)
	return p, e.err
}

func emailRedactPresentation(e *emailEngine, p *app.EmailLocalizedPresentation) {
	mails := []app.EmailMail{}
	if p.TargetKind == "mail" {
		m, ok := emailGet[app.EmailMail](e, "mail", p.TargetID)
		if ok {
			mails = append(mails, m)
		}
	} else {
		mails = emailList[app.EmailMail](e, emailRowsQuery{Kind: "mail", Related: p.TargetID, Limit: 20})
	}
	primary := append([]app.EmailMail{}, mails...)
	for _, m := range primary {
		if m.ReplyMailID != "" {
			original, ok := emailGet[app.EmailMail](e, "mail", m.ReplyMailID)
			if ok {
				mails = append(mails, original)
			}
		}
	}
	for _, m := range mails {
		if m.Verification == nil || m.Verification.Code == "" {
			continue
		}
		code := m.Verification.Code
		scrub := func(s *string) { *s = strings.ReplaceAll(*s, code, "[verification code]") }
		scrub(&p.Title)
		scrub(&p.Summary)
		scrub(&p.Explanation)
		scrub(&p.RequestedResponse)
		scrub(&p.Purpose)
		scrub(&p.ServiceLabel)
		for i := range p.Evidence {
			scrub(&p.Evidence[i].Text)
		}
		for id, text := range p.ConcernExplanations {
			scrub(&text)
			p.ConcernExplanations[id] = text
		}
	}
}

func emailPresentationReplyRevision(e *emailEngine, m app.EmailMail) string {
	if m.ReplyMailID == "" {
		return ""
	}
	original, found := emailGet[app.EmailMail](e, "mail", m.ReplyMailID)
	if !found {
		return "missing:" + m.ReplyMailID
	}
	return emailID(original.ID, fmt.Sprint(original.InputVersion), original.RepresentationID, string(emailJSON(original.Classification)))
}
