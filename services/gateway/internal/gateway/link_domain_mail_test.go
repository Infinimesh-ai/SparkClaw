package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

const linkTestOwner = "iscp-owner"

// linkGatewayFixture is a Gateway with one registered desktop client and
// the link runtime's receipts and domain objects, without a connection.
func linkGatewayFixture(t *testing.T) (*Server, *store.FileStore) {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.DeploymentID = "link-test-deployment"
	repository, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.RegisterClient(t.Context(), app.Client{ID: "link-desktop-client", OwnerID: linkTestOwner, ActorID: linkTestOwner, Name: "Link desktop", TokenHash: hashSecret("synthetic-link-client")}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, repository)
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	server := New(cfg, repository, tools, runtime, WithExecutions(filepath.Join(root, "execution"), nil))
	ctx, cancel := context.WithCancel(t.Context())
	server.BindLifecycleContext(ctx)
	receipts, err := openLinkReceipts(filepath.Join(root, "link", "receipts.json"))
	if err != nil {
		t.Fatal(err)
	}
	objects, err := openLinkDomainObjects(filepath.Join(root, "link", "objects", "domain"))
	if err != nil {
		t.Fatal(err)
	}
	server.link = &linkRuntime{receipts: receipts, domainObj: objects}
	t.Cleanup(func() {
		cancel()
		if server.executions != nil {
			server.executions.Wait()
			server.executions.Close()
		}
		_ = tools.Close()
	})
	return server, repository
}

// linkDirect calls one domain method for the fixture's desktop.
func linkDirect(ctx context.Context, s *Server, method string, args any, key string) (linkDomainResult, error) {
	raw, _ := json.Marshal(args)
	domain := method[:bytes.IndexByte([]byte(method), '.')]
	params, _ := json.Marshal(contract.DomainCallParams{Domain: domain, Method: method, Args: raw, IdempotencyKey: key})
	req := &link.Request{Principal: link.Principal{Principal: contract.Principal{OwnerID: linkTestOwner, DeviceID: "desktop-link-test", Class: contract.ClassDesktop}, ClientID: "link-desktop-client", Conditions: contract.Conditions{Screen: contract.ScreenLarge, Attachments: true, BrowserSurface: true}}, Op: contract.OpDomainCall, Params: params}
	out, err := s.linkDomainCall(ctx, req)
	if err != nil {
		return linkDomainResult{}, err
	}
	var call contract.DomainCallResult
	switch v := out.(type) {
	case contract.DomainCallResult:
		call = v
	case json.RawMessage:
		if err := json.Unmarshal(v, &call); err != nil {
			return linkDomainResult{}, err
		}
	}
	var result linkDomainResult
	return result, json.Unmarshal(call.Result, &result)
}

func newLinkMailFixture(t *testing.T, backend string) (*Server, *emailHTTPFixture) {
	t.Helper()
	server, file := linkGatewayFixture(t)
	var repo emailHTTPRepository = file
	if backend == "memory" {
		repo = store.NewMemoryStore()
	}
	f := &emailHTTPFixture{t: t, repo: repo, owner: linkTestOwner, root: t.TempDir(), browser: &noEmailBrowser{}}
	service, err := emailmanagement.New(repo, f.browser, emailautomation.DefaultRegistry(), &replyPolishAnalyzer{}, nil, emailmanagement.Options{WorkspaceRoot: f.root, QualifiedProviderModes: map[string]string{app.EmailProviderGmail: app.EmailProviderModeTimeRange}})
	f.must(err)
	server.emailManagement = service
	server.email = &fakeEmailController{}
	f.box, err = repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderGmail, Address: "owner@example.test", Enabled: true})
	f.must(err)
	return server, f
}

// hostHTTP is the host WebChat's request for the same owner.
func hostHTTP(t *testing.T, server *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.WithValue(t.Context(), requestPrincipalContextKey{}, requestPrincipal{Authenticated: true, OwnerID: linkTestOwner, ActorID: linkTestOwner, ClientID: "link-desktop-client"})
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body)).WithContext(ctx)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

// comparableMailJSON drops the parts of a mail answer sampled live.
func comparableMailJSON(t *testing.T, body []byte) any {
	t.Helper()
	var v any
	if json.Unmarshal(body, &v) != nil {
		t.Fatal(string(body))
	}
	if object, ok := v.(map[string]any); ok {
		if _, ok := object["capacity"].(map[string]any); ok {
			object["capacity"] = "live filesystem probe"
		}
		delete(object, "server_now")
	}
	return v
}

func TestLinkMailReadsMatchTheHostWebChat(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			server, f := newLinkMailFixture(t, backend)
			first := f.receive("link-first", time.Now().Add(-time.Hour))
			second := f.receive("link-second", time.Now())
			changed, err := server.emailManagement.ChangeAssignment(t.Context(), store.EmailManualAssignment{EmailCommand: f.command(), MailID: first.ID, Title: "Link event", ExpectedVersion: first.InputVersion})
			f.must(err)
			cases := []struct {
				method, path string
				args         map[string]any
			}{
				{"mail.conversations.list", "/api/email/conversations?limit=1", map[string]any{"limit": 1}},
				{"mail.conversations.list", "/api/email/conversations?q=unmatched", map[string]any{"q": "unmatched"}},
				{"mail.conversations.get", "/api/email/conversations/" + changed.ConversationID, map[string]any{"conversation": changed.ConversationID}},
				{"mail.conversations.messages", "/api/email/conversations/" + changed.ConversationID + "/messages", map[string]any{"conversation": changed.ConversationID}},
				{"mail.pending", "/api/email/pending", nil},
				{"mail.notifications", "/api/email/notifications?subtype=general", map[string]any{"subtype": "general"}},
				{"mail.interaction", "/api/email/interaction-mails?unassigned_only=true", map[string]any{"unassigned_only": true}},
				{"mail.message", "/api/email/messages/" + first.ID, map[string]any{"mail": first.ID}},
				{"mail.verification", "/api/email/messages/" + first.ID + "/verification", map[string]any{"mail": first.ID}},
				{"mail.render_preview", "/api/email/messages/" + first.ID + "/render-preview", map[string]any{"mail": first.ID}},
				{"mail.sender_rules.list", "/api/email/sender-rules?limit=100", map[string]any{"limit": 100}},
				{"mail.presentations.get", "/api/email/presentations?target_kind=mail&language=zh&target_id=" + first.ID + "&target_id=" + second.ID, map[string]any{"target_kind": "mail", "language": "zh", "target_ids": []string{first.ID, second.ID}}},
				{"mail.sync.status", "/api/email/sync-status", nil},
				{"mail.sync.warnings", "/api/email/sync-warnings?mailbox_id=" + f.box.ID, map[string]any{"mailbox_id": f.box.ID}},
				{"mail.sent_sources", "/api/email/sent-sources?mailbox_id=" + f.box.ID, map[string]any{"mailbox_id": f.box.ID}},
				{"mail.drafts.list", "/api/email/drafts?mailbox_id=" + f.box.ID, map[string]any{"mailbox_id": f.box.ID}},
				{"mail.providers.list", "/api/email/providers", nil},
				{"mail.conversations.list", "/api/email/conversations?limit=0", map[string]any{"limit": 0, "entry": "other"}},
			}
			before, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
			f.must(err)
			for _, tc := range cases {
				t.Run(tc.method+tc.path, func(t *testing.T) {
					got, err := linkDirect(t.Context(), server, tc.method, tc.args, "")
					want := hostHTTP(t, server, http.MethodGet, tc.path, "")
					if err != nil || got.Status != want.Code || !reflect.DeepEqual(comparableMailJSON(t, got.Body), comparableMailJSON(t, want.Body.Bytes())) {
						t.Fatalf("link %d %s %v; HTTP %d %s", got.Status, got.Body, err, want.Code, want.Body.String())
					}
				})
			}
			// The desktop attaches its own files; the host WebChat has none.
			capabilities, err := linkDirect(t.Context(), server, "mail.compose.capabilities", nil, "")
			want, _ := json.Marshal(server.emailManagement.ComposeCapabilities())
			if err != nil || capabilities.Status != 200 || !bytes.Equal(capabilities.Body, want) || !bytes.Contains(want, []byte(`"workspace_attachments":true`)) {
				t.Fatalf("compose capabilities %s %v", capabilities.Body, err)
			}
			after, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
			f.must(err)
			if before.Revision != after.Revision || f.browser.calls != 0 {
				t.Fatal("reads wrote state or dispatched the browser")
			}
		})
	}
}

func TestLinkMailFilesAreDomainObjects(t *testing.T) {
	server, f := newLinkMailFixture(t, "file")
	mail := f.receive("link-file", time.Now())
	for _, part := range []string{"", "part-1"} {
		got, err := linkDirect(t.Context(), server, "mail.file", map[string]any{"mail": mail.ID, "part_id": part}, "")
		if err != nil || got.Status != 200 || got.Object == nil || got.Object.Purpose != "mail_attachment" {
			t.Fatalf("file %+v %v", got, err)
		}
		raw, err := server.link.domainObj.readAll("desktop-link-test", *got.Object, "mail_attachment", 0)
		path := "/api/email/messages/" + mail.ID + "/file"
		if part != "" {
			path += "?part_id=" + part
		}
		direct := hostHTTP(t, server, "GET", path, "")
		if err != nil || direct.Code != 200 || !bytes.Equal(raw, direct.Body.Bytes()) || got.Object.SHA256 != execution.Digest(raw) {
			t.Fatal("the object differs from the host WebChat's file")
		}
	}
	for _, args := range []map[string]any{{"mail": mail.ID, "part_id": "../../etc/passwd"}, {"mail": "foreign"}} {
		if got, err := linkDirect(t.Context(), server, "mail.file", args, ""); err != nil || got.Status != 404 {
			t.Fatalf("%v: %+v %v", args, got, err)
		}
	}
}

func TestLinkMailChangesReplayAndStayOwnerScoped(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			server, f := newLinkMailFixture(t, backend)
			mail := f.receive("link-mutations", time.Now())
			call := func(method string, args map[string]any, status int) linkDomainResult {
				t.Helper()
				key := newLinkUUID()
				first, err := linkDirect(t.Context(), server, method, args, key)
				if err != nil || first.Status != status {
					t.Fatalf("%s %d %s %v", method, first.Status, first.Body, err)
				}
				// The same key answers the original result; nothing runs again.
				repeated, err := linkDirect(t.Context(), server, method, args, key)
				if err != nil || repeated.Status != status || !bytes.Equal(repeated.Body, first.Body) {
					t.Fatalf("replay differs %s", method)
				}
				return first
			}
			raw := call("mail.assignment", map[string]any{"mail": mail.ID, "title": "Original event", "expected_version": mail.InputVersion, "command_key": "link-assign"}, 200)
			var assigned emailmanagement.AssignmentChangeView
			f.must(json.Unmarshal(raw.Body, &assigned))
			detail, err := server.emailManagement.Conversation(t.Context(), f.owner, assigned.ConversationID)
			f.must(err)
			call("mail.conversations.rename", map[string]any{"conversation": assigned.ConversationID, "title": "Renamed event", "expected_version": detail.Conversation.Version, "command_key": "link-rename"}, 200)
			call("mail.conversations.rename", map[string]any{"conversation": assigned.ConversationID, "title": "Stale event", "expected_version": detail.Conversation.Version, "command_key": "link-stale"}, 409)
			call("mail.viewed", map[string]any{"mail_ids": []string{mail.ID}}, 200)
			if viewed, _, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID); err != nil || viewed.ViewedAt == nil {
				t.Fatal("view did not persist", err)
			}
			call("mail.presentations.ensure", map[string]any{"target_kind": "mail", "target_ids": []string{mail.ID}, "language": "zh"}, 202)
			call("mail.sync.request", map[string]any{"mailbox_id": f.box.ID}, 202)
			call("mail.reanalyze", map[string]any{"mail": mail.ID}, 202)
			raw = call("mail.replies.polish", map[string]any{"id": "link-polish", "mail_id": mail.ID, "instruction": "Confirm tomorrow", "language": "en"}, 200)
			var polished store.EmailDraft
			f.must(json.Unmarshal(raw.Body, &polished))
			if polished.ReplyMailID != mail.ID || polished.Body == "" {
				t.Fatal("polish lost the native reply binding")
			}
			current, _, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID)
			f.must(err)
			call("mail.classification", map[string]any{"mail": mail.ID, "entry": "notification", "expected_version": current.Classification.Revision, "remember_sender": true, "expected_rule_version": 0, "command_key": "link-classify"}, 200)
			rules, err := f.repo.ListEmailSenderRules(t.Context(), store.EmailQuery{OwnerID: f.owner, Limit: 100})
			f.must(err)
			if len(rules) != 1 {
				t.Fatal("remember sender rule missing")
			}
			call("mail.sender_rules.update", map[string]any{"rule": rules[0].ID, "entry": "interaction", "enabled": false, "expected_version": rules[0].Revision, "command_key": "link-rule"}, 200)
			call("mail.source.cleanup", map[string]any{"scope": "mail", "mail_id": mail.ID, "command_key": "link-cleanup"}, 200)
			if response := hostHTTP(t, server, "GET", "/api/email/messages/"+mail.ID+"/file", ""); response.Code != 410 {
				t.Fatalf("cleaned original still readable %d", response.Code)
			}
			// The owner is the device's; a record of another owner is not found.
			call("mail.viewed", map[string]any{"mail_ids": []string{"foreign"}}, 404)
			call("mail.assignment", map[string]any{"mail": "foreign", "title": "Foreign", "expected_version": 1, "command_key": "foreign", "owner_id": "foreign"}, 404)
			call("mail.intake.update", map[string]any{"provider": "gmail", "intake_enabled": false, "expected_mailbox_version": 1, "enabled": true}, 400)
			detail, err = server.emailManagement.Conversation(t.Context(), f.owner, assigned.ConversationID)
			f.must(err)
			call("mail.conversations.delete", map[string]any{"conversation": assigned.ConversationID, "expected_version": detail.Conversation.Version, "command_key": "link-delete"}, 200)
			if response := hostHTTP(t, server, "GET", "/api/email/conversations/"+assigned.ConversationID, ""); response.Code != 404 {
				t.Fatal("delete did not remove the event")
			}
			box, _, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
			f.must(err)
			call("mail.intake.update", map[string]any{"provider": "gmail", "intake_enabled": false, "expected_mailbox_version": box.Version}, 200)
			if f.browser.calls != 0 {
				t.Fatal("queued mail changes dispatched the browser inline")
			}
		})
	}
}

func TestLinkMailProvidersReplayOnceAndAnUnknownLoginNeverReopens(t *testing.T) {
	server, _ := linkGatewayFixture(t)
	status := emailautomation.ProviderStatus{Provider: app.EmailProviderOutlook, Enabled: true, State: app.EmailStateReady, Version: 3}
	controller := &fakeEmailController{providers: []emailautomation.ProviderStatus{status}, result: status}
	server.email = controller
	listed, err := linkDirect(t.Context(), server, "mail.providers.list", nil, "")
	if err != nil || listed.Status != 200 || !bytes.Contains(listed.Body, []byte("outlook")) {
		t.Fatalf("%+v %v", listed, err)
	}
	for _, method := range []string{"mail.providers.update", "mail.providers.check", "mail.providers.login"} {
		args := map[string]any{"provider": "outlook"}
		if method == "mail.providers.update" {
			args["enabled"], args["expected_version"] = true, 3
		}
		key := newLinkUUID()
		first, err := linkDirect(t.Context(), server, method, args, key)
		again, err2 := linkDirect(t.Context(), server, method, args, key)
		if err != nil || err2 != nil || first.Status != 200 || !bytes.Equal(first.Body, again.Body) {
			t.Fatalf("%s %+v %+v %v %v", method, first, again, err, err2)
		}
	}
	if !slices.Equal(controller.actions, []string{"update", "check", "login"}) {
		t.Fatal(controller.actions)
	}
	for _, args := range []map[string]any{{"provider": "outlook", "intake_enabled": true, "expected_mailbox_version": 0}, {"provider": "outlook", "enabled": true}, {"provider": "outlook", "enabled": true, "expected_version": -1}, {"provider": "unknown", "enabled": true, "expected_version": 1}} {
		if got, err := linkDirect(t.Context(), server, "mail.providers.update", args, newLinkUUID()); err != nil || got.Status != 400 {
			t.Fatalf("%v: %+v %v", args, got, err)
		}
	}
	if len(controller.actions) != 3 {
		t.Fatal("invalid input reached the provider")
	}
	// The login browser may have opened before the failure: the attempt
	// stays unknown and a retry with the same key never opens it again.
	controller.err = &emailautomation.Error{Code: app.ToolErrorEmailProviderUnavailable, Message: "controller response lost"}
	key := newLinkUUID()
	if _, err := linkDirect(t.Context(), server, "mail.providers.login", map[string]any{"provider": "qq_mail"}, key); !linkErrorCode(err, contract.CodeOutcomeUnknown) {
		t.Fatalf("lost login %v", err)
	}
	controller.err = nil
	if _, err := linkDirect(t.Context(), server, "mail.providers.login", map[string]any{"provider": "qq_mail"}, key); !linkErrorCode(err, contract.CodeOutcomeUnknown) {
		t.Fatalf("retried login %v", err)
	}
	if !slices.Equal(controller.actions, []string{"update", "check", "login", "login"}) {
		t.Fatal("an unknown login was repeated", controller.actions)
	}
}

func TestLinkMailSendUsesThePersistedDraftOnce(t *testing.T) {
	server, f := newLinkMailFixture(t, "file")
	saved, err := linkDirect(t.Context(), server, "mail.drafts.save", map[string]any{"draft": "link-draft", "mailbox_id": f.box.ID, "to": []string{"never-send@example.test"}, "subject": "controlled provider", "body": "private draft", "expected_version": 0}, newLinkUUID())
	if err != nil || saved.Status != 200 {
		t.Fatalf("draft %+v %v", saved, err)
	}
	key := newLinkUUID()
	send := map[string]any{"draft": "link-draft", "expected_version": 1, "idempotency_key": "link-send-key"}
	sent, err := linkDirect(t.Context(), server, "mail.drafts.send", send, key)
	if err != nil || sent.Status != 200 {
		t.Fatalf("send %+v %v", sent, err)
	}
	count := f.browser.calls
	replayed, err := linkDirect(t.Context(), server, "mail.drafts.send", send, key)
	if err != nil || replayed.Status != 200 || !bytes.Equal(replayed.Body, sent.Body) || f.browser.calls != count {
		t.Fatalf("duplicate side effect %+v calls=%d %v", replayed, f.browser.calls, err)
	}
	rows, err := server.emailManagement.Drafts(t.Context(), f.owner, "link-draft")
	if err != nil || len(rows) != 1 || rows[0].State != "sent" {
		t.Fatalf("missing durable send %+v %v", rows, err)
	}
	read, err := linkDirect(t.Context(), server, "mail.drafts.list", map[string]any{"draft": "link-draft"}, "")
	if err != nil || read.Status != 200 || !bytes.Contains(read.Body, []byte(`"state":"sent"`)) {
		t.Fatalf("draft read %+v %v", read, err)
	}
}

type intakeUncertainRepo struct{ emailmanagement.Repository }

func (r *intakeUncertainRepo) BindEmailMailbox(ctx context.Context, c store.EmailBindCommand) (app.EmailMailbox, error) {
	box, err := r.Repository.BindEmailMailbox(ctx, c)
	if err == nil {
		err = &store.StoreError{Code: store.StoreErrorUnknownOutcome, Operation: store.OperationBindEmailMailbox}
	}
	return box, err
}

func TestLinkMailIntakeReconcileSettlesALostChangeFromTheMailbox(t *testing.T) {
	server, f := newLinkMailFixture(t, "file")
	service := server.emailManagement
	uncertain, err := emailmanagement.New(&intakeUncertainRepo{Repository: f.repo}, f.browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: f.root})
	f.must(err)
	server.emailManagement = uncertain
	original := map[string]any{"provider": "gmail", "intake_enabled": false, "expected_mailbox_version": f.box.Version}
	key := newLinkUUID()
	// The commit happened, but storage could not confirm it.
	if _, err := linkDirect(t.Context(), server, "mail.intake.update", original, key); !linkErrorCode(err, contract.CodeOutcomeUnknown) {
		t.Fatalf("uncertain commit %v", err)
	}
	if _, err := linkDirect(t.Context(), server, "mail.intake.update", original, key); !linkErrorCode(err, contract.CodeOutcomeUnknown) {
		t.Fatalf("the uncertain change was answered %v", err)
	}
	server.emailManagement = service
	reconcile := func(args map[string]any) (map[string]any, error) {
		rawOriginal, _ := json.Marshal(args)
		var out map[string]any
		result, err := linkDirectRaw(t.Context(), server, "mail.intake.reconcile", map[string]any{"idempotency_key": key, "original": json.RawMessage(rawOriginal)})
		if err == nil {
			err = json.Unmarshal(result, &out)
		}
		return out, err
	}
	// Other content under the same key is a conflict, never a settlement.
	if _, err := reconcile(map[string]any{"provider": "gmail", "intake_enabled": true, "expected_mailbox_version": f.box.Version}); !linkErrorCode(err, contract.CodeConflict) {
		t.Fatalf("conflicting original %v", err)
	}
	settled, err := reconcile(original)
	response, _ := settled["response"].(map[string]any)
	if err != nil || settled["state"] != "completed" || response["status"] != float64(200) {
		t.Fatalf("settled %+v %v", settled, err)
	}
	// The settled answer is now the change's receipt.
	replayed, err := linkDirect(t.Context(), server, "mail.intake.update", original, key)
	if err != nil || replayed.Status != 200 || !bytes.Contains(replayed.Body, []byte(`"mailbox"`)) {
		t.Fatalf("receipt %+v %v", replayed, err)
	}
	boxes, err := f.repo.ListEmailMailboxes(t.Context(), f.owner)
	f.must(err)
	if len(boxes) != 1 || boxes[0].IntakeEnabled {
		t.Fatal(boxes)
	}
	// A key that never ran has nothing to settle.
	key = newLinkUUID()
	if _, err := reconcile(original); !linkErrorCode(err, contract.CodeNotFound) {
		t.Fatalf("never ran %v", err)
	}
}

// linkDirectRaw calls a method whose result is not a domain result.
func linkDirectRaw(ctx context.Context, s *Server, method string, args any) (json.RawMessage, error) {
	raw, _ := json.Marshal(args)
	domain := method[:bytes.IndexByte([]byte(method), '.')]
	params, _ := json.Marshal(contract.DomainCallParams{Domain: domain, Method: method, Args: raw})
	req := &link.Request{Principal: link.Principal{Principal: contract.Principal{OwnerID: linkTestOwner, DeviceID: "desktop-link-test", Class: contract.ClassDesktop}, ClientID: "link-desktop-client"}, Op: contract.OpDomainCall, Params: params}
	out, err := s.linkDomainCall(ctx, req)
	if err != nil {
		return nil, err
	}
	call, ok := out.(contract.DomainCallResult)
	if !ok {
		return nil, fmt.Errorf("unexpected %T", out)
	}
	return call.Result, nil
}

func TestLinkMailAttachmentsAreTheDevicesReviewedObjects(t *testing.T) {
	server, f := newLinkMailFixture(t, "file")
	meta, err := server.link.domainObj.put("desktop-link-test", app.EmailSendAttachmentPurpose, "report.txt", "text/plain", []byte("desktop reviewed"))
	f.must(err)
	object := app.EmailAttachmentObject(meta.reference())
	attachment := map[string]any{"local_file_id": "88888888-8888-4888-8888-888888888888", "object": object}
	save := map[string]any{"id": "reviewed-file", "mailbox_id": f.box.ID, "to": []string{"recipient@example.test"}, "subject": "s", "body": "b", "attachments": []any{attachment}}
	// A path never names an attachment.
	for _, source := range []string{"report.txt", "../report.txt", "https://example.test/file"} {
		input := map[string]any{"mailbox_id": f.box.ID, "attachments": []any{map[string]string{"path": source}}}
		if got, err := linkDirect(t.Context(), server, "mail.drafts.save", input, newLinkUUID()); err != nil || got.Status != 400 {
			t.Fatalf("path source %s: %+v %v", source, got, err)
		}
	}
	// Another device's object, or forged metadata, is never read.
	for _, alter := range []func(*app.EmailAttachmentObject){
		func(o *app.EmailAttachmentObject) { o.Size++ }, func(o *app.EmailAttachmentObject) { o.SHA256 = "a" + o.SHA256[1:] },
		func(o *app.EmailAttachmentObject) { o.Purpose = "file" }, func(o *app.EmailAttachmentObject) { o.ObjectID = newLinkUUID() },
	} {
		forged := object
		alter(&forged)
		input := map[string]any{"id": "forged", "mailbox_id": f.box.ID, "to": []string{"recipient@example.test"}, "subject": "s", "body": "b", "attachments": []any{map[string]any{"local_file_id": attachment["local_file_id"], "object": forged}}}
		if got, err := linkDirect(t.Context(), server, "mail.drafts.save", input, newLinkUUID()); err != nil || got.Status != 400 {
			t.Fatalf("forged %+v: %+v %v", forged, got, err)
		}
	}
	saved, err := linkDirect(t.Context(), server, "mail.drafts.save", save, newLinkUUID())
	var draft store.EmailDraft
	if err != nil || saved.Status != 200 || json.Unmarshal(saved.Body, &draft) != nil || len(draft.Attachments) != 1 || draft.Attachments[0].Object != object || draft.Attachments[0].Path != "" {
		t.Fatalf("save %+v %v", saved, err)
	}
	sent, err := linkDirect(t.Context(), server, "mail.drafts.send", map[string]any{"draft": draft.ID, "expected_version": 1, "idempotency_key": "reviewed-click"}, newLinkUUID())
	if err != nil || sent.Status != 200 || f.browser.calls == 0 {
		t.Fatalf("send %+v %v calls=%d", sent, err, f.browser.calls)
	}
}
