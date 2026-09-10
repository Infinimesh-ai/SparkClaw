package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmailDraftSendFenceSurvivesRestartAndConcurrentClicks(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repo EmailComposeRepository = NewMemoryStore()
			path := filepath.Join(t.TempDir(), "state.json")
			if backend == "file" {
				s, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repo = s
			}
			if backend == "postgres" {
				st, err := NewPostgresStore(t.Context(), newPostgresMigrationTestSchema(t))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(st.Close)
				repo = st
			}
			saved, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "save", Draft: EmailDraft{ID: "d", Mode: "compose", To: []string{"recipient@example.test"}, Body: "frozen"}})
			if err != nil {
				t.Fatal(err)
			}
			var claimed atomic.Int32
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					v, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "begin", Draft: EmailDraft{ID: "d"}, ExpectedVersion: saved.Draft.Version, SendKey: "click"})
					if err != nil {
						t.Error(err)
					}
					if v.Execute {
						claimed.Add(1)
					}
				}()
			}
			wg.Wait()
			if claimed.Load() != 1 {
				t.Fatalf("execute calls=%d", claimed.Load())
			}
			if backend == "file" {
				s, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repo = s
			}
			v, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "begin", Draft: EmailDraft{ID: "d"}, ExpectedVersion: 1, SendKey: "click"})
			if err != nil || v.Execute || v.Draft.Snapshot.Body != "frozen" {
				t.Fatalf("replay=%+v err=%v", v, err)
			}
			if _, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "begin", Draft: EmailDraft{ID: "d"}, ExpectedVersion: v.Draft.Version, SendKey: "different"}); err == nil {
				t.Fatal("new key bypassed sending fence")
			}
			if _, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "save", Draft: EmailDraft{ID: "d", Body: "edited"}, ExpectedVersion: v.Draft.Version}); err == nil {
				t.Fatal("edited frozen send")
			}
			if _, err := repo.ListEmailDrafts(t.Context(), "other", "d"); err == nil {
				t.Fatal("owner isolation failed")
			}
			_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "finish", Draft: EmailDraft{ID: "d", State: "unknown"}, SendKey: "click", ErrorCode: "email_send_outcome_unknown"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "begin", Draft: EmailDraft{ID: "d"}, ExpectedVersion: 3, SendKey: "retry"}); err == nil {
				t.Fatal("unknown outcome retried")
			}
		})
	}
}

func TestEmailDraftReconcileRequiresExactCapturedOutboundIdentity(t *testing.T) {
	repo := NewMemoryStore()
	f := emailFixture(t, repo)
	m := f.capture(f.admit("native-id", time.Now()))
	saved, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "save", Draft: EmailDraft{ID: "draft", MailboxID: f.box.ID, Body: "test"}})
	f.must(err)
	_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "begin", Draft: EmailDraft{ID: "draft"}, ExpectedVersion: saved.Draft.Version, SendKey: "send"})
	f.must(err)
	finish := EmailDraftCommand{OwnerID: f.owner, Action: "finish", Draft: EmailDraft{ID: "draft", State: "sent"}, SendKey: "send", Receipt: &app.EmailSendResult{Provider: f.box.Provider, ProviderMessageID: "native-id", Status: "sent"}}
	if _, err = repo.ChangeEmailDraft(t.Context(), finish); err == nil {
		t.Fatal("inbound accepted as sent identity")
	}
	_, err = emailMemoryRun(repo, t.Context(), OperationChangeEmailDraft, f.owner, "", nil, true, func(e *emailEngine) (bool, error) { m.Direction = "sent"; emailSaveMail(e, m); return true, e.err })
	f.must(err)
	out, err := repo.ChangeEmailDraft(t.Context(), finish)
	f.must(err)
	if out.Draft.SentMailID != m.ID || out.Draft.ReconciledAt == nil || out.Draft.ConversationID == "" {
		t.Fatal("exact source not associated")
	}
}
