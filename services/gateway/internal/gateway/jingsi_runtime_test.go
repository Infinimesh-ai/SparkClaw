package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiscope"
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
			if run.MessageContext.Source.Adapter != jingsiscope.AdapterID || run.MessageContext.Authorization.PrincipalID != "jingsi:space_demo:task_demo" {
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

func TestAdmissionRuleIsRecordedOnTheRunAndNeverWidensTheGrant(t *testing.T) {
	input := jingsiruntime.ExecutionInput{
		ExecutionID: "execution_admission",
		Authorization: jingsiruntime.Authorization{
			SpaceID: "space_demo", TaskID: "task_demo", Purpose: jingsiruntime.Purpose{Name: "task.execute"},
			Grant: jingsiruntime.OpaqueRef{ID: "grant:demo/1", Version: "v1"}, ToolScope: []string{"files.read"},
			DataScope: []string{"memory.context"}, NetworkScope: []string{}, ApprovalPolicy: "ask",
		},
		Budget: jingsiruntime.Budget{MaxRuntimeMS: 30000, MaxToolCalls: 3, MaxOutputBytes: 4096},
	}
	read := app.ToolDefinition{Name: "files.read", Directory: app.ToolDirectoryMetadata{Effects: []app.ToolEffect{app.ToolEffectWorkspaceRead}}}
	grant := jingSiGrant(input)
	persisted, err := jingsiscope.Parse(grant.Scopes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.DataScope, []string{"memory.context"}) || len(persisted.NetworkScope) != 0 {
		t.Fatalf("grant was widened: %#v", persisted)
	}
	if persisted.AllowsTool(read) {
		t.Fatal("ungranted workspace read was allowed")
	}
}

func TestJingSiGrantProjectionRoundTrips(t *testing.T) {
	input := jingsiruntime.ExecutionInput{
		ExecutionID: "execution_demo",
		Authorization: jingsiruntime.Authorization{
			SpaceID: "space_demo", TaskID: "task_demo", Purpose: jingsiruntime.Purpose{Name: "task.execute"},
			Grant: jingsiruntime.OpaqueRef{ID: "grant:demo/1", Version: "v1"}, ToolScope: []string{"files.read", "browser.read"},
			DataScope: []string{"memory.context"}, NetworkScope: []string{"external.read"}, ApprovalPolicy: "ask",
		},
		Budget: jingsiruntime.Budget{MaxRuntimeMS: 30000, MaxToolCalls: 3, MaxOutputBytes: 4096},
	}
	want := jingSiGrant(input)
	got, err := jingsiscope.Parse(want.Scopes())
	if err != nil {
		t.Fatalf("Parse(projection) error = %v", err)
	}
	want.Tools = []string{"browser.read", "files.read"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed grant = %#v, want %#v", got, want)
	}
	if got.MaxToolCalls != input.Budget.MaxToolCalls || got.ApprovalPolicy != input.Authorization.ApprovalPolicy ||
		got.GrantID != input.Authorization.Grant.ID || got.GrantVersion != input.Authorization.Grant.Version {
		t.Fatalf("projection lost authorization fields: %#v", got)
	}
}

func TestJingSiExecutorProjectsDeliveredAttachmentsAsOpaqueArtifactRefs(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	executor := jingSiAgentExecutor{runtime: runtime, repository: st}

	session, err := st.CreateSessionWithScope(t.Context(), "JingSi task task_demo", app.DefaultOwnerID, "", jingsiscope.AdapterID, true)
	if err != nil {
		t.Fatal(err)
	}
	executionID := "execution_artifacts"
	completed := time.Now().UTC()
	if _, err := st.SaveRun(t.Context(), app.AgentRun{
		ID: executionID, SessionID: session.ID, State: "completed", Summary: "The card was rendered.",
		StartedAt: completed.Add(-time.Second), CompletedAt: &completed,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(t.Context(), app.Message{
		ID: executionID + ":assistant", SessionID: session.ID, RunID: executionID, Role: "assistant",
		Content: "The card was rendered.", CreatedAt: completed,
		Attachments: []app.MessageAttachment{
			{ArtifactID: "obj_0123456789abcdef", Name: "weather.png", RelPath: "out/weather.png", URI: "workspace://out/weather.png", ContentType: "image/png", Bytes: 12},
			{ArtifactID: "obj_0123456789abcdef", Name: "weather.png", RelPath: "out/weather.png", URI: "workspace://out/weather.png", ContentType: "image/png", Bytes: 12},
			{ArtifactID: "obj_fedcba9876543210", Name: "notes.txt", RelPath: "out/notes.txt", URI: "workspace://out/notes.txt", ContentType: "text/plain", Bytes: 3},
			{Name: "unregistered.bin", RelPath: "out/unregistered.bin", ContentType: "application/octet-stream"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	input := jingsiruntime.ExecutionInput{
		ExecutionID: executionID,
		Authorization: jingsiruntime.Authorization{
			SpaceID: "space_demo", TaskID: "task_demo", Purpose: jingsiruntime.Purpose{Name: "task.execute"},
			Grant: jingsiruntime.OpaqueRef{ID: "grant_demo", Version: "v1"}, ApprovalPolicy: "deny",
			DeadlineAt: time.Now().UTC().Add(time.Minute),
		},
		Goal: "Render the weather card.", Budget: jingsiruntime.Budget{MaxRuntimeMS: 30000, MaxToolCalls: 0, MaxOutputBytes: 4096},
	}
	output, err := executor.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.State != "succeeded" || output.Summary != "The card was rendered." {
		t.Fatalf("existing run was not projected: %#v", output)
	}
	if len(output.ArtifactRefs) != 2 {
		t.Fatalf("artifact refs = %#v, want the two registered attachments once each", output.ArtifactRefs)
	}
	image, file := output.ArtifactRefs[0], output.ArtifactRefs[1]
	if image.Kind != "image" || image.MediaType != "image/png" || file.Kind != "file" || file.MediaType != "text/plain" {
		t.Fatalf("artifact kinds = %#v", output.ArtifactRefs)
	}
	for _, ref := range output.ArtifactRefs {
		if ref.Version != "v1" || !strings.HasPrefix(ref.ID, "artifact:") ||
			strings.Contains(ref.ID, "obj_") || strings.Contains(ref.ID, "weather") || strings.Contains(ref.ID, "notes") || strings.Contains(ref.ID, "/") {
			t.Fatalf("artifact ref leaks internal identity or is unversioned: %#v", ref)
		}
	}
	if image.ID == file.ID {
		t.Fatal("distinct artifacts share one reference")
	}
	again, err := executor.Execute(t.Context(), input)
	if err != nil || !reflect.DeepEqual(again.ArtifactRefs, output.ArtifactRefs) {
		t.Fatalf("replay changed artifact refs: %#v vs %#v (%v)", again.ArtifactRefs, output.ArtifactRefs, err)
	}
	other := input
	other.ExecutionID = "execution_other"
	if _, err := st.SaveRun(t.Context(), app.AgentRun{ID: other.ExecutionID, SessionID: session.ID, State: "completed", StartedAt: completed}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(t.Context(), app.Message{
		ID: other.ExecutionID + ":assistant", SessionID: session.ID, RunID: other.ExecutionID, Role: "assistant", Content: "again", CreatedAt: completed,
		Attachments: []app.MessageAttachment{{ArtifactID: "obj_0123456789abcdef", Name: "weather.png", RelPath: "out/weather.png", ContentType: "image/png"}},
	}); err != nil {
		t.Fatal(err)
	}
	otherOutput, err := executor.Execute(t.Context(), other)
	if err != nil || len(otherOutput.ArtifactRefs) != 1 || otherOutput.ArtifactRefs[0].ID == image.ID {
		t.Fatalf("artifact ref is not bound to the execution: %#v (%v)", otherOutput.ArtifactRefs, err)
	}
}
