package main

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

type observedStreamCaller struct {
	pipeCaller
	stopped chan struct{}
}

func (c *observedStreamCaller) StreamCall(ctx context.Context, r iscpworkbench.Request, emit func(json.RawMessage) error) (iscpworkbench.Response, error) {
	if err := emit(json.RawMessage(`{"verified":true}`)); err != nil {
		return iscpworkbench.Response{}, err
	}
	<-ctx.Done()
	close(c.stopped)
	return iscpworkbench.Response{}, ctx.Err()
}

type ipcCapture struct {
	frames chan map[string]json.RawMessage
}

func (w *ipcCapture) Write(raw []byte) (int, error) {
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(raw, &frame); err != nil {
		return 0, err
	}
	w.frames <- frame
	return len(raw), nil
}
func TestPipeStreamEventsCarryAuthorityAndCancelOnlyObservation(t *testing.T) {
	input, feed := io.Pipe()
	defer input.Close()
	defer feed.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	caller := &observedStreamCaller{stopped: make(chan struct{})}
	capture := &ipcCapture{frames: make(chan map[string]json.RawMessage, 8)}
	done := make(chan error, 1)
	go func() { done <- serve(ctx, input, &ipcWriter{writer: capture}, caller) }()
	send := func(frame any) {
		t.Helper()
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = feed.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	next := func() map[string]json.RawMessage {
		t.Helper()
		select {
		case f := <-capture.frames:
			return f
		case <-ctx.Done():
			t.Fatal("IPC response missing")
			return nil
		}
	}
	id := "11111111-1111-4111-8111-111111111111"
	request := iscpworkbench.Request{Type: iscpworkbench.RequestType, Profile: iscpworkbench.ProfileV2, ID: id, RequestID: "22222222-2222-4222-8222-222222222222", InstallationID: "33333333-3333-4333-8333-333333333333", Operation: iscpworkbench.OperationExecutionProgressStream, Body: json.RawMessage(`{"schema_version":1}`)}
	send(map[string]any{"ipc_version": 1, "type": "stream_call", "id": id, "request": request})
	frame := next()
	if string(frame["type"]) != `"stream_event"` || string(frame["authority_valid_for_ms"]) != "60000" || string(frame["id"]) != `"`+id+`"` {
		t.Fatalf("event lost verified authority/binding: %+v", frame)
	}
	// An ordinary RPC is usable while the stream observer remains active.
	send(map[string]any{"ipc_version": 1, "type": "call", "id": "ordinary", "request": iscpworkbench.Request{Type: iscpworkbench.RequestType, Profile: iscpworkbench.ProfileV2, ID: "44444444-4444-4444-8444-444444444444", Operation: iscpworkbench.OperationIdentity}})
	frame = next()
	if string(frame["id"]) != `"ordinary"` {
		t.Fatalf("ordinary RPC blocked: %+v", frame)
	}
	send(map[string]any{"ipc_version": 1, "type": "stream_cancel", "id": id})
	select {
	case <-caller.stopped:
	case <-ctx.Done():
		t.Fatal("observer cancellation lost")
	}
	frame = next()
	if string(frame["type"]) != `"response"` || string(frame["id"]) != `"`+id+`"` {
		t.Fatalf("completion binding lost: %+v", frame)
	}
	send(map[string]any{"ipc_version": 1, "type": "shutdown"})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("helper did not stop")
	}
}
