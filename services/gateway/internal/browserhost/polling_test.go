package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPollingHostSharesCommandFencesAndReplaysUnacknowledgedMessages(t *testing.T) {
	broker := newTestBroker(t)
	grant, _ := broker.IssueGrant(testIdentity)
	epoch, err := broker.OpenPolling(t.Context(), testIdentity, grant.HostID, grant.Token, "runtime_poll", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := broker.Poll(testIdentity, grant.HostID, epoch, 0)
	if err != nil || len(first) != 1 {
		t.Fatalf("welcome %v %v", first, err)
	}
	replay, _ := broker.Poll(testIdentity, grant.HostID, epoch, 0)
	if !reflect.DeepEqual(first, replay) {
		t.Fatal("lost poll response consumed the message")
	}
	if _, err = broker.Poll(testIdentity, grant.HostID, epoch, first[0].Sequence+1); !errors.Is(err, ErrFence) {
		t.Fatal("future cursor accepted")
	}
	acquired := make(chan Binding, 1)
	failure := make(chan error, 1)
	go func() {
		binding, err := broker.Acquire(context.Background(), Scope{Identity: testIdentity, ConversationID: "conversation", TaskID: "task"})
		if err != nil {
			failure <- err
		} else {
			acquired <- binding
		}
	}()
	after := first[0].Sequence
	pollCommand := func() Command {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			rows, err := broker.Poll(testIdentity, grant.HostID, epoch, after)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) > 0 {
				after = rows[0].Sequence
				var command Command
				if json.Unmarshal(rows[0].Body, &command) != nil {
					t.Fatal("invalid command")
				}
				return command
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("command missing")
		return Command{}
	}
	command := pollCommand()
	if command.Operation != "acquire" {
		t.Fatal(command)
	}
	if err = broker.ReceivePolling(testIdentity, grant.HostID, epoch, Message{SchemaVersion: 1, Type: "result", CommandID: command.CommandID, Binding: &command.Binding, Status: "completed", Output: json.RawMessage(`{"text":"isolated"}`)}); err != nil {
		t.Fatal(err)
	}
	var binding Binding
	select {
	case binding = <-acquired:
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("acquire timeout")
	}
	go func() {
		_, err := broker.Dispatch(t.Context(), binding, "poll_write", "fill", map[string]any{"ref": "snap:e1", "snapshot_id": "snap", "value": "private"})
		failure <- err
	}()
	command = pollCommand()
	if command.CommandID != "poll_write" {
		t.Fatal(command)
	}
	if err = broker.ClosePolling(testIdentity, grant.HostID, epoch); err != nil {
		t.Fatal(err)
	}
	if err = <-failure; !errors.Is(err, ErrUnknown) {
		t.Fatalf("lost write must remain unknown: %v", err)
	}
	fences := broker.Fences(testIdentity)
	if len(fences) != 1 || fences[0].State != "unknown" {
		t.Fatal(fences)
	}
}
func TestPollingHostRejectsCrossScopeAndRevocation(t *testing.T) {
	broker := newTestBroker(t)
	grant, _ := broker.IssueGrant(testIdentity)
	wrong := testIdentity
	wrong.OwnerID = "other"
	if _, err := broker.OpenPolling(t.Context(), wrong, grant.HostID, grant.Token, "runtime", nil); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	epoch, err := broker.OpenPolling(t.Context(), testIdentity, grant.HostID, grant.Token, "runtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	broker.RevokeClientHosts(testIdentity.ClientID)
	if _, err = broker.Poll(testIdentity, grant.HostID, epoch, 0); !errors.Is(err, ErrFence) && !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
