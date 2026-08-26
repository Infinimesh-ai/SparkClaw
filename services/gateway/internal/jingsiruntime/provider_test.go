package jingsiruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const testBearer = "test-runtime-bearer-credential"

type fakeExecutor struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	block   bool
}

func (f *fakeExecutor) Execute(ctx context.Context, input ExecutionInput) (ExecutionOutput, error) {
	f.mu.Lock()
	f.calls++
	if f.started != nil {
		select {
		case <-f.started:
		default:
			close(f.started)
		}
	}
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return ExecutionOutput{}, ctx.Err()
	}
	return ExecutionOutput{
		State: "succeeded", Summary: "bounded result",
		TraceRef: OpaqueRef{ID: "trace:test", Version: "v1"},
	}, nil
}

func (f *fakeExecutor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestProviderSubmitReplayStatusEventsAndRestart(t *testing.T) {
	stateDir := t.TempDir()
	executor := &fakeExecutor{}
	provider := newTestProvider(t, stateDir, executor)
	ctx, cancel := context.WithCancel(t.Context())
	provider.Start(ctx)
	defer stopProvider(t, provider, cancel)
	server := httptest.NewServer(provider)
	defer server.Close()

	request := submitRequest("request_submit_1", "Summarize the authorized note.")
	first := callRuntime(t, server.URL+"/v1/executions:submit", request, "task_demo:runtime-submit")
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first submit status = %d body=%s", first.StatusCode, first.Raw)
	}
	executionID := nestedString(t, first.Body, "payload", "execution", "execution_id")
	replayRequest := submitRequest("request_submit_2", "Summarize the authorized note.")
	replay := callRuntime(t, server.URL+"/v1/executions:submit", replayRequest, "task_demo:runtime-submit")
	if replay.StatusCode != http.StatusAccepted || nestedString(t, replay.Body, "payload", "execution", "execution_id") != executionID {
		t.Fatalf("exact replay did not return one execution: status=%d body=%s", replay.StatusCode, replay.Raw)
	}

	waitForState(t, server.URL, executionID, "succeeded")
	if executor.callCount() != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.callCount())
	}
	eventsRequest := executionEventsRequest(executionID)
	events := callRuntime(t, server.URL+"/v1/execution-events:list", eventsRequest, "")
	if events.StatusCode != http.StatusOK || nestedBool(t, events.Body, "payload", "terminal") != true {
		t.Fatalf("events response = %d %s", events.StatusCode, events.Raw)
	}
	page := nestedSlice(t, events.Body, "payload", "events")
	if len(page) != 4 {
		t.Fatalf("event count = %d, want accepted/queued/running/succeeded", len(page))
	}
	for index, raw := range page {
		event := raw.(map[string]any)
		if uint64(event["sequence"].(float64)) != uint64(index+1) {
			t.Fatalf("event sequence at %d = %#v", index, event)
		}
	}

	cancel()
	if err := provider.Wait(t.Context()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	restartedExecutor := &fakeExecutor{}
	restarted := newTestProvider(t, stateDir, restartedExecutor)
	restarted.Start(t.Context())
	restartedServer := httptest.NewServer(restarted)
	defer restartedServer.Close()
	lookup := callRuntime(t, restartedServer.URL+"/v1/executions:lookup", lookupRequest("request_lookup_restart"), "")
	if lookup.StatusCode != http.StatusOK || nestedString(t, lookup.Body, "payload", "outcome") != "bound" ||
		nestedString(t, lookup.Body, "payload", "execution", "execution_id") != executionID {
		t.Fatalf("restart lookup did not recover durable binding: %d %s", lookup.StatusCode, lookup.Raw)
	}
	if restartedExecutor.callCount() != 0 {
		t.Fatalf("terminal restart replayed execution %d times", restartedExecutor.callCount())
	}
}

func TestProviderSemanticDriftAndDurableNegativeFence(t *testing.T) {
	stateDir := t.TempDir()
	provider := newTestProvider(t, stateDir, &fakeExecutor{})
	provider.Start(t.Context())
	server := httptest.NewServer(provider)
	defer server.Close()

	first := callRuntime(t, server.URL+"/v1/executions:submit", submitRequest("request_submit", "Original goal."), "task_demo:runtime-submit")
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d body=%s", first.StatusCode, first.Raw)
	}
	drift := callRuntime(t, server.URL+"/v1/executions:submit", submitRequest("request_drift", "Changed goal."), "task_demo:runtime-submit")
	if drift.StatusCode != http.StatusConflict || nestedString(t, drift.Body, "payload", "code") != "idempotency_conflict" {
		t.Fatalf("semantic drift response = %d %s", drift.StatusCode, drift.Raw)
	}
	crossSpaceRequest := submitRequest("request_cross_space", "Original goal.")
	crossSpaceRequest.Authorization.SpaceID = "space_other"
	crossSpace := callRuntime(t, server.URL+"/v1/executions:submit", crossSpaceRequest, "task_demo:runtime-submit")
	if crossSpace.StatusCode != http.StatusConflict || nestedString(t, crossSpace.Body, "payload", "code") != "idempotency_conflict" {
		t.Fatalf("request key was reused across spaces: %d %s", crossSpace.StatusCode, crossSpace.Raw)
	}

	unknown := lookupRequest("request_lookup_unknown")
	unknown.Payload.RequestKey = "task_never_started:runtime-submit"
	fence := callRuntime(t, server.URL+"/v1/executions:lookup", unknown, "")
	if fence.StatusCode != http.StatusOK || nestedString(t, fence.Body, "payload", "outcome") != "not_started" {
		t.Fatalf("negative fence response = %d %s", fence.StatusCode, fence.Raw)
	}
	fencedSubmit := submitRequest("request_fenced_submit", "Must never run.")
	fencedSubmit.Payload.RequestKey = unknown.Payload.RequestKey
	fencedSubmit.Authorization.TaskID = "task_never_started"
	fenced := callRuntime(t, server.URL+"/v1/executions:submit", fencedSubmit, unknown.Payload.RequestKey)
	if fenced.StatusCode != http.StatusConflict || nestedString(t, fenced.Body, "payload", "code") != "idempotency_conflict" {
		t.Fatalf("fenced request key was revived: %d %s", fenced.StatusCode, fenced.Raw)
	}

	restarted := newTestProvider(t, stateDir, &fakeExecutor{})
	restarted.Start(t.Context())
	restartedServer := httptest.NewServer(restarted)
	defer restartedServer.Close()
	replayedFence := callRuntime(t, restartedServer.URL+"/v1/executions:lookup", unknown, "")
	if nestedString(t, replayedFence.Body, "payload", "negative_fence", "fence_id") != nestedString(t, fence.Body, "payload", "negative_fence", "fence_id") {
		t.Fatalf("negative fence changed across restart: before=%s after=%s", fence.Raw, replayedFence.Raw)
	}
}

func TestProviderCancelIsIdempotentAndAuthorizationBound(t *testing.T) {
	executor := &fakeExecutor{started: make(chan struct{}), block: true}
	provider := newTestProvider(t, t.TempDir(), executor)
	ctx, stop := context.WithCancel(t.Context())
	provider.Start(ctx)
	defer stopProvider(t, provider, stop)
	server := httptest.NewServer(provider)
	defer server.Close()
	accepted := callRuntime(t, server.URL+"/v1/executions:submit", submitRequest("request_submit", "Wait for cancellation."), "task_demo:runtime-submit")
	executionID := nestedString(t, accepted.Body, "payload", "execution", "execution_id")
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("executor did not start")
	}

	deniedRequest := cancelRequest(executionID)
	deniedRequest.Authorization.SpaceID = "space_other"
	denied := callRuntime(t, server.URL+"/v1/executions:cancel", deniedRequest, "")
	if denied.StatusCode != http.StatusNotFound || nestedString(t, denied.Body, "payload", "code") != "not_found" {
		t.Fatalf("authorization drift was not denied uniformly: %d %s", denied.StatusCode, denied.Raw)
	}

	first := callRuntime(t, server.URL+"/v1/executions:cancel", cancelRequest(executionID), "")
	if first.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", first.StatusCode, first.Raw)
	}
	waitForState(t, server.URL, executionID, "canceled")
	replay := callRuntime(t, server.URL+"/v1/executions:cancel", cancelRequest(executionID), "")
	if nestedString(t, replay.Body, "payload", "state") != "canceled" || !nestedBool(t, replay.Body, "payload", "idempotent") {
		t.Fatalf("terminal cancel replay changed outcome: %s", replay.Raw)
	}
}

func newTestProvider(t *testing.T, stateDir string, executor Executor) *Provider {
	t.Helper()
	provider, err := New(Config{
		StateDir: stateDir, BearerToken: testBearer, CallerID: "jingsi-service-v1", MaxConcurrent: 2,
	}, executor)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return provider
}

func authorization() Authorization {
	return Authorization{
		SpaceID: "space_demo", TaskID: "task_demo", Purpose: Purpose{Name: "task.execute"},
		Grant: OpaqueRef{ID: "grant_demo", Version: "v1"}, ToolScope: []string{"files.read"},
		DataScope: []string{"memory.context"}, NetworkScope: []string{}, ApprovalPolicy: "ask",
		DeadlineAt: time.Date(2030, 8, 25, 12, 30, 0, 0, time.UTC),
	}
}

func stopProvider(t *testing.T, provider *Provider, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer waitCancel()
	if err := provider.Wait(waitCtx); err != nil {
		t.Errorf("provider Wait() error = %v", err)
	}
}

func submitRequest(requestID, goal string) SubmitRequest {
	return SubmitRequest{
		Protocol: Protocol, Kind: "execution.submit.request", RequestID: requestID, Authorization: authorization(),
		Payload: SubmitPayload{
			RequestKey: "task_demo:runtime-submit", Goal: goal, Target: "sparkclaw",
			MemoryContext: &MemoryContext{
				Summary: "Authorized preference.", Confidence: 0.8,
				MemoryRefs: []OpaqueRef{{ID: "memory_demo", Version: "v1"}},
			},
			Budget: Budget{MaxRuntimeMS: 120000, MaxToolCalls: 8, MaxOutputBytes: 65536},
		},
	}
}

func lookupRequest(requestID string) LookupRequest {
	value := LookupRequest{Protocol: Protocol, Kind: "execution.lookup.request", RequestID: requestID, Authorization: authorization()}
	value.Payload.RequestKey = "task_demo:runtime-submit"
	return value
}

func executionStatusRequest(executionID string) ExecutionRequest {
	value := ExecutionRequest{Protocol: Protocol, Kind: "execution.status.request", RequestID: "request_status", Authorization: authorization()}
	value.Payload.ExecutionID = executionID
	return value
}

func executionEventsRequest(executionID string) EventsRequest {
	value := EventsRequest{Protocol: Protocol, Kind: "execution.events.request", RequestID: "request_events", Authorization: authorization()}
	value.Payload.ExecutionID = executionID
	value.Payload.Limit = 100
	return value
}

func cancelRequest(executionID string) CancelRequest {
	value := CancelRequest{Protocol: Protocol, Kind: "execution.cancel.request", RequestID: "request_cancel", Authorization: authorization()}
	value.Payload.ExecutionID = executionID
	value.Payload.ReasonCode = "user_requested"
	return value
}

type runtimeResponse struct {
	StatusCode int
	Body       map[string]any
	Raw        string
}

func callRuntime(t *testing.T, endpoint string, body any, idempotencyKey string) runtimeResponse {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", MediaType)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("http.Do() error = %v", err)
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(responseRaw, &decoded); err != nil {
		t.Fatalf("decode response error = %v body=%s", err, responseRaw)
	}
	return runtimeResponse{StatusCode: response.StatusCode, Body: decoded, Raw: string(responseRaw)}
}

func waitForState(t *testing.T, baseURL, executionID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	last := runtimeResponse{}
	for time.Now().Before(deadline) {
		response := callRuntime(t, baseURL+"/v1/executions:status", executionStatusRequest(executionID), "")
		last = response
		if response.StatusCode == http.StatusOK && nestedString(t, response.Body, "payload", "execution", "state") == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("execution %s did not reach %s; last=%d %s", executionID, want, last.StatusCode, last.Raw)
}

func nestedValue(t *testing.T, value any, path ...string) any {
	t.Helper()
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("path %v reached non-object %#v", path, current)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("path %v missing key %q in %#v", path, key, object)
		}
	}
	return current
}

func nestedString(t *testing.T, value any, path ...string) string {
	t.Helper()
	result, ok := nestedValue(t, value, path...).(string)
	if !ok {
		t.Fatalf("path %v is not string", path)
	}
	return result
}

func nestedBool(t *testing.T, value any, path ...string) bool {
	t.Helper()
	result, ok := nestedValue(t, value, path...).(bool)
	if !ok {
		t.Fatalf("path %v is not bool", path)
	}
	return result
}

func nestedSlice(t *testing.T, value any, path ...string) []any {
	t.Helper()
	result, ok := nestedValue(t, value, path...).([]any)
	if !ok {
		t.Fatalf("path %v is not array", path)
	}
	return result
}
