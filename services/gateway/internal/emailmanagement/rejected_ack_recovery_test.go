package emailmanagement

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestRejectedReceiptACKRecoversAfterFileStoreRestartWithoutRedispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	repo, err := store.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: app.EmailProviderQQMail, Address: "owner@example.test", Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	browser := &rejectingComposeBrowser{lifecycleComposeBrowser: &lifecycleComposeBrowser{composeFixture: &composeFixture{intakeFixture: &intakeFixture{}}, state: browsercontrol.EpochState{Generation: 1, OpenEpoch: 4}, ackError: errors.New("first ACK unavailable")}, code: -21101, reason: "PROVIDER_PARAMETER_REJECTED"}
	service := &Service{repository: repo, browser: browser}
	draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "review")
	if err != nil || failed.State != "failed" || failed.ErrorCode != app.EmailSendRejectedParameter || failed.Receipt == nil || failed.Receipt.Lifecycle.Acknowledged || browser.sends != 1 {
		t.Fatalf("initial rejection: draft=%+v err=%v sends=%d", failed, err, browser.sends)
	}
	// Discard the service and reopen the persisted store, as on process restart.
	reopened, err := store.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service = &Service{repository: reopened, browser: browser}
	browser.ackError = nil
	for range 2 {
		if err := service.lifecycleRound(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := reopened.ListEmailDrafts(t.Context(), "owner", draft.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	got := rows[0]
	if !got.Receipt.Lifecycle.Acknowledged || !got.Snapshot.Receipt.Lifecycle.Acknowledged || len(browser.acks) != 1 {
		t.Fatalf("rejected receipt not ACKed after restart: acks=%v receipt=%+v", browser.acks, got.Receipt.Lifecycle)
	}
	if got.State != failed.State || got.ErrorCode != failed.ErrorCode || got.Snapshot.InvocationID != failed.Snapshot.InvocationID || got.SentMailID != "" || got.TimelineMailID != "" {
		t.Fatalf("business result changed: %+v", got)
	}
	if _, err := service.SendDraft(t.Context(), "owner", got.ID, got.Version, "review"); err != nil {
		t.Fatal(err)
	}
	if browser.sends != 1 {
		t.Fatalf("recovery redispatched: %d", browser.sends)
	}
}

func TestTerminalACKEligibilityPreservesUnknownAndProofFencing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*store.EmailDraft)
		want   bool
	}{
		{"rejected", func(*store.EmailDraft) {}, true},
		{"sent", func(d *store.EmailDraft) { d.State = "sent"; d.Receipt.NotSent = nil }, true},
		{"unknown", func(d *store.EmailDraft) { d.State = "unknown" }, false},
		{"sending", func(d *store.EmailDraft) { d.State = "sending" }, false},
		{"acked", func(d *store.EmailDraft) { d.Receipt.Lifecycle.Acknowledged = true }, false},
		{"missing-proof", func(d *store.EmailDraft) { d.Receipt.NotSent = nil }, false},
		{"other-invocation", func(d *store.EmailDraft) { d.Receipt.NotSent.InvocationID = "other" }, false},
		{"other-provider", func(d *store.EmailDraft) { d.Receipt.Provider = app.EmailProviderGmail }, false},
		{"other-task", func(d *store.EmailDraft) { d.Receipt.Lifecycle.TaskID = "other" }, false},
		{"invalid-code", func(d *store.EmailDraft) { *d.Receipt.NotSent.ProviderCode = 0 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := -21101
			d := store.EmailDraft{State: "failed", Snapshot: &store.EmailDraftSnapshot{InvocationID: "invocation"}, Receipt: &app.EmailSendResult{Status: "not_sent", Provider: app.EmailProviderQQMail,
				Lifecycle: &app.EmailRuntimeLifecycle{OperationID: "invocation", TaskID: "task"},
				NotSent:   &app.EmailNotSentProof{SchemaVersion: 1, Kind: "provider_rejected", InvocationID: "invocation", TaskID: "task", LedgerEpoch: 4, IntentDigest: strings.Repeat("a", 64), ResourceDigest: strings.Repeat("b", 64), BindingDigest: strings.Repeat("c", 64), Reason: "PROVIDER_PARAMETER_REJECTED", ProviderCode: &code}}}
			tc.mutate(&d)
			if got := pendingTerminalACK(d, app.EmailProviderQQMail); got != tc.want {
				t.Fatalf("eligible=%v want=%v", got, tc.want)
			}
		})
	}
}
