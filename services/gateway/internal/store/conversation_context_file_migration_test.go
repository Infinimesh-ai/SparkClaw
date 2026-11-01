package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileConversationContextMigrationPreservesSnapshotAndFencesOldDecoder(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		name := "plaintext"
		if encrypted {
			name = "encrypted"
		}
		t.Run(name, func(t *testing.T) {
			opts := FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json"), EncryptAtRest: encrypted, EncryptionKey: "context-migration-test-only"}
			st, err := NewFileStoreWithOptions(opts)
			if err != nil {
				t.Fatal(err)
			}
			session := mustCreateSession(t, st, "original title")
			mustAddMessage(t, st, app.Message{ID: "original-message", SessionID: session.ID, Role: "assistant", Content: "original question", FilesUnavailable: true, Clarification: persistedClarification(t)})
			before := st.inner.snapshot()
			want, err := json.Marshal(before)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(want, &fields); err != nil {
				t.Fatal(err)
			}
			fields["messages"], err = json.Marshal(before.Messages)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if encrypted {
				legacy, err = st.encryption.encrypt(legacy)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(opts.Path, legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			st, err = NewFileStoreWithOptions(opts)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := json.Marshal(st.inner.snapshot())
			if err != nil || !bytes.Equal(actual, want) {
				t.Fatalf("legacy decode changed full snapshot: %v", err)
			}
			assertBytes := func(want []byte) {
				t.Helper()
				raw, err := os.ReadFile(opts.Path)
				if err != nil || !bytes.Equal(raw, want) {
					t.Fatalf("original file overwritten: %v", err)
				}
			}
			assertBytes(legacy)
			st.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1}
			_, err = st.AddMessage(t.Context(), app.Message{ID: "new-message", SessionID: session.ID, Role: "user", Content: "new request"})
			if err == nil {
				t.Fatal("injected write failure succeeded")
			}
			assertBytes(legacy)
			actual, err = json.Marshal(st.inner.snapshot())
			if err != nil || !bytes.Equal(actual, want) {
				t.Fatal("failed migration changed in-memory snapshot")
			}
			mustAddMessage(t, st, app.Message{ID: "new-message", SessionID: session.ID, Role: "user", Content: "new request"})
			raw, err := os.ReadFile(opts.Path)
			if err != nil {
				t.Fatal(err)
			}
			if encrypted {
				raw, err = st.encryption.decrypt(raw)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Equivalent to the old binary's messages member: no version-aware
			// decoding, so a numeric schema version must fail before any write.
			var old struct {
				Messages map[string][]app.Message `json:"messages"`
			}
			if err := json.Unmarshal(raw, &old); err == nil {
				t.Fatal("old binary accepted versioned messages")
			}
			restarted, err := NewFileStoreWithOptions(opts)
			if err != nil {
				t.Fatal(err)
			}
			got := mustListMessages(t, restarted, session.ID)
			if len(got) != 2 || got[0].Clarification.MAC != before.Messages[session.ID][0].Clarification.MAC || !got[0].FilesUnavailable {
				t.Fatalf("new snapshot lost old context: %#v", got)
			}
		})
	}
}

func TestFileConversationContextRejectsFutureOrMalformedVersionWithoutOverwriting(t *testing.T) {
	for _, messages := range []string{
		`{"schema_version":3,"by_session":{}}`,
		`{"schema_version":2}`,
		`{"schema_version":2,"by_session":{},"unknown":true}`,
		`{"schema_version":2,"by_session":[]}`,
	} {
		t.Run(messages, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "state.json")
			raw := []byte(`{"messages":` + messages + `}`)
			if err := os.WriteFile(filename, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewFileStore(filename); err == nil {
				t.Fatal("unsupported snapshot was accepted")
			}
			after, err := os.ReadFile(filename)
			if err != nil || !bytes.Equal(after, raw) {
				t.Fatalf("rejected snapshot changed: %v", err)
			}
		})
	}
	var legacy Snapshot
	if err := json.Unmarshal([]byte(`{"messages":{"schema_version":[{"id":"original"}],"by_session":[]}}`), &legacy); err != nil || legacy.Messages["schema_version"][0].ID != "original" {
		t.Fatalf("reserved-looking legacy session was lost: %#v %v", legacy.Messages, err)
	}
}
