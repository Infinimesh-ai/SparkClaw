package emailmanagement

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type epochFailureBrowser struct{ *lifecycleComposeBrowser }

func (*epochFailureBrowser) OperationEpoch(context.Context, string) (browsercontrol.EpochState, error) {
	return browsercontrol.EpochState{}, &browsercontrol.Error{Code: browsercontrol.CodeControllerUnavailable}
}
func TestComposeDiagnosticIdentityAndUnknownCauseDoNotAlterSend(t *testing.T) {
	for _, mode := range []string{"epoch", "legacy_unsupported", "legacy_epoch_failed"} {
		t.Run(mode, func(t *testing.T) {
			output := captureJobDiagnostics(t)
			_, s, b, box := lifecycleFixture(t)
			b.resultError = &emailautomation.Error{Code: app.ToolErrorEmailScriptTimeout, Message: "secret@example.test body credential https://private"}
			if mode == "legacy_unsupported" {
				s.browser = b.composeFixture
			}
			if mode == "legacy_epoch_failed" {
				s.browser = &epochFailureBrowser{b}
			}
			d, err := s.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "private-draft-subject", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "private-subject", Body: "private-body"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			out, err := s.SendDraft(t.Context(), "owner", d.ID, d.Version, "private-send-key")
			if err != nil || out.State != "unknown" || out.ErrorCode != "email_send_outcome_unknown" {
				t.Fatalf("result=%+v err=%v", out, err)
			}
			if _, err := s.SendDraft(t.Context(), "owner", d.ID, out.Version, "private-send-key"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SendDraft(t.Context(), "owner", d.ID, out.Version, "different"); err == nil {
				t.Fatal("unknown resent")
			}
			if b.sends != 1 {
				t.Fatalf("dispatches=%d", b.sends)
			}
			rows := map[string]map[string]any{}
			for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
				var row map[string]any
				if json.Unmarshal([]byte(line), &row) == nil {
					if stage, ok := row["stage"].(string); ok {
						rows[stage] = row
					}
				}
			}
			identity := rows["identity"]
			if identity["identity_mode"] != mode || identity["operation_id"] != out.Snapshot.InvocationID {
				t.Fatalf("identity missing/misbound: %s", output.String())
			}
			if mode == "legacy_epoch_failed" && identity["cause_code"] != browsercontrol.CodeControllerUnavailable {
				t.Fatal("lost epoch failure", identity)
			}
			failed := rows["send_result"]
			if failed["cause_code"] != string(app.ToolErrorEmailScriptTimeout) || failed["business_outcome"] != "unknown" || failed["operation_id"] != out.Snapshot.InvocationID {
				t.Fatalf("send cause missing: %s", output.String())
			}
			for _, secret := range []string{"secret@", "private-", "private/", "credential", "https://", "sink@"} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("leaked %q", secret)
				}
			}
		})
	}
}

func TestPendingACKFailureIsObservableWithoutChangingBusinessResult(t *testing.T) {
	output := captureJobDiagnostics(t)
	_, s, b, box := lifecycleFixture(t)
	b.ackError = &browsercontrol.Error{Code: browsercontrol.CodeControllerUnavailable}
	d, err := s.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.SendDraft(t.Context(), "owner", d.ID, d.Version, "review")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.lifecycleRound(t.Context()); err != nil {
		t.Fatal(err)
	}
	if out.State != "sent" || out.Receipt.Lifecycle.Acknowledged || b.sends != 1 {
		t.Fatal("ACK failure changed send", out)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) == nil && row["stage"] == "ack" {
			count++
			if row["operation_id"] != out.Snapshot.InvocationID || row["cause_code"] != browsercontrol.CodeControllerUnavailable || row["business_persisted"] != true {
				t.Fatal("wrong ACK cause", row)
			}
		}
	}
	if count != 2 {
		t.Fatalf("initial and maintenance ACK failures not both visible: %s", output.String())
	}
}

func TestFailedEpochAllocationNeverBorrowsPreviousOperation(t *testing.T) {
	output := captureJobDiagnostics(t)
	_, s, b, box := lifecycleFixture(t)
	b.resultError = &emailautomation.Error{Code: app.ToolErrorEmailInvalidInput}
	d, err := s.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.SendDraft(t.Context(), "owner", d.ID, d.Version, "first")
	if err != nil || failed.State != "failed" {
		t.Fatal(failed, err)
	}
	b.state = browsercontrol.EpochState{}
	if _, err := s.SendDraft(t.Context(), "owner", d.ID, failed.Version, "second"); err == nil {
		t.Fatal("invalid epoch accepted")
	}
	if b.sends != 1 {
		t.Fatal("invalid epoch dispatched")
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) == nil && row["identity_mode"] == "epoch_allocation_failed" {
			found = true
			if row["operation_id"] != "" {
				t.Fatal("new failure borrowed old operation", row)
			}
		}
	}
	if !found {
		t.Fatal("allocation failure missing")
	}
}

type failingFinishRepository struct{ Repository }

func (r failingFinishRepository) ChangeEmailDraft(ctx context.Context, c store.EmailDraftCommand) (store.EmailDraftResult, error) {
	if c.Action == "finish" {
		return store.EmailDraftResult{}, &store.StoreError{Code: store.StoreErrorDurability}
	}
	return r.Repository.ChangeEmailDraft(ctx, c)
}
func TestSendObservationCannotClaimFailedPersistenceAsBusinessCompletion(t *testing.T) {
	output := captureJobDiagnostics(t)
	repo, s, b, box := lifecycleFixture(t)
	s.repository = failingFinishRepository{repo}
	d, err := s.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.SendDraft(t.Context(), "owner", d.ID, d.Version, "review")
	if err == nil || out.State != "sending" {
		t.Fatal(out, err)
	}
	if _, err := s.SendDraft(t.Context(), "owner", d.ID, out.Version, "review"); err != nil {
		t.Fatal(err)
	}
	if b.sends != 1 || len(b.acks) != 0 {
		t.Fatal("persistence failure ACKed or resent")
	}
	seen := false
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		if row["business_persisted"] == true {
			t.Fatal("uncommitted result claimed persisted", row)
		}
		if row["stage"] == "persist_result" {
			seen = true
			if row["business_outcome"] != "uncommitted" || row["cause_code"] != "store_"+string(store.StoreErrorDurability) || row["cause_source"] != "store" {
				t.Fatal(row)
			}
		}
	}
	if !seen {
		t.Fatal("missing persistence boundary")
	}
}

type failingBeginRepository struct{ Repository }

func (r failingBeginRepository) ChangeEmailDraft(ctx context.Context, c store.EmailDraftCommand) (store.EmailDraftResult, error) {
	if c.Action == "begin" {
		return store.EmailDraftResult{}, &store.StoreError{Code: store.StoreErrorUnavailable}
	}
	return r.Repository.ChangeEmailDraft(ctx, c)
}
func TestEpochFailureRemainsVisibleWhenDurableBeginFails(t *testing.T) {
	output := captureJobDiagnostics(t)
	repo, s, b, box := lifecycleFixture(t)
	s.browser = &epochFailureBrowser{b}
	s.repository = failingBeginRepository{repo}
	d, err := s.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendDraft(t.Context(), "owner", d.ID, d.Version, "review"); store.StoreErrorCodeOf(err) != store.StoreErrorUnavailable {
		t.Fatal(err)
	}
	if b.sends != 0 {
		t.Fatal("failed begin dispatched")
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) == nil && row["stage"] == "identity" {
			found = true
			if row["identity_mode"] != "legacy_epoch_failed" || row["cause_code"] != browsercontrol.CodeControllerUnavailable || row["operation_id"] != "" {
				t.Fatal(row)
			}
		}
	}
	if !found {
		t.Fatalf("epoch failure disappeared behind begin failure: %s", output.String())
	}
}
