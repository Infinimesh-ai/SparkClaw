package iscppairing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/provisioning"
)

type controlledPairingRepository struct {
	memory *store.MemoryStore

	mu                 sync.Mutex
	saveAfterPersist   error
	saveWithoutPersist error
	getErrors          []error
	listErr            error
	operations         []string
	saveContext        context.Context
	getContext         context.Context
	listContext        context.Context
}

func newControlledPairingRepository() *controlledPairingRepository {
	return &controlledPairingRepository{memory: store.NewMemoryStore()}
}

func (r *controlledPairingRepository) SaveISCPOnboarding(ctx context.Context, receipt app.ISCPOnboarding) (app.ISCPOnboarding, error) {
	r.mu.Lock()
	r.saveContext = ctx
	r.operations = append(r.operations, "save")
	withoutPersist := r.saveWithoutPersist
	afterPersist := r.saveAfterPersist
	r.mu.Unlock()
	if withoutPersist != nil {
		return app.ISCPOnboarding{}, withoutPersist
	}
	saved, err := r.memory.SaveISCPOnboarding(ctx, receipt)
	if err != nil {
		return app.ISCPOnboarding{}, err
	}
	if afterPersist != nil {
		return app.ISCPOnboarding{}, afterPersist
	}
	return saved, nil
}

func (r *controlledPairingRepository) GetISCPOnboarding(ctx context.Context, id string) (app.ISCPOnboarding, bool, error) {
	r.mu.Lock()
	r.getContext = ctx
	r.operations = append(r.operations, "get")
	var injected error
	if len(r.getErrors) > 0 {
		injected = r.getErrors[0]
		r.getErrors = r.getErrors[1:]
	}
	r.mu.Unlock()
	if injected != nil {
		return app.ISCPOnboarding{}, false, injected
	}
	return r.memory.GetISCPOnboarding(ctx, id)
}

func (r *controlledPairingRepository) ListISCPOnboardings(ctx context.Context, ownerID string) ([]app.ISCPOnboarding, error) {
	r.mu.Lock()
	r.listContext = ctx
	r.operations = append(r.operations, "list")
	injected := r.listErr
	r.mu.Unlock()
	if injected != nil {
		return nil, injected
	}
	return r.memory.ListISCPOnboardings(ctx, ownerID)
}

func (r *controlledPairingRepository) AddAudit(ctx context.Context, event app.AuditEvent) error {
	r.mu.Lock()
	r.operations = append(r.operations, "audit")
	r.mu.Unlock()
	return r.memory.AddAudit(ctx, event)
}

type countingPairingAuthority struct {
	mu          sync.Mutex
	calls       int
	active      int
	maxActive   int
	result      AuthorityResult
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
}

func (a *countingPairingAuthority) Ready(context.Context) error { return nil }

func (a *countingPairingAuthority) IssuePairingTicket(ctx context.Context, request AuthorityRequest) (AuthorityResult, error) {
	a.mu.Lock()
	a.calls++
	a.active++
	if a.active > a.maxActive {
		a.maxActive = a.active
	}
	a.mu.Unlock()
	if a.entered != nil {
		a.enteredOnce.Do(func() { close(a.entered) })
		select {
		case <-a.release:
		case <-ctx.Done():
			return AuthorityResult{}, ctx.Err()
		}
	}
	a.mu.Lock()
	a.active--
	a.mu.Unlock()
	result := a.result
	result.Ticket.TicketID = request.RequestRef
	return result, nil
}

func newPendingTestService(now time.Time, repository Repository, authority Authority) *Service {
	service := New(repository, Options{
		Enabled: true, DomainID: "domain-a", ExpectedTicketType: provisioning.TypePairingTicket,
		DefaultTTL: 10 * time.Minute, Authority: authority,
	})
	service.now = func() time.Time { return now }
	return service
}

func pendingTestAuthority(now time.Time) *countingPairingAuthority {
	return &countingPairingAuthority{result: AuthorityResult{AuthorityRef: "authority-ref", Ticket: validTicket(now)}}
}

func TestPairingDefinitePersistenceFailureDoesNotDiscloseTicket(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	rawCause := errors.New("filesystem path /secret/state.json failed")
	repository.saveWithoutPersist = &store.StoreError{
		Code: store.StoreErrorDurability, Operation: store.OperationISCPOnboardingSave, Err: rawCause,
	}
	authority := pendingTestAuthority(now)
	service := newPendingTestService(now, repository, authority)
	issued, err := service.Start(context.Background(), app.DefaultOwnerID, app.DefaultOwnerID, StartRequest{DisplayName: "gateway"}, now)
	if issued.Ticket.Signature.Value != "" || !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "/secret/") {
		t.Fatalf("definite failure disclosed unsafe result: issued=%#v err=%v", issued, err)
	}
	if service.pending != nil || authority.calls != 1 || len(mustPairingListAudit(t, repository.memory, "")) != 0 {
		t.Fatalf("definite failure state pending=%v authority=%d audits=%d", service.pending, authority.calls, len(mustPairingListAudit(t, repository.memory, "")))
	}
}

func TestPairingUnknownOutcomeImmediatelyReconcilesOriginalTicket(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	repository.saveAfterPersist = &store.StoreError{
		Code: store.StoreErrorUnknownOutcome, Operation: store.OperationISCPOnboardingSave, Err: errors.New("commit response lost"),
	}
	authority := pendingTestAuthority(now)
	service := newPendingTestService(now, repository, authority)
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request-context")
	issued, err := service.Start(ctx, app.DefaultOwnerID, "actor-a", StartRequest{DisplayName: "gateway"}, now)
	if err != nil || issued.Ticket.Signature.Value == "" || authority.calls != 1 || service.pending != nil {
		t.Fatalf("immediate reconciliation issued=%#v err=%v calls=%d pending=%v", issued, err, authority.calls, service.pending)
	}
	repository.mu.Lock()
	operations := append([]string(nil), repository.operations...)
	saveContext, getContext := repository.saveContext, repository.getContext
	repository.mu.Unlock()
	if strings.Join(operations, ",") != "save,get,audit" {
		t.Fatalf("persistence/audit order = %v", operations)
	}
	if saveContext != ctx || getContext != ctx {
		t.Fatalf("request context was not propagated to save/get: save=%v get=%v", saveContext, getContext)
	}
}

func TestPairingDoesNotDiscloseTicketThatExpiresDuringPersistence(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	authority := pendingTestAuthority(now)
	service := newPendingTestService(now, repository, authority)
	service.now = func() time.Time { return now.Add(11 * time.Minute) }

	issued, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", StartRequest{DisplayName: "gateway"}, now)
	if issued.Ticket.Signature.Value != "" || !errors.Is(err, ErrTicketExpired) || service.pending != nil {
		t.Fatalf("expired ticket disclosed after persistence: issued=%#v err=%v pending=%v", issued, err, service.pending)
	}
	onboardings, listErr := repository.memory.ListISCPOnboardings(context.Background(), app.DefaultOwnerID)
	if listErr != nil || len(onboardings) != 1 || len(mustPairingListAudit(t, repository.memory, "")) != 1 {
		t.Fatalf("durable receipt/audit missing after expiry: onboardings=%#v list_err=%v audits=%d", onboardings, listErr, len(mustPairingListAudit(t, repository.memory, "")))
	}
}

func TestPairingPendingReconcilesOnNextMatchingRequestWithoutSecondAuthorityCall(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	repository.saveAfterPersist = &store.StoreError{
		Code: store.StoreErrorUnknownOutcome, Operation: store.OperationISCPOnboardingSave, Err: errors.New("commit response lost"),
	}
	repository.getErrors = []error{&store.StoreError{
		Code: store.StoreErrorUnavailable, Operation: store.OperationISCPOnboardingGet, Err: errors.New("barrier unavailable"),
	}}
	authority := pendingTestAuthority(now)
	service := newPendingTestService(now, repository, authority)
	request := StartRequest{DisplayName: "gateway"}
	if _, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", request, now); FailureCodeOf(err) != FailureUnavailable || service.pending == nil {
		t.Fatalf("initial unresolved result err=%v pending=%v", err, service.pending)
	}
	if _, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", StartRequest{DisplayName: "different"}, now); FailureCodeOf(err) != FailureConflict {
		t.Fatalf("different pending request error = %v code=%q", err, FailureCodeOf(err))
	}
	issued, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", request, now)
	if err != nil || issued.Ticket.Signature.Value == "" || authority.calls != 1 || service.pending != nil {
		t.Fatalf("next reconciliation issued=%#v err=%v calls=%d pending=%v", issued, err, authority.calls, service.pending)
	}
}

func TestPairingPendingConfirmedAbsenceClearsWithoutRetryingAuthority(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	repository.saveWithoutPersist = &store.StoreError{
		Code: store.StoreErrorUnknownOutcome, Operation: store.OperationISCPOnboardingSave, Err: errors.New("submission uncertain"),
	}
	authority := pendingTestAuthority(now)
	service := newPendingTestService(now, repository, authority)
	if _, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", StartRequest{DisplayName: "gateway"}, now); !errors.Is(err, ErrPersistence) || authority.calls != 1 || service.pending != nil {
		t.Fatalf("confirmed absence err=%v calls=%d pending=%v", err, authority.calls, service.pending)
	}
}

func TestPairingPendingExpiryDiscardsSignatureAfterReconciliation(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	repository.saveAfterPersist = &store.StoreError{
		Code: store.StoreErrorUnknownOutcome, Operation: store.OperationISCPOnboardingSave, Err: errors.New("commit response lost"),
	}
	repository.getErrors = []error{
		&store.StoreError{Code: store.StoreErrorUnavailable, Operation: store.OperationISCPOnboardingGet, Err: errors.New("barrier unavailable")},
		&store.StoreError{Code: store.StoreErrorUnavailable, Operation: store.OperationISCPOnboardingGet, Err: errors.New("barrier still unavailable")},
	}
	authority := pendingTestAuthority(now)
	service := newPendingTestService(now, repository, authority)
	request := StartRequest{DisplayName: "gateway"}
	if _, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", request, now); err == nil {
		t.Fatal("initial unknown outcome unexpectedly reconciled")
	}
	expiredAt := now.Add(11 * time.Minute)
	service.now = func() time.Time { return expiredAt }
	if _, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", request, expiredAt); err == nil || service.pending == nil || service.pending.ticket.Signature.Value != "" {
		t.Fatalf("expired unresolved pending retained signature: err=%v pending=%#v", err, service.pending)
	}
	issued, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", request, expiredAt)
	if issued.Ticket.Signature.Value != "" || !errors.Is(err, ErrTicketExpired) || authority.calls != 1 || service.pending != nil {
		t.Fatalf("expired reconciliation issued=%#v err=%v calls=%d pending=%v", issued, err, authority.calls, service.pending)
	}
}

func TestPairingStartAdmissionHonorsWaitingContext(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	authority := pendingTestAuthority(now)
	authority.entered = make(chan struct{})
	authority.release = make(chan struct{})
	service := newPendingTestService(now, repository, authority)
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", StartRequest{DisplayName: "first"}, now)
		firstDone <- err
	}()
	<-authority.entered

	waiting, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Start(waiting, app.DefaultOwnerID, "actor-a", StartRequest{DisplayName: "second"}, now); FailureCodeOf(err) != FailureUnavailable {
		t.Fatalf("canceled admission error = %v code=%q", err, FailureCodeOf(err))
	}
	close(authority.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if authority.calls != 1 {
		t.Fatalf("canceled waiter reached authority: calls=%d", authority.calls)
	}
}

func TestPairingConcurrentStartsSerializeAuthorityCalls(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	authority := pendingTestAuthority(now)
	authority.entered = make(chan struct{})
	authority.release = make(chan struct{})
	service := newPendingTestService(now, repository, authority)
	results := make(chan error, 2)
	go func() {
		_, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", StartRequest{DisplayName: "first"}, now)
		results <- err
	}()
	<-authority.entered
	go func() {
		_, err := service.Start(context.Background(), app.DefaultOwnerID, "actor-a", StartRequest{DisplayName: "second"}, now)
		results <- err
	}()
	time.Sleep(20 * time.Millisecond)
	authority.mu.Lock()
	maxBeforeRelease := authority.maxActive
	authority.mu.Unlock()
	if maxBeforeRelease != 1 {
		t.Fatalf("parallel authority calls before release = %d", maxBeforeRelease)
	}
	close(authority.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if authority.calls != 2 || authority.maxActive != 1 {
		t.Fatalf("authority calls=%d max_active=%d", authority.calls, authority.maxActive)
	}
}

func TestPairingListPropagatesContextAndProjectsSafeFailure(t *testing.T) {
	now := time.Now().UTC()
	repository := newControlledPairingRepository()
	repository.listErr = &store.StoreError{
		Code: store.StoreErrorTimeout, Operation: store.OperationISCPOnboardingList, Err: errors.New("dsn secret timeout"),
	}
	service := newPendingTestService(now, repository, pendingTestAuthority(now))
	ctx := context.WithValue(context.Background(), struct{}{}, "request-context")
	listed, err := service.List(ctx, app.DefaultOwnerID)
	if listed != nil || FailureCodeOf(err) != FailureTimeout || strings.Contains(err.Error(), "dsn secret") || repository.listContext != ctx {
		t.Fatalf("list=%#v err=%v code=%q context_propagated=%v", listed, err, FailureCodeOf(err), repository.listContext == ctx)
	}
}
