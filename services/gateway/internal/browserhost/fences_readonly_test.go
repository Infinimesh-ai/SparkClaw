package browserhost

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserFenceSnapshotNeverInitializesOrPersistsRecovery(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "control")
	rows, err := ReadFences(root, testIdentity)
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing snapshot %+v %v", rows, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("snapshot created directory", err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	fence := Fence{CommandID: "command", Scope: Scope{Identity: testIdentity, ConversationID: "conversation", TaskID: "task"}, Write: true, State: "dispatched", UpdatedAt: time.Now().UTC()}
	if err := persistFence(root, fence); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "command.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = ReadFences(root, testIdentity)
	if err != nil || len(rows) != 1 || rows[0].State != "unknown" || !rows[0].UpdatedAt.Equal(fence.UpdatedAt) {
		t.Fatalf("snapshot %+v %v", rows, err)
	}
	other := testIdentity
	other.ClientID = "other"
	rows, err = ReadFences(root, other)
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross-client %+v %v", rows, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("GET persisted interrupted fence")
	}
}
