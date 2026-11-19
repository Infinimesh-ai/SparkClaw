package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type conversationSyncBackend interface {
	SessionRepository
	ConversationRepository
	ConversationSyncRepository
}

func conversationSyncBackends(t *testing.T) map[string]conversationSyncBackend {
	backends := map[string]conversationSyncBackend{"memory": NewMemoryStore()}
	if dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN"); dsn != "" {
		pg, err := NewPostgresStore(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pg.Close)
		backends["postgres"] = pg
	}
	return backends
}

func TestConversationSyncFeedPagingAndTombstones(t *testing.T) {
	for name, s := range conversationSyncBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			owner := "owner-sync-" + strings.ToLower(app.NewID("o"))
			start, err := s.SessionChanges(ctx, owner, 0, 100)
			if err != nil || len(start) != 0 {
				t.Fatalf("fresh owner changes %v %v", start, err)
			}
			a, err := s.CreateSessionWithScope(ctx, "A", owner, "", "link", false)
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.CreateSessionWithScope(ctx, "B", owner, "", "webchat", false)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 7; i++ {
				m := app.Message{SessionID: a.ID, Role: "user", Content: strings.Repeat("x", i+1), CreatedAt: time.Now().Add(time.Duration(i) * time.Millisecond)}
				if i == 3 {
					m.ClientMessageID = "client-key-3"
				}
				if _, err := s.AddMessage(ctx, m); err != nil {
					t.Fatal(err)
				}
			}
			changes, err := s.SessionChanges(ctx, owner, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(changes) != 2 || changes[len(changes)-1].Session.ID != a.ID {
				t.Fatalf("changes %+v", changes)
			}
			last := changes[len(changes)-1].Session
			if last.LastSeq != 7 || last.Preview != "xxxxxxx" || last.Revision <= changes[0].Session.Revision {
				t.Fatalf("header %+v", last)
			}
			cursor := last.Revision
			page, more, err := s.ListMessagePage(ctx, a.ID, 0, 0, 3)
			if err != nil || !more || len(page) != 3 || page[0].Seq != 5 || page[2].Seq != 7 {
				t.Fatalf("latest page %+v more=%v err=%v", page, more, err)
			}
			older, more, err := s.ListMessagePage(ctx, a.ID, page[0].Seq, 0, 3)
			if err != nil || !more || len(older) != 3 || older[0].Seq != 2 || older[2].Seq != 4 {
				t.Fatalf("older page %+v more=%v", older, more)
			}
			oldest, more, err := s.ListMessagePage(ctx, a.ID, older[0].Seq, 0, 3)
			if err != nil || more || len(oldest) != 1 || oldest[0].Seq != 1 {
				t.Fatalf("oldest page %+v more=%v", oldest, more)
			}
			newer, _, err := s.ListMessagePage(ctx, a.ID, 0, 5, 10)
			if err != nil || len(newer) != 2 || newer[0].Seq != 6 {
				t.Fatalf("after page %+v", newer)
			}
			found, ok, err := s.FindMessageByClientID(ctx, a.ID, "client-key-3")
			if err != nil || !ok || found.Seq != 4 {
				t.Fatalf("client lookup %+v %v %v", found, ok, err)
			}
			if _, err := s.SetSessionArchived(ctx, b.ID, true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteSession(ctx, a.ID); err != nil {
				t.Fatal(err)
			}
			after, err := s.SessionChanges(ctx, owner, cursor, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != 2 || after[0].Session.ID != b.ID || !after[0].Session.Archived || after[1].Session.ID != a.ID || !after[1].Deleted {
				t.Fatalf("after cursor %+v", after)
			}
			if after[1].Session.Revision <= after[0].Session.Revision {
				t.Fatal("tombstone revision not monotonic")
			}
			if _, ok, _ := s.GetSession(ctx, a.ID); ok {
				t.Fatal("deleted session still readable")
			}
			others, _ := s.SessionChanges(ctx, owner+"-other", 0, 100)
			if len(others) != 0 {
				t.Fatal("owner isolation broken")
			}
		})
	}
}

func TestFileStoreBackfillsConversationSync(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"
	fs, err := NewFileStoreWithOptions(FileStoreOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sess, err := fs.CreateSessionWithScope(ctx, "old", app.DefaultOwnerID, "", "webchat", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := fs.AddMessage(ctx, app.Message{SessionID: sess.ID, Role: "user", Content: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate state written before sync existed.
	fs.inner.mu.Lock()
	ms := fs.inner.messages[sess.ID]
	for i := range ms {
		ms[i].Seq = 0
	}
	h := fs.inner.sessions[sess.ID]
	h.Revision, h.LastSeq, h.Preview = 0, 0, ""
	fs.inner.sessions[sess.ID] = h
	fs.inner.mu.Unlock()
	if _, err := fs.SetSessionArchived(ctx, sess.ID, true); err != nil { // forces a persisted write
		t.Fatal(err)
	}
	reopened, err := NewFileStoreWithOptions(FileStoreOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	page, _, err := reopened.ListMessagePage(ctx, sess.ID, 0, 0, 10)
	if err != nil || len(page) != 3 || page[0].Seq != 1 || page[2].Seq != 3 {
		t.Fatalf("backfilled page %+v %v", page, err)
	}
	header, ok, _ := reopened.SessionHeader(ctx, sess.ID)
	if !ok || header.LastSeq != 3 || header.Revision == 0 || header.Preview != "m" {
		t.Fatalf("backfilled header %+v", header)
	}
}
