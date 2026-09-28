package emailmanagement

import (
	"context"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"testing"
	"time"
)

type observerTransportFixture struct{ ack func(int64) error }

func (f observerTransportFixture) ReconcileMailObservers(context.Context, []browsercontrol.MailObserverBinding) (browsercontrol.MailObserverLease, error) {
	return browsercontrol.MailObserverLease{}, nil
}
func (f observerTransportFixture) PollMailObservers(context.Context, browsercontrol.MailObserverLease) (browsercontrol.MailObserverBatch, error) {
	return browsercontrol.MailObserverBatch{}, nil
}
func (f observerTransportFixture) AckMailObservers(_ context.Context, _ browsercontrol.MailObserverLease, seq int64) error {
	return f.ack(seq)
}

type observerFailStore struct {
	Repository
	fail bool
}

func (f *observerFailStore) ApplyEmailObserver(ctx context.Context, c store.EmailObserverCommand) (store.EmailObserverResult, error) {
	if f.fail {
		return store.EmailObserverResult{}, errors.New("disk unavailable")
	}
	return f.Repository.ApplyEmailObserver(ctx, c)
}

func TestObserverAcknowledgesOnlyCommittedWakeAndRechecksDisabledIntent(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := time.Now().UTC()
	setting, err := repo.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "email-owner", Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked}, 0)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := s.observerTargets(t.Context())
	if err != nil || len(targets) != 1 {
		t.Fatalf("intake targets: %d %v", len(targets), err)
	}
	lease := browsercontrol.MailObserverLease{Epoch: "test-controller", CredentialGeneration: 7}
	if err = s.registerObservers(t.Context(), lease, targets); err != nil {
		t.Fatal(err)
	}
	event := browsercontrol.MailObserverEvent{Epoch: lease.Epoch, CredentialGeneration: 7, Sequence: 1, Provider: app.EmailProviderGmail, OwnerScope: targets[0].binding.OwnerScope, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, Kind: "mailbox_changed", Reason: "gmail_topic_invalidation", State: "watching"}
	batch := browsercontrol.MailObserverBatch{Epoch: lease.Epoch, Events: []browsercontrol.MailObserverEvent{event}}
	acks := 0
	s.opts.ObserverTransport = observerTransportFixture{ack: func(seq int64) error {
		acks++
		b, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
		if err != nil || b.SignalRevision == 0 || b.LastEventSequence != seq {
			t.Fatal("ack preceded durable commit")
		}
		return nil
	}}
	failing := &observerFailStore{Repository: repo, fail: true}
	s.repository = failing
	if s.admitObserverBatch(t.Context(), lease, targets, batch) == nil || acks != 0 {
		t.Fatal("failed persistence acknowledged")
	}
	failing.fail = false
	if err = s.admitObserverBatch(t.Context(), lease, targets, batch); err != nil {
		t.Fatal(err)
	}
	if acks != 1 {
		t.Fatal("committed event not acknowledged")
	}
	setting.Enabled = false
	_, err = repo.UpdateEmailProviderSetting(t.Context(), setting, setting.Version)
	if err != nil {
		t.Fatal(err)
	}
	s.opts.ObserverTransport = observerTransportFixture{ack: func(int64) error { acks++; return nil }}
	batch.Events[0].Sequence = 2
	if err = s.admitObserverBatch(t.Context(), lease, targets, batch); err != nil {
		t.Fatal(err)
	}
	b, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.LastEventSequence != 1 || acks != 2 {
		t.Fatal("disabled intent was admitted or blocked stream")
	}
}
