package connector

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type lifecycleTestAdapter struct {
	start  func(context.Context, app.NotificationBinding, binding.StartOptions) (app.NotificationBinding, error)
	poll   func(context.Context, app.NotificationBinding) (binding.PollResult, error)
	cancel func(context.Context, app.NotificationBinding) error
}

func (*lifecycleTestAdapter) Availability() error { return nil }
func (*lifecycleTestAdapter) Policy() binding.AdapterPolicy {
	return binding.AdapterPolicy{}
}
func (a *lifecycleTestAdapter) Start(ctx context.Context, record app.NotificationBinding, options binding.StartOptions) (app.NotificationBinding, error) {
	if a.start == nil {
		record.Status = app.NotificationBindingWaitingConfirm
		return record, nil
	}
	return a.start(ctx, record, options)
}
func (a *lifecycleTestAdapter) Poll(ctx context.Context, record app.NotificationBinding) (binding.PollResult, error) {
	if a.poll == nil {
		return binding.PollResult{Status: record.Status}, nil
	}
	return a.poll(ctx, record)
}
func (a *lifecycleTestAdapter) Cancel(ctx context.Context, record app.NotificationBinding) error {
	if a.cancel == nil {
		return nil
	}
	return a.cancel(ctx, record)
}

type lifecycleCredentialRecorder struct {
	mu          sync.Mutex
	repository  store.ConnectorRepository
	sealRef     string
	sealErr     error
	deleteErr   error
	abortErr    error
	seals       []string
	deletes     []string
	aborts      []string
	proofErrors []error
}

func (*lifecycleCredentialRecorder) Ready() error { return nil }

func (r *lifecycleCredentialRecorder) Seal(ctx context.Context, bindingID, kind string, _ []byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, found, err := r.repository.GetNotificationBinding(ctx, bindingID)
	if err != nil || !found || record.Status != app.NotificationBindingCredentialPending || record.CredentialKind != kind || record.CredentialRef != "" {
		r.proofErrors = append(r.proofErrors, fmt.Errorf("seal proof record=%#v found=%v err=%v", record, found, err))
	}
	r.seals = append(r.seals, bindingID+"\x00"+kind)
	if r.sealErr != nil {
		return "", r.sealErr
	}
	return r.sealRef, nil
}

func (r *lifecycleCredentialRecorder) Delete(ctx context.Context, ref string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	bindings, err := r.repository.ListNotificationBindings(ctx, "", "")
	proven := false
	for _, record := range bindings {
		if record.Status == app.NotificationBindingRevoking && record.CredentialRef == ref {
			proven = true
		}
	}
	if err != nil || !proven {
		r.proofErrors = append(r.proofErrors, fmt.Errorf("delete proof ref=%q found=%v err=%v", ref, proven, err))
	}
	r.deletes = append(r.deletes, ref)
	return r.deleteErr
}

func (r *lifecycleCredentialRecorder) AbortSeal(ctx context.Context, bindingID, kind string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, found, err := r.repository.GetNotificationBinding(ctx, bindingID)
	if err != nil || !found || (record.Status != app.NotificationBindingStarting && record.Status != app.NotificationBindingCredentialPending && record.Status != app.NotificationBindingRevoking) || record.CredentialKind != kind {
		r.proofErrors = append(r.proofErrors, fmt.Errorf("abort proof record=%#v found=%v err=%v", record, found, err))
	}
	r.aborts = append(r.aborts, bindingID+"\x00"+kind)
	return r.abortErr
}

func (r *lifecycleCredentialRecorder) snapshot() (seals, deletes, aborts []string, proofErrors []error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seals), slices.Clone(r.deletes), slices.Clone(r.aborts), slices.Clone(r.proofErrors)
}

type lifecycleRuntimeProbe struct {
	check   func() error
	started chan error
}

func (p *lifecycleRuntimeProbe) Run(ctx context.Context, _ connectorruntime.RuntimeScope) error {
	err := p.check()
	p.started <- err
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func lifecycleTestConfig() config.Config {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: true, Provider: "alpha-provider"},
	}
	return cfg
}

func registerLifecycleTestConnector(t *testing.T, registry *Registry, adapter binding.Adapter, runtime connectorruntime.Runtime, kind string) {
	t.Helper()
	if err := registry.Register(Registration{
		Channel: "alpha", SetupKind: app.ConnectorSetupSecret, Binding: adapter,
		BindingProvider: "alpha-provider", CredentialKind: kind, Runtime: runtime,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBindingLifecycleCreatesStartingBeforeProviderWork(t *testing.T) {
	st := store.NewMemoryStore()
	adapter := &lifecycleTestAdapter{}
	adapter.start = func(ctx context.Context, record app.NotificationBinding, _ binding.StartOptions) (app.NotificationBinding, error) {
		persisted, found, err := st.GetNotificationBinding(ctx, record.ID)
		if err != nil || !found || persisted.Status != app.NotificationBindingStarting || persisted.Version != 1 || persisted.CredentialKind != "alpha-token" {
			return app.NotificationBinding{}, fmt.Errorf("provider started before durable identity: record=%#v found=%v err=%v", persisted, found, err)
		}
		record.Status = app.NotificationBindingWaitingConfirm
		return record, nil
	}
	registry := NewRegistry(lifecycleTestConfig(), st)
	registerLifecycleTestConnector(t, registry, adapter, nil, "alpha-token")
	started, err := registry.StartNotificationBinding(t.Context(), app.NotificationBinding{
		OwnerID: "owner", ActorID: "actor", Channel: "alpha",
	}, binding.StartOptions{CredentialSecret: "not-persisted"})
	if err != nil || started.Status != app.NotificationBindingWaitingConfirm || started.Version != 2 {
		t.Fatalf("started=%#v err=%v", started, err)
	}
}

func TestBindingLifecyclePollPersistsCredentialPendingBeforeSeal(t *testing.T) {
	st := store.NewMemoryStore()
	credentials := &lifecycleCredentialRecorder{repository: st, sealRef: "cred_alpha-binding"}
	adapter := &lifecycleTestAdapter{poll: func(context.Context, app.NotificationBinding) (binding.PollResult, error) {
		return binding.PollResult{
			Status: app.NotificationBindingActive, CredentialKind: "alpha-token", CredentialSecret: "secret",
		}, nil
	}}
	registry := NewRegistry(lifecycleTestConfig(), st).WithCredentialLifecycle(credentials)
	registerLifecycleTestConnector(t, registry, adapter, nil, "alpha-token")
	started, err := registry.StartNotificationBinding(t.Context(), app.NotificationBinding{OwnerID: "owner", ActorID: "actor", Channel: "alpha"}, binding.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	active, err := registry.PollNotificationBinding(t.Context(), started.ID)
	seals, _, _, proofErrors := credentials.snapshot()
	if err != nil || active.Status != app.NotificationBindingActive || active.CredentialRef != "cred_alpha-binding" || active.Version != 4 || len(seals) != 1 || len(proofErrors) != 0 {
		t.Fatalf("active=%#v err=%v seals=%v proofErrors=%v", active, err, seals, proofErrors)
	}
}

func TestBindingLifecycleRevokePersistsProofBeforeCleanup(t *testing.T) {
	st := store.NewMemoryStore()
	active := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-revoke", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingActive, CredentialKind: "alpha-token", CredentialRef: "cred_revoke",
	})
	credentials := &lifecycleCredentialRecorder{repository: st}
	registry := NewRegistry(lifecycleTestConfig(), st).WithCredentialLifecycle(credentials)
	registerLifecycleTestConnector(t, registry, &lifecycleTestAdapter{}, nil, "alpha-token")
	revoked, err := registry.RevokeNotificationBinding(t.Context(), active.ID)
	_, deletes, _, proofErrors := credentials.snapshot()
	if err != nil || revoked.Status != app.NotificationBindingRevoked || revoked.RevokedAt == nil || !slices.Equal(deletes, []string{"cred_revoke"}) || len(proofErrors) != 0 {
		t.Fatalf("revoked=%#v err=%v deletes=%v proofErrors=%v", revoked, err, deletes, proofErrors)
	}
}

func TestBindingLifecycleCleanupFailureRetainsRecoveryState(t *testing.T) {
	st := store.NewMemoryStore()
	active := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-revoke-failure", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingActive, CredentialKind: "alpha-token", CredentialRef: "cred_revoke-failure",
	})
	credentials := &lifecycleCredentialRecorder{repository: st, deleteErr: errors.New("vault unavailable")}
	registry := NewRegistry(lifecycleTestConfig(), st).WithCredentialLifecycle(credentials)
	registerLifecycleTestConnector(t, registry, &lifecycleTestAdapter{}, nil, "alpha-token")
	if _, err := registry.RevokeNotificationBinding(t.Context(), active.ID); err == nil {
		t.Fatal("Vault failure was accepted")
	}
	retained, found := storetest.MustGetNotificationBinding(t, st, active.ID)
	if !found || retained.Status != app.NotificationBindingRevoking || retained.CredentialRef != active.CredentialRef {
		t.Fatalf("retained=%#v found=%v", retained, found)
	}
}

func TestBindingLifecycleRejectsMismatchedLegacyCredentialOwnership(t *testing.T) {
	st := store.NewMemoryStore()
	ref := "provider:openclaw-weixin-qr:another-binding"
	if _, err := st.SaveCredentialSecret(t.Context(), store.NewCredentialCreate(app.CredentialSecret{
		Ref: ref, Kind: "openclaw-weixin-bot-token", Value: "legacy-raw-token",
	})); err != nil {
		t.Fatal(err)
	}
	active := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-legacy-mismatch", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingActive, CredentialRef: ref,
	})
	vault := credential.New(st, credential.Options{Key: strings.Repeat("m", 32)})
	registry := NewRegistry(lifecycleTestConfig(), st).WithCredentialLifecycle(vault)
	registerLifecycleTestConnector(t, registry, &lifecycleTestAdapter{}, nil, "alpha-token")
	if _, err := registry.RevokeNotificationBinding(t.Context(), active.ID); err == nil {
		t.Fatal("mismatched legacy credential ownership was accepted")
	}
	retained, found := storetest.MustGetNotificationBinding(t, st, active.ID)
	if !found || retained.Status != app.NotificationBindingRevoking || retained.CredentialRef != ref {
		t.Fatalf("retained binding=%#v found=%v", retained, found)
	}
	if _, found, err := st.GetCredentialSecret(t.Context(), ref); err != nil || !found {
		t.Fatalf("mismatched legacy credential was deleted: found=%v err=%v", found, err)
	}
}

func TestBindingLifecycleStartupRecoveryCompletesBeforeWorkers(t *testing.T) {
	st := store.NewMemoryStore()
	starting := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-starting", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingStarting, CredentialKind: "alpha-token",
	})
	pending := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-pending", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingCredentialPending, CredentialKind: "alpha-token",
	})
	revoking := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-revoking", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingRevoking, CredentialKind: "alpha-token", CredentialRef: "cred_recovery",
	})
	credentials := &lifecycleCredentialRecorder{repository: st}
	probe := &lifecycleRuntimeProbe{started: make(chan error, 1)}
	probe.check = func() error {
		for id, want := range map[string]string{
			starting.ID: app.NotificationBindingFailed,
			pending.ID:  app.NotificationBindingFailed,
			revoking.ID: app.NotificationBindingRevoked,
		} {
			got, found, err := st.GetNotificationBinding(context.Background(), id)
			if err != nil || !found || got.Status != want {
				return fmt.Errorf("worker observed unrecovered binding %s: %#v found=%v err=%v", id, got, found, err)
			}
		}
		return nil
	}
	registry := NewRegistry(lifecycleTestConfig(), st).WithCredentialLifecycle(credentials)
	registerLifecycleTestConnector(t, registry, &lifecycleTestAdapter{}, probe, "alpha-token")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-probe.started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("connector worker did not start after recovery")
	}
	_, deletes, aborts, proofErrors := credentials.snapshot()
	if !slices.Equal(deletes, []string{"cred_recovery"}) || len(aborts) != 2 || len(proofErrors) != 0 {
		t.Fatalf("deletes=%v aborts=%v proofErrors=%v", deletes, aborts, proofErrors)
	}
}

func TestBindingLifecycleRecoveryFailureBlocksWorkersAndRetainsState(t *testing.T) {
	st := store.NewMemoryStore()
	record := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-recovery-failure", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingStarting, CredentialKind: "alpha-token",
	})
	credentials := &lifecycleCredentialRecorder{repository: st, abortErr: errors.New("Vault unavailable")}
	probe := &lifecycleRuntimeProbe{started: make(chan error, 1), check: func() error { return nil }}
	registry := NewRegistry(lifecycleTestConfig(), st).WithCredentialLifecycle(credentials)
	registerLifecycleTestConnector(t, registry, &lifecycleTestAdapter{}, probe, "alpha-token")
	if err := registry.Start(t.Context()); err == nil {
		t.Fatal("recovery failure was accepted")
	}
	retained, found := storetest.MustGetNotificationBinding(t, st, record.ID)
	if !found || retained.Status != app.NotificationBindingStarting {
		t.Fatalf("retained=%#v found=%v", retained, found)
	}
	select {
	case <-probe.started:
		t.Fatal("connector worker started before successful recovery")
	default:
	}
}

func TestBindingLifecycleFileRestartRetainsCleanupProofAndTerminalIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connector-recovery.json")
	key := strings.Repeat("r", 32)
	st, err := store.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	vault := credential.New(st, credential.Options{Key: key})

	pending := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-file-pending", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingCredentialPending, CredentialKind: "alpha-token",
	})
	pendingRef, err := vault.Seal(t.Context(), pending.ID, pending.CredentialKind, []byte("pending-secret"))
	if err != nil {
		t.Fatal(err)
	}

	starting := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-file-revoking", OwnerID: "owner", ActorID: "actor", Channel: "alpha", Provider: "alpha-provider",
		Status: app.NotificationBindingStarting, CredentialKind: "alpha-token",
	})
	revokingRef, err := vault.Seal(t.Context(), starting.ID, starting.CredentialKind, []byte("revoking-secret"))
	if err != nil {
		t.Fatal(err)
	}
	active := starting
	active.Status = app.NotificationBindingActive
	active.CredentialRef = revokingRef
	active = storetest.MustUpdateNotificationBinding(t, st, starting, active)
	revoking := active
	revoking.Status = app.NotificationBindingRevoking
	revoking = storetest.MustUpdateNotificationBinding(t, st, active, revoking)

	legacyRef := "provider:openclaw-weixin-qr:binding-file-legacy-revoking"
	if _, err := st.SaveCredentialSecret(t.Context(), store.NewCredentialCreate(app.CredentialSecret{
		Ref: legacyRef, Kind: "openclaw-weixin-bot-token", Value: "legacy-raw-token",
	})); err != nil {
		t.Fatal(err)
	}
	legacyActive := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-file-legacy-revoking", OwnerID: "owner", ActorID: "actor", Channel: "weixin",
		Provider: "openclaw-weixin-qr", Status: app.NotificationBindingActive, CredentialRef: legacyRef,
	})
	legacyRevoking := legacyActive
	legacyRevoking.Status = app.NotificationBindingRevoking
	legacyRevoking = storetest.MustUpdateNotificationBinding(t, st, legacyActive, legacyRevoking)

	reloaded, err := store.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	reloadedVault := credential.New(reloaded, credential.Options{Key: key})
	registry := NewRegistry(lifecycleTestConfig(), reloaded).WithCredentialLifecycle(reloadedVault)
	registerLifecycleTestConnector(t, registry, &lifecycleTestAdapter{}, nil, "alpha-token")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}

	failed, found := storetest.MustGetNotificationBinding(t, reloaded, pending.ID)
	if !found || failed.Status != app.NotificationBindingFailed {
		t.Fatalf("pending recovery=%#v found=%v", failed, found)
	}
	revoked, found := storetest.MustGetNotificationBinding(t, reloaded, revoking.ID)
	if !found || revoked.Status != app.NotificationBindingRevoked || revoked.RevokedAt == nil {
		t.Fatalf("revoking recovery=%#v found=%v", revoked, found)
	}
	legacyRevoked, found := storetest.MustGetNotificationBinding(t, reloaded, legacyRevoking.ID)
	if !found || legacyRevoked.Status != app.NotificationBindingRevoked || legacyRevoked.RevokedAt == nil {
		t.Fatalf("legacy revoking recovery=%#v found=%v", legacyRevoked, found)
	}
	for _, ref := range []string{pendingRef, revokingRef, legacyRef} {
		if secret, found, err := reloaded.GetCredentialSecret(t.Context(), ref); err != nil || found || secret.Ref != "" {
			t.Fatalf("credential %q survived recovery: %#v found=%v err=%v", ref, secret, found, err)
		}
	}
	for _, terminal := range []app.NotificationBinding{failed, revoked, legacyRevoked} {
		candidate, err := reloaded.CreateNotificationBinding(t.Context(), app.NotificationBinding{
			ID: terminal.ID, OwnerID: terminal.OwnerID, ActorID: terminal.ActorID,
			Channel: terminal.Channel, Provider: terminal.Provider, Status: app.NotificationBindingStarting,
		})
		if candidate.ID != "" || store.StoreErrorCodeOf(err) != store.StoreErrorConflict {
			t.Fatalf("terminal ID %q was reusable: candidate=%#v err=%v", terminal.ID, candidate, err)
		}
	}
}
