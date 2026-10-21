package emailmanagement

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	SyncHealthHealthy     = "healthy"
	SyncHealthStarting    = "starting"
	SyncHealthStalled     = "stalled"
	SyncHealthPaused      = "paused"
	SyncHealthNotEnabled  = "not_enabled"
	SyncHealthAttention   = "needs_attention"
	SyncHealthUnavailable = "unavailable"
)

// SyncHealth is an on-demand projection for Check connection. It never changes
// a provider's sign-in state or advances a mailbox checkpoint.
type SyncHealth struct {
	State                   string     `json:"state"`
	Code                    string     `json:"code,omitempty"`
	CheckedAt               time.Time  `json:"checked_at"`
	PollThrough             *time.Time `json:"poll_through,omitempty"`
	ExpectedIntervalSeconds int64      `json:"expected_interval_seconds,omitempty"`
	StallAfterSeconds       int64      `json:"stall_after_seconds,omitempty"`
}

func (s *Service) ProviderSyncHealth(ctx context.Context, owner, provider string) (SyncHealth, error) {
	out := SyncHealth{State: SyncHealthNotEnabled, CheckedAt: s.now()}
	mailboxes, err := s.repository.ListEmailMailboxes(ctx, owner)
	if err != nil {
		return out, err
	}
	for _, box := range mailboxes {
		if box.Provider != provider {
			continue
		}
		if !box.Active {
			out.State = SyncHealthPaused
			continue
		}
		if !box.IntakeEnabled {
			out.State = SyncHealthPaused
			return out, nil
		}
		jobs, err := s.repository.ListEmailJobs(ctx, store.EmailQuery{OwnerID: owner, MailboxID: box.ID, JobKind: app.EmailJobDiscover, Limit: 100})
		if err != nil {
			return out, err
		}
		var current *app.EmailJob
		for i := range jobs {
			job := &jobs[i]
			if job.BindingGeneration == box.BindingGeneration && job.State != app.EmailJobRetired && (current == nil || job.UpdatedAt.After(current.UpdatedAt)) {
				current = job
			}
		}
		// Do not guess an active job's schedule from a truncated history.
		if current == nil && len(jobs) == 100 {
			out.State = SyncHealthUnavailable
			return out, nil
		}
		return s.mailboxSyncHealth(box, current), nil
	}
	return out, nil
}

func (s *Service) mailboxSyncHealth(box app.EmailMailbox, job *app.EmailJob) SyncHealth {
	out := SyncHealth{State: SyncHealthHealthy, CheckedAt: s.now()}
	if !box.Active || !box.IntakeEnabled || job != nil && job.State == app.EmailJobPaused {
		out.State = SyncHealthPaused
		return out
	}
	if s.incrementalMode(box.Provider) == app.EmailProviderModeUnqualified {
		out.State, out.Code = SyncHealthAttention, "email_incremental_unqualified"
		return out
	}
	if box.SyncState == app.EmailSyncOverflowConfirmation {
		out.State, out.Code = SyncHealthAttention, "email_interval_overflow"
		return out
	}
	interval := s.verificationInterval(box)
	if job != nil {
		interval = max(interval, job.PollInterval)
	}
	// Allow normal quiet-tier polling and a full bounded collection. Repeated
	// failures or renewed leases never extend this window without progress.
	stallAfter := max(15*time.Minute, 3*interval, interval+s.jobTimeout(app.EmailJobDiscover)+s.opts.ScanInterval)
	out.ExpectedIntervalSeconds, out.StallAfterSeconds = int64(interval/time.Second), int64(stallAfter/time.Second)
	anchor := box.IntakeEnabledAt
	if anchor.IsZero() {
		anchor = box.ActivatedAt
		if job != nil && job.CreatedAt.After(anchor) {
			anchor = job.CreatedAt
		}
	}
	// PollThrough is a verified collection watermark, including empty rounds;
	// provider probes, task attempts and newest-message timestamps are not.
	if !box.PollThrough.IsZero() {
		at := box.PollThrough
		out.PollThrough = &at
	}
	if box.PollThrough.After(anchor) {
		anchor = box.PollThrough
	} else {
		out.State = SyncHealthStarting
	}
	if anchor.IsZero() {
		out.State = SyncHealthUnavailable
		return out
	}
	if out.CheckedAt.Sub(anchor) > stallAfter {
		out.State, out.Code = SyncHealthStalled, "email_sync_stalled"
	}
	return out
}
