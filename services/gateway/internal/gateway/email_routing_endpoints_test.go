package gateway

import (
	"encoding/json"
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmailRoutingHTTPRejectsMalformedAndForeignRequests(t *testing.T) {
	f := newEmailHTTPFixture(t)
	m := f.receive("routing", time.Now())
	for _, path := range []string{"/api/email/notifications?subtype=bogus", "/api/email/notifications?subtype=general&subtype=verification", "/api/email/interaction-mails?subtype=general", "/api/email/notifications?limit=0", "/api/email/notifications?owner_id=other"} {
		if w := f.request("GET", path, ""); w.Code != 400 {
			t.Errorf("%s status=%d", path, w.Code)
		}
	}
	path := "/api/email/messages/" + m.ID + "/classification"
	for _, body := range []string{`{"entry":"notification","owner_id":"other"}`, `{"entry":"notification"} {}`, `{"entry":"bogus","command_key":"bad"}`, `{"entry":"notification","expected_version":1,"command_key":"bad","extra":true}`} {
		if w := f.request("POST", path, body); w.Code != 400 {
			t.Errorf("malformed status=%d body=%s", w.Code, w.Body.String())
		}
	}
	unauth := httptest.NewRequest("POST", path, strings.NewReader(`{"entry":"notification"}`))
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, unauth)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized=%d", w.Code)
	}
	otherBox, err := f.repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "other", CommandKey: "bind"}, Provider: "gmail", Address: "other@example.test", Enabled: true})
	f.must(err)
	admitted, err := f.repo.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: store.EmailCommand{OwnerID: "other", CommandKey: "admit"}, MailboxID: otherBox.ID, BindingGeneration: otherBox.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []store.EmailDiscoveryMember{{ProviderMessageID: "foreign", ProviderSelectionID: "foreign", Direction: "inbound"}}})
	f.must(err)
	foreign := admitted.Mails[0].ID
	for _, path := range []string{"/api/email/messages/" + foreign, "/api/email/messages/" + foreign + "/verification"} {
		if w := f.request("GET", path, ""); w.Code != 404 {
			t.Errorf("foreign read=%d", w.Code)
		}
	}
	if w := f.request("POST", "/api/email/messages/"+foreign+"/classification", `{"entry":"notification","expected_version":0,"command_key":"foreign"}`); w.Code != 404 {
		t.Fatalf("foreign override=%d", w.Code)
	}
}
func TestEmailRoutingHTTPManualRuleReplayAndStaleEdit(t *testing.T) {
	f := newEmailHTTPFixture(t)
	m := f.receive("manual-routing", time.Now())
	input := fmt.Sprintf(`{"entry":"notification","expected_version":%d,"remember_sender":true,"expected_rule_version":0,"command_key":"manual-rule"}`, m.Classification.Revision)
	path := "/api/email/messages/" + m.ID + "/classification"
	first := emailDecode[emailmanagement.MessageView](t, f.request("POST", path, input), 200)
	repeated := emailDecode[emailmanagement.MessageView](t, f.request("POST", path, input), 200)
	if first.Classification.Revision != repeated.Classification.Revision || repeated.Classification.Source != "manual" {
		t.Fatal("replay changed classification")
	}
	var rules struct {
		Rules []app.EmailSenderRule `json:"rules"`
	}
	w := f.request("GET", "/api/email/sender-rules", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rules) != nil || len(rules.Rules) != 1 || rules.Rules[0].Revision != 1 {
		t.Fatal("rule replay created duplicate/revision")
	}
	notifications := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/notifications?subtype=general", ""), 200)
	if len(notifications.Messages) != 1 || notifications.Messages[0].ID != m.ID {
		t.Fatal("manual route absent")
	}
	interactions := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/interaction-mails", ""), 200)
	if len(interactions.Messages) != 0 {
		t.Fatal("routed mail duplicated in interaction")
	}
	if w := f.request("POST", path, strings.Replace(input, "manual-rule", "stale-edit", 1)); w.Code != 409 {
		t.Fatalf("stale override=%d", w.Code)
	}
	rulePath := "/api/email/sender-rules/" + rules.Rules[0].ID
	if w := f.request("POST", rulePath, `{"entry":"interaction","enabled":true,"expected_version":0,"command_key":"stale-rule"}`); w.Code != 409 {
		t.Fatalf("stale rule=%d", w.Code)
	}
	emailDecode[map[string]any](t, f.request("POST", rulePath, `{"entry":"interaction","enabled":false,"expected_version":1,"command_key":"disable-rule"}`), 200)
	current, ok, err := f.repo.GetEmailMail(t.Context(), f.owner, m.ID)
	f.must(err)
	if !ok || current.Classification.EffectiveEntry != "notification" {
		t.Fatal("rule disable undid manual mail choice")
	}
}

func TestEmailRoutingHTTPVerificationOnlyRevealsThroughOwnerDetail(t *testing.T) {
	f := newEmailHTTPFixture(t)
	m := f.receive("654321", time.Now())
	_, err := f.repo.RequestEmailJob(t.Context(), store.EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobClassification, TargetID: m.ID, Dependencies: []string{"search"}, Rearm: true})
	f.must(err)
	job := f.claim(app.EmailJobClassification)
	_, err = f.repo.PublishEmailClassification(t.Context(), store.EmailClassificationCommand{EmailCommand: f.command(), Lease: f.lease(job), MailID: m.ID, Generation: job.Generation, Classification: app.EmailClassification{Category: "notification", NotificationSubtype: "verification", Purpose: "use 654321", Evidence: []app.EmailClassificationEvidence{{Ref: "representation:" + m.RepresentationID + ":body", Text: "654321"}}, InputFingerprint: job.InputFingerprint, EvidenceRefs: []string{"representation:" + m.RepresentationID + ":body"}}, Verification: &app.EmailVerification{Code: "654321", Purpose: "sign in using 654321", ExpiryEvidence: "Code 654321 is valid for five minutes after sending", EvidenceRef: "representation:" + m.RepresentationID + ":body"}})
	f.must(err)
	f.finish(job)
	list := f.request("GET", "/api/email/notifications?subtype=verification", "")
	if list.Code != 200 || strings.Contains(list.Body.String(), "654321") {
		t.Fatalf("verification leaked in list: status=%d", list.Code)
	}
	reveal := f.request("GET", "/api/email/messages/"+m.ID+"/verification", "")
	if reveal.Code != 200 || !strings.Contains(reveal.Body.String(), `"code":"654321"`) || reveal.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("controlled reveal contract failed")
	}
}
