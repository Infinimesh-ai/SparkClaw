package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestLANMailAttachmentTLSInstallationBoundaryAndDraftSend(t *testing.T) {
	s, repo, _, _ := workbenchISCPFixture(t, nil)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("gateway decoy"), 0600); err != nil {
		t.Fatal(err)
	}
	browser := &noEmailBrowser{}
	service, err := emailmanagement.New(repo, browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	s.emailManagement = service
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "iscp-owner", CommandKey: "lan-mail"}, Provider: app.EmailProviderGmail, Address: "owner@example.test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tls := httptest.NewTLSServer(s.Handler())
	defer tls.Close()
	plain := httptest.NewServer(s.Handler())
	defer plain.Close()
	const token = "synthetic-issued-client-credential-never-used-by-iscp"
	call := func(origin, method, route, installation, credential string, body []byte) (int, []byte) {
		t.Helper()
		r, _ := http.NewRequest(method, origin+route, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-SparkClaw-Installation", installation)
		r.Header.Set("X-SparkClaw-File-Name", url.PathEscape("report.txt"))
		r.Header.Set("X-SparkClaw-Digest", execution.Digest(body))
		r.Header.Set("X-SparkClaw-Transfer", "77777777-7777-4777-8777-"+execution.Digest(body)[:12])
		response, err := tls.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, raw
	}
	status, raw := call(tls.URL, "GET", "/api/email/compose-capabilities", iscpTestInstallation, token, nil)
	if status != 200 || bytes.Contains(raw, []byte(`"workspace_attachments":true`)) {
		t.Fatalf("unbound installation capability %d %s", status, raw)
	}
	// DesktopAuth requires this HTTP handshake before exposing connected state.
	status, raw = call(tls.URL, "POST", "/api/v1/installations", "", token, []byte(`{"schema_version":1,"installation_id":"`+iscpTestInstallation+`"}`))
	if status != 200 {
		t.Fatalf("LAN installation handshake %d %s", status, raw)
	}
	for _, denied := range []struct{ origin, installation, credential string }{
		{plain.URL, iscpTestInstallation, token}, {tls.URL, "", token}, {tls.URL, "22222222-2222-4222-8222-222222222222", token}, {tls.URL, iscpTestInstallation, "wrong-token"},
	} {
		status, raw := call(denied.origin, "GET", "/api/email/compose-capabilities", denied.installation, denied.credential, nil)
		if status == 200 && bytes.Contains(raw, []byte(`"workspace_attachments":true`)) {
			t.Fatalf("unqualified HTTP advertised attachments: %s", raw)
		}
		status, _ = call(denied.origin, "POST", "/api/v1/mail/attachments", denied.installation, denied.credential, []byte("outside"))
		if status != 401 && status != 403 {
			t.Fatalf("unqualified upload=%d", status)
		}
	}
	status, raw = call(tls.URL, "GET", "/api/email/compose-capabilities", iscpTestInstallation, token, nil)
	if status != 200 || !bytes.Contains(raw, []byte(`"workspace_attachments":true`)) {
		t.Fatalf("capability %d %s", status, raw)
	}
	content := []byte("bytes copied from desktop private local file")
	status, raw = call(tls.URL, "POST", "/api/v1/mail/attachments", iscpTestInstallation, token, content)
	var object app.EmailAttachmentObject
	if status != 200 || json.Unmarshal(raw, &object) != nil || object.Purpose != app.EmailSendAttachmentPurpose || object.SHA256 != execution.Digest(content) {
		t.Fatalf("upload %d %s", status, raw)
	}
	// Repeating transfer preparation does not extend its 24-hour deadline or
	// create another object, even when an upload response was lost.
	status, replay := call(tls.URL, "POST", "/api/v1/mail/attachments", iscpTestInstallation, token, content)
	if status != 200 || !bytes.Equal(raw, replay) {
		t.Fatalf("upload replay %d %s", status, replay)
	}
	request, _ := json.Marshal(map[string]any{"id": "lan-draft", "mailbox_id": box.ID, "to": []string{"recipient@example.test"}, "subject": "Reviewed local file", "body": "test", "attachments": []any{map[string]any{"local_file_id": "88888888-8888-4888-8888-888888888888", "object": object}}})
	status, raw = call(tls.URL, "POST", "/api/email/drafts", iscpTestInstallation, token, request)
	if status != 200 {
		t.Fatalf("save %d %s", status, raw)
	}
	status, raw = call(tls.URL, "GET", "/api/email/drafts?draft=lan-draft", iscpTestInstallation, token, nil)
	var draft store.EmailDraft
	if status != 200 || json.Unmarshal(raw, &draft) != nil || draft.ID != "lan-draft" || len(draft.Attachments) != 1 || draft.Attachments[0].Path != "" {
		t.Fatalf("snapshot %d %s", status, raw)
	}
	// The server's business resolver reads admitted object bytes, never the
	// same-named Gateway workspace source.
	stored, err := s.mailObjects.ReadAll(t.Context(), iscpobjects.Binding{DeploymentID: s.cfg.Gateway.DeploymentID, OwnerID: "iscp-owner", ClientID: "iscp-desktop-client", InstallationID: iscpTestInstallation, AuthorizationRevision: 1}, iscpworkbench.ObjectReference(object), app.EmailSendMaxAttachmentBytes)
	if err != nil || !bytes.Equal(stored, content) {
		t.Fatalf("desktop bytes changed: %v", err)
	}
	// Restarting object storage retains metadata and bytes without renewing TTL.
	s.mailObjectsMu.Lock()
	s.mailObjects = nil
	s.mailObjectsMu.Unlock()
	s.restoreLANMailObjects()
	status, raw = call(tls.URL, "GET", "/api/email/compose-capabilities", iscpTestInstallation, token, nil)
	if status != 200 || !bytes.Contains(raw, []byte(`"workspace_attachments":true`)) {
		t.Fatalf("restart capability %d %s", status, raw)
	}
	status, raw = call(tls.URL, "GET", "/api/v1/mail/attachments/"+object.ObjectID, iscpTestInstallation, token, nil)
	if status != 200 || !bytes.Contains(raw, []byte(object.ExpiresAt)) {
		t.Fatalf("restart object %d %s", status, raw)
	}
	status, raw = call(tls.URL, "POST", "/api/email/drafts/lan-draft/send", iscpTestInstallation, token, []byte(`{"expected_version":1,"idempotency_key":"one-confirmed-send"}`))
	if status != 200 || !bytes.Contains(raw, []byte(`"state":"sent"`)) || browser.calls != 2 {
		t.Fatalf("send %d %s effects=%d", status, raw, browser.calls)
	}
	if _, err := repo.RegisterClient(t.Context(), app.Client{ID: "other-client", OwnerID: "iscp-owner", Name: "other", TokenHash: hashSecret("another-device-credential")}); err != nil {
		t.Fatal(err)
	}
	if err := s.executions.Bind("iscp-owner", "other-client", iscpTestInstallation); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"GET", "DELETE"} {
		status, _ = call(tls.URL, verb, "/api/v1/mail/attachments/"+object.ObjectID, iscpTestInstallation, "another-device-credential", nil)
		if status != 409 {
			t.Fatalf("object crossed Client: %s %d", verb, status)
		}
	}
	status, _ = call(tls.URL, "DELETE", "/api/v1/mail/attachments/"+object.ObjectID, iscpTestInstallation, token, nil)
	if status != 200 {
		t.Fatal("release", status)
	}
	status, _ = call(tls.URL, "GET", "/api/v1/mail/attachments/"+object.ObjectID, iscpTestInstallation, token, nil)
	if status != 409 {
		t.Fatal("released object available", status)
	}
	maximum := bytes.Repeat([]byte("x"), int(app.EmailSendMaxAttachmentBytes))
	status, raw = call(tls.URL, "POST", "/api/v1/mail/attachments", iscpTestInstallation, token, maximum)
	if status != 200 {
		t.Fatalf("10 MiB upload %d %s", status, raw)
	}
	status, _ = call(tls.URL, "POST", "/api/v1/mail/attachments", iscpTestInstallation, token, append(maximum, 'x'))
	if status != 413 {
		t.Fatal("oversized upload", status)
	}
	if _, err := repo.RevokeClient(t.Context(), "iscp-desktop-client"); err != nil {
		t.Fatal(err)
	}
	status, _ = call(tls.URL, "POST", "/api/v1/mail/attachments", iscpTestInstallation, token, content)
	if status != 401 {
		t.Fatal("revoked upload", status)
	}
}
