package browsercontrol

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type controllerWireFixture struct {
	t                                       *testing.T
	mu                                      sync.Mutex
	mode                                    string
	payload                                 []byte
	lease                                   SessionLease
	command                                 string
	executions, chunks, concurrent, maximum int
	receipts                                []string
}

func wireJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func newControllerWireFixture(t *testing.T) (*controllerWireFixture, *ISCPControllerClient) {
	t.Helper()
	f := &controllerWireFixture{t: t}
	hooks := ControllerFenceHooks{
		Before: func(_ context.Context, intent ControllerIntent) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.receipts = append(f.receipts, intent.CommandID+":begin")
			return nil
		},
		After: func(_ context.Context, intent ControllerIntent, outcome string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.receipts = append(f.receipts, intent.CommandID+":"+outcome)
			return nil
		},
	}
	client, err := NewISCPControllerClient(f, wireJSON(map[string]any{"owner_id": "owner", "client_id": "client", "installation_id": "installation", "local_conversation_id": "conversation"}), 7, hooks)
	if err != nil {
		t.Fatal(err)
	}
	return f, client
}
func (f *controllerWireFixture) ControllerCall(ctx context.Context, operation string, raw json.RawMessage) (json.RawMessage, error) {
	if operation == "browser.controller.status" {
		return wireJSON(ClientControllerStatus{SchemaVersion: 1, Protocol: ClientControllerProtocol, Available: true, ProcessState: "stopped",
			Versions: map[string]string{"@playwright/mcp": "0.0.80", "playwright": "1.63.0-alpha-2026-08-31", "playwright-core": "1.63.0-alpha-2026-08-31"},
			Limits:   ControllerWireLimits{ResponseBytes: 10 << 20, TimeoutMS: 25000, WireBytes: 63488, CacheBytes: 20 << 20, ChunkBytes: 32768, ChunkConcurrency: 4}}), nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	if operation == "browser.controller.chunk" {
		var offset, length int
		_ = json.Unmarshal(input["offset"], &offset)
		_ = json.Unmarshal(input["length"], &length)
		f.mu.Lock()
		f.chunks++
		f.concurrent++
		if f.concurrent > f.maximum {
			f.maximum = f.concurrent
		}
		mode := f.mode
		found := false
		for _, row := range f.receipts {
			if row == f.command+":completed" {
				found = true
			}
		}
		f.mu.Unlock()
		if !found {
			f.t.Error("download started before completion receipt")
		}
		defer func() { f.mu.Lock(); f.concurrent--; f.mu.Unlock() }()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
		if mode == "lost_chunk" {
			return nil, errors.New("lost chunk")
		}
		if offset < 0 || length <= 0 || offset+length > len(f.payload) {
			return nil, errors.New("bad requested range")
		}
		bytes := append([]byte(nil), f.payload[offset:offset+length]...)
		if mode == "bad_hash" {
			bytes[0] ^= 1
		}
		if mode == "short_chunk" {
			bytes = bytes[:len(bytes)-1]
		}
		return wireJSON(map[string]any{"schema_version": 1, "command_id": input["command_id"], "result_id": input["result_id"], "offset": offset, "total_bytes": len(f.payload), "data_base64": base64.StdEncoding.EncodeToString(bytes)}), nil
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	digest, err := ControllerRequestDigest(operation, body)
	if err != nil || digest != body["digest"] {
		return nil, errors.New("invalid request digest")
	}
	command := body["command_id"].(string)
	reply := controllerReply{SchemaVersion: 1, CommandID: command, Digest: digest, Binding: input["binding"], Incarnation: strings.Repeat("a", 32), Outcome: "completed"}
	switch operation {
	case "browser.controller.acquire":
		f.lease = SessionLease{SchemaVersion: 1, State: "acquired", ProfileID: ClientControllerProfile, Lane: "mcp", SessionID: "session_" + strings.Repeat("b", 32), CredentialGeneration: 7, ControllerGeneration: 9, SessionGeneration: 1, PageGeneration: 1, ExpiresAt: time.Now().Add(time.Minute)}
		reply.Payload = wireJSON(f.lease)
	case "browser.controller.release":
		reply.Payload = wireJSON(ReleaseResult{SchemaVersion: 1, State: "released", ProfileID: ClientControllerProfile, ControllerGeneration: f.lease.ControllerGeneration, SessionGeneration: f.lease.SessionGeneration})
	case "browser.controller.execute":
		f.executions++
		switch f.mode {
		case "network_error":
			return nil, errors.New("connection lost after send")
		case "not_sent":
			return nil, ErrControllerNotSent
		case "refused":
			reply.Outcome, reply.ErrorCode = "not_dispatched", "browser_busy"
			return wireJSON(reply), nil
		case "wrong_digest":
			reply.Digest = strings.Repeat("0", 64)
			return wireJSON(reply), nil
		}
		var inner string
		var args map[string]any
		_ = json.Unmarshal(input["operation"], &inner)
		_ = json.Unmarshal(input["arguments"], &args)
		mutation, _ := ClientControllerOperation(inner, args)
		if mutation {
			f.lease.PageGeneration++
		}
		result := ExecutionResult{SchemaVersion: 1, State: "completed", ProfileID: ClientControllerProfile, Lane: "mcp", SessionID: f.lease.SessionID, CredentialGeneration: 7,
			ControllerGeneration: f.lease.ControllerGeneration, SessionGeneration: f.lease.SessionGeneration, PageGeneration: f.lease.PageGeneration, Operation: inner, Result: wireJSON(map[string]any{"text": strings.Repeat("value<>&中文", 10000)})}
		f.payload = wireJSON(result)
		f.command = command
		sum := sha256.Sum256(f.payload)
		reply.Result = &controllerResultDescriptor{ResultID: "result_fixture", TotalBytes: len(f.payload), SHA256: hex.EncodeToString(sum[:]), ExpiresAt: time.Now().Add(time.Minute)}
	default:
		return nil, errors.New("unexpected operation")
	}
	return wireJSON(reply), nil
}

func acquireWire(t *testing.T, client *ISCPControllerClient) SessionLease {
	t.Helper()
	lease, err := client.Acquire(t.Context(), AcquireRequest{ProfileID: ClientControllerProfile, CredentialGeneration: 7, SessionTTLMS: 60000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestISCPControllerRecordsEffectBeforeBoundedChunkAssembly(t *testing.T) {
	f, client := newControllerWireFixture(t)
	lease := acquireWire(t, client)
	result, err := client.Execute(t.Context(), ExecuteRequest{Lease: lease, Operation: "page.fill", Arguments: map[string]any{"ref": "f1e2", "text": "typed input", "submit": true}})
	if err != nil {
		t.Fatal(err)
	}
	if result.PageGeneration != lease.PageGeneration+1 || len(result.Result) < 100000 || f.executions != 1 || f.maximum != 4 {
		t.Fatalf("result/gates: gen=%d bytes=%d calls=%d max=%d", result.PageGeneration, len(result.Result), f.executions, f.maximum)
	}
	if _, err := client.Release(t.Context(), ReleaseRequest{ProfileID: ClientControllerProfile, SessionID: lease.SessionID, ControllerGeneration: lease.ControllerGeneration, SessionGeneration: lease.SessionGeneration}); err != nil {
		t.Fatal(err)
	}
}

func TestISCPControllerPayloadFailureCannotReplayCompletedMutation(t *testing.T) {
	for _, mode := range []string{"lost_chunk", "bad_hash", "short_chunk"} {
		t.Run(mode, func(t *testing.T) {
			f, client := newControllerWireFixture(t)
			lease := acquireWire(t, client)
			f.mode = mode
			_, err := client.Execute(t.Context(), ExecuteRequest{Lease: lease, Operation: "page.click", Arguments: map[string]any{"ref": "e2"}})
			if ErrorCode(err) != CodeResultUnavailable || ErrorRetryable(err) || f.executions != 1 {
				t.Fatalf("result failure: %v calls=%d", err, f.executions)
			}
			if f.receipts[len(f.receipts)-1] != f.command+":completed" {
				t.Fatal("completion was lost", f.receipts)
			}
			if _, err := client.Release(t.Context(), ReleaseRequest{ProfileID: ClientControllerProfile, SessionID: lease.SessionID, ControllerGeneration: lease.ControllerGeneration, SessionGeneration: lease.SessionGeneration}); err != nil {
				t.Fatal("stable session could not release after payload loss", err)
			}
		})
	}
}

func TestISCPControllerOnlyTrustedIdentityOutcomeCanResolveMutation(t *testing.T) {
	for _, row := range []struct{ mode, code, outcome string }{{"network_error", CodeWriteUnknown, "unknown"}, {"not_sent", CodeControllerUnavailable, "not_dispatched"}, {"refused", CodeBusy, "not_dispatched"}, {"wrong_digest", CodeWriteUnknown, "unknown"}} {
		t.Run(row.mode, func(t *testing.T) {
			f, client := newControllerWireFixture(t)
			lease := acquireWire(t, client)
			f.mode = row.mode
			_, err := client.Execute(t.Context(), ExecuteRequest{Lease: lease, Operation: "page.click", Arguments: map[string]any{"ref": "e2"}})
			if ErrorCode(err) != row.code || ErrorRetryable(err) {
				t.Fatalf("failure classification: %v code=%s", err, ErrorCode(err))
			}
			if !strings.HasSuffix(f.receipts[len(f.receipts)-1], ":"+row.outcome) || f.executions != 1 || f.chunks != 0 {
				t.Fatal("incorrect receipt or replay", f.receipts)
			}
		})
	}
}
