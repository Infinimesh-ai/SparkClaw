package store

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

const EmailSourceSummaryPolicy = "policy:email-source-summaries-v1"

func emailSummaryKind(kind string) bool {
	return kind == app.EmailJobMessageSummary || kind == app.EmailJobConversationSummary
}

// Canonical dependencies exclude all generated text, classifications and viewing.
func emailSourceSummaryRefs(e *emailEngine, kind, id string) []string {
	refs := []string{EmailSourceSummaryPolicy}
	if kind == app.EmailJobMessageSummary {
		return append(refs, "source:"+id)
	}
	refs = append(refs, "members:"+id)
	for _, member := range emailList[app.EmailMail](e, emailRowsQuery{Kind: "mail", Related: id, Limit: 20}) {
		refs = append(refs, "source:"+member.ID)
	}
	return refs
}

// A separate one-time migration must also cover installations whose event
// migration completed before summaries were restored.
func emailBackfillSourceSummaries(e *emailEngine, limit int) (int, bool, error) {
	type progress struct {
		Phase, Cursor string
		Done          bool
	}
	p, _ := emailGet[progress](e, "counter", "source_summary_migration_v1")
	if p.Done {
		return 0, false, e.err
	}
	if p.Phase == "" {
		p.Phase = "mail"
	}
	rows, err := e.db.list(emailRowsQuery{Kind: p.Phase, After: p.Cursor, Limit: limit})
	if err != nil {
		return 0, false, err
	}
	for _, row := range rows {
		kind := app.EmailJobMessageSummary
		eligible := false
		if p.Phase == "mail" {
			m, _ := emailGet[app.EmailMail](e, "mail", row.ID)
			eligible = m.RepresentationID != ""
		} else {
			c, _ := emailGet[app.EmailConversation](e, "conversation", row.ID)
			eligible = c.MemberCount > 0
			kind = app.EmailJobConversationSummary
		}
		if eligible {
			if _, err = emailRequest(e, EmailJobRequest{Kind: kind, TargetID: row.ID, Dependencies: []string{}}); err != nil {
				return 0, false, err
			}
		}
		p.Cursor = row.Sort
	}
	if len(rows) < limit {
		if p.Phase == "mail" {
			p.Phase = "conversation"
			p.Cursor = ""
		} else {
			p.Done = true
		}
	}
	emailPut(e, "counter", "source_summary_migration_v1", "", "", "", "", "source_summary_migration_v1", p)
	return len(rows), !p.Done, e.err
}

func emailHasSourceSummaryPolicy(t app.EmailAnalysisTarget) bool {
	_, ok := t.Inputs[EmailSourceSummaryPolicy]
	return ok
}
