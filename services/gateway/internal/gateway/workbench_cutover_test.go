package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

func TestWorkbenchCutoverRejectsRetiredRoutesAndDigestWithoutExecuting(t *testing.T) {
	s, st := credentialServer(t)
	const token = "synthetic-cutover-device-credential"
	const install = "11111111-1111-4111-8111-111111111111"
	registerCredential(t, st, "cutover-client", token)
	s.BindLifecycleContext(t.Context())
	var calls atomic.Int32
	s.executionExecutor = func(context.Context, execution.Envelope, map[string][]byte) (execution.Output, error) {
		calls.Add(1)
		return execution.Output{Content: "fresh matched execution"}, nil
	}
	t.Cleanup(func() {
		if s.executions != nil {
			s.executions.Close()
		}
	})
	oldRoot := s.cfg.State.Path + ".r3"
	if err := os.MkdirAll(oldRoot, 0700); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(oldRoot, "control.json")
	oldBytes := []byte("deliberately invalid historical state: must never be opened or imported")
	if err := os.WriteFile(oldFile, oldBytes, 0600); err != nil {
		t.Fatal(err)
	}
	request := func(method, route, digestHeader string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, route, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-SparkClaw-Installation", install)
		r.Header.Set("Content-Type", "application/json")
		if digestHeader != "" {
			r.Header.Set(digestHeader, execution.Digest(body))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/r3/installations"}, {"POST", "/api/r3/executions"},
		{"GET", "/api/r3/executions/original"}, {"PUT", "/api/r3/inputs/original/files/file"},
		{"POST", "/api/r3/executions/original/ack"}, {"POST", "/api/r3/executions/original/cancel"},
		{"GET", "/api/r3/mail/mailboxes"}, {"POST", "/api/r3/mail/box/sync"},
		{"POST", "/api/r3/hosts/grants"}, {"GET", "/api/r3/hosts/connect"},
		{"GET", "/api/v1/hosts/connect"},
	} {
		if w := request(route.method, route.path, "X-R3-Digest", []byte(`{}`)); w.Code != 404 {
			t.Fatalf("retired route %s %s: %d", route.method, route.path, w.Code)
		}
	}
	if s.executions != nil {
		t.Fatal("retired route initialized execution state")
	}
	if w := request("POST", "/api/v1/installations", "", []byte(`{"schema_version":1,"installation_id":"`+install+`"}`)); w.Code != 200 {
		t.Fatalf("fresh binding: %d %s", w.Code, w.Body.String())
	}
	e := execution.Envelope{SchemaVersion: 1, DeploymentID: s.cfg.Gateway.DeploymentID, OwnerID: "owner", ClientID: "cutover-client", InstallationID: install,
		ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444", Messages: []execution.Message{{Role: "user", Content: "one execution"}}}
	raw, _ := json.Marshal(e)
	if w := request("POST", "/api/v1/executions", "X-R3-Digest", raw); w.Code != 400 {
		t.Fatalf("retired digest accepted: %d %s", w.Code, w.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatal("retired request executed")
	}
	if w := request("POST", "/api/v1/executions", "X-SparkClaw-Digest", raw); w.Code != 202 {
		t.Fatalf("matched submit: %d %s", w.Code, w.Body.String())
	}
	s.executions.Wait()
	if calls.Load() != 1 {
		t.Fatalf("executions=%d", calls.Load())
	}
	if _, err := os.Stat(filepath.Join(s.cfg.State.Path+".execution", "control.json")); err != nil {
		t.Fatal(err)
	}
	if old, err := os.ReadFile(oldFile); err != nil || !bytes.Equal(old, oldBytes) {
		t.Fatalf("historical state changed: %s %v", old, err)
	}
}
