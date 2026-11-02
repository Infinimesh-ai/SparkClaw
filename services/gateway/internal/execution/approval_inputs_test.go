package execution

import (
	"context"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestApprovalCompletionBoundInputsAndContentFreeReceipt(t *testing.T) {
	var s *Service
	var effects atomic.Int32
	finish := make(chan struct{})
	seen := make(chan app.ApprovalInputValues, 1)
	form := &app.ApprovalInputForm{SchemaVersion: 1, Entries: []app.ApprovalInputEntry{{ID: "e1", Label: "Private canary", Fields: []app.ApprovalInputField{{Name: "notes", Label: "Notes", Type: app.ApprovalInputText, Required: true}}}}}
	row, err := NewPendingApprovalWithForm("completion_1", "notify.ask_approval", "Complete fields", nil, form)
	if err != nil {
		t.Fatal(err)
	}
	s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		decision, values, err := s.AwaitApprovalWithInputs(ctx, e, row)
		if err != nil {
			return Output{}, err
		}
		if decision == "approve" {
			effects.Add(1)
			seen <- values
		}
		select {
		case <-finish:
		case <-ctx.Done():
			return Output{}, ctx.Err()
		}
		return Output{Content: "complete"}, nil
	})
	if _, err = s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	pending := waitPending(t, s, e)
	pending.InputForm.Entries[0].Fields[0].Required = false
	if err = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); !errors.Is(err, ErrConflict) {
		t.Fatal("approve-only bypassed fields", err)
	}
	input := app.ApprovalInputSubmission{SchemaVersion: 1, Inputs: app.ApprovalInputValues{"e1": {"notes": "private completion canary"}}}
	if err = s.DecideApprovalWithInputs(e.OwnerID, "wrong-client", e.RequestID, row.ApprovalID, row.Digest, "approve", input); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for range 3 {
		if err = s.DecideApprovalWithInputs(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve", input); err != nil {
			t.Fatal(err)
		}
	}
	received := <-seen
	if received["e1"]["notes"] != "private completion canary" {
		t.Fatal(received)
	}
	input.Inputs["e1"]["notes"] = "different"
	if err = s.DecideApprovalWithInputs(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve", input); !errors.Is(err, ErrConflict) {
		t.Fatal("changed submission accepted", err)
	}
	status, _ := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if len(status.ApprovalReceipts) != 1 || status.ApprovalReceipts[0].InputsDigest == "" {
		t.Fatal(status)
	}
	raw, _ := os.ReadFile(filepath.Join(s.Root(), "control.json"))
	if strings.Contains(string(raw), "canary") || strings.Contains(string(raw), "input_form") {
		t.Fatal("input content entered control ledger")
	}
	close(finish)
	completed(t, s, e)
	if effects.Load() != 1 {
		t.Fatal("duplicate continuation")
	}
}
