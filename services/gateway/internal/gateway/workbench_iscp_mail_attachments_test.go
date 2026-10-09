package gateway

import (
	"context"
	"encoding/json"
	"errors"
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

type attachmentDraftReadRepository struct {
	emailmanagement.Repository
	read func(context.Context, string, string) ([]store.EmailDraft, error)
}

func (r *attachmentDraftReadRepository) ListEmailDrafts(ctx context.Context, owner, id string) ([]store.EmailDraft, error) {
	return r.read(ctx, owner, id)
}

func TestISCPDomainAttachmentScopeUsesOnlyAuthoritativeDraftRead(t *testing.T) {
	for _, scenario := range []string{"read_error", "concurrent_addition", "empty_read"} {
		t.Run(scenario, func(t *testing.T) {
			server, repo, cfg, textHandler := workbenchISCPFixture(t, nil)
			bindWorkbenchISCP(t, textHandler)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("reviewed"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.SaveOwnerProfile(t.Context(), app.OwnerProfile{ID: "iscp-owner", WorkspaceRoot: root}); err != nil {
				t.Fatal(err)
			}
			box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "iscp-owner", CommandKey: "attachment-read"}, Provider: app.EmailProviderGmail, Address: "owner@example.test", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			browser := &noEmailBrowser{}
			service, err := emailmanagement.New(repo, browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			// A caller can predict the next version, but that must never make an earlier
			// unprivileged preflight authorize a subsequently attached workspace file.
			input := store.EmailDraft{ID: "racing-draft", MailboxID: box.ID, To: []string{"recipient@example.test"}, Subject: "s", Body: "b"}
			if scenario == "read_error" {
				input.Attachments = []app.EmailSendAttachment{{Path: "report.txt"}}
			}
			draft, err := service.SaveDraft(t.Context(), "iscp-owner", input, 0)
			if err != nil {
				t.Fatal(err)
			}
			reads := 0
			wrapped := &attachmentDraftReadRepository{Repository: repo}
			wrapped.read = func(ctx context.Context, owner, id string) ([]store.EmailDraft, error) {
				reads++
				rows, err := repo.ListEmailDrafts(ctx, owner, id)
				if err != nil {
					return nil, err
				}
				if reads == 1 {
					switch scenario {
					case "read_error":
						return nil, errors.New("isolated repository unavailable")
					case "empty_read":
						return nil, nil
					case "concurrent_addition":
						newer := draft
						newer.Attachments = []app.EmailSendAttachment{{Path: "report.txt"}}
						if _, err := service.SaveDraft(t.Context(), owner, newer, draft.Version); err != nil {
							t.Fatal(err)
						}
					}
				}
				return rows, nil
			}
			restricted, err := emailmanagement.New(wrapped, browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			server.emailManagement = restricted
			adapter := &iscpDomainAdapter{server: server, config: cfg}
			expected := 1
			if scenario == "concurrent_addition" {
				expected = 2
			}
			raw, _ := json.Marshal(map[string]any{"expected_version": expected, "idempotency_key": "must-not-send"})
			request := domainTestRequest(iscpworkbench.OperationMailDraftsSend, raw)
			request.Params = map[string]string{"draft": draft.ID}
			result := adapter.mailWithSession(domainTestContext(t), request, iscpworkbench.SessionInfo{Scopes: []string{"mail.send", "mail.drafts.write", "mail.read"}})
			want := 503
			if scenario == "concurrent_addition" {
				want = 409
			}
			if scenario == "empty_read" {
				want = 404
			}
			if result.status != want || reads != 1 || browser.calls != 0 {
				t.Fatalf("status=%d reads=%d effects=%d body=%s", result.status, reads, browser.calls, result.body)
			}
		})
	}
}
