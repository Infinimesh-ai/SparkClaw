package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/latencytrace"
)

func TestPrivateLatencyTraceCorrelatesEncryptedRPC(t *testing.T) {
	if os.Getenv("SPARKCLAW_TRACE_TEST_CHILD") != "1" {
		directory := filepath.Join(t.TempDir(), "private-trace")
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPrivateLatencyTraceCorrelatesEncryptedRPC$", "-test.timeout=15s")
		cmd.Env = append(os.Environ(), "SPARKCLAW_TRACE_TEST_CHILD=1", latencytrace.Environment+"="+directory)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("trace integration failed: %v\n%s", err, output)
		}
		return
	}
	lateID := newUUID()
	lateEntered, lateRelease := make(chan struct{}), make(chan struct{})
	initiator, _, _, _ := testEndpoints(t, func(_ context.Context, request Request) Response {
		if request.ID == lateID {
			close(lateEntered)
			select {
			case <-lateRelease:
			case <-t.Context().Done():
			}
		}
		return Response{Status: 200, Body: json.RawMessage(`{"private":"never traced"}`)}
	})
	rpcID, requestID := newUUID(), newUUID()
	response, err := initiator.Call(t.Context(), Request{Type: RequestType, Profile: Profile, ID: rpcID, Operation: OperationLookup, RequestID: requestID})
	if err != nil || response.Status != 200 {
		t.Fatal("trace changed encrypted call result", err)
	}
	// Let an encrypted response arrive only after the caller has removed its
	// pending slot. It must still be visible without reanimating the call.
	lateCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	lateResult := make(chan error, 1)
	go func() {
		_, err := initiator.Call(lateCtx, Request{Type: RequestType, Profile: Profile, ID: lateID, Operation: OperationLookup, RequestID: requestID})
		lateResult <- err
	}()
	select {
	case <-lateEntered:
	case err := <-lateResult:
		t.Fatal("late fixture did not reach the handler", err)
	}
	if err := <-lateResult; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expired call was completed", err)
	}
	close(lateRelease)
	failed, failedSession := responseUnitEndpoint(t, func(context.Context) (iscpauth.Policy, error) {
		return iscpauth.Policy{}, errors.Join(iscpnodeissuer.ErrDenied, errors.New("private issuer detail"))
	})
	failedID, call := responsePending(failed, t.Context())
	call.trace = latencytrace.Fields{Role: RoleInitiator, RPCID: failedID, RequestID: requestID, Operation: OperationLookup}
	if err := failed.acceptResponse(failedSession.ctx, responseBytes(failedID, "never traced"), failedSession.id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if answer := awaitResponseResult(t, call.result); answer.err == nil {
		t.Fatal("trace changed denied response outcome")
	}
	want := map[string]bool{"rpc.call.start": false, "rpc.call": false, "rpc.send_authorization": false, "rpc.request_received": false, "rpc.request_authorization": false, "rpc.handler": false, "rpc.encrypted_received": false, "rpc.response_authorization": false, "rpc.delivered": false}
	failureClassSeen := false
	lateDropped := false
	lateDelivered := false
	lateDeadline := false
	envelopesSent, envelopesReceived := map[string]string{}, map[string]string{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		files, err := filepath.Glob(filepath.Join(os.Getenv(latencytrace.Environment), "*.ndjson"))
		if err != nil || len(files) != 1 {
			time.Sleep(time.Millisecond)
			continue
		}
		raw, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "never traced") || strings.Contains(string(raw), "fixture-access") || strings.Contains(string(raw), "private issuer detail") {
			t.Fatal("trace leaked business data")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			var row struct {
				Stage     string `json:"stage"`
				RPCID     string `json:"rpc_id"`
				RequestID string `json:"request_id"`
				Operation string `json:"operation"`
				Status    string `json:"status"`
				Role      string `json:"role"`
				Envelope  string `json:"envelope_id"`
				Reason    string `json:"reason"`
			}
			if json.Unmarshal([]byte(line), &row) != nil {
				continue
			}
			if row.Stage == "rpc.envelope_received" {
				envelopesReceived[row.Envelope] = row.Role
			}
			if row.RPCID == lateID {
				lateDropped = lateDropped || row.Stage == "rpc.response_dropped" && row.Reason == "no_pending_call" && uuidPattern.MatchString(row.Envelope)
				lateDelivered = lateDelivered || row.Stage == "rpc.delivered"
				lateDeadline = lateDeadline || row.Stage == "rpc.call" && row.Reason == "response_wait" && row.Status == "deadline"
			}
			if row.RPCID != rpcID && row.RPCID != failedID {
				continue
			}
			if row.RequestID != requestID || row.Operation != OperationLookup {
				t.Fatal("trace lost original execution correlation")
			}
			if row.RPCID == failedID {
				if row.Stage == "rpc.response_authorization.denied" && row.Status == "error" {
					failureClassSeen = true
				}
				continue
			}
			if _, tracked := want[row.Stage]; tracked {
				want[row.Stage] = true
			}
			if row.Stage == "rpc.envelope_created" {
				envelopesSent[row.Envelope] = row.Role
			}
		}
		complete := true
		for _, found := range want {
			complete = complete && found
		}
		correlated := len(envelopesSent) == 2
		for id, sender := range envelopesSent {
			receiver := envelopesReceived[id]
			correlated = correlated && uuidPattern.MatchString(id) && receiver != "" && receiver != sender
		}
		if complete && failureClassSeen && correlated && lateDeadline && lateDropped && !lateDelivered {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("missing correlated stages", want, "failure class", failureClassSeen, "sent envelopes", envelopesSent, "received envelopes", envelopesReceived, "deadline", lateDeadline, "late dropped", lateDropped, "late delivered", lateDelivered)
}
