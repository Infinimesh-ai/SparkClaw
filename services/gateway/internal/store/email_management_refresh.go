package store

import (
	"crypto/sha256"
	"encoding/binary"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"strings"
)

type emailRefreshIntent struct {
	ID        string
	Reference string
	Version   int64
	Cursor    string
	Done      bool
}

func emailRef(e *emailEngine, ref string) int64 {
	if strings.HasPrefix(ref, "mail:") {
		m, _ := emailGet[app.EmailMail](e, "mail", strings.TrimPrefix(ref, "mail:"))
		return m.InputVersion
	}
	if strings.HasPrefix(ref, "conversation:") {
		c, _ := emailGet[app.EmailConversation](e, "conversation", strings.TrimPrefix(ref, "conversation:"))
		return c.InputVersion
	}
	v, _ := emailGet[struct{ Version int64 }](e, "reference", ref)
	if strings.HasPrefix(ref, "summary:") {
		target, exists := emailGet[app.EmailAnalysisTarget](e, "target", strings.TrimPrefix(ref, "summary:"))
		if !exists {
			return v.Version
		}
		// Summary dependency rules form a DAG: conversation -> individual -> sources.
		// Include actual source versions now, before durable fan-out updates targets.
		digest := sha256.Sum256(emailJSON(struct {
			Version                     int64
			Pointer, State, Fingerprint string
		}{v.Version, target.SummaryID, target.State, emailFingerprint(e, target.Inputs)}))
		return int64(binary.BigEndian.Uint64(digest[:8]) & 0x7fffffffffffffff)
	}
	return v.Version
}
func emailTouch(e *emailEngine, ref string) {
	version := int64(0)
	if strings.HasPrefix(ref, "mail:") || strings.HasPrefix(ref, "conversation:") {
		version = emailRef(e, ref)
	} else {
		previous, _ := emailGet[struct{ Version int64 }](e, "reference", ref)
		version = previous.Version + 1
		emailPut(e, "reference", ref, "", "", "", "", ref, struct{ Version int64 }{version})
	}
	id := emailID(ref, string(emailJSON(version)))
	emailPut(e, "refresh", id, ref, "", "pending", "", id, emailRefreshIntent{ID: id, Reference: ref, Version: version})
}
func emailFingerprint(e *emailEngine, inputs map[string]int64) string {
	current := map[string]int64{}
	for ref := range inputs {
		current[ref] = emailRef(e, ref)
	}
	return emailID(string(emailJSON(current)))
}
func emailChangedMail(e *emailEngine, m app.EmailMail) {
	emailTouch(e, "mail:"+m.ID)
	if m.ConversationID != "" {
		conv, _ := emailGet[app.EmailConversation](e, "conversation", m.ConversationID)
		conv.InputVersion++
		conv.UpdatedAt = e.now
		emailSaveConversation(e, conv)
		emailTouch(e, "conversation:"+conv.ID)
	}
}
func emailExpand(e *emailEngine, c EmailRefreshCommand) (EmailRefreshResult, error) {
	out := EmailRefreshResult{}
	limit := emailLimit(c.Limit)
	n, more, backfillErr := emailBackfillClassifications(e, limit)
	if backfillErr != nil {
		return out, backfillErr
	}
	if n > 0 {
		return EmailRefreshResult{Processed: n, Remaining: true}, nil
	}
	_ = more
	intents := emailList[emailRefreshIntent](e, emailRowsQuery{Kind: "refresh", State: "pending", Limit: 1})
	if len(intents) == 0 {
		return out, e.err
	}
	intent := intents[0]
	deps := emailList[struct {
		Target    string
		Reference string
	}](e, emailRowsQuery{Kind: "dependency", Parent: intent.Reference, After: intent.Cursor, Limit: limit + 1})
	for i, d := range deps {
		if i == limit {
			break
		}
		t, ok := emailGet[app.EmailAnalysisTarget](e, "target", d.Target)
		if ok {
			if _, selected := t.Inputs[intent.Reference]; selected && emailFingerprint(e, t.Inputs) != t.InputFingerprint {
				refs := []string{}
				for ref := range t.Inputs {
					refs = append(refs, ref)
				}
				kind := t.Kind
				if kind == app.EmailJobAssignment {
					m, _ := emailGet[app.EmailMail](e, "mail", t.TargetID)
					if emailEffectiveEntry(m) == "notification" {
						continue
					}
					if m.ConversationID != "" {
						kind = app.EmailJobRelationshipCheck
					}
				}
				if _, err := emailRequest(e, EmailJobRequest{Kind: kind, TargetID: t.TargetID, Dependencies: refs}); err != nil {
					return out, err
				}
			}
		}
		intent.Cursor = emailID(d.Reference, d.Target)
		out.Processed++
	}
	intent.Done = len(deps) <= limit
	state := "pending"
	if intent.Done {
		state = "done"
	}
	emailPut(e, "refresh", intent.ID, intent.Reference, "", state, "", intent.ID, intent)
	out.Remaining = !intent.Done
	if !out.Remaining {
		out.Remaining = len(emailList[emailRefreshIntent](e, emailRowsQuery{Kind: "refresh", State: "pending", Limit: 1})) > 0
	}
	return out, e.err
}
func emailSaveConversation(e *emailEngine, c app.EmailConversation) {
	c.Summary = nil
	search := c.Title + " " + strings.Join(c.Participants, " ")
	if summary := emailProjectSummary(e, app.EmailJobConversationSummary, c.ID); summary != nil {
		search += " " + summary.Text
	}
	emailPut(e, "conversation", c.ID, "", "", "", search, emailOrder(c.UpdatedAt, c.ID), c)
}
func emailAssignment(e *emailEngine, c EmailAssignmentCommand) (app.EmailMail, error) {
	d := c.Decision
	m, err := emailMail(e, d.MailID)
	if err != nil {
		return m, err
	}
	if m.ConversationID != "" || emailEffectiveEntry(m) == "notification" {
		return m, errEmailConflict
	}
	if err = emailLeaseCheck(e, c.Lease, app.EmailJobAssignment, m.ID); err != nil {
		return m, err
	}
	if err = emailLeaseInputs(e, c.Lease, c.Generation, d.InputFingerprint); err != nil {
		return m, err
	}
	t, ok := emailGet[app.EmailAnalysisTarget](e, "target", app.EmailJobAssignment+":"+m.ID)
	if !ok || t.Generation != c.Generation || t.InputFingerprint != d.InputFingerprint || emailFingerprint(e, t.Inputs) != d.InputFingerprint || d.OwnerEpoch != emailCounter(e, "assignment_epoch", false) {
		return m, errEmailConflict
	}
	if d.ID == "" || d.ModelVersion == "" || d.PromptVersion == "" || len(d.EvidenceRefs) > 100 {
		return m, errEmailInvalid
	}
	if _, exists := emailGet[app.EmailAssignmentDecision](e, "decision", d.ID); exists {
		return m, errEmailConflict
	}
	var conv app.EmailConversation
	switch d.Action {
	case "pending":
	case "new":
		if strings.TrimSpace(d.Title) == "" || d.ConversationID != "" {
			return m, errEmailInvalid
		}
		conv = app.EmailConversation{ID: emailID(e.owner, "conversation", c.CommandKey), OwnerID: e.owner, Title: d.Title, CreatedAt: e.now}
		d.ConversationID = conv.ID
	case "append":
		if !containsEmail(c.CandidateConversationIDs, d.ConversationID) {
			return m, errEmailInvalid
		}
		var exists bool
		conv, exists = emailGet[app.EmailConversation](e, "conversation", d.ConversationID)
		if !exists {
			return m, errEmailNotFound
		}
	default:
		return m, errEmailInvalid
	}
	t.State = app.EmailSummaryCurrent
	emailPut(e, "target", t.Kind+":"+t.TargetID, t.Kind, t.TargetID, t.State, "", t.Kind+":"+t.TargetID, t)
	d.CreatedAt = e.now
	emailPut(e, "decision", d.ID, m.ID, d.ConversationID, "", "", emailOrder(e.now, d.ID), d)
	if d.Action != "pending" {
		m.ConversationID = conv.ID
		m.AssignmentState = app.EmailAssignmentAssigned
		m.InputVersion++
		conv.MemberCount++
		conv.MembershipVersion++
		conv.InputVersion++
		conv.UpdatedAt = e.now
		conv.Participants = emailUnique(append(conv.Participants, m.Participants...))
		conv.MailboxIDs = emailUnique(append(conv.MailboxIDs, m.MailboxID))
		if _, viewed := emailGet[app.EmailViewReceipt](e, "view", m.ID); !viewed {
			conv.UnseenCount++
		}
		emailSaveMail(e, m)
		emailSaveConversation(e, conv)
		emailCounter(e, "assignment_epoch", true)
		emailTouch(e, "mail:"+m.ID)
		emailTouch(e, "search")
		emailTouch(e, "conversation:"+conv.ID)
		_, err = emailRequest(e, EmailJobRequest{Kind: app.EmailJobConversationSummary, TargetID: conv.ID})
	}
	return m, err
}
func containsEmail(items []string, v string) bool {
	for _, item := range items {
		if item == v {
			return true
		}
	}
	return false
}
func emailConcern(e *emailEngine, c EmailConcernCommand) (app.EmailAssignmentConcern, error) {
	v := c.Concern
	if v.ID == "" || !containsEmail([]string{app.EmailConcernNone, app.EmailConcernSuspectedDuplicate, app.EmailConcernPendingCorrection}, v.Kind) || len(v.MailIDs) > 100 || len(v.ConversationIDs) > 100 {
		return v, errEmailInvalid
	}
	checked, err := emailMail(e, c.TargetID)
	if err != nil {
		return v, err
	}
	if checked.ConversationID == "" {
		return v, errEmailConflict
	}
	if v.Kind == app.EmailConcernNone {
		v.MailIDs = emailUnique(append(v.MailIDs, checked.ID))
		v.ConversationIDs = emailUnique(append(v.ConversationIDs, checked.ConversationID))
	}
	if len(v.MailIDs) == 0 || len(v.ConversationIDs) == 0 {
		return v, errEmailInvalid
	}
	if err = emailLeaseCheck(e, c.Lease, app.EmailJobRelationshipCheck, c.TargetID); err != nil {
		return v, err
	}
	if err = emailLeaseInputs(e, c.Lease, c.Generation, v.InputFingerprint); err != nil {
		return v, err
	}
	t, ok := emailGet[app.EmailAnalysisTarget](e, "target", app.EmailJobRelationshipCheck+":"+c.TargetID)
	if !ok || t.Generation != c.Generation || t.InputFingerprint != v.InputFingerprint || emailFingerprint(e, t.Inputs) != v.InputFingerprint {
		return v, errEmailConflict
	}
	for _, id := range v.MailIDs {
		if _, err = emailMail(e, id); err != nil {
			return v, err
		}
	}
	for _, id := range v.ConversationIDs {
		if _, ok := emailGet[app.EmailConversation](e, "conversation", id); !ok {
			return v, errEmailNotFound
		}
	}
	if _, exists := emailGet[app.EmailAssignmentConcern](e, "concern", v.ID); exists {
		return v, errEmailConflict
	}
	if t.ConcernID != "" {
		prior, exists := emailGet[app.EmailAssignmentConcern](e, "concern", t.ConcernID)
		if !exists {
			return v, errEmailCorrupt
		}
		emailPut(e, "concern", prior.ID, c.TargetID, "", "superseded", "", emailOrder(prior.CreatedAt, prior.ID), prior)
		for _, id := range prior.ConversationIDs {
			emailPut(e, "concern_link", emailID(prior.ID, id), id, prior.ID, "superseded", "", emailOrder(prior.CreatedAt, prior.ID), prior)
		}
	}
	v.OwnerID = e.owner
	v.Version = emailCounter(e, "concern_version", true)
	v.CreatedAt = e.now
	state := "active"
	if v.Kind == app.EmailConcernNone {
		state = "clear"
	}
	emailPut(e, "concern", v.ID, c.TargetID, "", state, "", emailOrder(e.now, v.ID), v)
	if state == "active" {
		for _, id := range v.ConversationIDs {
			emailPut(e, "concern_link", emailID(v.ID, id), id, v.ID, state, "", emailOrder(e.now, v.ID), v)
		}
	}
	t.ConcernID = v.ID
	t.State = app.EmailSummaryCurrent
	emailPut(e, "target", t.Kind+":"+t.TargetID, t.Kind, t.TargetID, t.State, "", t.Kind+":"+t.TargetID, t)
	return v, e.err
}

func emailSummary(e *emailEngine, c EmailSummaryCommand) (app.EmailSummary, error) {
	v := c.Summary
	if !containsEmail([]string{app.EmailJobMessageSummary, app.EmailJobConversationSummary}, v.TargetKind) || v.ID == "" || strings.TrimSpace(v.Text) == "" || len(v.Text) > 100000 || v.ModelVersion == "" || v.PromptVersion == "" {
		return v, errEmailInvalid
	}
	if err := emailLeaseCheck(e, c.Lease, v.TargetKind, v.TargetID); err != nil {
		return v, err
	}
	if err := emailLeaseInputs(e, c.Lease, v.Generation, v.InputFingerprint); err != nil {
		return v, err
	}
	if _, exists := emailGet[app.EmailSummary](e, "summary", v.ID); exists {
		return v, errEmailConflict
	}
	t, ok := emailGet[app.EmailAnalysisTarget](e, "target", v.TargetKind+":"+v.TargetID)
	if !ok {
		return v, errEmailNotFound
	}
	v.CreatedAt = e.now
	v.Current = t.Generation == v.Generation && t.InputFingerprint == v.InputFingerprint && emailFingerprint(e, t.Inputs) == v.InputFingerprint
	emailPut(e, "summary", v.ID, v.TargetID, v.TargetKind, "", "", emailOrder(e.now, v.ID), v)
	if v.Current {
		t.SummaryID = v.ID
		t.State = app.EmailSummaryCurrent
		emailPut(e, "target", t.Kind+":"+t.TargetID, t.Kind, t.TargetID, t.State, "", t.Kind+":"+t.TargetID, t)
		emailTouch(e, "summary:"+t.Kind+":"+t.TargetID)
		if t.Kind == app.EmailJobConversationSummary {
			conv, _ := emailGet[app.EmailConversation](e, "conversation", t.TargetID)
			emailSaveConversation(e, conv)
		}
		if t.Kind == app.EmailJobMessageSummary {
			m, _ := emailGet[app.EmailMail](e, "mail", t.TargetID)
			emailSaveMail(e, m)
			if m.ConversationID != "" {
				conv, _ := emailGet[app.EmailConversation](e, "conversation", m.ConversationID)
				conv.InputVersion++
				conv.UpdatedAt = e.now
				emailSaveConversation(e, conv)
				emailTouch(e, "conversation:"+conv.ID)
			}
		}
	}
	return v, e.err
}
