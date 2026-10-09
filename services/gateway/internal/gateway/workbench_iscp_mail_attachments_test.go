package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestISCPDomainMailAttachmentsRequireWorkspaceReadScope(t *testing.T) {
	server, repo, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("reviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveOwnerProfile(t.Context(), app.OwnerProfile{ID: "iscp-owner", WorkspaceRoot: root}); err != nil {
		t.Fatal(err)
	}
	browser := &noEmailBrowser{}
	service, err := emailmanagement.New(repo, browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	server.emailManagement = service
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "iscp-owner", CommandKey: "attachment"}, Provider: app.EmailProviderGmail, Address: "owner@example.test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &iscpDomainAdapter{server: server, config: cfg}
	raw, _ := json.Marshal(map[string]any{"id": "reviewed-file", "mailbox_id": box.ID, "to": []string{"recipient@example.test"}, "subject": "s", "body": "b", "attachments": []any{map[string]any{"path": "report.txt"}}})
	request := domainTestRequest(iscpworkbench.OperationMailDraftsSave, raw)
	mailOnly := iscpworkbench.SessionInfo{Scopes: []string{"mail.drafts.write", "mail.send", "mail.read"}}
	if result := adapter.mailWithSession(domainTestContext(t), request, mailOnly); result.status != 403 {
		t.Fatalf("mail-only saved %d %s", result.status, result.body)
	}
	withFiles := mailOnly
	withFiles.Scopes = append(append([]string{}, mailOnly.Scopes...), "files.read")
	saved := adapter.mailWithSession(domainTestContext(t), request, withFiles)
	var draft store.EmailDraft
	if saved.status != 200 || json.Unmarshal(saved.body, &draft) != nil || len(draft.Attachments) != 1 || draft.Attachments[0].SHA256 == "" {
		t.Fatalf("save=%d %s", saved.status, saved.body)
	}
	send := domainTestRequest(iscpworkbench.OperationMailDraftsSend, []byte(`{"expected_version":1,"idempotency_key":"reviewed-click"}`))
	send.Params = map[string]string{"draft": draft.ID}
	if result := adapter.mailWithSession(domainTestContext(t), send, mailOnly); result.status != 403 || browser.calls != 0 {
		t.Fatalf("mail-only send %d effects=%d", result.status, browser.calls)
	}
	if result := adapter.mailWithSession(domainTestContext(t), send, withFiles); result.status != 200 || browser.calls != 2 {
		t.Fatalf("authorized send %d %s effects=%d", result.status, result.body, browser.calls)
	}
}
