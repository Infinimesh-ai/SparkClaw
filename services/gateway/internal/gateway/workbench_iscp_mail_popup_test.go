package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func newMailPopupFixture(t *testing.T, backend string) (*Server, *emailHTTPFixture, wb.Config) {
	t.Helper()
	server, file, cfg, bind := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, bind)
	var repo emailHTTPRepository = file
	if backend == "memory" {
		repo = store.NewMemoryStore()
	}
	f := &emailHTTPFixture{t: t, repo: repo, owner: "iscp-owner", root: t.TempDir(), browser: &noEmailBrowser{}}
	service, err := emailmanagement.New(repo, f.browser, emailautomation.DefaultRegistry(), &replyPolishAnalyzer{}, nil, emailmanagement.Options{WorkspaceRoot: f.root, QualifiedProviderModes: map[string]string{app.EmailProviderGmail: app.EmailProviderModeTimeRange}})
	f.must(err)
	server.emailManagement = service
	server.email = &fakeEmailController{}
	f.box, err = repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderGmail, Address: "owner@example.test", Enabled: true})
	f.must(err)
	return server, f, cfg
}
func popupHTTP(t *testing.T, server *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body)).WithContext(domainTestContext(t))
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}
func popupJSON(t *testing.T, body []byte) any {
	t.Helper()
	var v any
	if json.Unmarshal(body, &v) != nil {
		t.Fatal(string(body))
	}
	if object, ok := v.(map[string]any); ok {
		if stamp, ok := object["server_now"].(string); ok {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				t.Fatal(err)
			}
			delete(object, "server_now")
		}
	}
	return v
}

func TestISCPMailPopupReadParityMemoryAndFile(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			server, f, cfg := newMailPopupFixture(t, backend)
			first := f.receive("popup-first", time.Now().Add(-time.Hour))
			second := f.receive("popup-second", time.Now())
			changed, err := server.emailManagement.ChangeAssignment(t.Context(), store.EmailManualAssignment{EmailCommand: f.command(), MailID: first.ID, Title: "Popup event", ExpectedVersion: first.InputVersion})
			f.must(err)
			adapter := &iscpDomainAdapter{server: server, config: cfg}
			cases := []struct {
				op, path string
				params   map[string]string
			}{
				{wb.OperationMailConversationsList, "/api/email/conversations?limit=1", map[string]string{"limit": "1"}},
				{wb.OperationMailConversationsList, "/api/email/conversations?q=unmatched", map[string]string{"q": "unmatched"}},
				{wb.OperationMailConversationsGet, "/api/email/conversations/" + changed.ConversationID, map[string]string{"conversation": changed.ConversationID}},
				{wb.OperationMailConversationsMessages, "/api/email/conversations/" + changed.ConversationID + "/messages", map[string]string{"conversation": changed.ConversationID}},
				{wb.OperationMailPending, "/api/email/pending", nil},
				{wb.OperationMailNotifications, "/api/email/notifications?subtype=general", map[string]string{"subtype": "general"}},
				{wb.OperationMailInteraction, "/api/email/interaction-mails?unassigned_only=true", map[string]string{"unassigned_only": "true"}},
				{wb.OperationMailVerification, "/api/email/messages/" + first.ID + "/verification", map[string]string{"mail": first.ID}},
				{wb.OperationMailRenderPreview, "/api/email/messages/" + first.ID + "/render-preview", map[string]string{"mail": first.ID}},
				{wb.OperationMailSenderRulesList, "/api/email/sender-rules?limit=100", map[string]string{"limit": "100"}},
				{wb.OperationMailPresentationsGet, "/api/email/presentations?target_kind=mail&language=zh&target_id=" + first.ID + "&target_id=" + second.ID, map[string]string{"target_kind": "mail", "language": "zh", "target_ids": fmt.Sprintf(`[%q,%q]`, first.ID, second.ID)}},
				{wb.OperationMailSyncStatus, "/api/email/sync-status", nil},
				{wb.OperationMailSyncWarnings, "/api/email/sync-warnings?mailbox_id=" + f.box.ID, map[string]string{"mailbox_id": f.box.ID}},
				{wb.OperationMailSentSources, "/api/email/sent-sources?mailbox_id=" + f.box.ID, map[string]string{"mailbox_id": f.box.ID}},
			}
			before, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
			f.must(err)
			for _, tc := range cases {
				t.Run(tc.op+tc.path, func(t *testing.T) {
					req := domainTestRequest(tc.op, nil)
					req.Params = tc.params
					got := adapter.mailPopup(domainTestContext(t), req)
					want := popupHTTP(t, server, http.MethodGet, tc.path, "")
					if got.status != want.Code || !reflect.DeepEqual(popupJSON(t, got.body), popupJSON(t, want.Body.Bytes())) {
						t.Fatalf("ISCP %d %s; HTTP %d %s", got.status, got.body, want.Code, want.Body.String())
					}
				})
			}
			after, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
			f.must(err)
			if before.Revision != after.Revision || f.browser.calls != 0 {
				t.Fatal("reads wrote state or dispatched browser")
			}
			for _, part := range []string{"", "part-1"} {
				path := "/api/email/messages/" + first.ID + "/file"
				if part != "" {
					path += "?part_id=" + part
				}
				got := popupHTTP(t, server, "GET", path, "")
				if got.Code != 200 || got.Header().Get("X-SparkClaw-Digest") != execution.Digest(got.Body.Bytes()) || got.Header().Get("Content-Length") != fmt.Sprint(got.Body.Len()) {
					t.Fatalf("download integrity %d %v", got.Code, got.Header())
				}
			}
		})
	}
}

func TestISCPMailPopupMutationsPreserveCASReplayAndOwnership(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			server, f, cfg := newMailPopupFixture(t, backend)
			mail := f.receive("popup-mutations", time.Now())
			handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
			f.must(err)
			call := func(op string, params map[string]string, body string, status int) wb.Response {
				t.Helper()
				req := domainTestRequest(op, []byte(body))
				req.Params = params
				first := handler(domainTestContext(t), req)
				if first.Status != status {
					t.Fatalf("%s %d %s", op, first.Status, first.Body)
				}
				// Restart the adapter to prove this is a durable, input-bound result.
				restarted, err := server.NewWorkbenchISCPDomainHandler(cfg)
				f.must(err)
				req.ID = domainTestRequest("id", nil).ID
				repeated := restarted(domainTestContext(t), req)
				if repeated.Status != status || !bytes.Equal(repeated.Body, first.Body) {
					t.Fatalf("replay differs %s", op)
				}
				return first
			}
			raw := call(wb.OperationMailAssignment, map[string]string{"mail": mail.ID}, fmt.Sprintf(`{"title":"Original popup event","expected_version":%d,"command_key":"popup-assign"}`, mail.InputVersion), 200)
			var assigned emailmanagement.AssignmentChangeView
			f.must(json.Unmarshal(raw.Body, &assigned))
			detail, err := server.emailManagement.Conversation(t.Context(), f.owner, assigned.ConversationID)
			f.must(err)
			call(wb.OperationMailConversationsRename, map[string]string{"conversation": assigned.ConversationID}, fmt.Sprintf(`{"title":"Renamed event","expected_version":%d,"command_key":"popup-rename"}`, detail.Conversation.Version), 200)
			call(wb.OperationMailConversationsRename, map[string]string{"conversation": assigned.ConversationID}, fmt.Sprintf(`{"title":"Stale event","expected_version":%d,"command_key":"popup-stale"}`, detail.Conversation.Version), 409)
			call(wb.OperationMailViewed, nil, fmt.Sprintf(`{"mail_ids":[%q]}`, mail.ID), 200)
			viewed, _, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID)
			f.must(err)
			if viewed.ViewedAt == nil {
				t.Fatal("view did not persist")
			}
			call(wb.OperationMailPresentationsEnsure, nil, fmt.Sprintf(`{"target_kind":"mail","target_ids":[%q],"language":"zh"}`, mail.ID), 202)
			call(wb.OperationMailSyncRequest, nil, fmt.Sprintf(`{"mailbox_id":%q}`, f.box.ID), 202)
			call(wb.OperationMailReanalyze, map[string]string{"mail": mail.ID}, `{}`, 202)
			raw = call(wb.OperationMailRepliesPolish, nil, fmt.Sprintf(`{"id":"popup-polish","mail_id":%q,"instruction":"Confirm tomorrow","language":"en"}`, mail.ID), 200)
			var polished store.EmailDraft
			f.must(json.Unmarshal(raw.Body, &polished))
			if polished.ReplyMailID != mail.ID || polished.Body == "" {
				t.Fatal("polish lost native reply binding")
			}
			current, _, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID)
			f.must(err)
			call(wb.OperationMailClassification, map[string]string{"mail": mail.ID}, fmt.Sprintf(`{"entry":"notification","expected_version":%d,"remember_sender":true,"expected_rule_version":0,"command_key":"popup-classify"}`, current.Classification.Revision), 200)
			rules, err := f.repo.ListEmailSenderRules(t.Context(), store.EmailQuery{OwnerID: f.owner, Limit: 100})
			f.must(err)
			if len(rules) != 1 {
				t.Fatal("remember sender rule missing")
			}
			call(wb.OperationMailSenderRulesUpdate, map[string]string{"rule": rules[0].ID}, fmt.Sprintf(`{"entry":"interaction","enabled":false,"expected_version":%d,"command_key":"popup-rule"}`, rules[0].Revision), 200)
			call(wb.OperationMailSourceCleanup, nil, fmt.Sprintf(`{"scope":"mail","mail_id":%q,"command_key":"popup-cleanup"}`, mail.ID), 200)
			if response := popupHTTP(t, server, "GET", "/api/email/messages/"+mail.ID+"/file", ""); response.Code != 410 {
				t.Fatalf("cleaned original still readable %d", response.Code)
			}
			// Route-specific decoders reject broadening; callers cannot add host paths,
			// owners, arbitrary URLs, or combine intake and provider settings.
			for _, tc := range []struct {
				op     string
				params map[string]string
				body   string
			}{
				{wb.OperationMailAssignment, map[string]string{"mail": mail.ID}, `{"owner_id":"foreign"}`},
				{wb.OperationMailSyncRequest, nil, `{"url":"https://example.invalid"}`},
				{wb.OperationMailIntakeUpdate, map[string]string{"provider": "gmail"}, `{"intake_enabled":false,"expected_mailbox_version":1,"enabled":true}`},
			} {
				call(tc.op, tc.params, tc.body, 400)
			}
			call(wb.OperationMailViewed, nil, `{"mail_ids":["foreign"]}`, 404)
			call(wb.OperationMailAssignment, map[string]string{"mail": "foreign"}, `{"title":"Foreign","expected_version":1,"command_key":"foreign"}`, 404)
			detail, err = server.emailManagement.Conversation(t.Context(), f.owner, assigned.ConversationID)
			f.must(err)
			call(wb.OperationMailConversationsDelete, map[string]string{"conversation": assigned.ConversationID}, fmt.Sprintf(`{"expected_version":%d,"command_key":"popup-delete"}`, detail.Conversation.Version), 200)
			if response := popupHTTP(t, server, "GET", "/api/email/conversations/"+assigned.ConversationID, ""); response.Code != 404 {
				t.Fatal("delete did not remove event")
			}
			box, _, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
			f.must(err)
			call(wb.OperationMailIntakeUpdate, map[string]string{"provider": "gmail"}, fmt.Sprintf(`{"intake_enabled":false,"expected_mailbox_version":%d}`, box.Version), 200)
			if f.browser.calls != 0 {
				t.Fatal("queued popup operations dispatched browser inline")
			}
		})
	}
}

func TestISCPMailPopupQualificationAndManagementScopes(t *testing.T) {
	server, f, cfg := newMailPopupFixture(t, "file")
	cfg.QualifiedCapabilities = mailPopupOperations()
	adapter := &iscpDomainAdapter{server: server, config: cfg}
	inspect := func(scopes []string) iscpDomainCapability {
		result := adapter.capabilitiesWithSession(domainTestContext(t), domainTestRequest(wb.OperationCapabilitiesGet, nil), wb.SessionInfo{Profile: wb.ProfileV2, Scopes: scopes})
		var body struct {
			Capabilities []iscpDomainCapability `json:"capabilities"`
		}
		f.must(json.Unmarshal(result.body, &body))
		for _, capability := range body.Capabilities {
			if capability.ID == "mail_popup" {
				return capability
			}
		}
		t.Fatal("popup capability missing")
		return iscpDomainCapability{}
	}
	scopes := []string{"mail.read", "mail.drafts.write", "settings.write", "objects.read"}
	if !inspect(scopes).Enabled {
		t.Fatal("logged-out popup is unavailable")
	}
	for _, scope := range scopes {
		if inspect(slices.DeleteFunc(slices.Clone(scopes), func(s string) bool { return s == scope })).Enabled {
			t.Fatalf("missing scope %s enabled popup", scope)
		}
	}
	adapter.config.QualifiedCapabilities = mailPopupOperations()[:len(mailPopupOperations())-1]
	if inspect(scopes).Enabled {
		t.Fatal("unqualified original interaction advertised")
	}
	for _, op := range []string{wb.OperationMailSourceCleanup, wb.OperationMailConversationsDelete, wb.OperationMailIntakeUpdate, wb.OperationMailClassification, wb.OperationMailRepliesPolish} {
		spec, _ := wb.LookupOperation(op)
		if workbenchOperationPermitted(spec, []string{spec.Scope}) {
			t.Fatalf("%s missing mail.read accepted", op)
		}
	}
}

func TestISCPMailPopupEncryptedSourceAndClosedInputs(t *testing.T) {
	server, f, cfg := newMailPopupFixture(t, "file")
	mail := f.receive("encrypted-popup", time.Now())
	network := startV2EncryptedFixture(t, server, cfg, []string{"mail.read", "mail.drafts.write"}, mailPopupOperations())
	network.bind(t)
	for _, part := range []string{"", "part-1"} {
		req := v2Request(wb.OperationMailFile, nil)
		req.Params = map[string]string{"mail": mail.ID, "part_id": part}
		result := network.call(t, req)
		requireV2Status(t, result, 200)
		if result.Object == nil || result.Object.Purpose != "mail_attachment" {
			t.Fatalf("file response is not a bounded object %+v", result)
		}
		bytes := network.download(t, *result.Object)
		path := "/api/email/messages/" + mail.ID + "/file"
		if part != "" {
			path += "?part_id=" + part
		}
		direct := popupHTTP(t, server, "GET", path, "")
		if direct.Code != 200 || string(bytes) != direct.Body.String() || result.Object.SHA256 != execution.Digest(bytes) {
			t.Fatal("encrypted file differs from original HTTP source")
		}
	}
	for _, tc := range []struct {
		op     string
		params map[string]string
		body   string
		status int
	}{
		{wb.OperationMailFile, map[string]string{"mail": mail.ID, "part_id": "../../etc/passwd"}, "", 404},
		{wb.OperationMailFile, map[string]string{"mail": "foreign"}, "", 404},
		{wb.OperationMailConversationsList, map[string]string{"limit": "0"}, "", 400},
		{wb.OperationMailPresentationsGet, map[string]string{"target_kind": "mail", "language": "zh", "target_ids": "[1]"}, "", 400},
		{wb.OperationMailPresentationsGet, map[string]string{"target_kind": "mail", "language": "zh", "target_ids": "[]"}, "", 400},
		{wb.OperationMailPresentationsGet, map[string]string{"target_kind": "mail", "language": "zh", "target_ids": `["foreign"]`}, "", 404},
		{wb.OperationMailSyncStatus, nil, `{"owner_id":"foreign"}`, 400},
		{wb.OperationMailSyncAcknowledge, map[string]string{"warning": "foreign"}, fmt.Sprintf(`{"mailbox_id":%q}`, f.box.ID), 404},
	} {
		request := v2Request(tc.op, []byte(tc.body))
		request.Params = tc.params
		result := network.call(t, request)
		if result.Status != tc.status {
			t.Fatalf("%s %v status %d want%d %s", tc.op, tc.params, result.Status, tc.status, result.Body)
		}
	}
	for _, operation := range []string{wb.OperationMailFile, wb.OperationMailConversationsList} {
		request := v2Request(operation, nil)
		request.Params = map[string]string{"path": "/etc/passwd"}
		if _, err := network.client.Call(network.ctx, request); err == nil {
			t.Fatalf("%s accepted arbitrary path parameter", operation)
		}
	}
	// A full popup's ordinary read scope does not grant destructive management.
	for _, op := range []string{wb.OperationMailSourceCleanup, wb.OperationMailConversationsDelete, wb.OperationMailClassification} {
		spec, _ := wb.LookupOperation(op)
		if workbenchOperationPermitted(spec, []string{"mail.read"}) {
			t.Fatalf("read-only scope admits %s", op)
		}
	}
}
