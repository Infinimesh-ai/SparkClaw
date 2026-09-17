package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"time"
)

const EmailTimelinePolicyVersion = "email-timeline-v2"

type EmailTimelinePolicy struct {
	Version        string            `json:"version"`
	ActivatedAt    time.Time         `json:"activated_at"`
	Remaining      bool              `json:"remaining"`
	RetiredCount   int               `json:"retired_count"`
	Cursors        map[string]string `json:"cursors,omitempty"`
	CompletedKinds map[string]bool   `json:"completed_kinds,omitempty"`
}

func emailTimelineLegacyKind(kind string) bool {
	return containsEmail([]string{app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobThreadSync, app.EmailJobSourceRecovery}, kind)
}

func emailTimelineActive(e *emailEngine) bool {
	p, ok := emailGet[EmailTimelinePolicy](e, "counter", "timeline_policy")
	if ok && p.Version != EmailTimelinePolicyVersion {
		e.err = errEmailConflict
	}
	return ok
}

// Activation immediately fences the old executor, then retires at most 100
// records per call. Completion is durable: future calls do not scan history.
func emailActivateTimeline(e *emailEngine) (EmailTimelinePolicy, error) {
	p, ok := emailGet[EmailTimelinePolicy](e, "counter", "timeline_policy")
	if ok && p.Version != EmailTimelinePolicyVersion {
		return p, errEmailConflict
	}
	if ok && !p.Remaining {
		return p, e.err
	}
	if !ok {
		p = EmailTimelinePolicy{Version: EmailTimelinePolicyVersion, ActivatedAt: e.now}
	}
	if p.Cursors == nil {
		p.Cursors = map[string]string{}
	}
	if p.CompletedKinds == nil {
		p.CompletedKinds = map[string]bool{}
	}
	p.Remaining = false
	for _, kind := range []string{app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobThreadSync, app.EmailJobSourceRecovery} {
		if p.CompletedKinds[kind] {
			continue
		}
		jobs := emailList[app.EmailJob](e, emailRowsQuery{Kind: "job", Related: kind, After: p.Cursors[kind], States: []string{app.EmailJobQueued, app.EmailJobRetryWait, app.EmailJobRunning, app.EmailJobPaused, app.EmailJobFailed}, Limit: 26})
		if len(jobs) > 25 {
			p.Remaining = true
			jobs = jobs[:25]
		} else {
			p.CompletedKinds[kind] = true
		}
		for _, job := range jobs {
			at := e.now
			job.RetiredAt, job.RetiredFromState, job.RetirementReason = &at, job.State, EmailTimelinePolicyVersion
			job.State = app.EmailJobRetired
			job.LeaseToken, job.LeaseExpiresAt = "", time.Time{}
			job.UpdatedAt = e.now
			emailSaveJob(e, job)
			p.Cursors[kind] = emailOrder(job.NextAttemptAt, job.ID)
			p.RetiredCount++
		}
	}
	emailPut(e, "counter", "timeline_policy", "", "", "", "", "timeline_policy", p)
	return p, e.err
}
