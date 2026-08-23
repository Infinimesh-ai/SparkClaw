package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type fixedStoreRuntimeMonitor struct {
	status  store.RuntimeStatus
	metrics []store.OperationMetric
}

func (m fixedStoreRuntimeMonitor) Status() store.RuntimeStatus { return m.status }
func (m fixedStoreRuntimeMonitor) Metrics() []store.OperationMetric {
	return append([]store.OperationMetric(nil), m.metrics...)
}

func TestReadyzFailsClosedWithSafeStoreProjection(t *testing.T) {
	monitor := fixedStoreRuntimeMonitor{status: store.RuntimeStatus{
		Backend: store.BackendPostgres, State: store.RuntimeStateUnready,
		Durable: true, ReasonCode: string(store.StoreErrorUnknownOutcome),
	}}
	server := newStoreRuntimeTestServer(t, monitor)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		OK    bool                `json:"ok"`
		Store store.RuntimeStatus `json:"store"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OK || payload.Store.Ready || payload.Store.Backend != store.BackendPostgres ||
		payload.Store.ReasonCode != string(store.StoreErrorUnknownOutcome) {
		t.Fatalf("unexpected store readiness: %#v", payload)
	}
	for _, forbidden := range []string{"postgres://", "password", "query", "owner_id"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("readiness leaked %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestMetricsExposeOnlyBoundedStoreLabels(t *testing.T) {
	monitor := fixedStoreRuntimeMonitor{
		status: store.RuntimeStatus{Backend: store.BackendFile, State: store.RuntimeStateReady, Ready: true, Durable: true},
		metrics: []store.OperationMetric{{
			Operation: store.OperationRunSave, Repository: "RunRepository", Mode: "write",
			Outcome: string(store.StoreErrorUnknownOutcome), Count: 2, DurationSeconds: 1.25,
		}},
	}
	server := newStoreRuntimeTestServer(t, monitor)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("metrics status = %d body=%s", response.Code, raw)
	}
	body := response.Body.String()
	for _, required := range []string{
		`sparkclaw_store_ready{backend="file"} 1`,
		`repository="RunRepository"`,
		`operation="run.save"`,
		`outcome="unknown_outcome"`,
		`sparkclaw_store_operations_total`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("metrics missing %q:\n%s", required, body)
		}
	}
	for _, forbidden := range []string{"owner_id", "postgres://", "state.json"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics leaked unbounded label %q:\n%s", forbidden, body)
		}
	}
}

func newStoreRuntimeTestServer(t *testing.T, monitor StoreRuntimeMonitor) *Server {
	t.Helper()
	cfg := testConfig(t.TempDir())
	repository := store.NewMemoryStore()
	tools := toolhub.New(cfg, repository)
	t.Cleanup(func() { _ = tools.Close() })
	traces := trace.NewWriterFromConfig(cfg)
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), traces)
	return NewWithTrace(cfg, repository, tools, runtime, traces, WithStoreRuntime(monitor))
}
