package emailmanagement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// lifecycleComposeBrowser models the App-CLI lifecycle 3.0 product control.
type lifecycleComposeBrowser struct {
	*composeFixture
	state      browsercontrol.EpochState
	invocation string
	acks       []string
	ackError   error
	resolved   []string
	lookups    map[string]string
	sealed     []int64
	reconcile  error
}

func (b *lifecycleComposeBrowser) SendForOwner(ctx context.Context, owner string, request app.EmailSendRequest) (app.EmailSendResult, error) {
	b.invocation = request.InvocationID
	result, err := b.composeFixture.SendForOwner(ctx, owner, request)
	if err == nil {
		result.Lifecycle = &app.EmailRuntimeLifecycle{OperationID: request.InvocationID, TaskID: "task-1", TerminalRevision: 1, ResultDigest: strings.Repeat("d", 64)}
	}
	return result, err
}
func (b *lifecycleComposeBrowser) ReconcileSendForOwner(context.Context, string, app.EmailSendRequest) (app.EmailSendResult, error) {
	if b.reconcile != nil {
		return app.EmailSendResult{}, b.reconcile
	}
	return app.EmailSendResult{}, errors.New("not used")
}
func (b *lifecycleComposeBrowser) OperationEpoch(context.Context, string) (browsercontrol.EpochState, error) {
	return b.state, nil
}
func (b *lifecycleComposeBrowser) SealOperationEpoch(_ context.Context, _ string, epoch int64, digest string) (browsercontrol.EpochState, error) {
	if len(digest) != 64 {
		return b.state, errors.New("bad digest")
	}
	b.sealed = append(b.sealed, epoch)
	b.state.SealedThrough, b.state.DrainingEpoch = epoch, nil
	return b.state, nil
}
func (b *lifecycleComposeBrowser) LookupSend(_ context.Context, _ string, id string) (string, string, error) {
	if outcome := b.lookups[id]; outcome != "" {
		return outcome, "", nil
	}
	return "known", "", nil
}
func (b *lifecycleComposeBrowser) AcknowledgeSend(_ context.Context, _ string, lifecycle app.EmailRuntimeLifecycle, receipt string) error {
	if b.ackError != nil {
		return b.ackError
	}
	b.acks = append(b.acks, lifecycle.OperationID+"|"+receipt)
	return nil
}
func (b *lifecycleComposeBrowser) ResolveSend(_ context.Context, _ string, id, resolution string) (string, error) {
	b.resolved = append(b.resolved, id+"|"+resolution)
	return "resolved", nil
}

func lifecycleFixture(t *testing.T) (*store.MemoryStore, *Service, *lifecycleComposeBrowser, app.EmailMailbox) {
	t.Helper()
	repo := store.NewMemoryStore()
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: "gmail", Address: "owner@example.test", Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	browser := &lifecycleComposeBrowser{composeFixture: &composeFixture{intakeFixture: &intakeFixture{}, address: "owner@example.test"},
		state: browsercontrol.EpochState{Generation: 1, OpenEpoch: 4, SealedThrough: 2, AuthorizationGeneration: 1}, lookups: map[string]string{}}
	return repo, &Service{repository: repo, browser: browser}, browser, box
}

func TestComposeIssuesOperationIdentityInOpenEpochAndAcknowledgesReceipt(t *testing.T) {
	_, service, browser, box := lifecycleFixture(t)
	draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	browser.ackError = errors.New("controller restarting")
	sent, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "review")
	if err != nil || sent.State != "sent" {
		t.Fatalf("%+v %v", sent, err)
	}
	generation, epoch, ok := app.OperationEpoch(sent.Snapshot.InvocationID)
	if !ok || generation != 1 || epoch != 4 || browser.invocation != sent.Snapshot.InvocationID {
		t.Fatalf("identity=%q", sent.Snapshot.InvocationID)
	}
	if sent.Receipt.Lifecycle == nil || sent.Receipt.Lifecycle.Acknowledged {
		t.Fatalf("an ACK failure must leave the receipt pending: %+v", sent.Receipt)
	}
	// The maintenance round retries the durable-receipt ACK exactly once.
	browser.ackError = nil
	if err := service.lifecycleRound(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.lifecycleRound(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, _ := service.repository.ListEmailDrafts(t.Context(), "owner", draft.ID)
	if len(browser.acks) != 1 || !rows[0].Receipt.Lifecycle.Acknowledged || rows[0].Snapshot.Receipt.Lifecycle == nil {
		t.Fatalf("acks=%v draft=%+v", browser.acks, rows[0].Receipt)
	}
}

func TestComposeSealsOnlyAfterEveryDrainingIdentityWasAdmitted(t *testing.T) {
	_, service, browser, box := lifecycleFixture(t)
	draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	browser.resultError = errors.New("transport lost")
	unknown, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "review")
	if err != nil || unknown.State != "unknown" {
		t.Fatalf("%+v %v", unknown, err)
	}
	draining := browser.state.OpenEpoch
	browser.state.DrainingEpoch, browser.state.OpenEpoch = &draining, draining+1
	browser.lookups[unknown.Snapshot.InvocationID] = "not_admitted"
	if err := service.lifecycleRound(t.Context()); err != nil || len(browser.sealed) != 0 {
		t.Fatalf("an unsubmitted identity must hold the drain: %v %v", browser.sealed, err)
	}
	browser.lookups[unknown.Snapshot.InvocationID] = "known"
	if err := service.lifecycleRound(t.Context()); err != nil || len(browser.sealed) != 1 || browser.sealed[0] != draining {
		t.Fatalf("sealed=%v err=%v", browser.sealed, err)
	}
}

func TestComposeUserResolutionClosesUnknownSendWithoutResending(t *testing.T) {
	for resolution, want := range map[string]string{"confirmed_sent": "sent", "confirmed_not_sent": "failed", "accept_unknown": "failed"} {
		t.Run(resolution, func(t *testing.T) {
			_, service, browser, box := lifecycleFixture(t)
			draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			browser.resultError = errors.New("transport lost")
			unknown, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "review")
			if err != nil || unknown.State != "unknown" {
				t.Fatalf("%+v %v", unknown, err)
			}
			if _, err := service.ResolveDraft(t.Context(), "owner", draft.ID, unknown.Version, "invented"); !errors.Is(err, ErrInvalidInput) {
				t.Fatal(err)
			}
			if _, err := service.ResolveDraft(t.Context(), "owner", draft.ID, unknown.Version-1, resolution); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			resolved, err := service.ResolveDraft(t.Context(), "owner", draft.ID, unknown.Version, resolution)
			if err != nil || resolved.State != want || resolved.ConfirmationSource != "user_confirmed" || resolved.Snapshot.UserResolution != resolution {
				t.Fatalf("%+v %v", resolved, err)
			}
			if len(browser.resolved) != 1 || browser.resolved[0] != unknown.Snapshot.InvocationID+"|"+resolution || browser.sends != 1 {
				t.Fatalf("resolved=%v sends=%d", browser.resolved, browser.sends)
			}
			if _, err := service.ResolveDraft(t.Context(), "owner", draft.ID, resolved.Version, resolution); !errors.Is(err, ErrConflict) {
				t.Fatal("a resolved send was resolved twice")
			}
			if want == "failed" {
				// A later send is an explicit new review with a new identity.
				browser.resultError = nil
				again, err := service.SendDraft(t.Context(), "owner", draft.ID, resolved.Version, "second-review")
				if err != nil || again.State != "sent" || again.Snapshot.InvocationID == unknown.Snapshot.InvocationID || browser.sends != 2 {
					t.Fatalf("%+v %v", again, err)
				}
			}
		})
	}
}

func TestComposeNeverDispatchedSendIsDefinitelyFailedAndReviewable(t *testing.T) {
	_, service, browser, box := lifecycleFixture(t)
	draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	browser.resultError = &emailautomation.Error{Code: app.ToolErrorEmailSendNotDispatched, Message: "not dispatched"}
	failed, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "review")
	if err != nil || failed.State != "failed" || failed.ErrorCode != string(app.ToolErrorEmailSendNotDispatched) {
		t.Fatalf("%+v %v", failed, err)
	}
	browser.resultError = nil
	again, err := service.SendDraft(t.Context(), "owner", draft.ID, failed.Version, "second-review")
	if err != nil || again.State != "sent" || again.Snapshot.InvocationID == failed.Snapshot.InvocationID {
		t.Fatalf("%+v %v", again, err)
	}
}

func TestComposeReconcileClosesAnUnknownSendThatWasNeverDispatched(t *testing.T) {
	_, service, browser, box := lifecycleFixture(t)
	draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	browser.resultError = errors.New("transport lost")
	unknown, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "review")
	if err != nil || unknown.State != "unknown" {
		t.Fatalf("%+v %v", unknown, err)
	}
	browser.reconcile = &emailautomation.Error{Code: app.ToolErrorEmailSendNotDispatched, Message: "not dispatched"}
	closed, err := service.ReconcileDraft(t.Context(), "owner", draft.ID)
	if err != nil || closed.State != "failed" || closed.ErrorCode != string(app.ToolErrorEmailSendNotDispatched) || closed.ConfirmationSource != "runtime_not_dispatched" {
		t.Fatalf("%+v %v", closed, err)
	}
	if browser.sends != 1 {
		t.Fatalf("reconciliation resent: %d", browser.sends)
	}
}

func TestComposeSealsADrainedEpochWithoutAnyDraft(t *testing.T) {
	_, service, browser, _ := lifecycleFixture(t)
	draining := browser.state.OpenEpoch
	browser.state.DrainingEpoch, browser.state.OpenEpoch = &draining, draining+1
	if err := service.lifecycleRound(t.Context()); err != nil || len(browser.sealed) != 1 || browser.sealed[0] != draining {
		t.Fatalf("sealed=%v err=%v", browser.sealed, err)
	}
}
