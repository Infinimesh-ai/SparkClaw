package gateway

import (
	"bytes"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3mail"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestR3MailHTTPInstallationSnapshotAndVerifiedAttachment(t *testing.T) {
	f := newEmailHTTPFixture(t)
	mail := f.receive("r3-http", time.Now())
	cfg := testConfig(f.root)
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "r3-mail-deployment"
	const token = "synthetic-mail-issued-client-token-long-enough"
	_, e := f.repo.RegisterClient(t.Context(), app.Client{ID: "r3-mail-client", OwnerID: f.owner, Name: "R3 Mail", TokenHash: hashSecret(token)})
	f.must(e)
	tools := toolhub.New(cfg, f.repo)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(f.repo, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	projection, e := emailmanagement.New(f.repo, f.browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: f.root})
	f.must(e)
	sync, e := r3mail.New(filepath.Join(f.root, "mail-sync"), r3mail.Repository{OwnerStatus: projection.ClientSyncOwnerStatus, Mailbox: projection.ClientSyncMailbox, Mailboxes: projection.ClientSyncMailboxes}, projection)
	f.must(e)
	instance := New(cfg, f.repo, tools, runtime, WithEmailManagement(projection), WithR3MailSync(sync), WithR3Executions(filepath.Join(f.root, "r3-control"), nil))
	instance.BindLifecycleContext(t.Context())
	const install = "11111111-1111-4111-8111-111111111111"
	request := func(method, path, body, installation string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-SparkClaw-Installation", installation)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		instance.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/r3/mail/mailboxes", "", install); w.Code != 403 {
		t.Fatalf("unregistered installation: %d", w.Code)
	}
	if w := request("POST", "/api/r3/installations", `{"schema_version":1,"installation_id":"`+install+`"}`, install); w.Code != 200 {
		t.Fatalf("install %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/r3/mail/mailboxes", "", install); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(f.box.Address)) {
		t.Fatalf("catalog %d %s", w.Code, w.Body.String())
	}
	endpoint := "/api/r3/mail/" + f.box.ID + "/sync"
	w := request("POST", endpoint, `{"cursor":"","limit":100}`, install)
	if w.Code != 200 {
		t.Fatalf("sync %d %s", w.Code, w.Body.String())
	}
	var snapshot r3mail.Response
	if e = json.Unmarshal(w.Body.Bytes(), &snapshot); e != nil {
		t.Fatal(e)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].ID != mail.ID || snapshot.Events[0].Mail.Attachments[0].SHA256 == "" {
		t.Fatalf("typed snapshot manifest %+v", snapshot)
	}
	if w := request("GET", endpoint, "", install); w.Code != 405 {
		t.Fatalf("GET mutated journal %d", w.Code)
	}
	for _, path := range []string{endpoint + "?cursor=x", endpoint} {
		body := `{"cursor":"","limit":100,"owner_id":"other"}`
		if path != endpoint {
			body = `{"cursor":"","limit":100}`
		}
		if w := request("POST", path, body, install); w.Code != 400 {
			t.Fatalf("invalid sync accepted %d", w.Code)
		}
	}
	file := "/api/r3/mail/" + f.box.ID + "/messages/" + mail.ID + "/attachments/part-1"
	if w := request("GET", file, "", ""); w.Code != 403 {
		t.Fatalf("missing install file access %d", w.Code)
	}
	if w := request("GET", file, "", install); w.Code != 200 || w.Body.String() != "Attachment evidence r3-http" {
		t.Fatalf("verified file %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", strings.Replace(file, f.box.ID, "wrong-mailbox", 1), "", install); w.Code != 404 {
		t.Fatalf("wrong mailbox file access %d", w.Code)
	}
	if f.browser.calls != 0 {
		t.Fatal("client sync activated mail collection browser")
	}
}
