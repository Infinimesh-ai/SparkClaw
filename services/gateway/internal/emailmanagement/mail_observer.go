package emailmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type observerTarget struct {
	owner   string
	box     app.EmailMailbox
	binding browsercontrol.MailObserverBinding
}

// This loop reads local intake intent, never probes the mailbox on renewal.
// A single local long poll returns immediately on an event, with a 25s idle
// heartbeat; Reader's one-minute verification remains independently scheduled.
func (s *Service) observeMail(ctx context.Context) {
	transport := s.opts.ObserverTransport
	var lease browsercontrol.MailObserverLease
	var targets []observerTarget
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = transport.ReconcileMailObservers(stop, nil)
	}()
	for ctx.Err() == nil {
		var err error
		targets, err = s.observerTargets(ctx)
		if err == nil {
			bindings := make([]browsercontrol.MailObserverBinding, 0, len(targets))
			for _, t := range targets {
				bindings = append(bindings, t.binding)
			}
			call, cancel := context.WithTimeout(ctx, 35*time.Second)
			lease, err = transport.ReconcileMailObservers(call, bindings)
			cancel()
		}
		if err == nil {
			err = s.registerObservers(ctx, lease, targets)
		}
		if err == nil {
			call, cancel := context.WithTimeout(ctx, 35*time.Second)
			var batch browsercontrol.MailObserverBatch
			batch, err = transport.PollMailObservers(call, lease)
			cancel()
			if err == nil {
				err = s.admitObserverBatch(ctx, lease, targets, batch)
			}
		}
		if err != nil && ctx.Err() == nil {
			s.degradeObservers(ctx, lease, targets)
			slog.Warn("email notification transport retry", "code", safeCode(err))
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

func (s *Service) observerTargets(ctx context.Context) ([]observerTarget, error) {
	owners, err := s.repository.ListOwnerProfiles(ctx)
	if err != nil {
		return nil, err
	}
	targets := []observerTarget{}
	used := map[string]bool{}
	for _, owner := range owners {
		boxes, err := s.repository.ListEmailMailboxes(ctx, owner.ID)
		if err != nil {
			return nil, err
		}
		for _, box := range boxes {
			if !box.Active || !box.IntakeEnabled || s.incrementalMode(box.Provider) == app.EmailProviderModeUnqualified {
				continue
			}
			setting, found, err := s.repository.GetEmailProviderSetting(ctx, owner.ID, box.Provider)
			if err != nil {
				return nil, err
			}
			if !found || !setting.Enabled || setting.State != app.EmailStateReady || setting.Account != app.EmailAccountDefault {
				continue
			}
			// A dedicated browser profile can prove one account per provider. An
			// ambiguous second owner must retain periodic Reader verification.
			if used[box.Provider] {
				continue
			}
			used[box.Provider] = true
			digest := sha256.Sum256([]byte(owner.ID))
			scope := hex.EncodeToString(digest[:])
			targets = append(targets, observerTarget{owner: owner.ID, box: box, binding: browsercontrol.MailObserverBinding{Provider: box.Provider, OwnerScope: scope, MailboxID: box.ID, AccountAddress: box.Address, BindingGeneration: box.BindingGeneration}})
		}
	}
	return targets, nil
}

func (s *Service) registerObservers(ctx context.Context, lease browsercontrol.MailObserverLease, targets []observerTarget) error {
	for _, t := range targets {
		if t.box.ObserverEpoch != lease.Epoch || t.box.ObserverCredentialGeneration != lease.CredentialGeneration {
			result, err := s.repository.ApplyEmailObserver(ctx, store.EmailObserverCommand{EmailCommand: command(t.owner, app.NewID("observer_register")), MailboxID: t.box.ID, BindingGeneration: t.box.BindingGeneration,
				CredentialGeneration: lease.CredentialGeneration, Action: "register", Epoch: lease.Epoch, PreviousEpoch: t.box.ObserverEpoch, State: "starting"})
			if err != nil {
				return err
			}
			if !result.Accepted {
				return fmt.Errorf("observer registration changed")
			}
		}
		for _, watch := range lease.Watches {
			if watch.MailboxID != t.box.ID || watch.BindingGeneration != t.box.BindingGeneration || watch.Provider != t.box.Provider || watch.State == t.box.WatchState && watch.WatchEpoch == t.box.WatchEpoch {
				continue
			}
			_, err := s.repository.ApplyEmailObserver(ctx, store.EmailObserverCommand{EmailCommand: command(t.owner, app.NewID("observer_snapshot")), MailboxID: t.box.ID, BindingGeneration: t.box.BindingGeneration, CredentialGeneration: lease.CredentialGeneration,
				Action: "snapshot", Epoch: lease.Epoch, Sequence: watch.Sequence, WatchEpoch: watch.WatchEpoch, State: watch.State})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) admitObserverBatch(ctx context.Context, lease browsercontrol.MailObserverLease, targets []observerTarget, batch browsercontrol.MailObserverBatch) error {
	// Re-read intent after a long poll: a mailbox can be disabled/rebound while
	// the socket was waiting. Store checks the binding again at commit.
	current, err := s.observerTargets(ctx)
	if err != nil {
		return err
	}
	var last int64
	for _, event := range batch.Events {
		if event.Epoch != lease.Epoch || event.CredentialGeneration != lease.CredentialGeneration || event.Sequence <= last {
			return fmt.Errorf("observer event fence invalid")
		}
		last = event.Sequence
		for _, t := range current {
			if t.binding.OwnerScope != event.OwnerScope || t.box.ID != event.MailboxID || t.box.Provider != event.Provider || t.box.BindingGeneration != event.BindingGeneration {
				continue
			}
			result, err := s.repository.ApplyEmailObserver(ctx, store.EmailObserverCommand{EmailCommand: command(t.owner, fmt.Sprintf("observer:%s:%d", event.Epoch, event.Sequence)), MailboxID: event.MailboxID, BindingGeneration: event.BindingGeneration,
				CredentialGeneration: event.CredentialGeneration, Action: event.Kind, Epoch: event.Epoch, Sequence: event.Sequence, WatchEpoch: event.WatchEpoch, State: event.State, Reason: event.Reason})
			if err != nil {
				return err
			}
			if result.Accepted && !result.Duplicate && event.Kind != "watch_state" {
				s.signal()
				slog.Info("email notification wake accepted", "provider", event.Provider, "sequence", event.Sequence, "reason", event.Reason)
			}
			break
		}
	}
	if last > 0 {
		return s.opts.ObserverTransport.AckMailObservers(ctx, lease, last)
	}
	return nil
}

func (s *Service) degradeObservers(ctx context.Context, lease browsercontrol.MailObserverLease, targets []observerTarget) {
	if lease.Epoch == "" {
		return
	}
	for _, t := range targets {
		_, err := s.repository.ApplyEmailObserver(ctx, store.EmailObserverCommand{EmailCommand: command(t.owner, app.NewID("observer_disconnect")), MailboxID: t.box.ID, BindingGeneration: t.box.BindingGeneration,
			CredentialGeneration: lease.CredentialGeneration, Action: "disconnected", Epoch: lease.Epoch, State: "degraded"})
		if err != nil && ctx.Err() == nil {
			slog.Warn("email observer degradation persistence failed", "code", safeCode(err))
		}
	}
}
