package store

import (
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// All transitions run in the existing owner transaction in Memory, File and
// PostgreSQL. A manual request is a single durable bit, not another queued job.
func emailPollRequest(e *emailEngine, j *app.EmailJob, c EmailJobRequest) {
	if j.Kind != app.EmailJobDiscover {
		return
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
	if c.SyncTrigger != "manual_refresh" || j.RefreshPending {
		return
	}
	j.RefreshPending = true
	j.RefreshRequestID = emailToken()
	if j.State != app.EmailJobRunning {
		j.SyncTrigger, j.SyncActor = c.SyncTrigger, c.SyncActor
	}
	emailSaveJob(e, *j)
	emailRefreshProjection(e, *j)
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
		j.RefreshPending, j.RefreshActiveID = false, ""
		emailRefreshProjection(e, *j)
		return
	}
	// A failed refresh retains the request through its existing bounded retry
	// budget; terminal exhaustion releases the button with the recorded error.
	if j.RefreshActiveID != "" && j.State != app.EmailJobRetryWait {
		j.RefreshPending, j.RefreshActiveID = false, ""
	}
	followup := j.RefreshPending && j.RefreshActiveID == ""
	if j.State == app.EmailJobRetryWait {
		// An automatic round's requested follow-up becomes the next retry, at
		// its original backoff deadline rather than immediately hammering it.
		emailRefreshProjection(e, *j)
		return
	}
	if followup || j.PollInterval > 0 {
		j.State, j.Attempt = app.EmailJobQueued, 0
		j.NextAttemptAt = e.now
		if !followup || j.ErrorCode != "" {
			delay := j.PollInterval
			if j.ErrorCode != "" {
				delay = max(delay, time.Minute)
			}
			j.NextAttemptAt = postgresTime(e.now.Add(delay))
		}
		if followup {
			j.SyncTrigger, j.SyncActor = "manual_refresh", j.OwnerID
		} else {
			j.SyncTrigger, j.SyncActor = "", ""
		}
		// Keep the last failure code until a successful round. It also fences
		// automatic interval migration from shortening a recovery deadline.
	}
	emailRefreshProjection(e, *j)
}

func emailPollStop(e *emailEngine, j *app.EmailJob) {
	if j.Kind == app.EmailJobDiscover && j.RefreshPending {
		j.RefreshPending, j.RefreshActiveID = false, ""
		emailRefreshProjection(e, *j)
	}
}
