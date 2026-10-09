package gateway

import (
	"context"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type replyPolishAnalyzer struct {
	input emailmanagement.AnalysisInput
}

func (a *replyPolishAnalyzer) Analyze(_ context.Context, input emailmanagement.AnalysisInput) (emailmanagement.AnalysisOutput, error) {
	a.input = input
	return emailmanagement.AnalysisOutput{Body: "您好，周二下午可以参加，请发送会议链接。谢谢。", ModelVersion: "reply-polish-fixture"}, nil
}

func (b *noEmailBrowser) Admit(context.Context, string, string) (emailautomation.AdmissionResult, error) {
	b.calls++
	return emailautomation.AdmissionResult{Provider: "gmail", Account: "default", AccountHint: "owner@example.com"}, nil
}
func (b *noEmailBrowser) SendForOwner(context.Context, string, app.EmailSendRequest) (app.EmailSendResult, error) {
	b.calls++
	return app.EmailSendResult{Provider: "gmail", Status: "sent"}, nil
}
func TestEmailComposeHTTPAuthenticationAndSendReplay(t *testing.T) {
	f := newEmailHTTPFixture(t)
	call := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if authenticated {
			r.Header.Set("Authorization", "Bearer email-owner-token")
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	input, _ := json.Marshal(map[string]any{"id": "draft", "mailbox_id": f.box.ID, "to": []string{"recipient@example.test"}, "subject": "Test", "body": "Test", "expected_version": 0})
	if w := call("POST", "/api/email/drafts", string(input), false); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth code=%d", w.Code)
	}
	w := call("POST", "/api/email/drafts", string(input), true)
	if w.Code != 200 {
		t.Fatalf("save %d: %s", w.Code, w.Body.String())
	}
	w = call("POST", "/api/email/drafts/draft/send", `{"expected_version":1,"idempotency_key":"click"}`, true)
	if w.Code != 200 {
		t.Fatalf("send %d: %s", w.Code, w.Body.String())
	}
	var sent store.EmailDraft
	if json.Unmarshal(w.Body.Bytes(), &sent) != nil || sent.State != "sent" {
		t.Fatal("missing sent state")
	}
	before := f.browser.calls
	w = call("POST", "/api/email/drafts/draft/send", `{"expected_version":1,"idempotency_key":"click"}`, true)
	if w.Code != 200 || f.browser.calls != before {
		t.Fatal("replay invoked browser")
	}
	_, err := f.repo.ChangeEmailDraft(t.Context(), store.EmailDraftCommand{OwnerID: "other-owner", Action: "save", Draft: store.EmailDraft{ID: "other-draft"}})
	f.must(err)
	if w = call("GET", "/api/email/drafts/other-draft", "", true); w.Code != 404 {
		t.Fatalf("cross-owner read code=%d", w.Code)
	}
}

func TestEmailComposeHTTPAllowsEscapedBodyWithinDecodedLimit(t *testing.T) {
	f := newEmailHTTPFixture(t)
	body := strings.Repeat("<", 200<<10)
	input, err := json.Marshal(map[string]any{"id": "escaped-draft", "mailbox_id": f.box.ID, "to": []string{"recipient@example.invalid"}, "subject": "Synthetic", "body": body, "expected_version": 0})
	f.must(err)
	if len(input) <= 1<<20 {
		t.Fatal("fixture must exercise JSON expansion beyond one MiB")
	}
	draft := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/drafts", string(input)), 200)
	if draft.Body != body {
		t.Fatal("escaped body did not survive draft persistence")
	}
	input, err = json.Marshal(map[string]any{"id": "oversized-draft", "mailbox_id": f.box.ID, "body": body + "x", "expected_version": 0})
	f.must(err)
	if got := f.request("POST", "/api/email/drafts", string(input)); got.Code != http.StatusBadRequest {
		t.Fatalf("decoded body limit: status %d", got.Code)
	}
	var value struct {
		Body string `json:"body"`
	}
	for _, payload := range []string{`{"body":"ok"}` + strings.Repeat(" ", 2<<20), `{"body":"ok"} {}`} {
		if decodeEmailCompose(httptest.NewRequest("POST", "/", strings.NewReader(payload)), &value) == nil {
			t.Fatal("oversized or trailing JSON request accepted")
		}
	}
}

func (b *noEmailBrowser) ReconcileSendForOwner(context.Context, string, app.EmailSendRequest) (app.EmailSendResult, error) {
	b.calls++
	return app.EmailSendResult{Provider: "gmail", Status: "unknown"}, nil
}

func TestEmailComposeHTTPReplyBindsOriginalAndPreservesNoticeMembership(t *testing.T) {
	f := newEmailHTTPFixture(t)
	original := f.receive("reply-target", time.Now())
	create, _ := json.Marshal(map[string]any{"id": "reply-draft", "expected_version": 0, "mode": "reply_all", "reply_mail_id": original.ID, "to": []string{}, "cc": []string{}, "subject": "", "body": ""})
	d := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/drafts", string(create)), 200)
	if len(d.To) != 1 || d.To[0] != "vendor@example.com" || d.MailboxID != f.box.ID || d.ReplyTarget == nil || d.ReplyTarget.ProviderMessageID != original.ProviderMessageID {
		t.Fatal("reply original/recipients not bound")
	}
	update, _ := json.Marshal(map[string]any{"expected_version": d.Version, "mailbox_id": d.MailboxID, "mode": "reply_all", "reply_mail_id": original.ID, "to": d.To, "cc": []string{"copy@example.com"}, "subject": d.Subject, "body": "I confirm."})
	d = emailDecode[store.EmailDraft](t, f.request("PUT", "/api/email/drafts/reply-draft", string(update)), 200)
	request, _ := json.Marshal(map[string]any{"expected_version": d.Version, "idempotency_key": "reply-click"})
	sent := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/drafts/reply-draft/send", string(request)), 200)
	if sent.State != "sent" || sent.ConversationID == "" || sent.Snapshot.ReplyTarget.ProviderMessageID != original.ProviderMessageID {
		t.Fatal("reply snapshot or matter missing")
	}
	unchanged, _, err := f.repo.GetEmailMail(t.Context(), f.owner, original.ID)
	f.must(err)
	if unchanged.ConversationID != original.ConversationID {
		t.Fatal("reply moved original membership")
	}
	local := emailDecode[emailmanagement.MessageView](t, f.request("GET", "/api/email/messages/"+sent.TimelineMailID, ""), 200)
	if local.LocalSendID == "" || local.OriginalAvailable || local.ReplyMailID != original.ID || local.ConfirmationSource != "provider_receipt" {
		t.Fatal("local reply provenance missing")
	}
}

func TestEmailComposeHTTPPolishesEditableDraftThenUsesNativeReply(t *testing.T) {
	analyzer := &replyPolishAnalyzer{}
	f := newEmailHTTPFixtureWithAnalyzer(t, analyzer)
	original := f.receive("polish-target", time.Now())
	request, _ := json.Marshal(map[string]any{
		"id": "polished-reply", "mail_id": original.ID, "instruction": "确认周二下午参加，并请对方发会议链接。", "language": "zh",
	})
	draft := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/replies/polish", string(request)), 200)
	if draft.Mode != "reply" || draft.ReplyMailID != original.ID || draft.Body == "" || len(draft.To) != 1 || draft.ReplyTarget == nil {
		t.Fatalf("polished native reply not bound: %+v", draft)
	}
	if analyzer.input.ReplyInstruction == "" || len(analyzer.input.Evidence) == 0 || analyzer.input.OutputLanguage != "zh" {
		t.Fatalf("reply context missing: %+v", analyzer.input)
	}
	update, _ := json.Marshal(map[string]any{
		"expected_version": draft.Version, "mailbox_id": draft.MailboxID, "mode": "reply", "reply_mail_id": original.ID,
		"to": draft.To, "cc": draft.CC, "subject": draft.Subject, "body": draft.Body + "\n我已补充这一句。",
	})
	draft = emailDecode[store.EmailDraft](t, f.request("PUT", "/api/email/drafts/"+draft.ID, string(update)), 200)
	sendRequest, _ := json.Marshal(map[string]any{"expected_version": draft.Version, "idempotency_key": "confirm-send"})
	sent := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/drafts/"+draft.ID+"/send", string(sendRequest)), 200)
	if sent.State != "sent" || sent.Snapshot == nil || sent.Snapshot.Mode != "reply" || sent.Snapshot.ReplyMailID != original.ID || !strings.Contains(sent.Snapshot.Body, "补充") {
		t.Fatalf("confirmed reply did not preserve editable draft: %+v", sent)
	}
}
func TestEmailComposeHTTPDraftPagesDoNotLoseOlderDrafts(t *testing.T) {
	f := newEmailHTTPFixture(t)
	for _, id := range []string{"first", "second", "third"} {
		_, err := f.repo.ChangeEmailDraft(t.Context(), store.EmailDraftCommand{OwnerID: f.owner, Action: "save", Draft: store.EmailDraft{ID: id, MailboxID: f.box.ID, Subject: id}})
		f.must(err)
	}
	page := emailDecode[store.EmailDraftPage](t, f.request("GET", "/api/email/drafts?limit=2", ""), 200)
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatal("draft page not bounded")
	}
	next := emailDecode[store.EmailDraftPage](t, f.request("GET", "/api/email/drafts?limit=2&cursor="+url.QueryEscape(page.NextCursor), ""), 200)
	if len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatal("older draft inaccessible")
	}
	if w := f.request("GET", "/api/email/drafts?limit=2&mailbox_id=other&cursor="+url.QueryEscape(page.NextCursor), ""); w.Code != 400 {
		t.Fatal("draft cursor crossed scope")
	}
}

func TestEmailComposeHTTPAttachmentPreconditionsAndLegacyDraft(t *testing.T) {
	f := newEmailHTTPFixture(t)
	f.must(os.WriteFile(filepath.Join(f.root, "report.txt"), []byte("reviewed version one"), 0600))
	makeInput := func(id string, version int, attachments any) string {
		body := map[string]any{"id": id, "mailbox_id": f.box.ID, "expected_version": version, "to": []string{"recipient@example.test"}, "subject": "Review", "body": "Confirmed"}
		if attachments != nil {
			body["attachments"] = attachments
		}
		raw, err := json.Marshal(body)
		f.must(err)
		return string(raw)
	}
	assertCode := func(response *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		var result struct {
			Code string `json:"code"`
		}
		if response.Code != status || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Code != code {
			t.Fatalf("response %d %s", response.Code, response.Body.String())
		}
	}
	assertCode(f.request("POST", "/api/email/drafts", makeInput("escape", 0, []any{map[string]any{"path": "../outside"}})), 400, "email_attachment_invalid")
	// Clients cannot nominate a name, digest, remote URL or frozen staging path.
	assertCode(f.request("POST", "/api/email/drafts", makeInput("forged", 0, []any{map[string]any{"path": "report.txt", "sha256": "forged"}})), 400, "email_invalid_request")
	first := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/drafts", makeInput("attachment", 0, []any{map[string]any{"path": "report.txt"}})), 200)
	if len(first.Attachments) != 1 || first.Attachments[0].SizeBytes != 20 {
		t.Fatalf("manifest %+v", first.Attachments)
	}
	f.must(os.WriteFile(filepath.Join(f.root, "report.txt"), []byte("edited"), 0600))
	assertCode(f.request("POST", "/api/email/drafts/attachment/send", `{"expected_version":1,"idempotency_key":"old-click"}`), 409, "email_attachment_changed")
	saved := emailDecode[store.EmailDraft](t, f.request("GET", "/api/email/drafts/attachment", ""), 200)
	if saved.State != "draft" || saved.Version != 1 {
		t.Fatal("precondition consumed draft")
	}
	updated := emailDecode[store.EmailDraft](t, f.request("PUT", "/api/email/drafts/attachment", makeInput("attachment", 1, []any{map[string]any{"path": "report.txt"}})), 200)
	if updated.Version != 2 || updated.Attachments[0].SHA256 == first.Attachments[0].SHA256 {
		t.Fatal("save did not refresh reviewed version")
	}
	assertCode(f.request("POST", "/api/email/drafts/attachment/send", `{"expected_version":1,"idempotency_key":"stale-review"}`), 409, "email_conflict")
	sent := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/drafts/attachment/send", `{"expected_version":2,"idempotency_key":"new-reviewed-click"}`), 200)
	if sent.State != "sent" || sent.Snapshot.Attachments[0] != updated.Attachments[0] {
		t.Fatal("reviewed manifest missing from sent snapshot")
	}
	legacy := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/drafts", makeInput("legacy", 0, nil)), 200)
	if len(legacy.Attachments) != 0 {
		t.Fatal("legacy draft gained attachments")
	}
	if out := emailDecode[store.EmailDraft](t, f.request("POST", "/api/email/drafts/legacy/send", `{"expected_version":1,"idempotency_key":"legacy-click"}`), 200); out.State != "sent" {
		t.Fatal("legacy draft cannot send")
	}
}
