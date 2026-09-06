package jingsiruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	state   string
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
	state := f.state
	if state == "" {
		state = "succeeded"
	}
	return ExecutionOutput{
		State: state, Summary: "bounded result",
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

func TestProviderRejectsEveryActionUntilStart(t *testing.T) {
	executor := &fakeExecutor{}
	provider := newTestProvider(t, t.TempDir(), executor)
	server := httptest.NewServer(provider)
	defer server.Close()

	early := callRuntime(t, server.URL+"/v1/executions:submit", submitRequest("request_early", "Must wait for Start."), "task_demo:runtime-submit")
	if early.StatusCode != http.StatusServiceUnavailable || nestedString(t, early.Body, "payload", "code") != "runtime_unavailable" ||
		!nestedBool(t, early.Body, "payload", "retryable") || nestedValue(t, early.Body, "payload", "retry_after_ms") == nil {
		t.Fatalf("submit before Start = %d %s", early.StatusCode, early.Raw)
	}
	lookup := callRuntime(t, server.URL+"/v1/executions:lookup", lookupRequest("request_early_lookup"), "")
	if lookup.StatusCode != http.StatusServiceUnavailable || nestedString(t, lookup.Body, "payload", "code") != "runtime_unavailable" {
		t.Fatalf("lookup before Start = %d %s", lookup.StatusCode, lookup.Raw)
	}
	if executor.callCount() != 0 || len(provider.store.byKey) != 0 {
		t.Fatalf("pre-Start request had side effects: calls=%d records=%d", executor.callCount(), len(provider.store.byKey))
	}

	ctx, cancel := context.WithCancel(t.Context())
	provider.Start(ctx)
	defer stopProvider(t, provider, cancel)
	accepted := callRuntime(t, server.URL+"/v1/executions:submit", submitRequest("request_after", "Must wait for Start."), "task_demo:runtime-submit")
	if accepted.StatusCode != http.StatusAccepted {
		t.Fatalf("submit after Start = %d %s", accepted.StatusCode, accepted.Raw)
	}
	waitForState(t, server.URL, nestedString(t, accepted.Body, "payload", "execution", "execution_id"), "succeeded")
}

// logCapture collects JSON slog lines from provider goroutines.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(raw []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(raw)
}

func (c *logCapture) lines(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		out = append(out, decoded)
	}
	return out
}

func (c *logCapture) find(t *testing.T, msg string) map[string]any {
	t.Helper()
	for _, line := range c.lines(t) {
		if line["msg"] == msg {
			return line
		}
	}
	t.Fatalf("no log line %q in %s", msg, c.buf.String())
	return nil
}

func TestProviderLogsOperationalEventsWithoutSecrets(t *testing.T) {
	capture := &logCapture{}
	logger := slog.New(slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug}))
	provider, err := New(Config{
		StateDir: t.TempDir(), BearerToken: testBearer, CallerID: "jingsi-service-v1", MaxConcurrent: 2, Logger: logger,
	}, &fakeExecutor{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	provider.Start(ctx)
	defer stopProvider(t, provider, cancel)
	server := httptest.NewServer(provider)
	defer server.Close()

	const wrongBearer = "wrong-bearer-credential-value"
	raw, _ := json.Marshal(submitRequest("request_unauth", "Secret goal text must not be logged."))
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/executions:submit", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+wrongBearer)
	request.Header.Set("Content-Type", MediaType)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("http.Do() error = %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong bearer status = %d", response.StatusCode)
	}
	rejected := capture.find(t, "jingsi runtime bearer rejected")
	if rejected["failures"] != float64(1) {
		t.Fatalf("bearer rejection did not count: %v", rejected)
	}

	const goal = "Secret goal text must not be logged."
	accepted := callRuntime(t, server.URL+"/v1/executions:submit", submitRequest("request_submit", goal), "task_demo:runtime-submit")
	executionID := nestedString(t, accepted.Body, "payload", "execution", "execution_id")
	waitForState(t, server.URL, executionID, "succeeded")
	finished := capture.find(t, "jingsi runtime execution finished")
	if finished["execution_id"] != executionID || finished["outcome"] != "succeeded" {
		t.Fatalf("terminal outcome line = %v", finished)
	}

	drift := callRuntime(t, server.URL+"/v1/executions:submit", submitRequest("request_drift", "Different secret goal."), "task_demo:runtime-submit")
	if drift.StatusCode != http.StatusConflict {
		t.Fatalf("drift status = %d %s", drift.StatusCode, drift.Raw)
	}
	conflict := capture.find(t, "jingsi runtime idempotency conflict")
	if conflict["reason"] != "semantic_drift" || conflict["request_key"] != "task_demo:runtime-submit" {
		t.Fatalf("conflict line = %v", conflict)
	}

	capture.mu.Lock()
	everything := capture.buf.String()
	capture.mu.Unlock()
	for _, secret := range []string{testBearer, wrongBearer, goal, "Different secret goal", "bounded result"} {
		if strings.Contains(everything, secret) {
			t.Fatalf("log output leaked %q: %s", secret, everything)
		}
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newRetentionProvider(t *testing.T, stateDir string, clock *fakeClock, executor Executor) *Provider {
	t.Helper()
	provider, err := New(Config{
		StateDir: stateDir, BearerToken: testBearer, CallerID: "jingsi-service-v1", MaxConcurrent: 2,
		Retention: 30 * 24 * time.Hour, Now: clock.Now,
	}, executor)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return provider
}

func TestProviderRetentionSweepDeletesOnlyExpiredTerminalRecordsAndFences(t *testing.T) {
	stateDir := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)}
	provider := newRetentionProvider(t, stateDir, clock, &fakeExecutor{})
	ctx, cancel := context.WithCancel(t.Context())
	provider.Start(ctx)
	defer stopProvider(t, provider, cancel)
	server := httptest.NewServer(provider)
	defer server.Close()

	submitKey := func(requestID, key string) string {
		t.Helper()
		request := submitRequest(requestID, "Age out after retention.")
		request.Payload.RequestKey = key
		response := callRuntime(t, server.URL+"/v1/executions:submit", request, key)
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("submit %s = %d %s", key, response.StatusCode, response.Raw)
		}
		return nestedString(t, response.Body, "payload", "execution", "execution_id")
	}
	fenceKey := func(requestID, key string) string {
		t.Helper()
		request := lookupRequest(requestID)
		request.Payload.RequestKey = key
		response := callRuntime(t, server.URL+"/v1/executions:lookup", request, "")
		if nestedString(t, response.Body, "payload", "outcome") != "not_started" {
			t.Fatalf("lookup %s = %d %s", key, response.StatusCode, response.Raw)
		}
		return nestedString(t, response.Body, "payload", "negative_fence", "committed_at")
	}

	oldExecution := submitKey("request_old", "task_demo:old")
	waitForState(t, server.URL, oldExecution, "succeeded")
	fenceKey("request_old_fence", "task_demo:old-fence")
	clock.advance(10 * 24 * time.Hour)
	youngExecution := submitKey("request_young", "task_demo:young")
	waitForState(t, server.URL, youngExecution, "succeeded")
	youngFenceAt := fenceKey("request_young_fence", "task_demo:young-fence")
	if entries := stateFiles(t, stateDir); entries != 4 {
		t.Fatalf("state files before sweep = %d, want 4", entries)
	}

	clock.advance(21 * 24 * time.Hour)
	provider.sweepRetention()
	if entries := stateFiles(t, stateDir); entries != 2 {
		t.Fatalf("state files after sweep = %d, want 2", entries)
	}
	provider.store.mu.Lock()
	_, oldKeyKept := provider.store.byKey[provider.store.key("jingsi-service-v1", "task_demo:old")]
	_, oldExecutionKept := provider.store.byExecution[oldExecution]
	_, youngKeyKept := provider.store.byKey[provider.store.key("jingsi-service-v1", "task_demo:young")]
	_, youngExecutionKept := provider.store.byExecution[youngExecution]
	provider.store.mu.Unlock()
	if oldKeyKept || oldExecutionKept || !youngKeyKept || !youngExecutionKept {
		t.Fatalf("indexes inconsistent after sweep: old key=%v exec=%v young key=%v exec=%v", oldKeyKept, oldExecutionKept, youngKeyKept, youngExecutionKept)
	}
	status := callRuntime(t, server.URL+"/v1/executions:status", executionStatusRequest(oldExecution), "")
	if status.StatusCode != http.StatusNotFound {
		t.Fatalf("expired execution still served: %d %s", status.StatusCode, status.Raw)
	}
	youngLookup := lookupRequest("request_young_relookup")
	youngLookup.Payload.RequestKey = "task_demo:young-fence"
	relooked := callRuntime(t, server.URL+"/v1/executions:lookup", youngLookup, "")
	if nestedString(t, relooked.Body, "payload", "negative_fence", "committed_at") != youngFenceAt {
		t.Fatalf("fence inside retention was replaced: %s", relooked.Raw)
	}
}

func TestProviderRetentionKeepsNonterminalWorkAndSweepsOnStart(t *testing.T) {
	stateDir := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)}
	provider := newRetentionProvider(t, stateDir, clock, &fakeExecutor{state: "approval_required"})
	ctx, cancel := context.WithCancel(t.Context())
	provider.Start(ctx)
	server := httptest.NewServer(provider)
	stopped := callRuntime(t, server.URL+"/v1/executions:submit", submitRequest("request_stopped", "Wait for approval."), "task_demo:runtime-submit")
	executionID := nestedString(t, stopped.Body, "payload", "execution", "execution_id")
	waitForState(t, server.URL, executionID, "approval_required")
	fence := lookupRequest("request_fence")
	fence.Payload.RequestKey = "task_demo:fence"
	callRuntime(t, server.URL+"/v1/executions:lookup", fence, "")
	server.Close()
	stopProvider(t, provider, cancel)

	clock.advance(40 * 24 * time.Hour)
	restarted := newRetentionProvider(t, stateDir, clock, &fakeExecutor{})
	restartCtx, restartCancel := context.WithCancel(t.Context())
	restarted.Start(restartCtx)
	defer stopProvider(t, restarted, restartCancel)
	deadline := time.Now().Add(3 * time.Second)
	for stateFiles(t, stateDir) != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if entries := stateFiles(t, stateDir); entries != 1 {
		t.Fatalf("state files after Start sweep = %d, want only the approval_required record", entries)
	}
	restartedServer := httptest.NewServer(restarted)
	defer restartedServer.Close()
	status := callRuntime(t, restartedServer.URL+"/v1/executions:status", executionStatusRequest(executionID), "")
	if status.StatusCode != http.StatusOK || nestedString(t, status.Body, "payload", "execution", "state") != "approval_required" {
		t.Fatalf("nonterminal work was swept: %d %s", status.StatusCode, status.Raw)
	}
}

func stateFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count
}

func TestProviderBoundsAcceptedButNotRunningSubmits(t *testing.T) {
	capture := &logCapture{}
	logger := slog.New(slog.NewJSONHandler(capture, nil))
	executor := &fakeExecutor{started: make(chan struct{}), block: true}
	provider, err := New(Config{
		StateDir: t.TempDir(), BearerToken: testBearer, CallerID: "jingsi-service-v1", MaxConcurrent: 1, Logger: logger,
	}, executor)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	provider.Start(ctx)
	defer stopProvider(t, provider, cancel)
	server := httptest.NewServer(provider)
	defer server.Close()

	submitKey := func(key string) runtimeResponse {
		t.Helper()
		request := submitRequest("request_"+strings.ReplaceAll(key, ":", "_"), "Hold a slot.")
		request.Payload.RequestKey = key
		return callRuntime(t, server.URL+"/v1/executions:submit", request, key)
	}
	running := submitKey("task_demo:running")
	if running.StatusCode != http.StatusAccepted {
		t.Fatalf("first submit = %d %s", running.StatusCode, running.Raw)
	}
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("executor did not start")
	}
	for i := 0; i < queueDepthFactor; i++ {
		if response := submitKey(fmt.Sprintf("task_demo:queued-%d", i)); response.StatusCode != http.StatusAccepted {
			t.Fatalf("queued submit %d = %d %s", i, response.StatusCode, response.Raw)
		}
	}
	rejected := submitKey("task_demo:overflow")
	if rejected.StatusCode != http.StatusServiceUnavailable || nestedString(t, rejected.Body, "payload", "code") != "runtime_unavailable" ||
		!nestedBool(t, rejected.Body, "payload", "retryable") || nestedValue(t, rejected.Body, "payload", "retry_after_ms") != float64(queueRetryAfterMS) {
		t.Fatalf("overflow submit = %d %s", rejected.StatusCode, rejected.Raw)
	}
	provider.store.mu.Lock()
	_, overflowStored := provider.store.byKey[provider.store.key("jingsi-service-v1", "task_demo:overflow")]
	provider.store.mu.Unlock()
	if overflowStored {
		t.Fatal("rejected submit left a record behind")
	}
	if replay := submitKey("task_demo:queued-0"); replay.StatusCode != http.StatusAccepted {
		t.Fatalf("exact replay was rejected by the queue bound: %d %s", replay.StatusCode, replay.Raw)
	}
	if line := capture.find(t, "jingsi runtime submit rejected by queue bound"); line["queued"] != float64(queueDepthFactor) {
		t.Fatalf("queue rejection line = %v", line)
	}

	runningID := nestedString(t, running.Body, "payload", "execution", "execution_id")
	if response := callRuntime(t, server.URL+"/v1/executions:cancel", cancelRequest(runningID), ""); response.StatusCode != http.StatusOK {
		t.Fatalf("cancel = %d %s", response.StatusCode, response.Raw)
	}
	waitForState(t, server.URL, runningID, "canceled")
	deadline := time.Now().Add(3 * time.Second)
	var admitted runtimeResponse
	for time.Now().Before(deadline) {
		admitted = submitKey("task_demo:overflow")
		if admitted.StatusCode == http.StatusAccepted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if admitted.StatusCode != http.StatusAccepted {
		t.Fatalf("queue slot was not released after a running execution ended: %d %s", admitted.StatusCode, admitted.Raw)
	}
}
