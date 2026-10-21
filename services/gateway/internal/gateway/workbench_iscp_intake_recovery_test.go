package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func intakeRecoveryAdapter(t *testing.T, backend string) (*iscpDomainAdapter, *emailHTTPFixture) {
	t.Helper()
	server, f, cfg := newMailPopupFixture(t, backend)
	journal, err := server.domainReceiptJournal(server.executionRoot)
	f.must(err)
	return &iscpDomainAdapter{server: server, config: cfg, receipts: journal}, f
}
func intakeOriginal(version int64, enabled bool) iscpworkbench.Request {
	r := domainTestRequest(iscpworkbench.OperationMailIntakeUpdate, []byte(fmt.Sprintf(`{"intake_enabled":%t,"expected_mailbox_version":%d}`, enabled, version)))
	r.Params = map[string]string{"provider": app.EmailProviderGmail}
	return r
}
func intakeRecoveryRequest(original iscpworkbench.Request) iscpworkbench.Request {
	body, _ := json.Marshal(map[string]any{"operation_id": original.OperationID, "original": map[string]any{"operation": original.Operation, "params": original.Params, "body": original.Body, "expected_revision": original.ExpectedRevision, "request_id": original.RequestID, "input_digest": original.InputDigest}})
	r := domainTestRequest(iscpworkbench.OperationMailIntakeReconcile, body)
	r.Params = original.Params
	return r
}
func seedIntakeIntent(t *testing.T, a *iscpDomainAdapter, original iscpworkbench.Request) {
	t.Helper()
	ctx := domainTestContext(t)
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	scope := a.receiptScope(principal, original.InstallationID)
	if err := a.receipts.save(scope, original.OperationID, iscpDomainReceipt{Version: 1, Digest: domainRequestDigest(original), Operation: original.Operation}); err != nil {
		t.Fatal(err)
	}
}
func intakeReceipt(t *testing.T, a *iscpDomainAdapter, original iscpworkbench.Request) iscpDomainReceipt {
	t.Helper()
	principal := requestPrincipal{OwnerID: "iscp-owner", ClientID: "iscp-desktop-client"}
	result, found, err := a.receipts.load(a.receiptScope(principal, original.InstallationID), original.OperationID)
	if err != nil || !found {
		t.Fatal(result, found, err)
	}
	return result
}
func TestISCPIntakeLegacyRecoveryFencesAndPreservesOriginal(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			a, f := intakeRecoveryAdapter(t, backend)
			original := intakeOriginal(f.box.Version, false)
			seedIntakeIntent(t, a, original)
			// A plain read cannot close or alter the unknown operation.
			before := intakeReceipt(t, a, original)
			status := popupHTTP(t, a.server, "GET", "/api/email/sync-status", "")
			if status.Code != 200 || !reflect.DeepEqual(before, intakeReceipt(t, a, original)) {
				t.Fatal("GET changed receipt")
			}
			request := intakeRecoveryRequest(original)
			result := a.handle(domainTestContext(t), request)
			if result.Status != 200 || !bytes.Contains(result.Body, []byte("email_intake_not_applied")) {
				t.Fatalf("reconcile %+v", result)
			}
			terminal := intakeReceipt(t, a, original)
			if !terminal.Complete || terminal.Status != 409 {
				t.Fatal(terminal)
			}
			box, _, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
			f.must(err)
			if !box.IntakeEnabled || box.Version != f.box.Version+1 || box.BindingGeneration != f.box.BindingGeneration {
				t.Fatal("negative fence changed intake state", box)
			}
			if replay := a.handle(domainTestContext(t), original); replay.Status != 409 || !bytes.Equal(replay.Body, terminal.Body) {
				t.Fatal("old original was executed", replay)
			}
			// Lost reconciliation response and a new adapter still return the same result.
			restarted := &iscpDomainAdapter{server: a.server, config: a.config, receipts: a.receipts}
			again := restarted.handle(domainTestContext(t), request)
			if !bytes.Equal(result.Body, again.Body) {
				t.Fatal("recovery is not idempotent")
			}
			if f.browser.calls != 0 {
				t.Fatal("recovery dispatched browser")
			}
		})
	}
}
func TestISCPIntakeRecoverCommitAfterCancellation(t *testing.T) {
	a, f := intakeRecoveryAdapter(t, "file")
	original := intakeOriginal(f.box.Version, false)
	ctx, cancel := context.WithCancel(domainTestContext(t))
	defer cancel()
	wrapped := &intakeCommitCancelRepo{Repository: f.repo, cancel: cancel}
	service, err := emailmanagement.New(wrapped, f.browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: f.root})
	f.must(err)
	a.server.emailManagement = service
	result := a.handle(ctx, original)
	if result.Status != 409 || !bytes.Contains(result.Body, []byte("operation_outcome_unknown")) {
		t.Fatal(result)
	}
	if receipt := intakeReceipt(t, a, original); receipt.Complete {
		t.Fatal("canceled original finalized")
	}
	recovered := a.handle(domainTestContext(t), intakeRecoveryRequest(original))
	if recovered.Status != 200 || !bytes.Contains(recovered.Body, []byte(`"status":200`)) {
		t.Fatal(recovered)
	}
	receipt := intakeReceipt(t, a, original)
	if !receipt.Complete || receipt.Status != 200 {
		t.Fatal(receipt)
	}
	boxes, err := f.repo.ListEmailMailboxes(t.Context(), f.owner)
	f.must(err)
	if len(boxes) != 1 || boxes[0].IntakeEnabled || boxes[0].Version != 2 {
		t.Fatal(boxes)
	}
}

type intakeCommitCancelRepo struct {
	emailmanagement.Repository
	cancel context.CancelFunc
}

func (r *intakeCommitCancelRepo) BindEmailMailbox(ctx context.Context, c store.EmailBindCommand) (app.EmailMailbox, error) {
	box, err := r.Repository.BindEmailMailbox(ctx, c)
	if err == nil {
		r.cancel()
	}
	return box, err
}

type intakeResolveCancelRepo struct {
	emailmanagement.Repository
	cancel context.CancelFunc
}

func (r *intakeResolveCancelRepo) ResolveEmailIntake(ctx context.Context, owner string, o store.EmailIntakeOperation) (store.EmailIntakeResolution, error) {
	v, err := r.Repository.ResolveEmailIntake(ctx, owner, o)
	if err == nil {
		r.cancel()
	}
	return v, err
}
func TestISCPIntakeReconciliationCancellationRecoversOriginalKey(t *testing.T) {
	a, f := intakeRecoveryAdapter(t, "file")
	original := intakeOriginal(1, false)
	seedIntakeIntent(t, a, original)
	ctx, cancel := context.WithCancel(domainTestContext(t))
	defer cancel()
	wrapped := &intakeResolveCancelRepo{Repository: f.repo, cancel: cancel}
	service, err := emailmanagement.New(wrapped, f.browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: f.root})
	f.must(err)
	a.server.emailManagement = service
	request := intakeRecoveryRequest(original)
	if result := a.handle(ctx, request); result.Status != 409 {
		t.Fatal(result)
	}
	if intakeReceipt(t, a, original).Complete {
		t.Fatal("canceled recovery published result")
	}
	if result := a.handle(domainTestContext(t), request); result.Status != 200 || !bytes.Contains(result.Body, []byte("email_intake_not_applied")) {
		t.Fatal(result)
	}
	versions, err := f.repo.GetEmailProviderVersions(t.Context(), f.owner)
	f.must(err)
	if versions[app.EmailProviderGmail] != 2 {
		t.Fatal("recovery advanced twice", versions)
	}
}
func TestISCPIntakeRecoveryRejectsConflictsAndForeignScope(t *testing.T) {
	a, f := intakeRecoveryAdapter(t, "memory")
	original := intakeOriginal(1, false)
	seedIntakeIntent(t, a, original)
	for _, kind := range []string{"body", "digest", "operation", "missing", "installation", "owner", "client", "deployment"} {
		t.Run(kind, func(t *testing.T) {
			altered := original
			request := intakeRecoveryRequest(altered)
			ctx := domainTestContext(t)
			candidate := &iscpDomainAdapter{server: a.server, config: a.config, receipts: a.receipts}
			switch kind {
			case "body":
				altered.Body = []byte(`{"intake_enabled":true,"expected_mailbox_version":1}`)
				request = intakeRecoveryRequest(altered)
			case "digest":
				altered.InputDigest = strings.Repeat("e", 64)
				request = intakeRecoveryRequest(altered)
			case "operation":
				altered.Operation = iscpworkbench.OperationMailDraftsSend
				request = intakeRecoveryRequest(altered)
			case "missing":
				altered.OperationID = domainTestRequest("id", nil).ID
				request = intakeRecoveryRequest(altered)
			case "installation":
				request.InstallationID = domainTestRequest("id", nil).ID
			case "owner":
				ctx = context.WithValue(ctx, requestPrincipalContextKey{}, requestPrincipal{Authenticated: true, OwnerID: "other", ClientID: "iscp-desktop-client"})
			case "client":
				ctx = context.WithValue(ctx, requestPrincipalContextKey{}, requestPrincipal{Authenticated: true, OwnerID: f.owner, ClientID: "other"})
			case "deployment":
				binding := *a.config.Binding
				binding.DeploymentID = "other"
				candidate.config.Binding = &binding
			}
			got := candidate.handle(ctx, request)
			if got.Status < 400 {
				t.Fatal("foreign/conflicting recovery accepted", got)
			}
			if intakeReceipt(t, a, original).Complete {
				t.Fatal("rejected recovery closed original")
			}
			versions, err := f.repo.GetEmailProviderVersions(t.Context(), f.owner)
			f.must(err)
			if versions[app.EmailProviderGmail] != 1 {
				t.Fatal(versions)
			}
		})
	}
	// A legacy operation with a changed provider revision has no positive proof.
	_, err := f.repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: f.owner, CommandKey: "other-update"}, Provider: app.EmailProviderGmail, Address: f.box.Address, Enabled: true, ExpectedVersion: 1})
	f.must(err)
	result := a.handle(domainTestContext(t), intakeRecoveryRequest(original))
	if result.Status != 409 || intakeReceipt(t, a, original).Complete {
		t.Fatal("ambiguous legacy operation resolved", result)
	}
}

type intakeCommitGateRepo struct {
	emailmanagement.Repository
	started, release chan struct{}
}

func (r *intakeCommitGateRepo) BindEmailMailbox(ctx context.Context, c store.EmailBindCommand) (app.EmailMailbox, error) {
	close(r.started)
	<-r.release
	return r.Repository.BindEmailMailbox(ctx, c)
}
func TestISCPIntakeLateOriginalCannotOverwriteNegativeReceipt(t *testing.T) {
	a, f := intakeRecoveryAdapter(t, "file")
	gate := &intakeCommitGateRepo{Repository: f.repo, started: make(chan struct{}), release: make(chan struct{})}
	service, err := emailmanagement.New(gate, f.browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: f.root})
	f.must(err)
	a.server.emailManagement = service
	original := intakeOriginal(1, false)
	done := make(chan iscpworkbench.Response, 1)
	go func() { done <- a.handle(domainTestContext(t), original) }()
	<-gate.started
	recovered := a.handle(domainTestContext(t), intakeRecoveryRequest(original))
	if recovered.Status != 200 {
		close(gate.release)
		t.Fatal(recovered)
	}
	terminal := intakeReceipt(t, a, original)
	if !bytes.Contains(terminal.Body, []byte("email_intake_not_applied")) {
		close(gate.release)
		t.Fatal(terminal)
	}
	close(gate.release)
	late := <-done
	if !bytes.Equal(late.Body, terminal.Body) || !reflect.DeepEqual(terminal, intakeReceipt(t, a, original)) {
		t.Fatal("late writer overwrote terminal proof", late)
	}
	box, _, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
	f.must(err)
	if !box.IntakeEnabled || box.Version != 2 {
		t.Fatal("late disable escaped version fence", box)
	}
}
func TestISCPIntakeRecoveryNeverClosesUnknownSend(t *testing.T) {
	a, _ := intakeRecoveryAdapter(t, "memory")
	send := intakeOriginal(1, false)
	send.Operation = iscpworkbench.OperationMailDraftsSend
	seedIntakeIntent(t, a, send)
	forged := send
	forged.Operation = iscpworkbench.OperationMailIntakeUpdate
	result := a.handle(domainTestContext(t), intakeRecoveryRequest(forged))
	if result.Status != 409 || intakeReceipt(t, a, send).Complete {
		t.Fatal("send uncertainty was closed", result)
	}
}

type intakeCommitUncertainRepo struct{ emailmanagement.Repository }

func (r *intakeCommitUncertainRepo) BindEmailMailbox(ctx context.Context, c store.EmailBindCommand) (app.EmailMailbox, error) {
	box, err := r.Repository.BindEmailMailbox(ctx, c)
	if err == nil {
		err = &store.StoreError{Code: store.StoreErrorUnknownOutcome, Operation: store.OperationBindEmailMailbox}
	}
	return box, err
}
func TestISCPIntakeUncertainCommitRemainsRecoverable(t *testing.T) {
	a, f := intakeRecoveryAdapter(t, "file")
	wrapped := &intakeCommitUncertainRepo{Repository: f.repo}
	service, err := emailmanagement.New(wrapped, f.browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: f.root})
	f.must(err)
	a.server.emailManagement = service
	original := intakeOriginal(1, false)
	got := a.handle(domainTestContext(t), original)
	if got.Status != 409 || !bytes.Contains(got.Body, []byte("operation_outcome_unknown")) || intakeReceipt(t, a, original).Complete {
		t.Fatal("uncertain persistence was finalized", got)
	}
	got = a.handle(domainTestContext(t), intakeRecoveryRequest(original))
	if got.Status != 200 || !bytes.Contains(got.Body, []byte(`"status":200`)) || intakeReceipt(t, a, original).Status != 200 {
		t.Fatal("committed business proof was lost", got)
	}
}
func TestISCPIntakeRecoveryPreservesRetiredResponseFence(t *testing.T) {
	a, f := intakeRecoveryAdapter(t, "file")
	original := intakeOriginal(1, false)
	if got := a.handle(domainTestContext(t), original); got.Status != 200 {
		t.Fatal(got)
	}
	receipt := intakeReceipt(t, a, original)
	principal := requestPrincipal{OwnerID: f.owner, ClientID: "iscp-desktop-client"}
	scope := a.receiptScope(principal, original.InstallationID)
	path := a.receipts.path(scope, original.OperationID)
	f.must(a.receipts.retireReceipt(filepath.Base(path), receipt))
	for range 2 {
		got := a.handle(domainTestContext(t), intakeRecoveryRequest(original))
		if got.Status != 200 || !bytes.Contains(got.Body, []byte(`"status":200`)) {
			t.Fatal("durable proof unavailable after body retention", got)
		}
		if intakeReceipt(t, a, original).Complete {
			t.Fatal("retired fence replaced")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("expired response body recreated", err)
		}
	}
}
