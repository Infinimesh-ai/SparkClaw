package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func schedulePresentation() *app.ClientPresentation {
	return &app.ClientPresentation{SchemaVersion: 1, ScheduleShow: true, Timezone: "Asia/Shanghai", RequestedAt: time.Now().UTC()}
}
func scheduleAction(e Envelope) *app.ClientAction {
	return &app.ClientAction{SchemaVersion: 1, Type: app.ClientActionScheduleShow, ActionID: e.RequestID, RequestID: e.RequestID, ConversationID: e.ConversationID, Timezone: e.ClientPresentation.Timezone, DateStart: "2030-02-01", DateEnd: "2030-02-02", ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
}

func TestClientPresentationEnvelopeStrictValidation(t *testing.T) {
	e := fixture()
	e.ClientPresentation = schedulePresentation()
	raw, _ := json.Marshal(e)
	if _, err := Decode(raw, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["schema_version"] = 2 },
		func(m map[string]any) { m["schedule_show"] = false },
		func(m map[string]any) { m["timezone"] = "bad/timezone" },
		func(m map[string]any) { delete(m, "requested_at") },
		func(m map[string]any) { m["request_id"] = "self-selected" },
	} {
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		change(fields["client_presentation"].(map[string]any))
		bad, _ := json.Marshal(fields)
		if _, err := Decode(bad, Digest(bad)); err == nil {
			t.Fatalf("accepted invalid capability: %s", bad)
		}
	}
	duplicate := strings.Replace(string(raw), `"schedule_show":true`, `"schedule_show":true,"schedule_show":true`, 1)
	if _, err := Decode([]byte(duplicate), Digest([]byte(duplicate))); err == nil {
		t.Fatal("accepted duplicate capability fields")
	}
}

func TestClientActionDeliveryAndPresentationReceiptsAreIndependentDurable(t *testing.T) {
	s, e, _ := setup(t, func(_ context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		return Output{Content: "requested desktop presentation", ClientAction: scheduleAction(e)}, nil
	})
	e.ClientPresentation = schedulePresentation()
	raw, _ := json.Marshal(e)
	if _, err := s.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	status := completed(t, s, e)
	var payload Payload
	if err := json.Unmarshal([]byte(status.Result.Payload), &payload); err != nil || payload.ClientAction == nil || payload.ClientAction.RequestID != e.RequestID {
		t.Fatalf("missing action: %+v %v", payload, err)
	}
	if status.ClientActionReceipt != nil {
		t.Fatal("backend claims view already shown")
	}
	receipt := &app.ClientActionReceipt{ActionID: e.RequestID, Status: "shown"}
	if err := s.Ack(e.OwnerID, e.ClientID, e.RequestID, 1, status.Result.Digest, true); err != nil {
		t.Fatal(err)
	}
	delivered, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || delivered.ClientActionReceipt != nil {
		t.Fatal("delivery ACK became presentation ACK", err)
	}
	s.Close()
	resumed, err := New(s.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	for range 2 {
		if err := resumed.AckWithClientAction(e.OwnerID, e.ClientID, e.RequestID, 1, status.Result.Digest, true, receipt); err != nil {
			t.Fatal(err)
		}
	}
	receipt.Status = "failed" // caller mutation must not alter the persisted receipt.
	got, err := resumed.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || got.ClientActionReceipt == nil || got.ClientActionReceipt.Status != "shown" {
		t.Fatalf("receipt: %+v %v", got, err)
	}
	if err := resumed.AckWithClientAction(e.OwnerID, e.ClientID, e.RequestID, 1, status.Result.Digest, true, receipt); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting outcome accepted", err)
	}
	if err := resumed.AckWithClientAction(e.OwnerID, "another-client", e.RequestID, 1, status.Result.Digest, true, &app.ClientActionReceipt{ActionID: e.RequestID, Status: "shown"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-client ACK", err)
	}
	got.ClientActionReceipt.Status = "failed"
	disk, err := Read(s.root)
	if err != nil {
		t.Fatal(err)
	}
	got, err = disk.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || got.ClientActionReceipt.Status != "shown" {
		t.Fatal("snapshot receipt", err)
	}
	control, err := os.ReadFile(filepath.Join(s.root, "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{e.ConversationID, "2030-02-01", "requested desktop presentation", "Asia/Shanghai"} {
		if strings.Contains(string(control), secret) {
			t.Fatal("presentation content leaked into control ledger", secret)
		}
	}
	if _, err := os.Stat(s.contentPath(keyFor(e.OwnerID, e.ClientID, e.RequestID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("result was not purged on delivery ACK", err)
	}
}

func TestClientActionCannotBeIssuedOrAcknowledgedWithoutBinding(t *testing.T) {
	for _, bad := range []string{"no-capability", "wrong-request", "wrong-conversation", "wrong-timezone", "bad-range"} {
		t.Run(bad, func(t *testing.T) {
			s, e, _ := setup(t, func(_ context.Context, e Envelope, _ map[string][]byte) (Output, error) {
				copy := e
				copy.ClientPresentation = schedulePresentation()
				a := scheduleAction(copy)
				switch bad {
				case "wrong-request":
					a.RequestID = newUUID()
					a.ActionID = a.RequestID
				case "wrong-conversation":
					a.ConversationID = newUUID()
				case "wrong-timezone":
					a.Timezone = "UTC"
				case "bad-range":
					a.DateEnd = a.DateStart
				}
				return Output{ClientAction: a}, nil
			})
			if bad != "no-capability" {
				e.ClientPresentation = schedulePresentation()
			}
			raw, _ := json.Marshal(e)
			if _, err := s.Submit(t.Context(), e, Digest(raw)); err != nil {
				t.Fatal(err)
			}
			s.Wait()
			got, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
			if err != nil || got.State != "failed" || got.Result != nil {
				t.Fatalf("accepted bad action: %+v %v", got, err)
			}
		})
	}
	s, e, digest := setup(t, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		return Output{Content: "ordinary chat"}, nil
	})
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	got := completed(t, s, e)
	if err := s.AckWithClientAction(e.OwnerID, e.ClientID, e.RequestID, 1, got.Result.Digest, true, &app.ClientActionReceipt{ActionID: e.RequestID, Status: "shown"}); !errors.Is(err, ErrConflict) {
		t.Fatal("unsolicited UI receipt", err)
	}
}
