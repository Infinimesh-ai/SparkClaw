package emailmanagement

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestClientMailProjectionNeverCachesVerificationSecretOrSourcePaths(t *testing.T) {
	repo := store.NewMemoryStore()
	service, browser, _ := newFixtureService(t, repo)
	browser.subject = "Your sign-in verification code 482193"
	browser.body = "Your verification code is 482193. It expires in ten minutes."
	if _, e := service.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0); e != nil {
		t.Fatal(e)
	}
	if e := seedFixtureCollection(t.Context(), service); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{app.EmailJobParse, app.EmailJobClassification} {
		if worked, e := service.workOne(t.Context(), []string{kind}); e != nil || !worked {
			t.Fatalf("%s worked=%v err=%v", kind, worked, e)
		}
	}
	boxes, e := repo.ListEmailMailboxes(t.Context(), "email-owner")
	if e != nil || len(boxes) != 1 {
		t.Fatalf("mailbox fixture %v", e)
	}
	page, e := service.ClientSyncMessages(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: boxes[0].ID, Limit: 100})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(page)
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Messages) != 1 || strings.Contains(string(raw), "482193") || strings.Contains(string(raw), "original_path") || strings.Contains(string(raw), "manifest_path") || page.Messages[0].Verification != nil {
		t.Fatalf("private sync fields exposed: %s", raw)
	}
}
