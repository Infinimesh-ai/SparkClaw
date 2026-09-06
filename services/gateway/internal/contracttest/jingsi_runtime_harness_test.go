package contracttest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiruntime"
)

const testBearer = "contracttest-runtime-bearer-credential"

// fakeExecutor stands in for the Agent Runtime exactly as the provider's own
// tests do: it counts invocations, signals its first start, and either blocks
// until the execution context ends or returns one bounded successful result
// carrying an opaque artifact and trace reference.
type fakeExecutor struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	block   bool
}

func newFakeExecutor(block bool) *fakeExecutor {
	return &fakeExecutor{started: make(chan struct{}), block: block}
}

func (f *fakeExecutor) Execute(ctx context.Context, input jingsiruntime.ExecutionInput) (jingsiruntime.ExecutionOutput, error) {
	f.mu.Lock()
	f.calls++
	select {
	case <-f.started:
	default:
		close(f.started)
	}
	block := f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return jingsiruntime.ExecutionOutput{}, ctx.Err()
	}
	return jingsiruntime.ExecutionOutput{
		State: "succeeded", Summary: "The authorized note was summarized.",
		ArtifactRefs: []jingsiruntime.ArtifactRef{{ID: "artifact:" + input.ExecutionID, Version: "v1", Kind: "document", MediaType: "text/plain"}},
		TraceRef:     jingsiruntime.OpaqueRef{ID: "trace:" + input.ExecutionID, Version: "v1"},
	}, nil
}

func (f *fakeExecutor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// harness runs one real jingsiruntime.Provider behind an httptest server with
// a frozen clock and an owner-only temp state directory.
type harness struct {
	t        *testing.T
	contract *centralContract
	executor *fakeExecutor
	stateDir string
	clock    time.Time
	server   *httptest.Server
	// lose drops the next response after the provider has handled the
	// request, so the client observes a transport failure while the
	// provider's side effects stand.
	lose atomic.Bool

	mu       sync.Mutex
	provider *jingsiruntime.Provider
	stop     func()
}

func newHarness(t *testing.T, contract *centralContract, clock time.Time, executor *fakeExecutor) *harness {
	t.Helper()
	h := &harness{t: t, contract: contract, executor: executor, stateDir: filepath.Join(t.TempDir(), "state"), clock: clock}
	h.startProvider()
	h.server = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(func() {
		h.server.Close()
		h.stopProvider()
	})
	return h
}

func (h *harness) startProvider() {
	h.t.Helper()
	provider, err := jingsiruntime.New(jingsiruntime.Config{
		StateDir: h.stateDir, BearerToken: testBearer, CallerID: "jingsi-service-v1", MaxConcurrent: 2,
		Now: func() time.Time { return h.clock },
	}, h.executor)
	if err != nil {
		h.t.Fatalf("jingsiruntime.New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	provider.Start(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.provider = provider
	h.stop = func() {
		cancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer waitCancel()
		if err := provider.Wait(waitCtx); err != nil {
			h.t.Errorf("provider Wait() error = %v", err)
		}
	}
}

func (h *harness) stopProvider() {
	h.mu.Lock()
	stop := h.stop
	h.stop = nil
	h.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// restart replaces the provider process on the same durable state directory.
func (h *harness) restart() {
	h.t.Helper()
	h.stopProvider()
	h.startProvider()
}

func (h *harness) serve(w http.ResponseWriter, request *http.Request) {
	h.mu.Lock()
	provider := h.provider
	h.mu.Unlock()
	if h.lose.CompareAndSwap(true, false) {
		provider.ServeHTTP(httptest.NewRecorder(), request)
		panic(http.ErrAbortHandler)
	}
	provider.ServeHTTP(w, request)
}

// setDurable moves the state directory away (or back) so the provider's
// durable dependency is unavailable without touching its internals.
func (h *harness) setDurable(durable bool) {
	h.t.Helper()
	from, to := h.stateDir+".offline", h.stateDir
	if !durable {
		from, to = to, from
	}
	if err := os.Rename(from, to); err != nil {
		h.t.Fatalf("toggle durable state: %v", err)
	}
}

type snapshot struct {
	records int
	calls   int
}

func (h *harness) snapshot() snapshot {
	return snapshot{records: h.recordCount(), calls: h.executor.callCount()}
}

func (h *harness) recordCount() int {
	h.t.Helper()
	entries, err := os.ReadDir(h.stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		h.t.Fatalf("list state directory: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count
}

type wireRequest struct {
	method   string
	path     string
	headers  map[string]string
	body     map[string]any
	bearer   string
	noBearer bool
}

type wireResult struct {
	status int
	body   map[string]any
	raw    string
	before snapshot
	after  snapshot
}

func (h *harness) do(request wireRequest) (wireResult, error) {
	h.t.Helper()
	before := h.snapshot()
	raw, err := json.Marshal(request.body)
	if err != nil {
		h.t.Fatalf("json.Marshal() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(ctx, request.method, h.server.URL+request.path, bytes.NewReader(raw))
	if err != nil {
		h.t.Fatalf("http.NewRequest() error = %v", err)
	}
	if !request.noBearer {
		bearer := request.bearer
		if bearer == "" {
			bearer = testBearer
		}
		httpRequest.Header.Set("Authorization", "Bearer "+bearer)
	}
	httpRequest.Header.Set("Content-Type", h.contract.mediaType)
	for name, value := range request.headers {
		httpRequest.Header.Set(name, value)
	}
	response, err := h.server.Client().Do(httpRequest)
	if err != nil {
		return wireResult{before: before, after: h.snapshot()}, err
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatalf("io.ReadAll() error = %v", err)
	}
	if got := response.Header.Get("Content-Type"); got != h.contract.mediaType {
		h.t.Fatalf("response Content-Type = %q, want %q", got, h.contract.mediaType)
	}
	var decoded map[string]any
	if err := json.Unmarshal(responseRaw, &decoded); err != nil {
		h.t.Fatalf("decode response error = %v body=%s", err, responseRaw)
	}
	return wireResult{status: response.StatusCode, body: decoded, raw: string(responseRaw), before: before, after: h.snapshot()}, nil
}

func (h *harness) must(request wireRequest) wireResult {
	h.t.Helper()
	result, err := h.do(request)
	if err != nil {
		h.t.Fatalf("%s %s transport error = %v", request.method, request.path, err)
	}
	return result
}

// scenarioState carries what a fixture scenario learns while it runs: the
// seed submit, the real execution handle standing in for the fixture's
// synthetic one, and the most recent request per operation for replays.
type scenarioState struct {
	seed          fixtureMessage
	executionID   string
	acceptedAt    string
	lastRequest   map[string]wireRequest
	lastRequestID string
	lastResult    *wireResult
	transportErr  error
}

func newScenarioState(t *testing.T, contract *centralContract) *scenarioState {
	t.Helper()
	submit := contract.operation(t, "execution.submit")
	for _, message := range contract.caseByCategory(t, "submit_idempotency").Messages {
		if message.ExpectedValid && message.Body["kind"] == submit.RequestKind {
			return &scenarioState{seed: message, lastRequest: map[string]wireRequest{}}
		}
	}
	t.Fatal("central submit_idempotency case has no valid submit request to seed from")
	return nil
}

func (s *scenarioState) remember(kind string, request wireRequest, requestID string, result wireResult) {
	s.lastRequest[kind] = request
	s.lastRequestID = requestID
	s.lastResult = &result
}

func (s *scenarioState) seedAuthorization(t *testing.T) map[string]any {
	t.Helper()
	return deepCopy(t, valueObject(t, s.seed.Body["authorization"]))
}

func (s *scenarioState) seedDeadline(t *testing.T) time.Time {
	t.Helper()
	deadline, err := time.Parse(time.RFC3339, valueString(t, valueObject(t, s.seed.Body["authorization"]), "deadline_at"))
	if err != nil {
		t.Fatalf("seed deadline_at: %v", err)
	}
	return deadline
}

func deepCopy(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("copy JSON: %v", err)
	}
	var copied map[string]any
	if err := json.Unmarshal(raw, &copied); err != nil {
		t.Fatalf("copy JSON: %v", err)
	}
	return copied
}

// requestFor turns a fixture request message into a wire request: binding
// method/path by kind, the fixture headers, and the fixture body with the
// synthetic execution id replaced by the scenario's real handle.
func (h *harness) requestFor(message fixtureMessage, state *scenarioState) wireRequest {
	h.t.Helper()
	kind := valueString(h.t, message.Body, "kind")
	operation, ok := h.contract.byRequestKind[kind]
	if !ok {
		h.t.Fatalf("message %q kind %q is not bound as a request", message.Label, kind)
	}
	headers := map[string]string{}
	for name, value := range message.Headers {
		headers[name] = value
	}
	body := deepCopy(h.t, message.Body)
	h.substituteExecution(body, state)
	return wireRequest{method: operation.Method, path: operation.Path, headers: headers, body: body}
}

// replayFor synthesizes the request behind a fixture response message that
// has no request of its own: the last request of that operation (or a lookup
// built from the seed authorization) re-sent under the response's request id
// and request key.
func (h *harness) replayFor(message fixtureMessage, state *scenarioState) wireRequest {
	h.t.Helper()
	kind := valueString(h.t, message.Body, "kind")
	operation, ok := h.contract.bySuccessKind[kind]
	if !ok {
		h.t.Fatalf("message %q kind %q is not bound as a success kind", message.Label, kind)
	}
	template, ok := state.lastRequest[operation.RequestKind]
	if !ok {
		if operation.Operation != "execution.lookup" {
			h.t.Fatalf("message %q needs a prior %s request to replay", message.Label, operation.Operation)
		}
		template = h.lookupRequest(state, "", "")
	}
	request := wireRequest{method: template.method, path: template.path, headers: map[string]string{}, body: deepCopy(h.t, template.body)}
	for name, value := range template.headers {
		request.headers[name] = value
	}
	request.body["request_id"] = message.Body["request_id"]
	payload := valueObject(h.t, request.body["payload"])
	if requestKey, ok := valueObject(h.t, message.Body["payload"])["request_key"].(string); ok {
		payload["request_key"] = requestKey
		if _, bound := request.headers[h.contract.requestKeyHeader]; bound {
			request.headers[h.contract.requestKeyHeader] = requestKey
		}
	}
	h.substituteExecution(request.body, state)
	return request
}

func (h *harness) substituteExecution(body map[string]any, state *scenarioState) {
	payload, ok := body["payload"].(map[string]any)
	if !ok || state.executionID == "" {
		return
	}
	if _, has := payload["execution_id"]; has {
		payload["execution_id"] = state.executionID
	}
}

func (h *harness) lookupRequest(state *scenarioState, requestID, requestKey string) wireRequest {
	h.t.Helper()
	operation := h.contract.operation(h.t, "execution.lookup")
	if requestID == "" {
		requestID = "contracttest-lookup"
	}
	if requestKey == "" {
		requestKey = valueString(h.t, valueObject(h.t, state.seed.Body["payload"]), "request_key")
	}
	return wireRequest{method: operation.Method, path: operation.Path, headers: map[string]string{}, body: map[string]any{
		"protocol": h.contract.protocol, "kind": operation.RequestKind, "request_id": requestID,
		"authorization": state.seedAuthorization(h.t), "payload": map[string]any{"request_key": requestKey},
	}}
}

// executionRequest builds a status/events/cancel request for the scenario's
// real execution under the seed authorization.
func (h *harness) executionRequest(state *scenarioState, operationName, requestID string, extra map[string]any) wireRequest {
	h.t.Helper()
	if state.executionID == "" {
		h.t.Fatalf("scenario has no execution handle for %s", operationName)
	}
	operation := h.contract.operation(h.t, operationName)
	payload := map[string]any{"execution_id": state.executionID}
	for key, value := range extra {
		payload[key] = value
	}
	return wireRequest{method: operation.Method, path: operation.Path, headers: map[string]string{}, body: map[string]any{
		"protocol": h.contract.protocol, "kind": operation.RequestKind, "request_id": requestID,
		"authorization": state.seedAuthorization(h.t), "payload": payload,
	}}
}

// submitForKey re-issues the seed submit under another request key.
func (h *harness) submitForKey(state *scenarioState, requestID, requestKey string) wireRequest {
	h.t.Helper()
	request := h.requestFor(state.seed, state)
	request.body["request_id"] = requestID
	valueObject(h.t, request.body["payload"])["request_key"] = requestKey
	request.headers[h.contract.requestKeyHeader] = requestKey
	return request
}

func (h *harness) seed(state *scenarioState) wireResult {
	h.t.Helper()
	request := h.requestFor(state.seed, state)
	operation := h.contract.operation(h.t, "execution.submit")
	result := h.must(request)
	if result.status != operation.SuccessStatus || result.body["kind"] != operation.SuccessKind {
		h.t.Fatalf("seed submit = %d %s", result.status, result.raw)
	}
	state.remember(operation.RequestKind, request, valueString(h.t, request.body, "request_id"), result)
	state.executionID = nestedString(h.t, result.body, "payload", "execution", "execution_id")
	state.acceptedAt = nestedString(h.t, result.body, "payload", "execution", "accepted_at")
	h.settle(state)
	return result
}

func (h *harness) executionState(state *scenarioState) string {
	h.t.Helper()
	result := h.must(h.executionRequest(state, "execution.status", "contracttest-status", nil))
	if result.status != http.StatusOK {
		h.t.Fatalf("status poll = %d %s", result.status, result.raw)
	}
	return nestedString(h.t, result.body, "payload", "execution", "state")
}

func (h *harness) waitForState(state *scenarioState, accept func(string) bool, want string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		last = h.executionState(state)
		if accept(last) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("execution %s did not reach %s; last state %s", state.executionID, want, last)
}

func (h *harness) terminalStates() []string {
	return h.contract.enum(h.t, "$defs", "terminalState")
}

// settle waits until the scenario's execution is quiescent, so executor call
// counts compared around a later request are not racing the background run.
func (h *harness) settle(state *scenarioState) {
	h.t.Helper()
	if state.executionID == "" {
		return
	}
	if h.executor.block {
		select {
		case <-h.executor.started:
		case <-time.After(5 * time.Second):
			h.t.Fatal("blocking executor did not start")
		}
		return
	}
	terminal := h.terminalStates()
	h.waitForState(state, func(value string) bool { return slices.Contains(terminal, value) }, "a terminal state")
}

// match compares a fixture response message with an actual provider response.
// Constant fields must be equal, identifiers must be tokens, timestamps must
// parse, enums must be members; synthetic ids and illustrative pre-terminal
// states are not compared literally. With tolerateMissing the fixture keys
// the provider did not emit are returned instead of failing.
func (h *harness) match(message fixtureMessage, result wireResult, tolerateMissing bool) []string {
	h.t.Helper()
	kind := valueString(h.t, message.Body, "kind")
	if got := valueString(h.t, result.body, "protocol"); got != h.contract.protocol || got != message.Body["protocol"] {
		h.t.Fatalf("%s: response protocol = %q", message.Label, got)
	}
	if got := valueString(h.t, result.body, "kind"); got != kind {
		h.t.Fatalf("%s: response kind = %q, want %q: %s", message.Label, got, kind, result.raw)
	}
	if got := valueString(h.t, result.body, "request_id"); got != message.Body["request_id"] {
		h.t.Fatalf("%s: response request_id = %q, want %q", message.Label, got, message.Body["request_id"])
	}
	if kind == "problem" {
		if result.status < 400 {
			h.t.Fatalf("%s: problem status = %d", message.Label, result.status)
		}
	} else if operation := h.contract.bySuccessKind[kind]; result.status != operation.SuccessStatus {
		h.t.Fatalf("%s: status = %d, want %d: %s", message.Label, result.status, operation.SuccessStatus, result.raw)
	}
	missing := make([]string, 0)
	h.matchValue(message.Label, "payload", message.Body["payload"], result.body["payload"], tolerateMissing, &missing)
	return missing
}

var constantResponseFields = map[string]struct{}{
	"outcome": {}, "code": {}, "side_effects": {}, "reason_code": {}, "request_key": {},
	"retryable": {}, "idempotent": {}, "terminal": {},
}

var tokenResponseFields = map[string]struct{}{
	"execution_id": {}, "request_id": {}, "event_id": {}, "fence_id": {}, "id": {}, "version": {}, "kind": {},
}

func (h *harness) matchValue(label, path string, expected, actual any, tolerateMissing bool, missing *[]string) {
	h.t.Helper()
	key := path[strings.LastIndex(path, ".")+1:]
	switch typed := expected.(type) {
	case map[string]any:
		object, ok := actual.(map[string]any)
		if !ok {
			h.t.Fatalf("%s: %s is %T, want object", label, path, actual)
		}
		keys := make([]string, 0, len(typed))
		for name := range typed {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			value, present := object[name]
			if !present {
				if tolerateMissing {
					*missing = append(*missing, path+"."+name)
					continue
				}
				h.t.Fatalf("%s: response lacks %s.%s", label, path, name)
			}
			h.matchValue(label, path+"."+name, typed[name], value, tolerateMissing, missing)
		}
	case []any:
		list, ok := actual.([]any)
		if !ok || (len(typed) > 0 && len(list) == 0) {
			h.t.Fatalf("%s: %s is %#v, want non-empty array", label, path, actual)
		}
	case string:
		value, ok := actual.(string)
		if !ok {
			h.t.Fatalf("%s: %s is %T, want string", label, path, actual)
		}
		h.matchString(label, path, key, typed, value)
	case bool:
		value, ok := actual.(bool)
		if !ok {
			h.t.Fatalf("%s: %s is %T, want bool", label, path, actual)
		}
		if _, constant := constantResponseFields[key]; constant && value != typed {
			h.t.Fatalf("%s: %s = %v, want %v", label, path, value, typed)
		}
	case float64:
		value, ok := actual.(float64)
		if !ok {
			h.t.Fatalf("%s: %s is %T, want number", label, path, actual)
		}
		if key == "retry_after_ms" {
			minimum := h.contract.schemaNumber(h.t, "$defs", "lookupPayload", "oneOf", "2", "properties", "retry_after_ms", "minimum")
			maximum := h.contract.schemaNumber(h.t, "$defs", "lookupPayload", "oneOf", "2", "properties", "retry_after_ms", "maximum")
			if value < minimum || value > maximum {
				h.t.Fatalf("%s: retry_after_ms = %v outside [%v, %v]", label, value, minimum, maximum)
			}
		}
	default:
		h.t.Fatalf("%s: unsupported fixture value at %s: %T", label, path, expected)
	}
}

func (h *harness) matchString(label, path, key, expected, actual string) {
	h.t.Helper()
	terminal := h.terminalStates()
	switch {
	case key == "state":
		states := append(h.contract.enum(h.t, "$defs", "executionState"), h.contract.enum(h.t, "$defs", "cancelResult", "allOf", "1", "properties", "payload", "properties", "state")...)
		if !slices.Contains(states, actual) {
			h.t.Fatalf("%s: %s = %q is not a contract state", label, path, actual)
		}
		// Terminal and cancel-requested states are deterministic expectations
		// the scenario waits for; earlier states in a fixture are illustrative.
		if (slices.Contains(terminal, expected) || expected == "cancel_requested") && actual != expected {
			h.t.Fatalf("%s: %s = %q, want %q", label, path, actual, expected)
		}
	case key == "type":
		if !slices.Contains(h.contract.enum(h.t, "$defs", "executionEvent", "properties", "type"), actual) {
			h.t.Fatalf("%s: %s = %q is not a contract event type", label, path, actual)
		}
	case strings.HasSuffix(key, "_at"):
		if _, err := time.Parse(time.RFC3339Nano, actual); err != nil || !strings.HasSuffix(actual, "Z") {
			h.t.Fatalf("%s: %s = %q is not a UTC timestamp", label, path, actual)
		}
	default:
		if _, constant := constantResponseFields[key]; constant {
			if actual != expected {
				h.t.Fatalf("%s: %s = %q, want %q", label, path, actual, expected)
			}
			if key == "code" && !slices.Contains(h.contract.enum(h.t, "$defs", "problemPayload", "properties", "code"), actual) {
				h.t.Fatalf("%s: problem code %q is not a contract code", label, actual)
			}
		}
		if _, token := tokenResponseFields[key]; token && !h.contract.isToken(actual) {
			h.t.Fatalf("%s: %s = %q is not a contract token", label, path, actual)
		}
	}
}

func nestedValue(t *testing.T, value any, path ...string) any {
	t.Helper()
	current, ok := jsonPath(value, path...)
	if !ok {
		t.Fatalf("path %v missing in %#v", path, value)
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

func nestedNumber(t *testing.T, value any, path ...string) float64 {
	t.Helper()
	result, ok := nestedValue(t, value, path...).(float64)
	if !ok {
		t.Fatalf("path %v is not number", path)
	}
	return result
}

func nestedObject(t *testing.T, value any, path ...string) map[string]any {
	t.Helper()
	return valueObject(t, nestedValue(t, value, path...))
}

func nestedSlice(t *testing.T, value any, path ...string) []any {
	t.Helper()
	result, ok := nestedValue(t, value, path...).([]any)
	if !ok {
		t.Fatalf("path %v is not array", path)
	}
	return result
}

func hasKey(value any, path ...string) bool {
	_, ok := jsonPath(value, path...)
	return ok
}

// collectRefs gathers every object stored under the named key anywhere in
// the value, including elements of arrays stored under the plural key.
func collectRefs(value any, singular, plural string) []map[string]any {
	found := make([]map[string]any, 0)
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == singular {
					if object, ok := child.(map[string]any); ok {
						found = append(found, object)
					}
				}
				if key == plural {
					if list, ok := child.([]any); ok {
						for _, item := range list {
							if object, ok := item.(map[string]any); ok {
								found = append(found, object)
							}
						}
					}
				}
				walk(child)
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(value)
	return found
}
