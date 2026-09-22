package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestLocalSharedBackendDualClientQualification(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("SPARKCLAW_DUAL_CLIENT_POSTGRES_DSN"))
	lanHost := strings.TrimSpace(os.Getenv("SPARKCLAW_DUAL_CLIENT_LAN_HOST"))
	if dsn == "" || lanHost == "" {
		t.Skip("run scripts/qualify-local-shared-backend.sh for isolated PostgreSQL and LAN-client qualification")
	}
	curlImage := strings.TrimSpace(os.Getenv("SPARKCLAW_DUAL_CLIENT_CURL_IMAGE"))
	if curlImage == "" {
		curlImage = "curlimages/curl:latest"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repository, err := store.NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "dual-client-qualification"
	const ownerID = "owner-dual-client"
	const desktopToken = "desktop-qualification-token-0123456789abcdef"
	const webToken = "web-qualification-token-0123456789abcdef0123"
	if _, err := repository.SaveOwnerProfile(ctx, app.OwnerProfile{
		ID: ownerID, DisplayName: "Dual Client Owner", WorkspaceRoot: root, Preferences: map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	for _, client := range []app.Client{
		{ID: "client-qualification-desktop", OwnerID: ownerID, ActorID: ownerID, Name: "Desktop", TokenHash: hashSecret(desktopToken)},
		{ID: "client-qualification-web", OwnerID: ownerID, ActorID: ownerID, Name: "LAN Web", TokenHash: hashSecret(webToken)},
	} {
		if _, err := repository.RegisterClient(ctx, client); err != nil {
			t.Fatal(err)
		}
	}

	tools := toolhub.New(cfg, repository)
	defer tools.Close()
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	gateway := New(cfg, repository, tools, runtime)
	gateway.BindLifecycleContext(ctx)
	server := httptest.NewUnstartedServer(gateway.Handler())
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	desktopOrigin := fmt.Sprintf("http://127.0.0.1:%d", port)
	webOrigin := fmt.Sprintf("http://%s:%d", lanHost, port)

	desktopDo := func(method, path, body, contentType string) *http.Response {
		t.Helper()
		request, requestErr := http.NewRequest(method, desktopOrigin+path, strings.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+desktopToken)
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return response
	}
	webCurl := func(arguments ...string) []byte {
		t.Helper()
		args := append([]string{"run", "--rm", curlImage, "--fail-with-body", "--silent", "--show-error"}, arguments...)
		command := exec.CommandContext(ctx, "docker", args...)
		raw, commandErr := command.CombinedOutput()
		if commandErr != nil {
			t.Fatalf("LAN Web client failed: %v: %s", commandErr, raw)
		}
		return raw
	}
	webJSON := func(method, path, body string) []byte {
		arguments := []string{"-X", method, "-H", "Authorization: Bearer " + webToken}
		if body != "" {
			arguments = append(arguments, "-H", "Content-Type: application/json", "--data-binary", body)
		}
		return webCurl(append(arguments, webOrigin+path)...)
	}

	desktopIdentity := desktopDo(http.MethodGet, "/api/workbench/identity", "", "")
	assertQualificationIdentity(t, desktopIdentity.Body, desktopIdentity.StatusCode, "client-qualification-desktop")
	desktopIdentity.Body.Close()
	webIdentity := webJSON(http.MethodGet, "/api/workbench/identity", "")
	assertQualificationIdentity(t, io.NopCloser(bytes.NewReader(webIdentity)), http.StatusOK, "client-qualification-web")

	initialDesktop := desktopDo(http.MethodGet, "/api/sessions", "", "")
	assertQualificationSessions(t, initialDesktop.Body, initialDesktop.StatusCode, 0, "")
	initialDesktop.Body.Close()
	assertQualificationSessions(t, io.NopCloser(bytes.NewReader(webJSON(http.MethodGet, "/api/sessions", ""))), http.StatusOK, 0, "")

	createdResponse := desktopDo(http.MethodPost, "/api/sessions", `{"title":"Created by desktop"}`, "application/json")
	if createdResponse.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(createdResponse.Body)
		createdResponse.Body.Close()
		t.Fatalf("create session status=%d body=%s", createdResponse.StatusCode, raw)
	}
	var created app.Session
	if err := json.NewDecoder(createdResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	createdResponse.Body.Close()
	assertQualificationSessions(t, io.NopCloser(bytes.NewReader(webJSON(http.MethodGet, "/api/sessions", ""))), http.StatusOK, 1, "Created by desktop")

	streamRequest, _ := http.NewRequest(http.MethodGet, desktopOrigin+"/api/workbench/events/stream", nil)
	streamRequest.Header.Set("Authorization", "Bearer "+desktopToken)
	streamResponse, err := http.DefaultClient.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer streamResponse.Body.Close()
	streamReader := bufio.NewReader(streamResponse.Body)
	if initial := readSSETestBlock(t, streamReader, 2*time.Second); !strings.Contains(initial, `"reason":"resync"`) {
		t.Fatalf("initial SSE block=%q", initial)
	}
	webJSON(http.MethodPatch, "/api/sessions/"+url.PathEscape(created.ID), `{"title":"Renamed by LAN Web"}`)
	deadline := time.Now().Add(2 * time.Second)
	for {
		block := readSSETestBlock(t, streamReader, time.Until(deadline))
		if strings.Contains(block, `"category":"sessions"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("desktop did not receive the LAN Web invalidation within two seconds")
		}
	}
	updated := desktopDo(http.MethodGet, "/api/sessions/"+url.PathEscape(created.ID), "", "")
	var updatedSession app.Session
	if err := json.NewDecoder(updated.Body).Decode(&updatedSession); err != nil {
		t.Fatal(err)
	}
	updated.Body.Close()
	if updated.StatusCode != http.StatusOK || updatedSession.Title != "Renamed by LAN Web" {
		t.Fatalf("desktop did not read LAN Web update: status=%d session=%#v", updated.StatusCode, updatedSession)
	}

	const fileContents = "identical bytes from the shared backend\n"
	var uploadBody bytes.Buffer
	uploadWriter := multipart.NewWriter(&uploadBody)
	if err := uploadWriter.WriteField("session_id", created.ID); err != nil {
		t.Fatal(err)
	}
	part, err := uploadWriter.CreateFormFile("file", "shared.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, fileContents); err != nil {
		t.Fatal(err)
	}
	if err := uploadWriter.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest, _ := http.NewRequest(http.MethodPost, desktopOrigin+"/api/documents/upload", &uploadBody)
	uploadRequest.Header.Set("Authorization", "Bearer "+desktopToken)
	uploadRequest.Header.Set("Content-Type", uploadWriter.FormDataContentType())
	uploadResponse, err := http.DefaultClient.Do(uploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	var uploadResult struct {
		RelPath string `json:"rel_path"`
	}
	if err := json.NewDecoder(uploadResponse.Body).Decode(&uploadResult); err != nil {
		t.Fatal(err)
	}
	uploadResponse.Body.Close()
	if uploadResponse.StatusCode != http.StatusCreated {
		t.Fatalf("desktop upload status=%d", uploadResponse.StatusCode)
	}
	fileQuery := url.Values{"path": {filepath.ToSlash(uploadResult.RelPath)}, "session_id": {created.ID}}
	webFile := webCurl("-H", "Authorization: Bearer "+webToken, webOrigin+"/api/documents/file?"+fileQuery.Encode())
	if string(webFile) != fileContents {
		t.Fatalf("LAN Web downloaded different bytes: %q", webFile)
	}

	webJSON(http.MethodDelete, "/api/sessions/"+url.PathEscape(created.ID), "")
	finalDesktop := desktopDo(http.MethodGet, "/api/sessions", "", "")
	assertQualificationSessions(t, finalDesktop.Body, finalDesktop.StatusCode, 0, "")
	finalDesktop.Body.Close()

	forgedCommand := exec.CommandContext(ctx, "docker", append([]string{
		"run", "--rm", curlImage, "--silent", "--show-error",
		"-o", "/dev/null", "-w", "%{http_code}", "-X", http.MethodPost,
		"-H", "Host: 127.0.0.1:18790", "-H", "X-Forwarded-For: 127.0.0.1",
		"-H", "X-SparkClaw-Electron: true", "-H", "Idempotency-Key: forged-qualification",
		"-H", "Content-Type: application/json", "--data-binary", `{"client_name":"Forged"}`,
		webOrigin + "/api/clients",
	})...)
	forgedRaw, err := forgedCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("forged LAN request failed to complete: %v: %s", err, forgedRaw)
	}
	forgedStatus := strings.TrimSpace(string(forgedRaw))
	if forgedStatus != "401" && forgedStatus != "403" {
		t.Fatalf("forged LAN source obtained authority: status=%s", forgedStatus)
	}
}

func assertQualificationIdentity(t *testing.T, body io.ReadCloser, status int, clientID string) {
	t.Helper()
	var identity map[string]string
	if err := json.NewDecoder(body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || identity["deployment_id"] != "dual-client-qualification" ||
		identity["owner_id"] != "owner-dual-client" || identity["client_id"] != clientID {
		t.Fatalf("identity status=%d payload=%#v", status, identity)
	}
}

func assertQualificationSessions(t *testing.T, body io.ReadCloser, status, count int, title string) {
	t.Helper()
	var payload struct {
		Sessions []app.Session `json:"sessions"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || len(payload.Sessions) != count || (title != "" && payload.Sessions[0].Title != title) {
		t.Fatalf("sessions status=%d payload=%#v", status, payload.Sessions)
	}
}
