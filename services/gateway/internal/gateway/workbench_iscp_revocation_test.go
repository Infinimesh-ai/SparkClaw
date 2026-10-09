package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestWorkbenchISCPRevocationCancelsDetachedSubmission(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	server, _, cfg, handler := workbenchISCPFixture(t, func(ctx context.Context, _ execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return execution.Output{}, ctx.Err()
	})
	bindWorkbenchISCP(t, handler)
	input := workbenchISCPEnvelope()
	raw, _ := json.Marshal(input)
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw)); result.Status != http.StatusAccepted {
		t.Fatalf("submit: %d", result.Status)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("execution not started")
	}
	// This is the signed issuer-revocation callback path. It must work even
	// though the unrelated bearer Client row has not been manually revoked.
	server.cancelClientConnections(cfg.Binding.ClientID)
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("detached execution survived revocation")
	}
	server.executions.Wait()
	status, err := server.executions.Lookup(input.OwnerID, input.ClientID, input.RequestID)
	if err != nil || status.State != "unknown" || status.Result != nil {
		t.Fatalf("revocation fence: %+v %v", status, err)
	}
	// A previously authorized worker arriving after cancellation must not admit
	// another execution with the same Client, even with a new request ID.
	input.RequestID = "66666666-6666-4666-8666-666666666666"
	raw, _ = json.Marshal(input)
	request := workbenchISCPRequest(iscpworkbench.OperationSubmit, raw)
	request.RequestID = input.RequestID
	if result := handler(t.Context(), request); result.Status < 400 {
		t.Fatalf("post-revocation admission: %d", result.Status)
	}
}
