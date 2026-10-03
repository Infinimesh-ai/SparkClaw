package gateway

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3mail"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// This fixture deletes only synthetic typed mail. Client-cache deletion never
// calls this command: authoritative writes retain existing CAS/key semantics.
func TestR3MailTypedDeleteConflictReplayAndDurableTombstone(t *testing.T) {
	f := newEmailHTTPFixture(t)
	mail := f.assign(f.receive("r3-delete-fixture", time.Now()), "")
	projection, e := emailmanagement.New(f.repo, f.browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: f.root})
	f.must(e)
	root := filepath.Join(f.root, "r3-mail")
	sync, e := r3mail.New(root, f.repo, projection)
	f.must(e)
	initial, e := sync.Sync(t.Context(), f.owner, f.box.ID, "", 100)
	f.must(e)
	if len(initial.Events) != 1 || initial.Events[0].Mail.BodyText != "unique-contract-term-r3-delete-fixture" || len(initial.Events[0].Mail.Attachments) != 1 {
		t.Fatalf("incomplete projection %+v", initial)
	}
	conversation, found, e := f.repo.GetEmailConversation(t.Context(), f.owner, mail.ConversationID)
	f.must(e)
	if !found {
		t.Fatal("fixture conversation missing")
	}
	command := store.EmailConversationDelete{EmailCommand: f.command(), ConversationID: conversation.ID, ExpectedVersion: conversation.InputVersion - 1}
	if _, e = f.repo.DeleteEmailConversation(t.Context(), command); store.StoreErrorCodeOf(e) != store.StoreErrorConflict {
		t.Fatalf("stale mail delete accepted %v", e)
	}
	command.ExpectedVersion = conversation.InputVersion
	deleted, e := f.repo.DeleteEmailConversation(t.Context(), command)
	f.must(e)
	replay, e := f.repo.DeleteEmailConversation(t.Context(), command)
	f.must(e)
	if !reflect.DeepEqual(deleted, replay) {
		t.Fatal("mail request-key replay changed")
	}
	command.ExpectedVersion++
	if _, e = f.repo.DeleteEmailConversation(t.Context(), command); store.StoreErrorCodeOf(e) != store.StoreErrorConflict {
		t.Fatalf("changed command-key replay accepted %v", e)
	}
	sync, e = r3mail.New(root, f.repo, projection)
	f.must(e)
	delta, e := sync.Sync(t.Context(), f.owner, f.box.ID, initial.Cursor, 100)
	f.must(e)
	if delta.Epoch != initial.Epoch || len(delta.Events) != 1 || !delta.Events[0].Deleted || delta.Events[0].Mail != nil || delta.Events[0].ID != mail.ID {
		t.Fatalf("durable tombstone missing %+v", delta)
	}
	if f.browser.calls != 0 {
		t.Fatal("mail projection invoked collector browser")
	}
}
