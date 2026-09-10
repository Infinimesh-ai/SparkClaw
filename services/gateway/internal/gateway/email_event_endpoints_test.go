package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func eventHTTPAssign(t *testing.T, f *emailHTTPFixture, m app.EmailMail, target, title, key string) emailmanagement.AssignmentChangeView {
	t.Helper()
	current := emailDecode[emailmanagement.MessageView](t, f.request("GET", "/api/email/messages/"+m.ID, ""), 200)
	body := fmt.Sprintf(`{"conversation_id":%q,"title":%q,"expected_version":%d,"command_key":%q}`, target, title, current.Version, key)
	return emailDecode[emailmanagement.AssignmentChangeView](t, f.request("POST", "/api/email/messages/"+m.ID+"/assignment", body), 200)
}
func TestEmailEventHTTPAssignmentAndRenameReplay(t *testing.T) {
	f := newEmailHTTPFixture(t)
	m := f.receive("manual-event", time.Now())
	current := emailDecode[emailmanagement.MessageView](t, f.request("GET", "/api/email/messages/"+m.ID, ""), 200)
	if current.Version != m.InputVersion {
		t.Fatal("mail response must expose its edit version")
	}
	path := "/api/email/messages/" + m.ID + "/assignment"
	body := fmt.Sprintf(`{"title":"Confirm purchase delivery","expected_version":%d,"command_key":"create-event"}`, current.Version)
	first := emailDecode[emailmanagement.AssignmentChangeView](t, f.request("POST", path, body), 200)
	replay := emailDecode[emailmanagement.AssignmentChangeView](t, f.request("POST", path, body), 200)
	if first.ConversationID == "" || replay.ConversationID != first.ConversationID || replay.Mail.Version != first.Mail.Version || first.Mail.AssignmentSource != "manual" {
		t.Fatalf("unstable manual replay: %+v %+v", first, replay)
	}
	if w := f.request("POST", path, strings.Replace(body, "create-event", "stale-event", 1)); w.Code != 409 {
		t.Fatalf("stale assignment=%d: %s", w.Code, w.Body.String())
	}
	detail := emailDecode[emailmanagement.ConversationDetail](t, f.request("GET", "/api/email/conversations/"+first.ConversationID, ""), 200)
	row, ok, err := f.repo.GetEmailConversation(t.Context(), f.owner, first.ConversationID)
	f.must(err)
	if !ok || detail.Conversation.Version != row.InputVersion {
		t.Fatal("conversation response must expose its edit version")
	}
	if detail.Conversation.Title != "Confirm purchase delivery" || detail.Conversation.Summary != "" || detail.Conversation.ProcessingState != "ready" {
		t.Fatalf("detail depends on summary: %+v", detail)
	}
	timeline := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/conversations/"+first.ConversationID+"/messages", ""), 200)
	if len(timeline.Messages) != 1 || timeline.Messages[0].BodyText != "unique-contract-term-manual-event" || timeline.Messages[0].Summary != "" {
		t.Fatalf("source timeline=%+v", timeline)
	}
	path = "/api/email/conversations/" + first.ConversationID + "/rename"
	body = fmt.Sprintf(`{"title":"Confirm delivery date","expected_version":%d,"command_key":"rename-event"}`, detail.Conversation.Version)
	renamed := emailDecode[emailmanagement.ConversationDetail](t, f.request("POST", path, body), 200)
	repeated := emailDecode[emailmanagement.ConversationDetail](t, f.request("POST", path, body), 200)
	if renamed.Conversation.Title != "Confirm delivery date" || repeated.Conversation.Version != renamed.Conversation.Version {
		t.Fatal("rename replay changed title/version")
	}
	if w := f.request("POST", path, strings.Replace(body, "rename-event", "stale-rename", 1)); w.Code != 409 {
		t.Fatalf("stale rename=%d", w.Code)
	}
	if w := f.request("POST", path, fmt.Sprintf(`{"title":" ","expected_version":%d,"command_key":"blank"}`, renamed.Conversation.Version)); w.Code != 400 {
		t.Fatalf("blank rename=%d", w.Code)
	}
	if f.browser.calls != 0 {
		t.Fatal("event edit ran browser inline")
	}
}
func TestEmailEventHTTPMixedRoutingAndManualIndexCorrection(t *testing.T) {
	f := newEmailHTTPFixture(t)
	classify := func(m app.EmailMail, key string) {
		emailDecode[emailmanagement.MessageView](t, f.request("POST", "/api/email/messages/"+m.ID+"/classification", fmt.Sprintf(`{"entry":"notification","expected_version":%d,"command_key":%q}`, m.Classification.Revision, key)), 200)
	}
	notice := f.receive("shipping", time.Now())
	classify(notice, "make-notice")
	shipping := eventHTTPAssign(t, f, notice, "", "Track shipment", "shipping-event")
	interactive := f.receive("address", time.Now().Add(time.Minute))
	address := eventHTTPAssign(t, f, interactive, "", "Confirm address", "address-event")
	query := func(q string) emailmanagement.ConversationsView {
		return emailDecode[emailmanagement.ConversationsView](t, f.request("GET", "/api/email/conversations"+q, ""), 200)
	}
	n := query("?entry=notification")
	if len(n.Conversations) != 1 || n.Conversations[0].ID != shipping.ConversationID {
		t.Fatalf("notification events=%+v", n)
	}
	i := query("?entry=interaction")
	if len(i.Conversations) != 1 || i.Conversations[0].ID != address.ConversationID {
		t.Fatalf("interaction events=%+v", i)
	}
	if all := query(""); len(all.Conversations) != 2 {
		t.Fatalf("all-category picker=%+v", all)
	}
	loose := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/notifications?unassigned_only=true", ""), 200)
	if len(loose.Messages) != 0 {
		t.Fatal("assigned notice duplicated as loose")
	}
	eventHTTPAssign(t, f, interactive, shipping.ConversationID, "", "correct-address")
	mixed := emailDecode[emailmanagement.ConversationDetail](t, f.request("GET", "/api/email/conversations/"+shipping.ConversationID, ""), 200)
	if mixed.Conversation.EffectiveEntry != "interaction" || mixed.Conversation.MemberCount != 2 {
		t.Fatalf("mixed category=%+v", mixed)
	}
	if list := query("?entry=notification"); len(list.Conversations) != 0 {
		t.Fatalf("mixed event still in notifications=%+v", list)
	}
	timeline := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/conversations/"+shipping.ConversationID+"/messages", ""), 200)
	if len(timeline.Messages) != 2 {
		t.Fatalf("destination index=%+v", timeline)
	}
	former := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/conversations/"+address.ConversationID+"/messages", ""), 200)
	if len(former.Messages) != 0 {
		t.Fatal("old event index retained moved mail")
	}
	latest := f.receive("delivered", time.Now().Add(2*time.Minute))
	classify(latest, "latest-notice")
	eventHTTPAssign(t, f, latest, shipping.ConversationID, "", "append-notice")
	mixed = emailDecode[emailmanagement.ConversationDetail](t, f.request("GET", "/api/email/conversations/"+shipping.ConversationID, ""), 200)
	if mixed.Conversation.EffectiveEntry != "interaction" || mixed.Conversation.MemberCount != 3 {
		t.Fatalf("latest notification moved event=%+v", mixed)
	}
}
func TestEmailEventHTTPOwnerIsolationAndInput(t *testing.T) {
	f := newEmailHTTPFixture(t)
	own := f.receive("own", time.Now())
	owner, box := f.owner, f.box
	f.owner = "foreign-owner"
	var err error
	f.box, err = f.repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderGmail, Address: "foreign@example.com", Enabled: true})
	f.must(err)
	foreign := f.receive("foreign", time.Now())
	_, err = f.repo.ActivateEmailEventPolicy(t.Context(), f.command())
	f.must(err)
	foreign, err = f.repo.ChangeEmailAssignment(t.Context(), store.EmailManualAssignment{EmailCommand: f.command(), MailID: foreign.ID, Title: "Foreign event", ExpectedVersion: foreign.InputVersion})
	f.must(err)
	f.owner, f.box = owner, box
	for _, path := range []string{"/api/email/messages/" + foreign.ID + "/assignment", "/api/email/conversations/" + foreign.ConversationID + "/rename"} {
		body := `{"title":"Foreign attempt","expected_version":1,"command_key":"foreign-attempt"}`
		if w := f.request("POST", path, body); w.Code != 404 {
			t.Fatalf("foreign mutation=%d: %s", w.Code, w.Body.String())
		}
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous mutation=%d", w.Code)
		}
	}
	path := "/api/email/messages/" + own.ID + "/assignment"
	body := fmt.Sprintf(`{"conversation_id":%q,"expected_version":%d,"command_key":"foreign-target"}`, foreign.ConversationID, own.InputVersion)
	if w := f.request("POST", path, body); w.Code != 404 {
		t.Fatalf("foreign target=%d: %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"title":"Purpose","owner_id":"foreign-owner"}`, `{"title":"Purpose"} {}`, `{"title":"Purpose","extra":true}`} {
		if w := f.request("POST", path, body); w.Code != 400 {
			t.Fatalf("bad body=%d", w.Code)
		}
	}
	for _, q := range []string{"?entry=bogus", "?entry=notification&entry=interaction", "?owner_id=foreign-owner"} {
		if w := f.request("GET", "/api/email/conversations"+q, ""); w.Code != 400 {
			t.Fatalf("bad query=%s status=%d", q, w.Code)
		}
	}
	all := emailDecode[emailmanagement.ConversationsView](t, f.request("GET", "/api/email/conversations", ""), 200)
	for _, conv := range all.Conversations {
		if conv.ID == foreign.ConversationID {
			t.Fatal("foreign event leaked into picker")
		}
	}
}
