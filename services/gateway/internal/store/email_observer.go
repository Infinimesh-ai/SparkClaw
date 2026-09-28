package store

import (
	"context"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Registration and event admission use the same owner transaction as Reader
// jobs. The caller acknowledges Controller delivery only after this commits.
type EmailObserverCommand struct {
	EmailCommand
	MailboxID            string
	BindingGeneration    int64
	CredentialGeneration int64
	Action               string
	Epoch                string
	PreviousEpoch        string
	Sequence             int64
	WatchEpoch           string
	State                string
	Reason               string
}

type EmailObserverResult struct {
	Accepted  bool `json:"accepted"`
	Duplicate bool `json:"duplicate"`
}

func emailObserver(e *emailEngine, c EmailObserverCommand) (EmailObserverResult, error) {
	out := EmailObserverResult{}
	if c.CredentialGeneration < 1 || len(c.Epoch) < 1 || len(c.Epoch) > 128 || len(c.WatchEpoch) > 128 ||
		!containsEmail([]string{"register", "snapshot", "watch_state", "mailbox_changed", "resync_required", "disconnected"}, c.Action) ||
		!containsEmail([]string{"starting", "watching", "degraded", "login_required", "stopped"}, c.State) {
		return out, errEmailInvalid
	}
	box, ok := emailGet[app.EmailMailbox](e, "mailbox", c.MailboxID)
	if !ok || !box.Active || !box.IntakeEnabled || box.BindingGeneration != c.BindingGeneration {
		return out, e.err
	}
	if c.Action == "register" {
		if box.ObserverCredentialGeneration > c.CredentialGeneration {
			return out, nil
		}
		if box.ObserverEpoch == c.Epoch && box.ObserverCredentialGeneration == c.CredentialGeneration {
			out.Accepted = true
			out.Duplicate = true
			return out, nil
		}
		if box.ObserverEpoch != c.PreviousEpoch {
			return out, nil
		}
		box.ObserverEpoch = c.Epoch
		box.ObserverCredentialGeneration = c.CredentialGeneration
		box.LastEventEpoch = ""
		box.LastEventSequence = 0
		box.WatchEpoch = ""
		box.WatchState = "starting"
		box.WakeAdapterQualified = false
		box.UpdatedAt = e.now
		emailSaveMailbox(e, box)
		out.Accepted = true
		return out, e.err
	}
	if box.ObserverEpoch != c.Epoch || box.ObserverCredentialGeneration != c.CredentialGeneration {
		return out, nil
	}
	if c.Action == "snapshot" {
		if c.Sequence < box.LastEventSequence {
			return out, nil
		}
		if box.WatchState != c.State || box.WatchEpoch != c.WatchEpoch {
			box.WatchState = c.State
			box.WatchEpoch = c.WatchEpoch
			box.UpdatedAt = e.now
			emailSaveMailbox(e, box)
		}
		out.Accepted = true
		return out, e.err
	}
	if c.Action == "disconnected" {
		if box.WatchState != "degraded" {
			box.WatchState = "degraded"
			box.WakeAdapterQualified = false
			box.UpdatedAt = e.now
			emailSaveMailbox(e, box)
		}
		out.Accepted = true
		return out, e.err
	}
	if c.Sequence < 1 {
		return out, errEmailInvalid
	}
	if c.Sequence <= box.LastEventSequence {
		out.Accepted = true
		out.Duplicate = true
		return out, nil
	}
	if c.Action == "mailbox_changed" && !containsEmail([]string{"qq_inbound_envelope", "gmail_topic_invalidation", "outlook_delivery_change"}, c.Reason) {
		return out, errEmailInvalid
	}
	if c.Action == "resync_required" && !containsEmail([]string{"registration", "document_replaced", "sequence_gap", "buffer_overflow", "observer_degraded"}, c.Reason) {
		return out, errEmailInvalid
	}
	box.WatchState = c.State
	box.WatchEpoch = c.WatchEpoch
	box.LastNotificationReason = strings.TrimSpace(c.Reason)
	box.LastNotificationAt = e.now
	box.WakeAdapterQualified = false
	box.UpdatedAt = e.now
	emailSaveMailbox(e, box)
	if c.Action == "watch_state" {
		box.LastEventEpoch = c.Epoch
		box.LastEventSequence = c.Sequence
		emailSaveMailbox(e, box)
	} else {
		_, err := emailRequest(e, EmailJobRequest{EmailCommand: c.EmailCommand, Kind: app.EmailJobDiscover, TargetID: box.ID, MailboxID: box.ID,
			BindingGeneration: box.BindingGeneration, Rearm: true, SyncTrigger: "notification_hint", SyncActor: "mail_observer", EventEpoch: c.Epoch, EventSequence: c.Sequence})
		if err != nil {
			return out, err
		}
	}
	out.Accepted = true
	return out, e.err
}

func (s *MemoryStore) ApplyEmailObserver(ctx context.Context, c EmailObserverCommand) (EmailObserverResult, error) {
	if c.CommandKey == "" {
		return EmailObserverResult{}, errEmailCommandInvalid(ctx, OperationApplyEmailObserver)
	}
	return emailMemoryRun(s, ctx, OperationApplyEmailObserver, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailObserverResult, error) { return emailObserver(e, c) })
}
func (s *PostgresStore) ApplyEmailObserver(ctx context.Context, c EmailObserverCommand) (EmailObserverResult, error) {
	if c.CommandKey == "" {
		return EmailObserverResult{}, errEmailCommandInvalid(ctx, OperationApplyEmailObserver)
	}
	return emailPostgresRun(s, ctx, OperationApplyEmailObserver, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailObserverResult, error) { return emailObserver(e, c) })
}
func (s *FileStore) ApplyEmailObserver(ctx context.Context, c EmailObserverCommand) (EmailObserverResult, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApplyEmailObserver, fileAdmissionCapacity)
	if err != nil {
		return EmailObserverResult{}, err
	}
	defer release()
	if c.CommandKey == "" {
		return EmailObserverResult{}, errEmailCommandInvalid(ctx, OperationApplyEmailObserver)
	}
	return emailFileRun(s, ctx, OperationApplyEmailObserver, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailObserverResult, error) { return emailObserver(e, c) })
}
