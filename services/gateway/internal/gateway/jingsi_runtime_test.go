package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestJingSiRuntimeRouteUsesDedicatedAuthAndExecutesAgentRuntime(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.APIToken = "different-web-gateway-token"
	cfg.JingSiRuntime.Enabled = true
	cfg.JingSiRuntime.StateDir = filepath.Join(root, "runtime-v1")
	cfg.JingSiRuntime.BearerToken = "dedicated-runtime-secret"
	cfg.JingSiRuntime.MaxConcurrent = 1
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	provider, err := NewJingSiRuntimeProvider(cfg, runtime, st)
	if err != nil {
		t.Fatalf("NewJingSiRuntimeProvider() error = %v", err)
	}
	server := New(cfg, st, tools, runtime, WithJingSiRuntime(provider))
	lifecycle, stop := context.WithCancel(t.Context())
	server.BindLifecycleContext(lifecycle)
	defer func() {
		stop()
		waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.WaitForBackgroundWork(waitCtx); err != nil {
			t.Errorf("WaitForBackgroundWork() error = %v", err)
		}
	}()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	submit := jingsiruntime.SubmitRequest{
		Protocol: jingsiruntime.Protocol, Kind: "execution.submit.request", RequestID: "request_gateway_submit",
		Authorization: jingsiruntime.Authorization{
			SpaceID: "space_demo", TaskID: "task_demo", Purpose: jingsiruntime.Purpose{Name: "task.execute"},
			Grant: jingsiruntime.OpaqueRef{ID: "grant_demo", Version: "v1"}, ToolScope: []string{},
			DataScope: []string{}, NetworkScope: []string{}, ApprovalPolicy: "deny",
			DeadlineAt: time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond),
		},
		Payload: jingsiruntime.SubmitPayload{
			RequestKey: "task_demo:runtime-submit", Goal: "Answer with one short greeting.", Target: "sparkclaw",
			Budget: jingsiruntime.Budget{MaxRuntimeMS: 30000, MaxToolCalls: 0, MaxOutputBytes: 4096},
		},
	}
	response := gatewayRuntimeCall(t, httpServer.URL+"/v1/executions:submit", submit, "task_demo:runtime-submit")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d body=%s", response.StatusCode, response.Raw)
	}
	executionID := response.Payload["execution"].(map[string]any)["execution_id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if run, found, err := st.GetRun(t.Context(), executionID); err == nil && found && run.ID == executionID && run.MessageContext != nil {
			if run.MessageContext.Source.Adapter != "jingsi-runtime-v1" || run.MessageContext.Authorization.PrincipalID != "jingsi:space_demo:task_demo" {
				t.Fatalf("runtime lost authenticated ingress context: %#v", run.MessageContext)
			}
			if run.MessageContext.ReturnRoute.Mode != app.ReturnNowhere {
				t.Fatalf("runtime unexpectedly selected delivery route: %#v", run.MessageContext.ReturnRoute)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("accepted Runtime v1 execution never entered the existing Agent Runtime store")
}

type gatewayRuntimeResponse struct {
	StatusCode int
	Payload    map[string]any
	Raw        string
}

func gatewayRuntimeCall(t *testing.T, endpoint string, body any, idempotencyKey string) gatewayRuntimeResponse {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer dedicated-runtime-secret")
	request.Header.Set("Content-Type", jingsiruntime.MediaType)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(responseRaw, &decoded); err != nil {
		t.Fatalf("decode response: %v body=%s", err, responseRaw)
	}
	return gatewayRuntimeResponse{StatusCode: response.StatusCode, Payload: decoded.Payload, Raw: string(responseRaw)}
}
