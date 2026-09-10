package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"net/mail"
	"strings"
)

func emailEntryValid(v string) bool { return v == "notification" || v == "interaction" }
func emailSenderAddress(e *emailEngine, m app.EmailMail) string {
	v, ok := emailGet[app.EmailRepresentation](e, "representation", m.RepresentationID)
	if !ok || len(v.From) != 1 {
		return ""
	}
	a, err := mail.ParseAddress(v.From[0])
	if err != nil {
		return ""
	}
	i := strings.LastIndex(a.Address, "@")
	if i <= 0 {
		return ""
	}
	return a.Address[:i] + "@" + strings.ToLower(a.Address[i+1:])
}
func emailEffectiveEntry(m app.EmailMail) string {
	if m.Classification != nil && m.Classification.EffectiveEntry == "notification" {
		return "notification"
	}
	return "interaction"
}
func emailApplyRule(e *emailEngine, m *app.EmailMail) {
	if m.Classification != nil && (m.Classification.Source == "manual" || m.Classification.Source == "rule") {
		return
	}
	address := emailSenderAddress(e, *m)
	if address == "" || m.Direction == "sent" {
		return
	}
	rule, ok := emailGet[app.EmailSenderRule](e, "sender_rule", emailID("sender", address))
	if !ok || !rule.Enabled || m.ArrivalSequence <= rule.EffectiveAfterSequence {
		return
	}
	revision := int64(1)
	if m.Classification != nil {
		revision = m.Classification.Revision + 1
	}
	m.Classification = &app.EmailClassification{Category: "unknown", EffectiveEntry: rule.Entry, Source: "rule", NotificationSubtype: "", State: "ready", Revision: revision, RuleRevision: rule.Revision, SenderAddress: address, ReasonCode: "sender_rule", UpdatedAt: e.now}
	if rule.Entry == "notification" {
		m.Classification.NotificationSubtype = "general"
	}
}
func emailSaveClassification(e *emailEngine, m app.EmailMail) error {
	if emailEvents(e) {
		emailSaveMail(e, m)
		if m.ConversationID != "" {
			c, _ := emailGet[app.EmailConversation](e, "conversation", m.ConversationID)
			emailRecountEvent(e, &c)
			emailSaveConversation(e, c)
		}
		if m.RepresentationID != "" && m.ConversationID == "" {
			_, err := emailRequest(e, EmailJobRequest{Kind: app.EmailJobAssignment, TargetID: m.ID})
			return err
		}
		return e.err
	}
	// Membership remains immutable; only routed interaction members count as unseen.
	previous, _ := emailGet[app.EmailMail](e, "mail", m.ID)
	if m.ConversationID != "" && emailEffectiveEntry(previous) != emailEffectiveEntry(m) {
		conv, ok := emailGet[app.EmailConversation](e, "conversation", m.ConversationID)
		if ok {
			if _, seen := emailGet[app.EmailViewReceipt](e, "view", m.ID); !seen {
				if emailEffectiveEntry(m) == "notification" {
					conv.UnseenCount--
				} else {
					conv.UnseenCount++
				}
			}
			conv.InputVersion++
			emailSaveConversation(e, conv)
			emailTouch(e, "conversation:"+conv.ID)
		}
	}
	emailSaveMail(e, m)
	emailTouch(e, "search")
	if m.RepresentationID != "" && (m.Classification == nil || m.Classification.NotificationSubtype != "verification") {
		if _, err := emailRequest(e, EmailJobRequest{Kind: app.EmailJobMessageSummary, TargetID: m.ID, Rearm: true}); err != nil {
			return err
		}
	}
	if emailEffectiveEntry(m) == "interaction" && m.RepresentationID != "" {
		kind := app.EmailJobAssignment
		if m.ConversationID != "" {
			kind = app.EmailJobRelationshipCheck
		}
		_, err := emailRequest(e, EmailJobRequest{Kind: kind, TargetID: m.ID, Rearm: true})
		return err
	}
	return e.err
}
func emailClassification(e *emailEngine, c EmailClassificationCommand) (app.EmailMail, error) {
	m, err := emailMail(e, c.MailID)
	if err != nil {
		return m, err
	}
	if err = emailLeaseCheck(e, c.Lease, app.EmailJobClassification, m.ID); err != nil {
		return m, err
	}
	if err = emailLeaseInputs(e, c.Lease, c.Generation, c.Classification.InputFingerprint); err != nil {
		return m, err
	}
	target, ok := emailGet[app.EmailAnalysisTarget](e, "target", app.EmailJobClassification+":"+m.ID)
	if !ok || target.Generation != c.Generation || target.InputFingerprint != c.Classification.InputFingerprint || emailFingerprint(e, target.Inputs) != target.InputFingerprint {
		return m, errEmailConflict
	}
	if c.Verification != nil {
		rep, ok := emailGet[app.EmailRepresentation](e, "representation", m.RepresentationID)
		if !ok || c.Classification.Category != "notification" || c.Classification.NotificationSubtype != "verification" || c.Verification.Code == "" || len(c.Verification.Code) > 32 || !strings.Contains(rep.BodyText, c.Verification.Code) || c.Verification.EvidenceRef != "representation:"+rep.ID+":body" {
			return m, errEmailInvalid
		}
	}
	v := c.Classification
	if v.InputPath != "" && (!emailSafePath(v.InputPath) || !emailHashValid(v.InputSHA256)) {
		return m, errEmailInvalid
	}
	if v.OutputPath != "" && (!emailSafePath(v.OutputPath) || !emailHashValid(v.OutputSHA256)) {
		return m, errEmailInvalid
	}
	if len(v.RequestedResponse) > 1000 || len(v.Purpose) > 512 || len(v.ServiceLabel) > 256 || len(v.Reason) > 2000 || len(v.Evidence) > 3 {
		return m, errEmailInvalid
	}
	for _, evidence := range v.Evidence {
		if len(evidence.Text) > 500 || len(evidence.Ref) > 512 {
			return m, errEmailInvalid
		}
	}
	if c.Verification != nil {
		code := c.Verification.Code
		v.RequestedResponse = strings.ReplaceAll(v.RequestedResponse, code, "[verification code]")
		v.Reason = strings.ReplaceAll(v.Reason, code, "[verification code]")
		v.Purpose = strings.ReplaceAll(v.Purpose, code, "[verification code]")
		v.ServiceLabel = strings.ReplaceAll(v.ServiceLabel, code, "[verification code]")
	}
	if !containsEmail([]string{"unknown", "notification", "interaction"}, v.Category) || len(v.EvidenceRefs) > 100 {
		return m, errEmailInvalid
	}
	if m.Classification == nil || (m.Classification.Source != "manual" && m.Classification.Source != "rule") {
		v.Revision = 1
		if m.Classification != nil {
			v.Revision = m.Classification.Revision + 1
		}
		v.Source = "model"
		v.EffectiveEntry = v.Category
		v.State = "ready"
		v.SenderAddress = emailSenderAddress(e, m)
		v.UpdatedAt = e.now
		if v.Category == "unknown" || v.Uncertainty {
			v.EffectiveEntry = "interaction"
			v.Source = "fallback"
			v.Uncertainty = true
		}
		if v.EffectiveEntry != "notification" {
			v.NotificationSubtype = ""
		} else if !containsEmail([]string{"verification", "promotion", "account_security", "general"}, v.NotificationSubtype) {
			return m, errEmailInvalid
		}
		m.Classification = &v
		if c.Verification != nil || !emailEvents(e) {
			m.Verification = c.Verification
		}
		if m.Verification != nil {
			m.Verification.Revision = v.Revision
		}
	}
	emailApplyRule(e, &m)
	target.State = app.EmailSummaryCurrent
	emailPut(e, "target", target.Kind+":"+target.TargetID, target.Kind, target.TargetID, target.State, "", target.Kind+":"+target.TargetID, target)
	return m, emailSaveClassification(e, m)
}
func emailOverrideClassification(e *emailEngine, c EmailClassificationOverride) (app.EmailMail, error) {
	m, err := emailMail(e, c.MailID)
	if err != nil {
		return m, err
	}
	if !emailEntryValid(c.Entry) {
		return m, errEmailInvalid
	}
	revision := int64(0)
	if m.Classification != nil {
		revision = m.Classification.Revision
	}
	if revision != c.ExpectedVersion {
		return m, errEmailConflict
	}
	address := emailSenderAddress(e, m)
	ruleRevision := int64(0)
	if c.RememberSender {
		if address == "" {
			return m, errEmailInvalid
		}
		id := emailID("sender", address)
		old, _ := emailGet[app.EmailSenderRule](e, "sender_rule", id)
		if old.Revision != c.ExpectedRuleVersion {
			return m, errEmailConflict
		}
		rule := app.EmailSenderRule{ID: id, Address: address, Entry: c.Entry, Enabled: true, Revision: old.Revision + 1, EffectiveAfterSequence: emailCounter(e, "arrival", false), UpdatedAt: e.now}
		emailPut(e, "sender_rule", id, address, "", "active", address, id, rule)
		ruleRevision = rule.Revision
	}
	m.InputVersion++
	m.Classification = &app.EmailClassification{Category: "unknown", EffectiveEntry: c.Entry, Source: "manual", State: "ready", Revision: revision + 1, RuleRevision: ruleRevision, SenderAddress: address, ReasonCode: "manual_choice", UpdatedAt: e.now}
	if c.Entry == "notification" {
		m.Classification.NotificationSubtype = "general"
	}
	return m, emailSaveClassification(e, m)
}
func emailUpdateSenderRule(e *emailEngine, c EmailSenderRuleCommand) (app.EmailSenderRule, error) {
	rule, ok := emailGet[app.EmailSenderRule](e, "sender_rule", c.RuleID)
	if !ok {
		return rule, errEmailNotFound
	}
	if !emailEntryValid(c.Entry) {
		return rule, errEmailInvalid
	}
	if c.ExpectedVersion != rule.Revision {
		return rule, errEmailConflict
	}
	rule.Entry = c.Entry
	rule.Enabled = c.Enabled
	rule.Revision++
	rule.EffectiveAfterSequence = emailCounter(e, "arrival", false)
	rule.UpdatedAt = e.now
	state := "disabled"
	if rule.Enabled {
		state = "active"
	}
	emailPut(e, "sender_rule", rule.ID, rule.Address, "", state, rule.Address, rule.ID, rule)
	return rule, e.err
}

// Backfill traverses one bounded historical page per refresh, without moving
// members. New parsed messages already schedule classification transactionally.
func emailBackfillClassifications(e *emailEngine, limit int) (int, bool, error) {
	state, _ := emailGet[struct {
		Cursor string
		Done   bool
	}](e, "counter", "classification_backfill_v2")
	if state.Done {
		return 0, false, e.err
	}
	rows := emailList[app.EmailMail](e, emailRowsQuery{Kind: "mail", After: state.Cursor, Limit: limit})
	for _, m := range rows {
		if m.RepresentationID != "" && m.Classification == nil {
			if _, err := emailRequest(e, EmailJobRequest{Kind: app.EmailJobClassification, TargetID: m.ID}); err != nil {
				return 0, false, err
			}
		}
		state.Cursor = emailOrder(m.SourceTime, m.ID)
	}
	if len(rows) == 0 && state.Cursor == "" {
		return 0, false, e.err
	}
	state.Done = len(rows) < limit
	emailPut(e, "counter", "classification_backfill_v2", "", "", "", "", "classification_backfill_v2", state)
	return len(rows), !state.Done, e.err
}
