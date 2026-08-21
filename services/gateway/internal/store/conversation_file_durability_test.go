package store

import (
	"reflect"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileConversationDefiniteFailureRestoresAtomicWrite(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, store, "New SparkClaw Session")
	before := store.captureFileRollback()
	store.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}

	stored, err := store.AddMessage(t.Context(), app.Message{
		ID: "message-rollback", SessionID: session.ID, Role: "user", Content: "must roll back",
	})
	if stored.ID != "" || StoreErrorCodeOf(err) != StoreErrorDurability || !errorsIsFileCommitInjected(err) {
		t.Fatalf("stored=%#v err=%v code=%q", stored, err, StoreErrorCodeOf(err))
	}
	if after := store.captureFileRollback(); !reflect.DeepEqual(after, before) {
		t.Fatal("failed message append did not restore message, session, and event state")
	}
	restarted, err := NewFileStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if messages := mustListMessages(t, restarted, session.ID); len(messages) != 0 {
		t.Fatalf("failed message survived restart = %#v", messages)
	}
	if head := mustMessageEventHead(t, restarted, session.ID); head != "" {
		t.Fatalf("failed message event survived restart = %q", head)
	}
}
