package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestExecutionInstalledGETAfterRestartDoesNotStartOrMutateService(t *testing.T) {
	root := t.TempDir()
	controlRoot := filepath.Join(root, "execution")
	const install = "11111111-1111-4111-8111-111111111111"
	const token = "read-only-execution-test-client-token"
	e := execution.Envelope{SchemaVersion: 1, OwnerID: "owner", ClientID: "client", InstallationID: install, RequestID: testWorkbenchRequestID()}
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	st := store.NewMemoryStore()
	for _, client := range []app.Client{{ID: "client", OwnerID: "owner", TokenHash: hashSecret(token)}, {ID: "other-client", OwnerID: "other-owner", TokenHash: hashSecret("other-client-test-token")}} {
		client.Name = "reader"
		if _, err := st.RegisterClient(t.Context(), client); err != nil {
			t.Fatal(err)
		}
	}
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	server := New(cfg, st, tools, agent.Runtime{}, WithExecutions(controlRoot, nil))
	request := func(route, credential, installation string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", route, nil)
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-SparkClaw-Installation", installation)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	route := "/api/v1/executions/" + e.RequestID
	if w := request(route, token, install); w.Code != 403 {
		t.Fatalf("missing installation %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(controlRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("GET created control storage", err)
	}
	service, err := execution.New(controlRoot, func(context.Context, execution.Envelope, map[string][]byte) (execution.Output, error) {
		return execution.Output{Content: "ready", Files: map[string][]byte{"output.txt": []byte("verified content")}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Bind(e.OwnerID, e.ClientID, install); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(t.Context(), e, execution.Digest([]byte("fixed input"))); err != nil {
		t.Fatal(err)
	}
	service.Wait()
	status, err := service.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || status.Result == nil {
		t.Fatalf("result %+v %v", status, err)
	}
	var payload execution.Payload
	if err := json.Unmarshal([]byte(status.Result.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	service.Close()
	if err := os.WriteFile(filepath.Join(controlRoot, "content", "orphan.sealed"), []byte("GET must not prune"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		out := map[string]string{}
		err := filepath.WalkDir(controlRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			out[path] = info.Mode().String() + info.ModTime().String()
			if !entry.IsDir() {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				out[path] += execution.Digest(raw)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	if w := request(route, token, install); w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if w := request(route+"/files/"+payload.Files[0].ID, token, install); w.Code != 200 || w.Body.String() != "verified content" || w.Header().Get("X-Content-SHA256") != payload.Files[0].SHA256 {
		t.Fatalf("file %d %s", w.Code, w.Body.String())
	}
	for _, pair := range [][2]string{{"other-client-test-token", install}, {token, testWorkbenchRequestID()}} {
		if w := request(route, pair[0], pair[1]); w.Code != 403 {
			t.Fatalf("identity bypass %d %s", w.Code, w.Body.String())
		}
	}
	if server.executions != nil {
		t.Fatal("GET initialized writable execution service")
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("GET changed persisted ledger, key, lock or spool")
	}
}
