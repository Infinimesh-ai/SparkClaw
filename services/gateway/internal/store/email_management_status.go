package store

import (
	"context"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func errEmailCommandInvalid(ctx context.Context, op StoreOperation) error {
	return storeError(ctx, op, StoreErrorInvalid, errEmailInvalid)
}
func emailClaimOptional(e *emailEngine, c EmailJobClaim) (emailOptional[app.EmailJob], error) {
	v, ok, err := emailClaim(e, c)
	return emailOptional[app.EmailJob]{v, ok}, err
}
func emailMailboxes(e *emailEngine) ([]app.EmailMailbox, error) {
	v := emailList[app.EmailMailbox](e, emailRowsQuery{Kind: "mailbox", Limit: 100})
	return v, e.err
}
func emailOwnerStatus(e *emailEngine) (app.EmailOwnerStatus, error) {
	v, _ := emailGet[app.EmailOwnerStatus](e, "counter", "owner_status")
	// Historical counters predate routed notifications. Derive exception count
	// from its complete indexed scope, including old snapshots without rewriting.
	v.PendingCount = 0
	q := emailRowsQuery{Kind: "mail", CapturedOnly: true, PendingOnly: true, Limit: 100}
	for {
		rows := emailList[app.EmailMail](e, q)
		v.PendingCount += len(rows)
		if e.err != nil || len(rows) < q.Limit {
			break
		}
		last := rows[len(rows)-1]
		q.After = emailOrder(last.SourceTime, last.ID)
	}
	return v, e.err
}
func emailUpdateStatus(e *emailEngine, r EmailRecord) {
	if !containsEmail([]string{"mail", "mailbox", "sync_failure", "job", "conversation", "concern", "view", "summary", "sender_rule", "presentation"}, r.Kind) {
		return
	}
	old, exists, err := e.db.get(r.Kind, r.ID)
	if err != nil {
		e.err = err
		return
	}
	status, _ := emailGet[app.EmailOwnerStatus](e, "counter", "owner_status")
	status.Revision++
	switch r.Kind {
	case "mail":
		var next, previous app.EmailMail
		if err = json.Unmarshal(r.Data, &next); err != nil {
			e.err = err
			return
		}
		if exists {
			if err = json.Unmarshal(old.Data, &previous); err != nil {
				e.err = err
				return
			}
		}
		if next.CaptureID != "" && previous.CaptureID == "" {
			status.CapturedCount++
		}
		pending := func(m app.EmailMail) int {
			if m.CaptureID != "" && m.RepresentationID == "" {
				return 1
			}
			return 0
		}
		status.PendingCount += pending(next) - pending(previous)
	case "conversation":
		if !exists {
			status.ConversationCount++
		}
	case "job":
		var next, previous app.EmailJob
		if err = json.Unmarshal(r.Data, &next); err != nil {
			e.err = err
			return
		}
		if exists {
			if err = json.Unmarshal(old.Data, &previous); err != nil {
				e.err = err
				return
			}
		}
		backlog := func(j app.EmailJob) int {
			if j.Kind == app.EmailJobDiscover && j.PollInterval > 0 && j.State == app.EmailJobQueued && !j.RefreshPending && j.ErrorCode == "" {
				// A future automatic heartbeat is idle, not mail waiting to sync.
				return 0
			}
			if j.State == app.EmailJobPaused && (j.ErrorCode == emailPageBatchSuperseded || j.ErrorCode == emailEventSuspended) {
				return 0
			}
			if containsEmail([]string{app.EmailJobQueued, app.EmailJobRetryWait, app.EmailJobRunning, app.EmailJobPaused}, j.State) {
				return 1
			}
			return 0
		}
		status.BacklogCount += backlog(next) - backlog(previous)
	}
	if e.err == nil {
		e.err = e.db.put(EmailRecord{Owner: e.owner, Kind: "counter", ID: "owner_status", Sort: "owner_status", Data: emailJSON(status)})
	}
}
