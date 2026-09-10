package gateway

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestEmailPresentationHTTPReadPurityAndAuthorization(t *testing.T) {
	f := newEmailHTTPFixture(t)
	m := f.receive("presentation-http", time.Now())
	query := fmt.Sprintf("/api/email/presentations?target_kind=mail&language=zh&target_id=%s", m.ID)
	before, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
	f.must(err)
	out := emailDecode[emailmanagement.PresentationsView](t, f.request("GET", query, ""), 200)
	if len(out.Items) != 1 || out.Items[0].State != "missing" {
		t.Fatalf("initial state=%+v", out)
	}
	after, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
	f.must(err)
	if before.Revision != after.Revision || f.browser.calls != 0 {
		t.Fatal("GET mutated state or called browser")
	}
	request := httptest.NewRequest("GET", query, nil)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatalf("unauthenticated response=%d", response.Code)
	}
	for _, bad := range []string{"/api/email/presentations?target_kind=mail&language=fr&target_id=" + m.ID, query + "&language=en", query + "&unknown=x", "/api/email/presentations?target_kind=mail&language=zh&target_id=foreign"} {
		got := f.request("GET", bad, "")
		if got.Code != 400 && got.Code != 404 {
			t.Fatalf("invalid read status=%d", got.Code)
		}
	}
	body := fmt.Sprintf(`{"target_kind":"mail","target_ids":[%q],"language":"zh"}`, m.ID)
	queued := emailDecode[emailmanagement.PresentationsView](t, f.request("POST", "/api/email/presentations/ensure", body), 202)
	again := emailDecode[emailmanagement.PresentationsView](t, f.request("POST", "/api/email/presentations/ensure", body), 202)
	if queued.Items[0].ID != again.Items[0].ID || again.Items[0].State != "queued" {
		t.Fatal("POST failed to coalesce")
	}
	job, found, err := f.repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobPresentation}, LeaseDuration: time.Minute})
	f.must(err)
	if !found {
		t.Fatal("POST did not enqueue durable work")
	}
	_, found, err = f.repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobPresentation}, LeaseDuration: time.Minute})
	f.must(err)
	if found {
		t.Fatal("duplicate presentation job")
	}
	read := emailDecode[emailmanagement.PresentationsView](t, f.request("GET", query, ""), 200)
	if read.Items[0].State != "running" || read.Items[0].TargetID != m.ID {
		t.Fatal("GET does not reflect leased work")
	}
	_, err = f.repo.FinishEmailJob(t.Context(), store.EmailJobFinish{EmailJobLease: f.lease(job), ErrorCode: "email_model_unavailable"})
	f.must(err)
	failed := emailDecode[emailmanagement.PresentationsView](t, f.request("POST", "/api/email/presentations/ensure", body), 202)
	if failed.Items[0].State != "failed" {
		t.Fatal("automatic ensure rearmed permanent failure")
	}
	if f.browser.calls != 0 {
		t.Fatal("presentation request invoked browser")
	}
	if got := f.request("POST", "/api/email/presentations/ensure", fmt.Sprintf(`{"target_kind":"mail","target_ids":[%q,"foreign"],"language":"en"}`, m.ID)); got.Code != 404 {
		t.Fatalf("mixed owner batch=%d", got.Code)
	}
	english := emailDecode[emailmanagement.PresentationsView](t, f.request("GET", "/api/email/presentations?target_kind=mail&language=en&target_id="+m.ID, ""), 200)
	if english.Items[0].State != "missing" {
		t.Fatal("invalid batch partially enqueued work")
	}
}

func TestPendingIncludesAdmissionWithoutOriginalAndExplicitRecovery(t *testing.T) {
	f := newEmailHTTPFixture(t)
	admitted, err := f.repo.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []store.EmailDiscoveryMember{{ProviderMessageID: "pending-capture", ProviderSelectionID: "pending-capture", Direction: "inbound"}}})
	f.must(err)
	out := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/pending", ""), 200)
	if len(out.Messages) != 1 || out.Messages[0].ID != admitted.Mails[0].ID || out.Messages[0].OriginalAvailable {
		t.Fatal("uncaptured admission exception hidden from Pending")
	}
	result := emailDecode[emailmanagement.ScheduleResult](t, f.request("POST", "/api/email/messages/"+admitted.Mails[0].ID+"/reanalyze", "{}"), 202)
	if !result.Scheduled || f.browser.calls != 0 {
		t.Fatal("explicit recovery should queue collection, never run browser inline")
	}
}
