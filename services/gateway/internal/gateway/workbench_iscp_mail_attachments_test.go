package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func mailAttachmentObjectContext(t *testing.T) (context.Context, *iscpobjects.Store, iscpobjects.Binding, app.EmailSendAttachment) {
	t.Helper()
	objects, err := iscpobjects.NewStore(filepath.Join(t.TempDir(), "objects"), iscpobjects.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	binding := iscpobjects.Binding{DeploymentID: "iscp-test-deployment", OwnerID: "iscp-owner", ClientID: "iscp-desktop-client", InstallationID: iscpTestInstallation, AuthorizationRevision: 1}
	ref, err := objects.Put(t.Context(), binding, app.EmailSendAttachmentPurpose, "report.txt", "text/plain", []byte("desktop reviewed"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(domainTestContext(t), iscpObjectContextKey{}, iscpObjectAccess{objects, binding})
	return ctx, objects, binding, app.EmailSendAttachment{LocalFileID: "88888888-8888-4888-8888-888888888888", Object: app.EmailAttachmentObject(ref)}
}

func TestISCPDomainMailAttachmentsRequireLocalObjectAndReadScope(t *testing.T) {
	server, repo, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("Gateway decoy must not be sent"), 0600); err != nil {
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
	ctx, objects, binding, attachment := mailAttachmentObjectContext(t)
	raw, _ := json.Marshal(map[string]any{"id": "reviewed-file", "mailbox_id": box.ID, "to": []string{"recipient@example.test"}, "subject": "s", "body": "b", "attachments": []any{map[string]any{"local_file_id": attachment.LocalFileID, "object": attachment.Object}}})
	request := domainTestRequest(iscpworkbench.OperationMailDraftsSave, raw)
	mailOnly := iscpworkbench.SessionInfo{Scopes: []string{"mail.drafts.write", "mail.send", "mail.read"}}
	if result := adapter.mailWithSession(ctx, request, mailOnly); result.status != 403 {
		t.Fatalf("mail-only saved %d %s", result.status, result.body)
	}
	withFiles := mailOnly
	withFiles.Scopes = append(append([]string{}, mailOnly.Scopes...), "files.read")
	if result := adapter.mailWithSession(domainTestContext(t), request, withFiles); result.status != 400 {
		t.Fatalf("missing bound object service=%d %s", result.status, result.body)
	}
	for _, source := range []string{"report.txt", filepath.Join(root, "report.txt"), "../report.txt", "https://example.test/file"} {
		pathInput, _ := json.Marshal(map[string]any{"mailbox_id": box.ID, "attachments": []any{map[string]string{"path": source}}})
		if result := adapter.mailWithSession(ctx, domainTestRequest(iscpworkbench.OperationMailDraftsSave, pathInput), withFiles); result.status != 400 {
			t.Fatalf("path source accepted: %d %s", result.status, result.body)
		}
	}
	saved := adapter.mailWithSession(ctx, request, withFiles)
	var draft store.EmailDraft
	if saved.status != 200 || json.Unmarshal(saved.body, &draft) != nil || len(draft.Attachments) != 1 || draft.Attachments[0].SHA256 != "sha256:"+attachment.Object.SHA256 || draft.Attachments[0].Object != attachment.Object || draft.Attachments[0].Path != "" {
		t.Fatalf("save=%d %s", saved.status, saved.body)
	}
	send := domainTestRequest(iscpworkbench.OperationMailDraftsSend, []byte(`{"expected_version":1,"idempotency_key":"reviewed-click"}`))
	send.Params = map[string]string{"draft": draft.ID}
	if result := adapter.mailWithSession(ctx, send, mailOnly); result.status != 403 || browser.calls != 0 {
		t.Fatalf("mail-only send %d effects=%d", result.status, browser.calls)
	}
	for _, alter := range []func(*iscpobjects.Binding){
		func(b *iscpobjects.Binding) { b.OwnerID = "other" }, func(b *iscpobjects.Binding) { b.ClientID = "other" }, func(b *iscpobjects.Binding) { b.InstallationID = "other" }, func(b *iscpobjects.Binding) { b.AuthorizationRevision++ },
	} {
		changed := binding
		alter(&changed)
		other := context.WithValue(ctx, iscpObjectContextKey{}, iscpObjectAccess{objects, changed})
		if result := adapter.mailWithSession(other, send, withFiles); result.status != 409 || browser.calls != 0 {
			t.Fatalf("send crossed binding %+v: %d %s", changed, result.status, result.body)
		}
		if result := adapter.mailWithSession(other, request, withFiles); result.status != 400 {
			t.Fatalf("save crossed binding %+v: %d %s", changed, result.status, result.body)
		}
	}
	if result := adapter.mailWithSession(ctx, send, withFiles); result.status != 200 || browser.calls != 2 {
		t.Fatalf("authorized send %d %s effects=%d", result.status, result.body, browser.calls)
	}
}

func TestISCPMailAttachmentObjectsRejectOtherBindingsAndForgedMetadata(t *testing.T) {
	ctx, objects, binding, attachment := mailAttachmentObjectContext(t)
	reader := mailAttachmentObjects{}
	for _, alter := range []func(*iscpobjects.Binding){
		func(b *iscpobjects.Binding) { b.DeploymentID = "other" }, func(b *iscpobjects.Binding) { b.OwnerID = "other" }, func(b *iscpobjects.Binding) { b.ClientID = "other" }, func(b *iscpobjects.Binding) { b.InstallationID = "other" }, func(b *iscpobjects.Binding) { b.AuthorizationRevision++ },
	} {
		changed := binding
		alter(&changed)
		other := context.WithValue(ctx, iscpObjectContextKey{}, iscpObjectAccess{objects, changed})
		if _, _, err := reader.ReadAttachmentObject(other, "iscp-owner", attachment.Object, app.EmailSendMaxAttachmentBytes); err == nil {
			t.Fatalf("cross-bound read %+v", changed)
		}
	}
	for _, alter := range []func(*app.EmailAttachmentObject){
		func(o *app.EmailAttachmentObject) { o.Name = "benign.txt" }, func(o *app.EmailAttachmentObject) { o.MediaType = "image/png" }, func(o *app.EmailAttachmentObject) { o.Purpose = "file" }, func(o *app.EmailAttachmentObject) { o.Size++ }, func(o *app.EmailAttachmentObject) { o.Version++ }, func(o *app.EmailAttachmentObject) { o.SHA256 = strings.Repeat("a", 64) }, func(o *app.EmailAttachmentObject) { o.ExpiresAt = "2099-01-01T00:00:00Z" },
	} {
		forged := attachment.Object
		alter(&forged)
		if _, _, err := reader.ReadAttachmentObject(ctx, "iscp-owner", forged, app.EmailSendMaxAttachmentBytes); err == nil {
			t.Fatalf("forged metadata accepted %+v", forged)
		}
	}
	wrongPurpose, err := objects.Put(ctx, binding, "file", "report.txt", "text/plain", []byte("desktop reviewed"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.ReadAttachmentObject(ctx, "iscp-owner", app.EmailAttachmentObject(wrongPurpose), app.EmailSendMaxAttachmentBytes); err == nil {
		t.Fatal("general file reused as mail attachment")
	}
	// Optional display metadata can be absent; the authority always supplies it.
	minimal := attachment.Object
	minimal.Name, minimal.MediaType, minimal.ExpiresAt = "", "", ""
	actual, raw, err := reader.ReadAttachmentObject(ctx, "iscp-owner", minimal, app.EmailSendMaxAttachmentBytes)
	if err != nil || actual != attachment.Object || string(raw) != "desktop reviewed" {
		t.Fatalf("authoritative metadata missing %+v %v", actual, err)
	}
	releaseBody, _ := json.Marshal(map[string]any{"object_id": actual.ObjectID, "version": actual.Version})
	response, handled := objects.Handle(ctx, binding, domainTestRequest(iscpworkbench.OperationObjectRelease, releaseBody))
	if !handled || response.Status != 200 {
		t.Fatalf("release %+v", response)
	}
	if _, _, err = reader.ReadAttachmentObject(ctx, "iscp-owner", actual, app.EmailSendMaxAttachmentBytes); err == nil {
		t.Fatal("released attachment survived")
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
			// A caller can predict a version, but an earlier preflight cannot
			// authorize a newly attached desktop object.
			objectCtx, _, _, local := mailAttachmentObjectContext(t)
			saveCtx := emailmanagement.WithAttachmentObjects(objectCtx, true, mailAttachmentObjects{})
			input := store.EmailDraft{ID: "racing-draft", MailboxID: box.ID, To: []string{"recipient@example.test"}, Subject: "s", Body: "b"}
			if scenario == "read_error" {
				input.Attachments = []app.EmailSendAttachment{local}
			}
			draft, err := service.SaveDraft(saveCtx, "iscp-owner", input, 0)
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
						newer.Attachments = []app.EmailSendAttachment{local}
						if _, err := service.SaveDraft(saveCtx, owner, newer, draft.Version); err != nil {
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
