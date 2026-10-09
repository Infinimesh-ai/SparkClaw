package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestPrivateIPCOriginalBodyAndSchema(t *testing.T) {
	body := []byte("{\n \"text\": \"original\"\n}")
	frame := map[string]any{"ipc_version": 1, "type": "call", "id": "pipe-call", "body_base64": base64.StdEncoding.EncodeToString(body), "request": map[string]any{"type": iscpworkbench.RequestType, "profile": iscpworkbench.Profile, "id": "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "operation": iscpworkbench.OperationIdentity}}
	raw, _ := json.Marshal(frame)
	decoded, err := decodeCall(raw)
	if err != nil || !bytes.Equal(decoded.Request.Body, body) {
		t.Fatalf("original body lost: %s %v", decoded.Request.Body, err)
	}
	for _, raw := range []string{`{"ipc_version":2,"type":"shutdown"}`, `{"ipc_version":1,"type":"shutdown","token":"secret"}`, `{"ipc_version":1,"type":"shutdown"} {}`, `{"ipc_version":1,"type":"call","id":"x","request":{"type":"task.invoke","profile":"sparkclaw.workbench.transport.v1","id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","operation":"http.proxy"}}`} {
		if _, err := decodeCall([]byte(raw)); err == nil {
			t.Fatalf("invalid frame accepted %s", raw)
		}
	}
}

type pipeCaller struct {
	mu     sync.Mutex
	body   []byte
	closed bool
}

func (c *pipeCaller) Run(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
func (c *pipeCaller) Close() error                  { c.mu.Lock(); c.closed = true; c.mu.Unlock(); return nil }
func (c *pipeCaller) Call(ctx context.Context, request iscpworkbench.Request) (iscpworkbench.Response, error) {
	c.mu.Lock()
	c.body = append([]byte(nil), request.Body...)
	c.mu.Unlock()
	return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: iscpworkbench.Profile, ID: request.ID, Status: 200}, nil
}

func TestEOFAndShutdownCloseHelper(t *testing.T) {
	for _, input := range []string{"", `{"ipc_version":1,"type":"shutdown"}` + "\n"} {
		caller := &pipeCaller{}
		var output bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := serve(ctx, strings.NewReader(input), &ipcWriter{writer: &output}, caller)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		caller.mu.Lock()
		closed := caller.closed
		caller.mu.Unlock()
		if !closed {
			t.Fatal("helper did not close endpoint")
		}
	}
}

func (c *pipeCaller) DeleteAuthorization(context.Context, string, uint64) (iscpauth.DeletionReceipt, error) {
	return iscpauth.DeletionReceipt{}, nil
}
func (c *pipeCaller) AuthorizationDeletionReceipt(context.Context, string, uint64) (iscpauth.DeletionReceipt, error) {
	return iscpauth.DeletionReceipt{}, nil
}

func TestControlOnlyRejectsBusinessPipeFrames(t *testing.T) {
	body := `{"ipc_version":1,"type":"call","id":"x","request":{"type":"task.invoke","profile":"sparkclaw.workbench.transport.v1","id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","operation":"workbench.identity"}}` + "\n"
	var output bytes.Buffer
	err := serveFrames(context.Background(), strings.NewReader(body), &ipcWriter{writer: &output}, &pipeCaller{}, nil, true)
	if err == nil {
		t.Fatal("control-only pipe accepted a business call")
	}
}
func TestDeletionControlSchema(t *testing.T) {
	valid := `{"ipc_version":1,"type":"authorization_delete_receipt","id":"receipt","operation_id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","expected_revision":1}`
	if _, err := decodeCall([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCall([]byte(strings.Replace(valid, `"expected_revision":1`, `"expected_revision":0`, 1))); err == nil {
		t.Fatal("zero deletion revision accepted")
	}
}
