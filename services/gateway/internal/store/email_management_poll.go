package store

import (
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Each accepted click has its own durable settlement record. The job and
// mailbox fields remain a projection of the newest request for the UI.
type emailSyncRefreshRequest struct {
	ID                string     `json:"id"`
	OwnerID           string     `json:"owner_id"`
	MailboxID         string     `json:"mailbox_id"`
	BindingGeneration int64      `json:"binding_generation"`
	RefreshRevision   int64      `json:"refresh_revision"`
	State             string     `json:"state"`
	AcceptedAt        time.Time  `json:"accepted_at"`
	SettledAt         *time.Time `json:"settled_at,omitempty"`
	CancelledAt       *time.Time `json:"cancelled_at,omitempty"`
}

func emailSaveSyncRefreshRequest(e *emailEngine, request emailSyncRefreshRequest) {
	emailPut(e, "sync_refresh_request", request.ID, request.MailboxID, "", request.State, "",
		fmt.Sprintf("%020d/%s", request.RefreshRevision, request.ID), request)
}

func emailCloseSyncRefreshRequests(e *emailEngine, mailboxID string, bindingGeneration, throughRevision int64, cancel bool) {
	query := emailRowsQuery{Kind: "sync_refresh_request", Parent: mailboxID, State: "pending", Asc: true, Limit: 100}
	for {
		requests := emailList[emailSyncRefreshRequest](e, query)
		if e.err != nil || len(requests) == 0 {
			return
		}
		for _, request := range requests {
			if request.BindingGeneration != bindingGeneration || cancel {
				request.State = "cancelled"
				at := e.now
				request.CancelledAt = &at
			} else if request.RefreshRevision <= throughRevision {
				request.State = "settled"
				at := e.now
				request.SettledAt = &at
			} else {
				return
			}
			emailSaveSyncRefreshRequest(e, request)
		}
		if len(requests) < query.Limit {
			return
		}
		last := requests[len(requests)-1]
		query.After = fmt.Sprintf("%020d/%s", last.RefreshRevision, last.ID)
	}
}

// All transitions run in the existing owner transaction in Memory, File and
// PostgreSQL. A manual request updates the durable discover job rather than
// creating another job. Requests accepted during a running query must retain
// their own identity so that query cannot settle work it never observed.
// false means a retransmitted observer event made no change and must not
// shorten an idle deadline through the generic Rearm path.
func emailPollRequest(e *emailEngine, j *app.EmailJob, c EmailJobRequest) bool {
	if j.Kind != app.EmailJobDiscover {
		return true
	}
	if c.AutomaticPoll && j.PollInterval != c.RepeatInterval {
		j.PollInterval = c.RepeatInterval
		// Upgrade only an idle automatic deadline. Never advance RetryWait or a
		// running lease. Legacy queued jobs recorded the completion/rearm time
		// in UpdatedAt; this conservative anchor avoids waiting another 20 min.
		if j.State == app.EmailJobQueued && !j.RefreshPending && j.ErrorCode == "" {
			next := postgresTime(j.UpdatedAt.Add(c.RepeatInterval))
			if j.NextAttemptAt.After(next) {
				j.NextAttemptAt = next
			}
		}
		emailSaveJob(e, *j)
	}
	if c.SyncTrigger != "manual_refresh" && c.SyncTrigger != "notification_hint" {
		return true
	}
	box, ok := emailGet[app.EmailMailbox](e, "mailbox", j.MailboxID)
	if !ok || box.BindingGeneration != j.BindingGeneration {
		return false
	}
	if c.SyncTrigger == "notification_hint" {
		if c.EventEpoch == box.LastEventEpoch && c.EventSequence <= box.LastEventSequence {
			return false
		}
		box.LastEventEpoch, box.LastEventSequence = c.EventEpoch, c.EventSequence
	}
	box.SignalRevision++
	box.UpdatedAt = e.now
	emailSaveMailbox(e, box)
	if c.SyncTrigger == "notification_hint" {
		// No Refresh projection is created for an observer hint.
		return true
	}
	j.RefreshPending = true
	j.RefreshRequestID = emailToken()
	j.RefreshRevision = box.SignalRevision
	emailSaveSyncRefreshRequest(e, emailSyncRefreshRequest{ID: j.RefreshRequestID, OwnerID: e.owner, MailboxID: j.MailboxID,
		BindingGeneration: j.BindingGeneration, RefreshRevision: j.RefreshRevision, State: "pending", AcceptedAt: e.now})
	if j.State != app.EmailJobRunning {
		j.SyncTrigger, j.SyncActor = c.SyncTrigger, c.SyncActor
	}
	emailSaveJob(e, *j)
	emailRefreshProjection(e, *j)
	return true
}

func emailRefreshProjection(e *emailEngine, j app.EmailJob) {
	box, ok := emailGet[app.EmailMailbox](e, "mailbox", j.MailboxID)
	if !ok || box.BindingGeneration != j.BindingGeneration ||
		(box.RefreshPending == j.RefreshPending && box.RefreshRequestID == j.RefreshRequestID) {
		return
	}
	box.RefreshPending, box.RefreshRequestID = j.RefreshPending, j.RefreshRequestID
	box.UpdatedAt = e.now
	emailSaveMailbox(e, box)
}

func emailPollClaim(j *app.EmailJob) {
	if j.Kind == app.EmailJobDiscover && j.RefreshPending {
		j.RefreshActiveID = j.RefreshRequestID
		j.SyncTrigger, j.SyncActor = "manual_refresh", j.OwnerID
	}
}

func emailPollFinish(e *emailEngine, j *app.EmailJob) {
	if j.Kind != app.EmailJobDiscover {
		return
	}
	box, ok := emailGet[app.EmailMailbox](e, "mailbox", j.MailboxID)
	if !ok || !box.Active || !box.IntakeEnabled || box.BindingGeneration != j.BindingGeneration {
		emailCloseSyncRefreshRequests(e, j.MailboxID, j.BindingGeneration, 0, true)
		j.RefreshPending, j.RefreshActiveID = false, ""
		emailRefreshProjection(e, *j)
		return
	}
	// The job may have returned normally after committing an incomplete Reader
	// result. Keep that Store error on its idle deadline so a later hint cannot
	// bypass the existing one-minute recovery wait.
	if j.ErrorCode == "" && box.LastSyncErrorCode != "" {
		j.ErrorCode = box.LastSyncErrorCode
	}
	// A Refresh settles only after a qualified query. Retry exhaustion remains
	// an error on the job while the periodic executor continues to carry the
	// outstanding revision.
	covered := j.RefreshRevision > 0 && box.ReconciledRevision >= j.RefreshRevision
	if j.State != app.EmailJobRetryWait && covered {
		j.RefreshPending, j.RefreshActiveID = false, ""
	}
	qualified := (box.SyncState == app.EmailSyncIdle || box.SyncState == app.EmailSyncCoverageGap) && box.LastSyncErrorCode == ""
	followup := j.ErrorCode == "" && qualified && (box.SignalRevision > box.ReconciledRevision || j.RefreshPending && (j.RefreshActiveID == "" || j.RefreshActiveID != j.RefreshRequestID))
	if j.State == app.EmailJobRetryWait {
		// An automatic round's requested follow-up becomes the next retry, at
		// its original backoff deadline rather than immediately hammering it.
		emailRefreshProjection(e, *j)
		return
	}
	periodicOnly, tailFound := false, box.RoundNewTailFound
	if qualified && j.ErrorCode == "" && !followup {
		periodicOnly = box.SignalRevision == box.LastFinishedReconciledRevision
		if periodicOnly && tailFound {
			box.LastPeriodicOnlyDiscovery = e.now
			box.PeriodicEmptyStreak = 0
			// A mail found only by polling is a conservative reason to return
			// immediately to the one-minute fallback tier.
			if j.PollInterval > time.Minute {
				j.PollInterval = time.Minute
			}
		} else if periodicOnly && !box.LastPeriodicOnlyDiscovery.IsZero() && box.PeriodicEmptyStreak < 3 {
			box.PeriodicEmptyStreak++
		}
		if periodicOnly && !tailFound && box.WakeAdapterQualified && box.WatchState == "watching" && !box.LastPeriodicOnlyDiscovery.IsZero() {
			switch box.PeriodicEmptyStreak {
			case 1:
				j.PollInterval = 2 * time.Minute
			case 2:
				j.PollInterval = 4 * time.Minute
			default:
				j.PollInterval = 5 * time.Minute
			}
		}
		box.LastFinishedReconciledRevision = box.ReconciledRevision
		box.RoundNewTailFound = false
		box.UpdatedAt = e.now
		emailSaveMailbox(e, box)
	}
	if followup || j.PollInterval > 0 {
		j.State, j.Attempt = app.EmailJobQueued, 0
		j.NextAttemptAt = e.now
		if !followup || j.ErrorCode != "" {
			delay := j.PollInterval
			if j.ErrorCode == "" && qualified {
				if !j.PostActivityRecheck || !periodicOnly || tailFound {
					delay = min(delay, time.Minute)
					j.PostActivityRecheck = true
				} else {
					j.PostActivityRecheck = false
				}
			}
			if j.ErrorCode != "" {
				delay = max(delay, time.Minute)
			}
			j.NextAttemptAt = postgresTime(e.now.Add(delay))
		}
		if followup {
			j.PostActivityRecheck = false
			if j.RefreshPending {
				j.SyncTrigger, j.SyncActor = "manual_refresh", j.OwnerID
			} else {
				j.SyncTrigger, j.SyncActor = "notification_hint", j.OwnerID
			}
		} else {
			j.SyncTrigger, j.SyncActor = "", ""
		}
		// Keep the last failure code until a successful round. It also fences
		// automatic interval migration from shortening a recovery deadline.
	}
	emailRefreshProjection(e, *j)
}

func emailPollStop(e *emailEngine, j *app.EmailJob) {
	if j.Kind == app.EmailJobDiscover {
		emailCloseSyncRefreshRequests(e, j.MailboxID, j.BindingGeneration, 0, true)
	}
	if j.Kind == app.EmailJobDiscover && j.RefreshPending {
		j.RefreshPending, j.RefreshActiveID = false, ""
		emailRefreshProjection(e, *j)
	}
}
