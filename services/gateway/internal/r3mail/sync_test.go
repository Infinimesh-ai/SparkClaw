package r3mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readRepository(repo store.EmailRepository) Repository {
	return Repository{OwnerStatus: repo.GetEmailOwnerStatus, Mailbox: repo.GetEmailMailbox, Mailboxes: repo.ListEmailMailboxes}
}

type projection struct{ rows []emailmanagement.MessageView }

func (p *projection) ClientSyncMessages(_ context.Context, q store.EmailQuery) (emailmanagement.MessagesView, error) {
	return emailmanagement.MessagesView{Messages: p.rows}, nil
}
func box(t *testing.T, repo store.EmailRepository) app.EmailMailbox {
	t.Helper()
	b, e := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func row(b app.EmailMailbox, id, subject string) emailmanagement.MessageView {
	return emailmanagement.MessageView{ID: id, MailboxID: b.ID, Version: 1, Subject: subject, To: []string{}, CC: []string{}, Attachments: []emailmanagement.AttachmentView{}}
}
func mustSync(t *testing.T, s *Service, b app.EmailMailbox, c string, limit int) Response {
	t.Helper()
	out, e := s.Sync(t.Context(), "owner", b.ID, c, limit)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestDurableSnapshotDeltaTombstonesAndIsolation(t *testing.T) {
	repo := store.NewMemoryStore()
	b := box(t, repo)
	p := &projection{rows: []emailmanagement.MessageView{row(b, "a", "First"), row(b, "b", "Second")}}
	root := t.TempDir()
	s, e := New(root, readRepository(repo), p)
	if e != nil {
		t.Fatal(e)
	}
	first := mustSync(t, s, b, "", 1)
	if !first.More || first.Mode != "snapshot" || len(first.Events) != 1 {
		t.Fatalf("bad snapshot %+v", first)
	}
	last := mustSync(t, s, b, first.Cursor, 1)
	if last.More || len(last.Events) != 1 || last.Sequence != 2 {
		t.Fatalf("bad second page %+v", last)
	}
	s, e = New(root, readRepository(repo), p)
	if e != nil {
		t.Fatal(e)
	}
	same := mustSync(t, s, b, last.Cursor, 10)
	if same.Epoch != last.Epoch || len(same.Events) != 0 {
		t.Fatalf("restart lost revision %+v", same)
	}
	p.rows = []emailmanagement.MessageView{row(b, "b", "Changed")}
	delta := mustSync(t, s, b, last.Cursor, 1)
	if len(delta.Events) != 1 || !delta.Events[0].Deleted || delta.Events[0].ID != "a" || !delta.More {
		t.Fatalf("missing deletion %+v", delta)
	}
	next := mustSync(t, s, b, delta.Cursor, 1)
	if len(next.Events) != 1 || next.Events[0].Mail.Subject != "Changed" || next.More {
		t.Fatalf("missing update %+v", next)
	}
	replay := mustSync(t, s, b, last.Cursor, 10)
	if len(replay.Events) != 2 {
		t.Fatal("lost durable retransmission")
	}
	if _, e = s.Sync(t.Context(), "other", b.ID, last.Cursor, 10); !errors.Is(e, ErrNotFound) {
		t.Fatalf("cross-owner read %v", e)
	}
	other, e := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "other-box"}, Provider: app.EmailProviderQQMail, Address: "qq@example.com", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	p.rows = nil
	if _, e = s.Sync(t.Context(), "owner", other.ID, last.Cursor, 10); !errors.Is(e, ErrReset) {
		t.Fatalf("cross-mailbox cursor accepted: %v", e)
	}
	raw, e := os.ReadFile(filepath.Join(root, scope("owner", b.ID)+".json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "manifest_path") || strings.Contains(string(raw), "credential") {
		t.Fatal("journal contains non-mail private state")
	}
}
func TestSnapshotChangeGapEpochAndDiskFailure(t *testing.T) {
	repo := store.NewMemoryStore()
	b := box(t, repo)
	p := &projection{rows: []emailmanagement.MessageView{row(b, "a", "First"), row(b, "b", "Second")}}
	root := t.TempDir()
	s, _ := New(root, readRepository(repo), p)
	first := mustSync(t, s, b, "", 1)
	p.rows[0].Subject = "New"
	if _, e := s.Sync(t.Context(), "owner", b.ID, first.Cursor, 1); !errors.Is(e, ErrReset) {
		t.Fatalf("mixed snapshot allowed %v", e)
	}
	stable := mustSync(t, s, b, "", 100)
	state, e := s.read("owner", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	state.Sequence = MaxEvents + 10
	state.Floor = state.Sequence - 1
	state.Events = []Event{}
	if e = s.persist(state); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Sync(t.Context(), "owner", b.ID, stable.Cursor, 10); !errors.Is(e, ErrReset) {
		t.Fatalf("gap not rejected %v", e)
	}
	if e = os.Remove(filepath.Join(root, scope("owner", b.ID)+".json")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Sync(t.Context(), "owner", b.ID, stable.Cursor, 10); !errors.Is(e, ErrReset) {
		t.Fatalf("epoch reset not detected %v", e)
	}
	fail := filepath.Join(t.TempDir(), "not-a-directory")
	if e = os.WriteFile(fail, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	bad, _ := New(fail, readRepository(repo), p)
	if _, e = bad.Sync(t.Context(), "owner", b.ID, "", 10); e == nil {
		t.Fatal("disk failure claimed synchronized")
	}
}
func TestLimitsAndNarrowProjection(t *testing.T) {
	repo := store.NewMemoryStore()
	b := box(t, repo)
	v := row(b, "a", "Subject")
	v.BodyText = strings.Repeat("x", 65*1024)
	v.Verification = &emailmanagement.VerificationView{Purpose: "never-cache"}
	p := &projection{rows: []emailmanagement.MessageView{v}}
	s, _ := New(t.TempDir(), readRepository(repo), p)
	out := mustSync(t, s, b, "", 100)
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "never-cache") || !out.Events[0].Mail.BodyTruncated || out.Events[0].Mail.BodyText != "" {
		t.Fatal("projection failed field/size whitelist")
	}
	p.rows[0].Subject = strings.Repeat("s", MaxMailBytes)
	if _, e := s.Sync(t.Context(), "owner", b.ID, out.Cursor, 100); !errors.Is(e, ErrLimit) {
		t.Fatalf("oversized mail accepted %v", e)
	}
	for _, limit := range []int{0, 101} {
		if _, e := s.Sync(t.Context(), "owner", b.ID, "", limit); !errors.Is(e, ErrInvalid) {
			t.Fatal("invalid limit accepted")
		}
	}
}

func TestSnapshotEntireEncodedPageFitsTrustedOneMiBTransport(t *testing.T) {
	repo := store.NewMemoryStore()
	b := box(t, repo)
	p := &projection{}
	for i := 0; i < 10; i++ {
		p.rows = append(p.rows, row(b, fmt.Sprintf("mail-%d", i), strings.Repeat("s", 220*1024)))
	}
	s, err := New(t.TempDir(), readRepository(repo), p)
	if err != nil {
		t.Fatal(err)
	}
	cursor, received, pages := "", 0, 0
	for {
		out := mustSync(t, s, b, cursor, 100)
		encoded, err := json.Marshal(out)
		if err != nil || len(encoded) > MaxPageBytes {
			t.Fatalf("oversized complete envelope %d: %v", len(encoded), err)
		}
		received += len(out.Events)
		pages++
		if !out.More {
			break
		}
		cursor = out.Cursor
		if pages > 10 {
			t.Fatal("bounded snapshot did not progress")
		}
	}
	if received != 10 || pages < 2 {
		t.Fatalf("snapshot not fully paged: mails=%d pages=%d", received, pages)
	}
}

type noBrowser struct{}

func (noBrowser) AdmitIntake(context.Context, string, string) (app.EmailAdmissionBinding, error) {
	return app.EmailAdmissionBinding{}, fmt.Errorf("browser prohibited")
}
func (noBrowser) DiscoverForOwner(context.Context, string, app.EmailReadRequest) (app.EmailDiscoveryResult, error) {
	return app.EmailDiscoveryResult{}, fmt.Errorf("browser prohibited")
}
func TestFileBackendAuthoritativeCollectorContinuesWithoutClient(t *testing.T) {
	root := t.TempDir()
	repo, e := store.NewFileStore(filepath.Join(root, "repository.json"))
	if e != nil {
		t.Fatal(e)
	}
	b := box(t, repo)
	projection, e := emailmanagement.New(repo, noBrowser{}, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: root})
	if e != nil {
		t.Fatal(e)
	}
	s, _ := New(filepath.Join(root, "sync"), readRepository(repo), projection)
	admit := func(id string) {
		t.Helper()
		_, e := repo.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: id}, MailboxID: b.ID, BindingGeneration: b.BindingGeneration, ObservedAt: time.Now(), Members: []store.EmailDiscoveryMember{{ProviderMessageID: id, ProviderSelectionID: id, Direction: "inbound", Folder: "inbox", SourceTime: time.Now()}}, Coverage: "partial"})
		if e != nil {
			t.Fatal(e)
		}
	}
	admit("first")
	first := mustSync(t, s, b, "", 100)
	if len(first.Events) != 1 {
		t.Fatal("typed authoritative mail missing")
	}
	admit("while-offline")
	restarted, e := store.NewFileStore(filepath.Join(root, "repository.json"))
	if e != nil {
		t.Fatal(e)
	}
	projection, e = emailmanagement.New(restarted, noBrowser{}, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: root})
	if e != nil {
		t.Fatal(e)
	}
	s, _ = New(filepath.Join(root, "sync"), readRepository(restarted), projection)
	later := mustSync(t, s, b, first.Cursor, 100)
	if len(later.Events) != 1 || later.Events[0].Deleted || later.Sequence != 2 {
		t.Fatalf("offline collector update lost %+v", later)
	}
	records, e := restarted.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "owner", MailboxID: b.ID, Limit: 100})
	if e != nil || len(records.Items) != 2 {
		t.Fatalf("sync mutated authoritative records: %v %+v", e, records)
	}
}
