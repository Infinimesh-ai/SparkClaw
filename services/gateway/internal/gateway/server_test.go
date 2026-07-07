package gateway

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestUploadDocumentSavesSingleFileArtifact(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("session_id", "session_upload"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "example.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("# Uploaded\nSparkClaw document upload.")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/documents/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload returned %d: %s", resp.StatusCode, raw)
	}
	var decoded struct {
		Artifact app.ArtifactObject `json:"artifact"`
		Path     string             `json:"path"`
		RelPath  string             `json:"rel_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Artifact.Kind != "document_upload" || decoded.Artifact.SessionID != "session_upload" {
		t.Fatalf("unexpected artifact: %#v", decoded.Artifact)
	}
	raw, err := os.ReadFile(decoded.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "SparkClaw document upload") {
		t.Fatalf("uploaded file content mismatch: %q", raw)
	}
	objects := st.ListArtifactObjects(10)
	if len(objects) == 0 || objects[0].Kind != "document_upload" {
		t.Fatalf("upload artifact not stored: %#v", objects)
	}
}

func TestUploadDocumentAddsExtensionFromContentType(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="pdf"`)
	partHeader.Set("Content-Type", "application/pdf")
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("%PDF-1.4\n% uploaded test pdf\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/documents/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload returned %d: %s", resp.StatusCode, raw)
	}
	var decoded struct {
		Artifact app.ArtifactObject `json:"artifact"`
		Path     string             `json:"path"`
		RelPath  string             `json:"rel_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(decoded.RelPath, ".pdf") {
		t.Fatalf("expected inferred .pdf extension, got %q", decoded.RelPath)
	}
	if decoded.Artifact.ContentType != "application/pdf" {
		t.Fatalf("unexpected content type: %q", decoded.Artifact.ContentType)
	}
	raw, err := os.ReadFile(decoded.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte("%PDF-1.4")) {
		t.Fatalf("uploaded bytes were not preserved: %q", raw)
	}
}

func TestUploadImageSavesUnderMedia(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	userRoot := filepath.Join(root, "users", "owner-a")
	session := st.CreateSessionWithScope("user upload", "owner-a", userRoot, "webchat", false)
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAFgwJ/lwN7WAAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("session_id", session.ID); err != nil {
		t.Fatal(err)
	}
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="sample.png"`)
	partHeader.Set("Content-Type", "application/octet-stream")
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(png); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/documents/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload returned %d: %s", resp.StatusCode, raw)
	}
	var decoded struct {
		Artifact app.ArtifactObject `json:"artifact"`
		Path     string             `json:"path"`
		RelPath  string             `json:"rel_path"`
		Media    map[string]any     `json:"media"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.ToSlash(decoded.RelPath), "media/") {
		t.Fatalf("image should be saved under media/: %#v", decoded)
	}
	if decoded.Artifact.Kind != "media_image_upload" {
		t.Fatalf("unexpected image artifact kind: %#v", decoded.Artifact)
	}
	if _, err := os.Stat(filepath.Join(userRoot, "media")); err != nil {
		t.Fatalf("media directory should be created automatically: %v", err)
	}
	if !strings.HasPrefix(decoded.Path, userRoot) {
		t.Fatalf("image should be saved under session workspace root: %#v", decoded)
	}
	if decoded.Media["width"] == nil || decoded.Media["height"] == nil {
		t.Fatalf("media metadata should include dimensions: %#v", decoded.Media)
	}
}

func TestAvailableDocumentsIncludesUploadsAndMedia(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	if err := os.MkdirAll(filepath.Join(root, "uploads", "20260702"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "media", "20260702"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "uploads", "20260702", "note.txt"), []byte("note"), 0o644); err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAFgwJ/lwN7WAAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "media", "20260702", "image.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/api/documents/available?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("available returned %d: %s", resp.StatusCode, raw)
	}
	var decoded struct {
		Documents []app.ArtifactObject `json:"documents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	kinds := map[string]string{}
	for _, object := range decoded.Documents {
		keys = append(keys, object.Key)
		kinds[object.Key] = object.Kind
	}
	if !slices.Contains(keys, "uploads/20260702/note.txt") || !slices.Contains(keys, "media/20260702/image.png") {
		t.Fatalf("available documents should include uploads and media, got %#v", keys)
	}
	if kinds["media/20260702/image.png"] != "media_image_upload" {
		t.Fatalf("media object should keep image kind: %#v", kinds)
	}
}

func TestApprovalExecutesPatchAfterApproval(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "example.txt"), []byte("alpha\nbeta\ngamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	traces := trace.NewWriterFromConfig(cfg)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), traces)
	server := NewWithTrace(cfg, st, tools, runtime, traces)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	sendTestMessage(t, ts.URL, sessionID, "apply patch\n```diff\n--- a/example.txt\n+++ b/example.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+bravo\n gamma\n```")

	approvals := getApprovals(t, ts.URL)
	if len(approvals) != 1 {
		t.Fatalf("expected one approval, got %d", len(approvals))
	}
	runID := approvals[0]["run_id"].(string)
	pendingRun, ok := st.GetRun(runID)
	if !ok {
		t.Fatalf("run %q missing before approval", runID)
	}
	if pendingRun.State != "approval_pending" || pendingRun.CompletedAt != nil {
		t.Fatalf("run should wait for approval before execution: %#v", pendingRun)
	}
	resp, err := http.Post(ts.URL+"/api/approvals/"+approvals[0]["id"].(string)+"/approve", "application/json", bytes.NewBufferString(`{"note":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve returned %d", resp.StatusCode)
	}
	var approved struct {
		ToolCall struct {
			Status string         `json:"status"`
			Result map[string]any `json:"result"`
		} `json:"tool_call"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	if approved.ToolCall.Status != "completed_after_approval" {
		t.Fatalf("unexpected tool call status: %q", approved.ToolCall.Status)
	}
	completedRun, ok := st.GetRun(runID)
	if !ok {
		t.Fatalf("run %q missing after approval", runID)
	}
	if completedRun.State != "completed" || completedRun.CompletedAt == nil {
		t.Fatalf("run should complete after approval resolution: %#v", completedRun)
	}
	manifestPath, _ := approved.ToolCall.Result["manifest_path"].(string)
	rollbackPath, _ := approved.ToolCall.Result["rollback_patch_path"].(string)
	if manifestPath == "" || rollbackPath == "" {
		t.Fatalf("patch result missing rollback metadata: %#v", approved.ToolCall.Result)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("patch manifest missing: %v", err)
	}
	rollbackRaw, err := os.ReadFile(rollbackPath)
	if err != nil || !strings.Contains(string(rollbackRaw), "-bravo") || !strings.Contains(string(rollbackRaw), "+beta") {
		t.Fatalf("rollback patch incomplete raw=%q err=%v", rollbackRaw, err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "example.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "alpha\nbravo\ngamma" {
		t.Fatalf("patch was not applied: %q", string(raw))
	}
	traceResp, err := http.Get(ts.URL + "/api/traces/" + runID)
	if err != nil {
		t.Fatal(err)
	}
	defer traceResp.Body.Close()
	var refreshed struct {
		Run struct {
			State       string     `json:"state"`
			CompletedAt *time.Time `json:"completed_at"`
		} `json:"run"`
		ToolCalls []struct {
			Tool   string `json:"tool"`
			Status string `json:"status"`
		} `json:"tool_calls"`
	}
	if err := json.NewDecoder(traceResp.Body).Decode(&refreshed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, call := range refreshed.ToolCalls {
		if call.Tool == "code.apply_patch" && call.Status == "completed_after_approval" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refreshed trace did not include completed patch call: %#v", refreshed.ToolCalls)
	}
	if refreshed.Run.State != "completed" || refreshed.Run.CompletedAt == nil {
		t.Fatalf("refreshed trace did not include completed run state: %#v", refreshed.Run)
	}
}

func TestRunCompletesOnlyAfterAllApprovalsResolve(t *testing.T) {
	st := store.NewMemoryStore()
	run := app.AgentRun{
		ID:        "run_multi_approval",
		SessionID: "sess_multi_approval",
		State:     "approval_pending",
		Risk:      app.RiskDangerous,
		StartedAt: time.Now().UTC(),
	}
	st.SaveRun(run)
	created := time.Now().UTC()
	st.SaveApproval(app.Approval{
		ID:        "ap_one",
		SessionID: run.SessionID,
		RunID:     run.ID,
		Tool:      "email.send",
		Risk:      app.RiskDangerous,
		Status:    "pending",
		Summary:   "Send email",
		CreatedAt: created,
	})
	st.SaveApproval(app.Approval{
		ID:        "ap_two",
		SessionID: run.SessionID,
		RunID:     run.ID,
		Tool:      "calendar.create",
		Risk:      app.RiskDangerous,
		Status:    "pending",
		Summary:   "Create calendar event",
		CreatedAt: created,
	})
	server := &Server{store: st}

	if _, err := st.ResolveApproval("ap_one", "approved", "ok"); err != nil {
		t.Fatal(err)
	}
	server.completeRunIfApprovalsResolved(run.ID)
	pendingRun, ok := st.GetRun(run.ID)
	if !ok {
		t.Fatalf("run %q missing", run.ID)
	}
	if pendingRun.State != "approval_pending" || pendingRun.CompletedAt != nil {
		t.Fatalf("run completed before all approvals resolved: %#v", pendingRun)
	}
	if _, err := st.ResolveApproval("ap_two", "rejected", "no"); err != nil {
		t.Fatal(err)
	}
	server.completeRunIfApprovalsResolved(run.ID)
	completedRun, ok := st.GetRun(run.ID)
	if !ok {
		t.Fatalf("run %q missing after approvals", run.ID)
	}
	if completedRun.State != "completed" || completedRun.CompletedAt == nil {
		t.Fatalf("run did not complete after all approvals resolved: %#v", completedRun)
	}
}

func TestMetricsEndpointReturnsRuntimeCounters(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	traces := trace.NewWriterFromConfig(cfg)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), traces)
	server := NewWithTrace(cfg, st, tools, runtime, traces)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	sendTestMessage(t, ts.URL, sessionID, "Remember that metrics should count memory candidates")

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics returned %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"sparkclaw_sessions_total 1",
		"sparkclaw_messages_total 2",
		"sparkclaw_agent_runs_total 1",
		"sparkclaw_model_calls_total 4",
		"sparkclaw_model_call_errors_total 0",
		"sparkclaw_gateway_rate_limit_rejections_total 0",
		"sparkclaw_memory_candidates_total 1",
		"sparkclaw_episode_summaries_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q in:\n%s", want, body)
		}
	}
}

func TestGatewayRateLimitRejectsExcessRequests(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.RateLimit.Enabled = true
	cfg.Gateway.RateLimit.RequestsPerMinute = 60
	cfg.Gateway.RateLimit.Burst = 1

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	first, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request returned %d", first.StatusCode)
	}
	second, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request returned %d", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatalf("rate-limited response missing Retry-After")
	}
	ready, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	ready.Body.Close()
	if ready.StatusCode != http.StatusOK {
		t.Fatalf("public readyz should bypass rate limit, got %d", ready.StatusCode)
	}
	metrics, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer metrics.Body.Close()
	raw, err := io.ReadAll(metrics.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "sparkclaw_gateway_rate_limit_rejections_total 1") {
		t.Fatalf("metrics missing rate-limit rejection count:\n%s", raw)
	}
}

func TestChatEndpointSupportsManualModelProfileWithoutTools(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/chat", "application/json", bytes.NewBufferString(`{"message":"hello from direct chat","profile":"deep"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat returned %d", resp.StatusCode)
	}
	var decoded struct {
		Message string `json:"message"`
		Model   struct {
			Lane    string `json:"lane"`
			Profile string `json:"profile"`
			Mock    bool   `json:"mock"`
		} `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Model.Lane != "deep" || decoded.Model.Profile != cfg.Model.Deep.Name || !decoded.Model.Mock || decoded.Message == "" {
		t.Fatalf("unexpected chat response: %#v", decoded)
	}
	if len(st.ListSessions()) != 0 || len(st.ListToolCalls("")) != 0 || len(st.ListApprovals("")) != 0 {
		t.Fatalf("direct chat should not mutate agent state")
	}
	calls := st.ListModelCalls("", "")
	if len(calls) != 1 || calls[0].Operation != "direct_chat" || calls[0].Lane != "deep" || calls[0].TotalTokens == 0 {
		t.Fatalf("direct chat model call not recorded: %#v", calls)
	}
}

func TestChatEndpointRejectsUnknownProfile(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/chat", "application/json", bytes.NewBufferString(`{"message":"hello","profile":"embedding"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown chat profile returned %d", resp.StatusCode)
	}
}

func TestSessionEventStreamEmitsSSE(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions/"+sessionID+"/events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream returned %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("stream content type = %q", got)
	}
	scanner := bufio.NewScanner(resp.Body)
	var eventName, dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
		}
		if eventName != "" && dataLine != "" {
			break
		}
	}
	if eventName != "session.created" {
		t.Fatalf("first SSE event = %q", eventName)
	}
	var event struct {
		SessionID string `json:"session_id"`
		Type      string `json:"type"`
	}
	if err := json.Unmarshal([]byte(dataLine), &event); err != nil {
		t.Fatal(err)
	}
	if event.SessionID != sessionID || event.Type != "session.created" {
		t.Fatalf("unexpected event payload: %#v", event)
	}
}

func TestEmptySessionListEndpointsReturnArrays(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	resp, err := http.Get(ts.URL + "/api/sessions/" + sessionID + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Messages == nil || len(decoded.Messages) != 0 {
		t.Fatalf("messages should be an empty array: %#v", decoded)
	}
}

func TestMemoryEditorUpdatesAndDeletesAcceptedMemory(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	session := st.CreateSession("Memory editor")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskDraft, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   session.ID,
		RunID:       run.ID,
		Kind:        "profile",
		Content:     "SparkClaw keeps the first memory",
		Sensitivity: "normal",
		Reason:      "test",
	})
	_, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}

	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	updateBody, _ := json.Marshal(map[string]string{
		"kind":    "procedural",
		"content": "SparkClaw keeps the edited memory",
	})
	updateResp, err := http.Post(ts.URL+"/api/memories/"+memory.ID+"/update", "application/json", bytes.NewReader(updateBody))
	if err != nil {
		t.Fatal(err)
	}
	defer updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("memory update returned %d", updateResp.StatusCode)
	}
	var updated app.Memory
	if err := json.NewDecoder(updateResp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Kind != "procedural" || updated.Content != "SparkClaw keeps the edited memory" {
		t.Fatalf("memory did not update: %#v", updated)
	}

	searchResp, err := http.Get(ts.URL + "/api/memories?query=edited%20memory")
	if err != nil {
		t.Fatal(err)
	}
	defer searchResp.Body.Close()
	var search struct {
		Memories []app.Memory `json:"memories"`
	}
	if err := json.NewDecoder(searchResp.Body).Decode(&search); err != nil {
		t.Fatal(err)
	}
	if len(search.Memories) != 1 || search.Memories[0].ID != memory.ID {
		t.Fatalf("updated memory was not searchable: %#v", search.Memories)
	}

	invalidResp, err := http.Post(ts.URL+"/api/memories/"+memory.ID+"/update", "application/json", bytes.NewBufferString(`{"kind":"profile","content":"   "}`))
	if err != nil {
		t.Fatal(err)
	}
	invalidResp.Body.Close()
	if invalidResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("blank memory content returned %d", invalidResp.StatusCode)
	}
	sensitiveResp, err := http.Post(ts.URL+"/api/memories/"+memory.ID+"/update", "application/json", bytes.NewBufferString(`{"kind":"profile","content":"api_key is sk-editor-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	sensitiveResp.Body.Close()
	if sensitiveResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("sensitive memory update returned %d", sensitiveResp.StatusCode)
	}

	deleteResp, err := http.Post(ts.URL+"/api/memories/"+memory.ID+"/delete", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("memory delete returned %d", deleteResp.StatusCode)
	}
	if matches := st.SearchMemories("edited memory"); len(matches) != 0 {
		t.Fatalf("deleted memory still searchable: %#v", matches)
	}
	missingResp, err := http.Post(ts.URL+"/api/memories/"+memory.ID+"/delete", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing memory delete returned %d", missingResp.StatusCode)
	}
}

func TestMemoryExportArchivesSnapshot(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	session := st.CreateSession("Memory export")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskDraft, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
	profile := st.GetOwnerProfile()
	profile.DisplayName = "Export Owner"
	st.UpdateOwnerProfile(profile)
	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   session.ID,
		RunID:       run.ID,
		Kind:        "profile",
		Content:     "SparkClaw keeps export snapshots",
		Sensitivity: "normal",
		Reason:      "test",
	})
	st.SaveEpisodeSummary(app.EpisodeSummary{
		SessionID: session.ID,
		RunID:     run.ID,
		Goal:      "Export memory",
		Outcome:   "Snapshot archived",
		Risk:      app.RiskDraft,
		ModelLane: "fast",
		Summary:   "Memory export test episode.",
		CreatedAt: time.Now().UTC(),
	})
	_, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := st.UpdateMemory(memory.ID, "procedural", "SparkClaw export keeps edited memory")
	if err != nil {
		t.Fatal(err)
	}

	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	exportResp, err := http.Get(ts.URL + "/api/memories/export")
	if err != nil {
		t.Fatal(err)
	}
	defer exportResp.Body.Close()
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("memory export returned %d", exportResp.StatusCode)
	}
	var snapshot app.MemoryExport
	if err := json.NewDecoder(exportResp.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.OwnerProfile.DisplayName != "Export Owner" {
		t.Fatalf("owner profile missing from export: %#v", snapshot.OwnerProfile)
	}
	if snapshot.Counts.Memories != 1 || snapshot.Counts.MemoryCandidates != 1 || snapshot.Counts.Episodes != 1 {
		t.Fatalf("memory export counts wrong: %#v", snapshot.Counts)
	}
	if len(snapshot.Memories) != 1 || snapshot.Memories[0].ID != updated.ID || snapshot.Memories[0].Content != updated.Content {
		t.Fatalf("memory export missing edited memory: %#v", snapshot.Memories)
	}

	archiveResp, err := http.Post(ts.URL+"/api/memories/export", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer archiveResp.Body.Close()
	if archiveResp.StatusCode != http.StatusCreated {
		t.Fatalf("memory export archive returned %d", archiveResp.StatusCode)
	}
	var archived struct {
		Export   app.MemoryExport   `json:"export"`
		Artifact app.ArtifactObject `json:"artifact"`
	}
	if err := json.NewDecoder(archiveResp.Body).Decode(&archived); err != nil {
		t.Fatal(err)
	}
	if archived.Artifact.Kind != "memory_export" || archived.Artifact.URI == "" || archived.Artifact.Bytes == 0 {
		t.Fatalf("memory export artifact incomplete: %#v", archived.Artifact)
	}
	if archived.Artifact.Path == "" {
		t.Fatalf("memory export artifact path missing: %#v", archived.Artifact)
	}
	raw, err := os.ReadFile(archived.Artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), updated.Content) {
		t.Fatalf("archived memory export missing edited memory: %s", string(raw))
	}
	objects := st.ListArtifactObjects(10)
	if !slices.ContainsFunc(objects, func(object app.ArtifactObject) bool {
		return object.Kind == "memory_export" && object.URI == archived.Artifact.URI
	}) {
		t.Fatalf("artifact catalog missing memory export: %#v", objects)
	}
}

func TestMemoryRetentionPrunesExpiredMemories(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Memory.RetentionDays = 7

	now := time.Now().UTC()
	session := app.Session{ID: "s_retention", Title: "Memory retention", CreatedAt: now, UpdatedAt: now}
	run := app.AgentRun{ID: "run_retention", SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: now}
	old := app.Memory{
		ID:        "mem_old_retention",
		Kind:      "profile",
		Content:   "SparkClaw old retention memory",
		SourceID:  run.ID,
		CreatedAt: now.AddDate(0, 0, -30),
	}
	fresh := app.Memory{
		ID:        "mem_fresh_retention",
		Kind:      "profile",
		Content:   "SparkClaw fresh retention memory",
		SourceID:  run.ID,
		CreatedAt: now,
	}
	statePath := filepath.Join(root, "state.json")
	snapshot := store.Snapshot{
		Sessions: map[string]app.Session{
			session.ID: session,
		},
		Runs: map[string]app.AgentRun{
			run.ID: run,
		},
		Memories: map[string]app.Memory{
			old.ID:   old,
			fresh.ID: fresh,
		},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}

	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/memories?query=retention")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("memory search returned %d", resp.StatusCode)
	}
	var decoded struct {
		Memories []app.Memory `json:"memories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Memories) != 1 || decoded.Memories[0].ID != fresh.ID {
		t.Fatalf("retention did not prune old memory: %#v", decoded.Memories)
	}
	if oldMatches := st.SearchMemories("old retention"); len(oldMatches) != 0 {
		t.Fatalf("old memory remained in store: %#v", oldMatches)
	}
	if !hasGatewayAuditType(st.ListAudit(session.ID), "memory.pruned") {
		t.Fatalf("retention prune was not audited: %#v", st.ListAudit(session.ID))
	}
}

func TestRunFeedbackPersistsAndRefreshesTrace(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	traces := trace.NewWriterFromConfig(cfg)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), traces)
	server := NewWithTrace(cfg, st, tools, runtime, traces)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	result := sendTestMessageResult(t, ts.URL, sessionID, "Search for SparkClaw")
	runID := result["run"].(map[string]any)["id"].(string)
	messageID := result["message"].(map[string]any)["id"].(string)
	body, _ := json.Marshal(map[string]string{
		"message_id": messageID,
		"rating":     "corrected",
		"correction": "Prefer citing local file evidence.",
	})
	resp, err := http.Post(ts.URL+"/api/runs/"+runID+"/feedback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feedback returned %d", resp.StatusCode)
	}
	var feedback app.RunFeedback
	if err := json.NewDecoder(resp.Body).Decode(&feedback); err != nil {
		t.Fatal(err)
	}
	if feedback.Rating != "corrected" || feedback.Correction == "" || feedback.MessageID != messageID {
		t.Fatalf("feedback response incomplete: %#v", feedback)
	}
	listResp, err := http.Get(ts.URL + "/api/runs/" + runID + "/feedback")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	var listed struct {
		Feedback []app.RunFeedback `json:"feedback"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Feedback) != 1 || listed.Feedback[0].ID != feedback.ID {
		t.Fatalf("feedback list did not include saved feedback: %#v", listed.Feedback)
	}
	traceResp, err := http.Get(ts.URL + "/api/traces/" + runID)
	if err != nil {
		t.Fatal(err)
	}
	defer traceResp.Body.Close()
	var refreshed trace.RunTrace
	if err := json.NewDecoder(traceResp.Body).Decode(&refreshed); err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Feedback) != 1 || refreshed.Feedback[0].Correction != feedback.Correction {
		t.Fatalf("trace did not include feedback: %#v", refreshed.Feedback)
	}
	if !hasGatewayAuditType(st.ListAudit(sessionID), "run_feedback.saved") {
		t.Fatalf("feedback audit event missing: %#v", st.ListAudit(sessionID))
	}
}

func TestManualToolInvokeRequiresApprovalForDangerousTool(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args":       map[string]any{"command": "echo should-not-run"},
	})
	resp, err := http.Post(ts.URL+"/api/tools/shell.exec_sandboxed/invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("invoke returned %d", resp.StatusCode)
	}
	approvals := getApprovals(t, ts.URL)
	if len(approvals) != 1 || approvals[0]["tool"] != "shell.exec_sandboxed" {
		t.Fatalf("expected pending shell approval, got %#v", approvals)
	}
	calls := st.ListToolCalls(sessionID)
	if len(calls) != 1 || calls[0].Status != "approval_pending" {
		t.Fatalf("expected approval-pending tool call, got %#v", calls)
	}
	verifier, ok := approvals[0]["arguments"].(map[string]any)["_verifier"].(map[string]any)
	if !ok {
		t.Fatalf("manual approval missing verifier decision: %#v", approvals[0])
	}
	if verifier["lane"] != "deep" || verifier["required_user_confirmation"] != true {
		t.Fatalf("manual verifier decision incomplete: %#v", verifier)
	}
	if !hasGatewayAuditType(st.ListAudit(sessionID), "verifier.deep_check") {
		t.Fatalf("manual verifier audit event missing: %#v", st.ListAudit(sessionID))
	}
}

func TestManualToolInvokeValidatesArgumentsBeforeApproval(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args": map[string]any{
			"to":      []any{"owner@example.test", 42},
			"subject": "SparkClaw checklist",
			"body":    "Deployment is ready.",
		},
	})
	resp, err := http.Post(ts.URL+"/api/tools/email.send/invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invoke returned %d", resp.StatusCode)
	}
	if approvals := getApprovals(t, ts.URL); len(approvals) != 0 {
		t.Fatalf("invalid arguments should not create approval: %#v", approvals)
	}
}

func TestManualNotifyApprovalCanBeConfirmed(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args": map[string]any{
			"summary": "Confirm deployment note",
			"reason":  "Owner should approve the message.",
		},
	})
	resp, err := http.Post(ts.URL+"/api/tools/notify.ask_approval/invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("notify invoke returned %d", resp.StatusCode)
	}
	approvals := getApprovals(t, ts.URL)
	if len(approvals) != 1 || approvals[0]["tool"] != "notify.ask_approval" {
		t.Fatalf("expected pending notify approval, got %#v", approvals)
	}
	approvedResp, err := http.Post(ts.URL+"/api/approvals/"+approvals[0]["id"].(string)+"/approve", "application/json", bytes.NewBufferString(`{"note":"confirmed"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer approvedResp.Body.Close()
	var approved struct {
		ToolCall struct {
			Status string         `json:"status"`
			Result map[string]any `json:"result"`
		} `json:"tool_call"`
	}
	if err := json.NewDecoder(approvedResp.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	if approved.ToolCall.Status != "completed_after_approval" || approved.ToolCall.Result["status"] != "approval_confirmed" {
		t.Fatalf("notify approval did not complete cleanly: %#v", approved)
	}
}

func TestChaosEvalRejectsPromptInjectionToolEscalation(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/evals/run", "application/json", bytes.NewBufferString(`{"profile":"chaos"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("chaos eval returned %d", resp.StatusCode)
	}
	var run struct {
		Status string `json:"status"`
		Cases  []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"cases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "passed" || len(run.Cases) != 2 {
		t.Fatalf("unexpected chaos eval result: %#v", run)
	}
	seen := map[string]string{}
	for _, evalCase := range run.Cases {
		seen[evalCase.Name] = evalCase.Status
	}
	if seen["prompt_injection_chaos"] != "passed" || seen["tool_repair_missing_knowledge_index"] != "passed" {
		t.Fatalf("unexpected chaos eval cases: %#v", run)
	}
}

func TestSmokeEvalChecksModelRouting(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/evals/run", "application/json", bytes.NewBufferString(`{"profile":"smoke"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("smoke eval returned %d", resp.StatusCode)
	}
	var run struct {
		Status string `json:"status"`
		Cases  []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"cases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "passed" {
		t.Fatalf("unexpected smoke eval result: %#v", run)
	}
	seen := map[string]string{}
	for _, evalCase := range run.Cases {
		seen[evalCase.Name] = evalCase.Status
	}
	if seen["model_routing"] != "passed" {
		t.Fatalf("smoke eval did not pass model_routing: %#v", run.Cases)
	}
	if seen["pairing_auth_boundary"] != "passed" {
		t.Fatalf("smoke eval did not pass pairing_auth_boundary: %#v", run.Cases)
	}
	if seen["schema_repair_missing_calendar_end"] != "passed" {
		t.Fatalf("smoke eval did not pass schema_repair_missing_calendar_end: %#v", run.Cases)
	}
}

func TestSmokeEvalDoesNotPruneExistingMemories(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	ownerSession := st.CreateSession("Owner Memory")
	ownerRun := app.AgentRun{
		ID:        "run_owner_memory",
		SessionID: ownerSession.ID,
		State:     "completed",
		ModelLane: "fast",
		Risk:      app.RiskRead,
		StartedAt: time.Now().UTC().AddDate(0, 0, -30),
	}
	st.SaveRun(ownerRun)
	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   ownerSession.ID,
		RunID:       ownerRun.ID,
		Kind:        "profile",
		Content:     "SparkClaw owner memory should survive smoke eval",
		Sensitivity: "normal",
		Status:      "pending",
		Reason:      "test setup",
	})
	if _, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted"); err != nil || memory == nil {
		t.Fatalf("setup memory failed memory=%#v err=%v", memory, err)
	}

	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/evals/run", "application/json", bytes.NewBufferString(`{"profile":"smoke"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("smoke eval returned %d", resp.StatusCode)
	}
	var run app.EvalRun
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "passed" {
		t.Fatalf("unexpected smoke eval result: %#v", run)
	}
	if memories := st.SearchMemories("owner memory should survive"); len(memories) != 1 {
		t.Fatalf("smoke eval pruned existing memory: %#v", memories)
	}
	if candidates := st.ListMemoryCandidates("pending"); len(candidates) != 0 {
		t.Fatalf("smoke eval left review candidates in main store: %#v", candidates)
	}
}

func TestFailedEvalArchivesFailureArtifact(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Workspaces.DefaultRoot = ""
	cfg.Workspaces.Allowlist = nil
	cfg.Logging.RedactPatterns = []string{"trace_secret"}
	cfg.Memory.RedactPatterns = nil

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/evals/run", "application/json", bytes.NewBufferString(`{"profile":"smoke"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("smoke eval returned %d", resp.StatusCode)
	}
	var run app.EvalRun
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" || len(run.FailureArchives) == 0 {
		t.Fatalf("failed eval did not include failure archive: %#v", run)
	}
	archive := run.FailureArchives[0]
	if archive.URI == "" || archive.Path == "" || archive.CaseName == "" {
		t.Fatalf("archive metadata incomplete: %#v", archive)
	}
	raw, err := os.ReadFile(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"eval_id"`)) || !bytes.Contains(raw, []byte(archive.CaseName)) {
		t.Fatalf("archive file missing failure context: %s", raw)
	}
	if fetched, ok := st.GetEvalRun(run.ID); !ok || len(fetched.FailureArchives) != len(run.FailureArchives) {
		t.Fatalf("persisted eval did not retain archives: %#v ok=%v", fetched, ok)
	}

	listResp, err := http.Get(ts.URL + "/api/evals")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("eval list returned %d", listResp.StatusCode)
	}
	var listed struct {
		EvalRuns []app.EvalRun `json:"eval_runs"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.EvalRuns) != 1 || listed.EvalRuns[0].ID != run.ID || len(listed.EvalRuns[0].FailureArchives) == 0 {
		t.Fatalf("eval list did not include archived run: %#v", listed)
	}
	artifactResp, err := http.Get(ts.URL + "/api/artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer artifactResp.Body.Close()
	var artifacts struct {
		Artifacts []app.ArtifactObject `json:"artifacts"`
	}
	if err := json.NewDecoder(artifactResp.Body).Decode(&artifacts); err != nil {
		t.Fatal(err)
	}
	if len(artifacts.Artifacts) == 0 || artifacts.Artifacts[0].Kind != "eval_failure" || artifacts.Artifacts[0].EvalID != run.ID {
		t.Fatalf("artifact list did not include eval failure archive: %#v", artifacts.Artifacts)
	}
}

func TestApprovedPersonalSideEffectsWriteMockRecords(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args": map[string]any{
			"to":      []string{"owner@example.test"},
			"subject": "SparkClaw checklist",
			"body":    "Deployment is ready.",
		},
	})
	resp, err := http.Post(ts.URL+"/api/tools/email.send/invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("email send invoke returned %d", resp.StatusCode)
	}
	approvals := getApprovals(t, ts.URL)
	if len(approvals) != 1 || approvals[0]["tool"] != "email.send" {
		t.Fatalf("expected pending email approval, got %#v", approvals)
	}
	if _, err := os.Stat(filepath.Join(root, ".sparkclaw", "mock", "email_outbox.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("email was written before approval err=%v", err)
	}
	modifiedResp, err := http.Post(ts.URL+"/api/approvals/"+approvals[0]["id"].(string)+"/modify", "application/json", bytes.NewBufferString(`{"note":"tighten copy","args":{"subject":"SparkClaw checklist updated","body":"Deployment is ready after review."}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer modifiedResp.Body.Close()
	if modifiedResp.StatusCode != http.StatusOK {
		t.Fatalf("email modify returned %d", modifiedResp.StatusCode)
	}
	var modified struct {
		Approval struct {
			Status    string         `json:"status"`
			Arguments map[string]any `json:"arguments"`
		} `json:"approval"`
		ToolCall struct {
			Status    string         `json:"status"`
			Arguments map[string]any `json:"arguments"`
		} `json:"tool_call"`
	}
	if err := json.NewDecoder(modifiedResp.Body).Decode(&modified); err != nil {
		t.Fatal(err)
	}
	if modified.Approval.Status != "pending" || modified.ToolCall.Status != "approval_pending" {
		t.Fatalf("modify resolved approval unexpectedly: %#v", modified)
	}
	if modified.Approval.Arguments["subject"] != "SparkClaw checklist updated" || modified.ToolCall.Arguments["body"] != "Deployment is ready after review." {
		t.Fatalf("modify did not update pending arguments: %#v", modified)
	}
	invalidModifyResp, err := http.Post(ts.URL+"/api/approvals/"+approvals[0]["id"].(string)+"/modify", "application/json", bytes.NewBufferString(`{"args":{"to":["owner@example.test",42]}}`))
	if err != nil {
		t.Fatal(err)
	}
	invalidModifyResp.Body.Close()
	if invalidModifyResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid email modify returned %d", invalidModifyResp.StatusCode)
	}
	approvedResp, err := http.Post(ts.URL+"/api/approvals/"+approvals[0]["id"].(string)+"/approve", "application/json", bytes.NewBufferString(`{"note":"approved"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer approvedResp.Body.Close()
	if approvedResp.StatusCode != http.StatusOK {
		t.Fatalf("email approval returned %d", approvedResp.StatusCode)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".sparkclaw", "mock", "email_outbox.jsonl"))
	if err != nil || !strings.Contains(string(raw), "SparkClaw checklist updated") || strings.Contains(string(raw), "Deployment is ready.\"") {
		t.Fatalf("email mock record missing raw=%q err=%v", raw, err)
	}

	calendarBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args": map[string]any{
			"title": "SparkClaw Demo",
			"start": "2026-05-23T10:00:00Z",
			"end":   "2026-05-23T10:30:00Z",
		},
	})
	calendarResp, err := http.Post(ts.URL+"/api/tools/calendar.create/invoke", "application/json", bytes.NewReader(calendarBody))
	if err != nil {
		t.Fatal(err)
	}
	calendarResp.Body.Close()
	if calendarResp.StatusCode != http.StatusAccepted {
		t.Fatalf("calendar create invoke returned %d", calendarResp.StatusCode)
	}
	approvals = getApprovals(t, ts.URL)
	if len(approvals) != 1 || approvals[0]["tool"] != "calendar.create" {
		t.Fatalf("expected pending calendar approval, got %#v", approvals)
	}
	calendarApproved, err := http.Post(ts.URL+"/api/approvals/"+approvals[0]["id"].(string)+"/approve", "application/json", bytes.NewBufferString(`{"note":"approved"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer calendarApproved.Body.Close()
	if calendarApproved.StatusCode != http.StatusOK {
		t.Fatalf("calendar approval returned %d", calendarApproved.StatusCode)
	}
	raw, err = os.ReadFile(filepath.Join(root, ".sparkclaw", "mock", "calendar_created_events.jsonl"))
	if err != nil || !strings.Contains(string(raw), "SparkClaw Demo") {
		t.Fatalf("calendar mock record missing raw=%q err=%v", raw, err)
	}
	artifacts := st.ListArtifactObjects(10)
	if !hasArtifactKind(artifacts, "tool_observation") {
		t.Fatalf("approved side effects did not archive observations: %#v", artifacts)
	}
}

func TestFileDeleteRequiresApprovalAndMovesToTrash(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "delete-me.txt")
	if err := os.WriteFile(target, []byte("delete me after approval"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args": map[string]any{
			"path":   "delete-me.txt",
			"reason": "golden cleanup",
		},
	})
	resp, err := http.Post(ts.URL+"/api/tools/file.delete/invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("file.delete invoke returned %d", resp.StatusCode)
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "delete me after approval" {
		t.Fatalf("file moved before approval raw=%q err=%v", raw, err)
	}
	approvals := getApprovals(t, ts.URL)
	if len(approvals) != 1 || approvals[0]["tool"] != "file.delete" {
		t.Fatalf("expected pending file.delete approval, got %#v", approvals)
	}
	approvedResp, err := http.Post(ts.URL+"/api/approvals/"+approvals[0]["id"].(string)+"/approve", "application/json", bytes.NewBufferString(`{"note":"approved"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer approvedResp.Body.Close()
	if approvedResp.StatusCode != http.StatusOK {
		t.Fatalf("file.delete approval returned %d", approvedResp.StatusCode)
	}
	var approved struct {
		ToolCall struct {
			Status string         `json:"status"`
			Result map[string]any `json:"result"`
		} `json:"tool_call"`
	}
	if err := json.NewDecoder(approvedResp.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	if approved.ToolCall.Status != "completed_after_approval" || approved.ToolCall.Result["status"] != "moved_to_trash" {
		t.Fatalf("unexpected approved file.delete result: %#v", approved.ToolCall)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("source file should be moved after approval err=%v", err)
	}
	trashPath, _ := approved.ToolCall.Result["trash_path"].(string)
	raw, err := os.ReadFile(trashPath)
	if err != nil || string(raw) != "delete me after approval" {
		t.Fatalf("trash file missing raw=%q err=%v", raw, err)
	}
	manifestPath, _ := approved.ToolCall.Result["manifest_path"].(string)
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil || !strings.Contains(string(manifestRaw), "golden cleanup") {
		t.Fatalf("delete manifest missing raw=%q err=%v", manifestRaw, err)
	}
}

func TestSensitiveMemoryRequiresApprovalBeforePersisting(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	traces := trace.NewWriterFromConfig(cfg)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), traces)
	server := NewWithTrace(cfg, st, tools, runtime, traces)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args": map[string]any{
			"content": "Deployment api_key is sk-approved-sensitive-test",
			"kind":    "credential_note",
			"reason":  "Owner explicitly approved retaining this sensitive note.",
		},
	})
	resp, err := http.Post(ts.URL+"/api/tools/memory.write_sensitive/invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("memory.write_sensitive invoke returned %d", resp.StatusCode)
	}
	var queued struct {
		ToolCall app.ToolCall `json:"tool_call"`
		Approval app.Approval `json:"approval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&queued); err != nil {
		t.Fatal(err)
	}
	if queued.ToolCall.Status != "approval_pending" || queued.Approval.Tool != "memory.write_sensitive" {
		t.Fatalf("unexpected queued sensitive memory approval: %#v", queued)
	}
	if memories := st.SearchMemories("sk-approved-sensitive-test"); len(memories) != 0 {
		t.Fatalf("sensitive memory persisted before approval: %#v", memories)
	}
	pendingRun, ok := st.GetRun(queued.ToolCall.RunID)
	if !ok || pendingRun.State != "approval_pending" || pendingRun.CompletedAt != nil {
		t.Fatalf("manual sensitive memory run should be approval pending: %#v", pendingRun)
	}
	traceResp, err := http.Get(ts.URL + "/api/traces/" + queued.ToolCall.RunID)
	if err != nil {
		t.Fatal(err)
	}
	traceResp.Body.Close()
	if traceResp.StatusCode != http.StatusOK {
		t.Fatalf("manual pending approval trace returned %d", traceResp.StatusCode)
	}

	approvedResp, err := http.Post(ts.URL+"/api/approvals/"+queued.Approval.ID+"/approve", "application/json", bytes.NewBufferString(`{"note":"approved sensitive memory"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer approvedResp.Body.Close()
	if approvedResp.StatusCode != http.StatusOK {
		t.Fatalf("sensitive memory approval returned %d", approvedResp.StatusCode)
	}
	var approved struct {
		ToolCall app.ToolCall `json:"tool_call"`
	}
	if err := json.NewDecoder(approvedResp.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	if approved.ToolCall.Status != "completed_after_approval" {
		t.Fatalf("sensitive memory did not complete after approval: %#v", approved.ToolCall)
	}
	memories := st.SearchMemories("sk-approved-sensitive-test")
	if len(memories) != 1 || memories[0].Kind != "credential_note" || memories[0].SourceID != queued.ToolCall.RunID {
		t.Fatalf("approved sensitive memory not persisted: %#v", memories)
	}
	completedRun, ok := st.GetRun(queued.ToolCall.RunID)
	if !ok || completedRun.State != "completed" || completedRun.CompletedAt == nil {
		t.Fatalf("manual sensitive memory run did not complete: %#v", completedRun)
	}
	refreshedTraceResp, err := http.Get(ts.URL + "/api/traces/" + queued.ToolCall.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer refreshedTraceResp.Body.Close()
	var refreshed struct {
		Run struct {
			State string `json:"state"`
		} `json:"run"`
		ToolCalls []app.ToolCall `json:"tool_calls"`
	}
	if err := json.NewDecoder(refreshedTraceResp.Body).Decode(&refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.Run.State != "completed" || len(refreshed.ToolCalls) != 1 || refreshed.ToolCalls[0].Status != "completed_after_approval" {
		t.Fatalf("refreshed sensitive memory trace incomplete: %#v", refreshed)
	}
}

func TestAPITokenProtectsAPIRoutes(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.APIToken = "secret-token"
	cfg.State.EncryptAtRest = true
	cfg.State.EncryptionKey = "state-secret"
	cfg.Tools.Web.Search.Enabled = true
	cfg.Tools.Web.Search.Provider = "parallel-free"
	cfg.Plugins.Entries.Parallel.Config.WebSearch.APIKey = "par-super-secret"
	cfg.Plugins.Entries.Parallel.Config.WebSearch.BaseURL = "https://search.parallel.ai/mcp"
	cfg.Plugins.Entries.Parallel.Config.WebSearch.MaxResults = 7

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	health, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("healthz returned %d", health.StatusCode)
	}

	unauthorized, err := http.Get(ts.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("api route without token returned %d", unauthorized.StatusCode)
	}

	unauthorizedChat, err := http.Post(ts.URL+"/chat", "application/json", bytes.NewBufferString(`{"message":"hello","profile":"fast"}`))
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedChat.Body.Close()
	if unauthorizedChat.StatusCode != http.StatusUnauthorized {
		t.Fatalf("chat route without token returned %d", unauthorizedChat.StatusCode)
	}

	chatReq, err := http.NewRequest(http.MethodPost, ts.URL+"/chat", bytes.NewBufferString(`{"message":"hello","profile":"fast"}`))
	if err != nil {
		t.Fatal(err)
	}
	chatReq.Header.Set("Content-Type", "application/json")
	chatReq.Header.Set("Authorization", "Bearer secret-token")
	authorizedChat, err := http.DefaultClient.Do(chatReq)
	if err != nil {
		t.Fatal(err)
	}
	authorizedChat.Body.Close()
	if authorizedChat.StatusCode != http.StatusOK {
		t.Fatalf("chat route with token returned %d", authorizedChat.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	authorized, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer authorized.Body.Close()
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("api route with token returned %d", authorized.StatusCode)
	}
	var decoded struct {
		Gateway struct {
			APIToken string `json:"api_token"`
		} `json:"gateway"`
		Model struct {
			HTTPTimeoutSeconds int  `json:"http_timeout_seconds"`
			DisableThinking    bool `json:"disable_thinking"`
			Fast               struct {
				Name          string `json:"name"`
				ContextTokens int    `json:"context_tokens"`
				MaxTokens     int    `json:"max_tokens"`
			} `json:"fast"`
		} `json:"model"`
		State struct {
			DSN               string `json:"dsn"`
			EncryptAtRest     bool   `json:"encrypt_at_rest"`
			EncryptionKey     string `json:"encryption_key"`
			EncryptionKeyFile string `json:"encryption_key_file"`
		} `json:"state"`
		Tools struct {
			Web struct {
				Search struct {
					Enabled          bool   `json:"enabled"`
					Provider         string `json:"provider"`
					BaseURL          string `json:"base_url"`
					APIKeyConfigured bool   `json:"api_key_configured"`
					APIKey           string `json:"api_key"`
					MaxResults       int    `json:"max_results"`
				} `json:"search"`
			} `json:"web"`
		} `json:"tools"`
		ToolPolicy struct {
			PolicyPath                      string         `json:"policy_path"`
			DefinitionCount                 int            `json:"definition_count"`
			RiskCounts                      map[string]int `json:"risk_counts"`
			DefinitionApprovalRequiredTools []string       `json:"definition_approval_required_tools"`
			DeniedTools                     []string       `json:"denied_tools"`
		} `json:"tool_policy"`
	}
	if err := json.NewDecoder(authorized.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Gateway.APIToken != "" {
		t.Fatal("api token was exposed in /api/config")
	}
	if decoded.Model.Fast.Name == "" || decoded.Model.Fast.ContextTokens == 0 || decoded.Model.Fast.MaxTokens == 0 || decoded.Model.HTTPTimeoutSeconds == 0 {
		t.Fatalf("model profile summary missing: %#v", decoded.Model.Fast)
	}
	if decoded.State.DSN != "" {
		t.Fatalf("state dsn should be redacted/empty for non-postgres config: %#v", decoded.State)
	}
	if !decoded.State.EncryptAtRest || decoded.State.EncryptionKey != "configured" || decoded.State.EncryptionKeyFile != "missing" {
		t.Fatalf("state encryption status was not exposed safely: %#v", decoded.State)
	}
	if !decoded.Tools.Web.Search.Enabled || decoded.Tools.Web.Search.Provider != "parallel-free" || decoded.Tools.Web.Search.MaxResults != 7 || !decoded.Tools.Web.Search.APIKeyConfigured {
		t.Fatalf("web search safe summary missing: %#v", decoded.Tools.Web.Search)
	}
	if decoded.Tools.Web.Search.APIKey != "" {
		t.Fatalf("parallel api key was exposed: %#v", decoded.Tools.Web.Search)
	}
	if decoded.ToolPolicy.PolicyPath == "" || decoded.ToolPolicy.DefinitionCount == 0 || decoded.ToolPolicy.RiskCounts["dangerous"] == 0 {
		t.Fatalf("tool policy summary missing: %#v", decoded.ToolPolicy)
	}
	if len(decoded.ToolPolicy.DefinitionApprovalRequiredTools) == 0 || len(decoded.ToolPolicy.DeniedTools) == 0 {
		t.Fatalf("tool policy lists missing: %#v", decoded.ToolPolicy)
	}
}

func TestToolPolicyEditorPersistsAndUpdatesRuntimePolicy(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Security.ToolPolicyPath = filepath.Join(root, "tools.policy.json")
	cfg.Security.DeniedTools = []string{}
	cfg.Security.ApprovalRequiredTools = []string{}

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	updateResp, err := http.Post(ts.URL+"/api/tool-policy", "application/json", bytes.NewBufferString(`{"deny":["files.write_draft"],"approval_required":["files.read"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("policy update returned %d", updateResp.StatusCode)
	}
	var updated struct {
		DeniedTools                     []string `json:"denied_tools"`
		ConfiguredApprovalRequiredTools []string `json:"configured_approval_required_tools"`
	}
	if err := json.NewDecoder(updateResp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(updated.DeniedTools, "files.write_draft") || !slices.Contains(updated.ConfiguredApprovalRequiredTools, "files.read") {
		t.Fatalf("policy update response missing tools: %#v", updated)
	}
	raw, err := os.ReadFile(cfg.Security.ToolPolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "files.write_draft") || !strings.Contains(string(raw), "files.read") {
		t.Fatalf("policy file did not persist update: %s", raw)
	}

	sessionID := createTestSession(t, ts.URL)
	readBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args":       map[string]any{"path": "missing.txt", "max_bytes": 100},
	})
	readResp, err := http.Post(ts.URL+"/api/tools/files.read/invoke", "application/json", bytes.NewReader(readBody))
	if err != nil {
		t.Fatal(err)
	}
	readResp.Body.Close()
	if readResp.StatusCode != http.StatusAccepted {
		t.Fatalf("files.read should require approval after policy update, got %d", readResp.StatusCode)
	}
	writeBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"args":       map[string]any{"path": "draft.md", "content": "blocked"},
	})
	writeResp, err := http.Post(ts.URL+"/api/tools/files.write_draft/invoke", "application/json", bytes.NewReader(writeBody))
	if err != nil {
		t.Fatal(err)
	}
	writeResp.Body.Close()
	if writeResp.StatusCode != http.StatusForbidden {
		t.Fatalf("files.write_draft should be denied after policy update, got %d", writeResp.StatusCode)
	}
	agentReadResult := sendTestMessageResult(t, ts.URL, sessionID, "Read missing.txt")
	toolCalls, ok := agentReadResult["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("agent read result missing tool call: %#v", agentReadResult)
	}
	agentReadCall := toolCalls[0].(map[string]any)
	if agentReadCall["tool"] != "files.read" || agentReadCall["status"] != "approval_pending" {
		t.Fatalf("agent files.read should require approval after policy update: %#v", agentReadCall)
	}

	futureToolsResp, err := http.Post(ts.URL+"/api/tool-policy", "application/json", bytes.NewBufferString(`{"deny":["future.tool"],"approval_required":["future.approval"]}`))
	if err != nil {
		t.Fatal(err)
	}
	futureToolsResp.Body.Close()
	if futureToolsResp.StatusCode != http.StatusOK {
		t.Fatalf("future tool policy update returned %d", futureToolsResp.StatusCode)
	}

	invalidResp, err := http.Post(ts.URL+"/api/tool-policy", "application/json", bytes.NewBufferString(`{"deny":[],"approval_required":["bad tool"]}`))
	if err != nil {
		t.Fatal(err)
	}
	invalidResp.Body.Close()
	if invalidResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid tool policy update returned %d", invalidResp.StatusCode)
	}
}

func TestOwnerProfileEndpointUpdatesProfile(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	getResp, err := http.Get(ts.URL + "/api/owner")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("owner get returned %d", getResp.StatusCode)
	}
	var initial app.OwnerProfile
	if err := json.NewDecoder(getResp.Body).Decode(&initial); err != nil {
		t.Fatal(err)
	}
	if initial.ID != app.DefaultOwnerID || initial.DisplayName == "" {
		t.Fatalf("default owner profile missing: %#v", initial)
	}

	body := `{"display_name":"Local Owner","email":"owner@example.test","preferences":{"tone":"brief","timezone":"Asia/Shanghai"}}`
	updateResp, err := http.Post(ts.URL+"/api/owner", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("owner update returned %d", updateResp.StatusCode)
	}
	var updated app.OwnerProfile
	if err := json.NewDecoder(updateResp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.ID != app.DefaultOwnerID || updated.DisplayName != "Local Owner" || updated.Email != "owner@example.test" || updated.Preferences["timezone"] != "Asia/Shanghai" {
		t.Fatalf("owner profile update mismatch: %#v", updated)
	}
	if !hasGatewayAuditType(st.ListAudit(""), "owner_profile.updated") {
		t.Fatalf("owner update was not audited")
	}

	badResp, err := http.Post(ts.URL+"/api/owner", "application/json", bytes.NewBufferString(`{"display_name":"Local Owner","email":"bad","preferences":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid owner email returned %d", badResp.StatusCode)
	}
}

func TestProfilesEndpointAndSessionOwnerIsolation(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	wxRoot := filepath.Join(root, "users", "wx_owner")
	st.SaveOwnerProfile(app.OwnerProfile{
		ID:               "wx_owner",
		Source:           "weixin",
		ExternalRef:      "bind:user",
		WorkspaceRoot:    wxRoot,
		DefaultChannel:   "weixin",
		DefaultBindingID: "bind",
		DisplayName:      "微信用户",
		Preferences:      map[string]string{},
	})
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	createResp, err := http.Post(ts.URL+"/api/sessions", "application/json", bytes.NewBufferString(`{"title":"wx session","owner_id":"wx_owner"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create session returned %d", createResp.StatusCode)
	}
	var created app.Session
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.OwnerID != "wx_owner" || created.WorkspaceRoot != wxRoot {
		t.Fatalf("session did not inherit profile scope: %#v", created)
	}

	defaultList, err := http.Get(ts.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer defaultList.Body.Close()
	var defaultPayload struct {
		Sessions []app.Session `json:"sessions"`
	}
	if err := json.NewDecoder(defaultList.Body).Decode(&defaultPayload); err != nil {
		t.Fatal(err)
	}
	if len(defaultPayload.Sessions) != 0 {
		t.Fatalf("default owner list should not include weixin session: %#v", defaultPayload.Sessions)
	}

	wxList, err := http.Get(ts.URL + "/api/sessions?owner_id=wx_owner")
	if err != nil {
		t.Fatal(err)
	}
	defer wxList.Body.Close()
	var wxPayload struct {
		Sessions []app.Session `json:"sessions"`
	}
	if err := json.NewDecoder(wxList.Body).Decode(&wxPayload); err != nil {
		t.Fatal(err)
	}
	if len(wxPayload.Sessions) != 1 || wxPayload.Sessions[0].ID != created.ID {
		t.Fatalf("weixin owner list missing scoped session: %#v", wxPayload.Sessions)
	}

	profilesResp, err := http.Get(ts.URL + "/api/profiles")
	if err != nil {
		t.Fatal(err)
	}
	defer profilesResp.Body.Close()
	var profilesPayload struct {
		Profiles []app.OwnerProfile `json:"profiles"`
	}
	if err := json.NewDecoder(profilesResp.Body).Decode(&profilesPayload); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(profilesPayload.Profiles, func(profile app.OwnerProfile) bool {
		return profile.ID == "wx_owner" && profile.ExternalRef == "bind:user"
	}) {
		t.Fatalf("profiles list missing weixin profile: %#v", profilesPayload.Profiles)
	}
}

func TestPairingIssuesClientToken(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	unauthorized, err := http.Get(ts.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired request returned %d", unauthorized.StatusCode)
	}

	started, err := http.Post(ts.URL+"/api/pairing/start", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer started.Body.Close()
	if started.StatusCode != http.StatusCreated {
		t.Fatalf("pairing start returned %d", started.StatusCode)
	}
	var start struct {
		PairingID string `json:"pairing_id"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(started.Body).Decode(&start); err != nil {
		t.Fatal(err)
	}
	if start.PairingID == "" || start.Code == "" {
		t.Fatalf("pairing start did not return id/code: %#v", start)
	}

	claimBody, _ := json.Marshal(map[string]string{
		"pairing_id":  start.PairingID,
		"code":        start.Code,
		"client_name": "webchat",
	})
	claimed, err := http.Post(ts.URL+"/api/pairing/claim", "application/json", bytes.NewReader(claimBody))
	if err != nil {
		t.Fatal(err)
	}
	defer claimed.Body.Close()
	if claimed.StatusCode != http.StatusCreated {
		t.Fatalf("pairing claim returned %d", claimed.StatusCode)
	}
	var claim struct {
		Token  string `json:"token"`
		Client struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"client"`
	}
	if err := json.NewDecoder(claimed.Body).Decode(&claim); err != nil {
		t.Fatal(err)
	}
	if claim.Token == "" || claim.Client.ID == "" || claim.Client.Name != "webchat" {
		t.Fatalf("pairing claim did not return token/client: %#v", claim)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+claim.Token)
	authorized, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	authorized.Body.Close()
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("paired token request returned %d", authorized.StatusCode)
	}
	clientsReq, err := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	if err != nil {
		t.Fatal(err)
	}
	clientsReq.Header.Set("Authorization", "Bearer "+claim.Token)
	clientsResp, err := http.DefaultClient.Do(clientsReq)
	if err != nil {
		t.Fatal(err)
	}
	defer clientsResp.Body.Close()
	var clients struct {
		Clients []app.Client `json:"clients"`
	}
	if err := json.NewDecoder(clientsResp.Body).Decode(&clients); err != nil {
		t.Fatal(err)
	}
	if len(clients.Clients) != 1 || clients.Clients[0].ID != claim.Client.ID {
		t.Fatalf("paired client did not list: %#v", clients.Clients)
	}
	revokeReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/clients/"+claim.Client.ID+"/revoke", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	revokeReq.Header.Set("Authorization", "Bearer "+claim.Token)
	revokeResp, err := http.DefaultClient.Do(revokeReq)
	if err != nil {
		t.Fatal(err)
	}
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("revoke client returned %d", revokeResp.StatusCode)
	}
	afterRevokeReq, err := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	afterRevokeReq.Header.Set("Authorization", "Bearer "+claim.Token)
	afterRevoke, err := http.DefaultClient.Do(afterRevokeReq)
	if err != nil {
		t.Fatal(err)
	}
	afterRevoke.Body.Close()
	if afterRevoke.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token request returned %d", afterRevoke.StatusCode)
	}

	reused, err := http.Post(ts.URL+"/api/pairing/claim", "application/json", bytes.NewReader(claimBody))
	if err != nil {
		t.Fatal(err)
	}
	reused.Body.Close()
	if reused.StatusCode != http.StatusBadRequest {
		t.Fatalf("reused pairing code returned %d", reused.StatusCode)
	}
}

func TestNotificationBindingStartPollAndRevoke(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:   true,
		Provider:  "openclaw-weixin-compatible",
		BaseURL:   "http://127.0.0.1:1",
		Token:     "secret-token",
		Recipient: "wx-user-123456",
	}
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	startResp, err := http.Post(ts.URL+"/api/notification-bindings/weixin/start", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusCreated {
		t.Fatalf("start binding returned %d", startResp.StatusCode)
	}
	var started map[string]any
	if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	id := started["id"].(string)
	if started["status"] != "waiting_scan" || started["qr_code_url"] == "" {
		t.Fatalf("unexpected start response: %#v", started)
	}

	pollResp, err := http.Get(ts.URL + "/api/notification-bindings/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer pollResp.Body.Close()
	var polled map[string]any
	if err := json.NewDecoder(pollResp.Body).Decode(&polled); err != nil {
		t.Fatal(err)
	}
	if polled["status"] != "active" {
		t.Fatalf("expected active binding after poll, got %#v", polled)
	}
	if polled["credential_ref"] != "configured" {
		t.Fatalf("credential ref should be redacted/configured: %#v", polled)
	}
	if strings.Contains(fmt.Sprint(polled["external_user_id"]), "123456") {
		t.Fatalf("external user id should be redacted: %#v", polled)
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/notification-bindings/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	revokeResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer revokeResp.Body.Close()
	var revoked map[string]any
	if err := json.NewDecoder(revokeResp.Body).Decode(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked["status"] != "revoked" {
		t.Fatalf("expected revoked binding, got %#v", revoked)
	}
}

func TestNotificationBindingSecondActiveDoesNotStealDefault(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:   true,
		Provider:  "openclaw-weixin-compatible",
		BaseURL:   "http://127.0.0.1:1",
		Token:     "secret-token",
		Recipient: "wx-default-recipient",
	}
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	startAndPoll := func() map[string]any {
		t.Helper()
		startResp, err := http.Post(ts.URL+"/api/notification-bindings/weixin/start", "application/json", bytes.NewBufferString(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		defer startResp.Body.Close()
		if startResp.StatusCode != http.StatusCreated {
			t.Fatalf("start binding returned %d", startResp.StatusCode)
		}
		var started map[string]any
		if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
			t.Fatal(err)
		}
		id := started["id"].(string)
		pollResp, err := http.Get(ts.URL + "/api/notification-bindings/" + id)
		if err != nil {
			t.Fatal(err)
		}
		defer pollResp.Body.Close()
		var polled map[string]any
		if err := json.NewDecoder(pollResp.Body).Decode(&polled); err != nil {
			t.Fatal(err)
		}
		if polled["status"] != "active" {
			t.Fatalf("expected active binding after poll, got %#v", polled)
		}
		return polled
	}

	first := startAndPoll()
	second := startAndPoll()
	if first["default_for_channel"] != true {
		t.Fatalf("first active binding should become default when no default exists: %#v", first)
	}
	if second["default_for_channel"] == true {
		t.Fatalf("second active binding should not steal default without explicit request: %#v", second)
	}
	bindings := st.ListNotificationBindings("weixin", "active")
	defaults := 0
	for _, binding := range bindings {
		if binding.DefaultForChannel {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly one default binding, got %d in %#v", defaults, bindings)
	}
}

func TestTraceEndpointReturnsRunTrace(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	traces := trace.NewWriterFromConfig(cfg)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), traces)
	server := NewWithTrace(cfg, st, tools, runtime, traces)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	sessionID := createTestSession(t, ts.URL)
	sendTestMessage(t, ts.URL, sessionID, "Search for missing-token")
	runs := st.ListRuns(sessionID)
	if len(runs) == 0 {
		t.Fatal("run was not saved")
	}
	resp, err := http.Get(ts.URL + "/api/traces/" + runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trace returned %d", resp.StatusCode)
	}
	var decoded struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
		ModelCalls []app.ModelCall  `json:"model_calls"`
		ToolCalls  []map[string]any `json:"tool_calls"`
		Episode    struct {
			RunID string   `json:"run_id"`
			Tools []string `json:"tools"`
		} `json:"episode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Run.ID != runs[0].ID {
		t.Fatalf("trace run id = %q, want %q", decoded.Run.ID, runs[0].ID)
	}
	if len(decoded.ToolCalls) == 0 {
		t.Fatal("trace did not include tool calls")
	}
	if !hasServerTestModelCall(decoded.ModelCalls, "task_hint", "fast") ||
		!hasServerTestModelCall(decoded.ModelCalls, "react_step_1", "fast") ||
		!hasServerTestModelCall(decoded.ModelCalls, "guard", "guard") {
		t.Fatalf("trace did not include model call telemetry: %#v", decoded.ModelCalls)
	}
	listResp, err := http.Get(ts.URL + "/api/traces")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("trace list returned %d", listResp.StatusCode)
	}
	var traceList struct {
		Traces []app.TraceMetadata `json:"traces"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&traceList); err != nil {
		t.Fatal(err)
	}
	if len(traceList.Traces) == 0 || traceList.Traces[0].RunID != runs[0].ID {
		t.Fatalf("trace list did not include run metadata: %#v", traceList.Traces)
	}
	if traceList.Traces[0].ToolCallCount == 0 || traceList.Traces[0].ModelCallCount == 0 || traceList.Traces[0].ArtifactURI == "" {
		t.Fatalf("trace metadata missing diagnostic fields: %#v", traceList.Traces[0])
	}
	artifactResp, err := http.Get(ts.URL + "/api/artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer artifactResp.Body.Close()
	if artifactResp.StatusCode != http.StatusOK {
		t.Fatalf("artifact list returned %d", artifactResp.StatusCode)
	}
	var artifactList struct {
		Artifacts []app.ArtifactObject `json:"artifacts"`
	}
	if err := json.NewDecoder(artifactResp.Body).Decode(&artifactList); err != nil {
		t.Fatal(err)
	}
	if len(artifactList.Artifacts) == 0 || artifactList.Artifacts[0].Kind != "trace" || artifactList.Artifacts[0].RunID != runs[0].ID {
		t.Fatalf("artifact list missing trace object: %#v", artifactList.Artifacts)
	}
	if !hasArtifactKind(artifactList.Artifacts, "tool_observation") {
		t.Fatalf("artifact list missing tool observation object: %#v", artifactList.Artifacts)
	}
	modelResp, err := http.Get(ts.URL + "/api/sessions/" + sessionID + "/model-calls?run_id=" + runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer modelResp.Body.Close()
	var modelDecoded struct {
		ModelCalls []app.ModelCall `json:"model_calls"`
	}
	if err := json.NewDecoder(modelResp.Body).Decode(&modelDecoded); err != nil {
		t.Fatal(err)
	}
	if len(modelDecoded.ModelCalls) < 2 || !hasServerTestModelCall(modelDecoded.ModelCalls, "guard", "guard") {
		t.Fatalf("model call API returned unexpected payload: %#v", modelDecoded.ModelCalls)
	}
	if decoded.Episode.RunID != runs[0].ID || len(decoded.Episode.Tools) == 0 {
		t.Fatalf("trace did not include episode summary: %#v", decoded.Episode)
	}
	episodesResp, err := http.Get(ts.URL + "/api/sessions/" + sessionID + "/episodes")
	if err != nil {
		t.Fatal(err)
	}
	defer episodesResp.Body.Close()
	var episodesDecoded struct {
		Episodes []appEpisode `json:"episodes"`
	}
	if err := json.NewDecoder(episodesResp.Body).Decode(&episodesDecoded); err != nil {
		t.Fatal(err)
	}
	if len(episodesDecoded.Episodes) != 1 || episodesDecoded.Episodes[0].RunID != runs[0].ID {
		t.Fatalf("episode API returned unexpected payload: %#v", episodesDecoded.Episodes)
	}
}

func hasArtifactKind(objects []app.ArtifactObject, kind string) bool {
	for _, object := range objects {
		if object.Kind == kind {
			return true
		}
	}
	return false
}

func hasServerTestModelCall(calls []app.ModelCall, operation, lane string) bool {
	for _, call := range calls {
		if call.Operation == operation && call.Lane == lane && call.TotalTokens > 0 {
			return true
		}
	}
	return false
}

type appEpisode struct {
	RunID string `json:"run_id"`
}

func TestSkillsEndpointListsLocalSkills(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	skillPath := filepath.Join(skillsDir, "local_files", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte(`---
name: local_files
description: Search and read local workspace files.
risk_level: low
allowed_tools: ["files.search", "files.read"]
activation:
  keywords: ["file", "workspace"]
---

Use read-only file tools first.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.Skills.Dirs = []string{skillsDir}

	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded struct {
		Skills []struct {
			Name         string   `json:"name"`
			AllowedTools []string `json:"allowed_tools"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Skills) != 1 || decoded.Skills[0].Name != "local_files" || decoded.Skills[0].AllowedTools[0] != "files.search" {
		t.Fatalf("unexpected skills response: %#v", decoded.Skills)
	}
}

func createTestSession(t *testing.T, baseURL string) string {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/sessions", "application/json", bytes.NewBufferString(`{"title":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded["id"].(string)
}

func sendTestMessage(t *testing.T, baseURL, sessionID, content string) {
	t.Helper()
	_ = sendTestMessageResult(t, baseURL, sessionID, content)
}

func sendTestMessageResult(t *testing.T, baseURL, sessionID, content string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	resp, err := http.Post(baseURL+"/api/sessions/"+sessionID+"/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("message returned %d", resp.StatusCode)
	}
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func getApprovals(t *testing.T, baseURL string) []map[string]any {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/approvals?status=pending")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded struct {
		Approvals []map[string]any `json:"approvals"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Approvals
}

func hasGatewayAuditType(events []app.AuditEvent, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func testConfig(root string) config.Config {
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = filepath.Join(root, ".sparkclaw", "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	return cfg
}
