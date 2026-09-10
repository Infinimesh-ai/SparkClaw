package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type emailOptional[T any] struct {
	Value T
	Found bool
}

func emailReadRecord[T any](e *emailEngine, kind, id string) (emailOptional[T], error) {
	v, ok := emailGet[T](e, kind, id)
	return emailOptional[T]{v, ok}, e.err
}
func emailProjectSummary(e *emailEngine, kind, id string) *app.EmailSummary {
	t, ok := emailGet[app.EmailAnalysisTarget](e, "target", kind+":"+id)
	if !ok || t.SummaryID == "" {
		return nil
	}
	v, ok := emailGet[app.EmailSummary](e, "summary", t.SummaryID)
	if !ok {
		return nil
	}
	v.Current = t.State == app.EmailSummaryCurrent && t.Generation == v.Generation && t.InputFingerprint == v.InputFingerprint && emailFingerprint(e, t.Inputs) == v.InputFingerprint
	return &v
}
func emailProjectMail(e *emailEngine, m app.EmailMail) app.EmailMail {
	if v, ok := emailGet[app.EmailViewReceipt](e, "view", m.ID); ok {
		m.ViewedAt = &v.FirstViewedAt
	}
	m.Summary = emailProjectSummary(e, app.EmailJobMessageSummary, m.ID)
	return m
}
func emailGetMail(e *emailEngine, id string) (emailOptional[app.EmailMail], error) {
	v, ok := emailGet[app.EmailMail](e, "mail", id)
	if ok {
		v = emailProjectMail(e, v)
	}
	return emailOptional[app.EmailMail]{v, ok}, e.err
}
func emailGetConversation(e *emailEngine, id string) (emailOptional[app.EmailConversation], error) {
	v, ok := emailGet[app.EmailConversation](e, "conversation", id)
	if ok {
		v.Summary = emailProjectSummary(e, app.EmailJobConversationSummary, id)
		v.HistoricalMixed = emailConversationMixed(e, id)
	}
	return emailOptional[app.EmailConversation]{v, ok}, e.err
}
func emailGetTarget(e *emailEngine, kind, id string) (emailOptional[app.EmailAnalysisTarget], error) {
	t, ok := emailGet[app.EmailAnalysisTarget](e, "target", kind+":"+id)
	if ok && emailFingerprint(e, t.Inputs) != t.InputFingerprint {
		t.State = app.EmailSummaryStale
	}
	return emailOptional[app.EmailAnalysisTarget]{t, ok}, e.err
}
func emailMails(e *emailEngine, q EmailQuery) (EmailMailPage, error) {
	q.CursorKind = "mail"
	var err error
	q, err = emailScopedCursor(q, e.now)
	if err != nil {
		return EmailMailPage{}, err
	}
	limit := emailLimit(q.Limit)
	r := emailRowsQuery{Kind: "mail", Direction: q.Direction, RequireNativeCapture: q.RequireNativeCapture, Validity: q.Validity, AsOf: q.AsOf, Entry: q.Entry, NotificationSubtype: q.NotificationSubtype, PendingOnly: q.PendingOnly, UnassignedOnly: q.UnassignedOnly, CapturedOnly: q.CapturedOnly, Parent: q.MailboxID, Related: q.ConversationID, Search: q.Search, After: q.After, Limit: limit + 1}
	rows := emailList[app.EmailMail](e, r)
	out := EmailMailPage{Items: []app.EmailMail{}, ServerNow: e.now}
	if q.Entry != "" {
		counts, err := e.db.count(r)
		if err != nil {
			return out, err
		}
		out.Counts = &counts
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		out.NextCursor = emailEncodeScopeCursor(q, emailOrder(last.SourceTime, last.ID))
	}
	for _, m := range rows {
		out.Items = append(out.Items, emailProjectMail(e, m))
	}
	return out, e.err
}
func emailConversations(e *emailEngine, q EmailQuery) (EmailConversationPage, error) {
	q.CursorKind = "conversation"
	var err error
	q, err = emailScopedCursor(q, e.now)
	if err != nil {
		return EmailConversationPage{}, err
	}
	limit := emailLimit(q.Limit)
	rows := emailList[app.EmailConversation](e, emailRowsQuery{Kind: "conversation", InteractionConversation: q.Entry == "interaction", ConversationMailbox: q.MailboxID, ConversationSearch: true, Search: q.Search, After: q.After, Limit: limit + 1})
	out := EmailConversationPage{Items: []app.EmailConversation{}}
	if q.Entry == "interaction" {
		counts, err := e.db.count(emailRowsQuery{Kind: "mail", Entry: "interaction", CapturedOnly: true, Parent: q.MailboxID, Search: q.Search, AsOf: e.now})
		if err != nil {
			return out, err
		}
		out.Counts = &counts
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		out.NextCursor = emailEncodeScopeCursor(q, emailOrder(last.UpdatedAt, last.ID))
	}
	for _, v := range rows {
		v.Summary = emailProjectSummary(e, app.EmailJobConversationSummary, v.ID)
		v.HistoricalMixed = emailConversationMixed(e, v.ID)
		out.Items = append(out.Items, v)
	}
	return out, e.err
}
func emailConcerns(e *emailEngine, q EmailQuery) ([]app.EmailAssignmentConcern, error) {
	kind := "concern"
	parent := ""
	if q.ConversationID != "" {
		kind = "concern_link"
		parent = q.ConversationID
	}
	rows := emailList[app.EmailAssignmentConcern](e, emailRowsQuery{Kind: kind, Parent: parent, State: "active", After: q.After, Limit: emailLimit(q.Limit)})
	return rows, e.err
}
func emailQueryRows[T any](e *emailEngine, q EmailQuery, kind string) ([]T, error) {
	rows := emailList[T](e, emailRowsQuery{Kind: kind, Parent: q.MailboxID, After: q.After, Limit: emailLimit(q.Limit), Asc: kind == "thread"})
	return rows, e.err
}

func emailConversationMixed(e *emailEngine, id string) bool {
	q := emailRowsQuery{Kind: "mail", Related: id, Entry: "notification", AsOf: e.now}
	notices, err := e.db.exists(q)
	if err != nil {
		e.err = err
		return false
	}
	if !notices {
		return false
	}
	q.Entry = "interaction"
	interactions, err := e.db.exists(q)
	if err != nil {
		e.err = err
		return false
	}
	return interactions
}
