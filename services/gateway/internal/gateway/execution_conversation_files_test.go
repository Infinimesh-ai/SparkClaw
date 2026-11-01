package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

func TestConversationFileMaterializationWaitsForAdmittedDigest(t *testing.T) {
	var service *execution.Service
	workspace := t.TempDir()
	data := []byte("original input")
	manifest := execution.File{ID: "11111111-1111-4111-8111-111111111112", Name: "report.md", Size: len(data), SHA256: execution.Digest(data)}
	result := make(chan error, 1)
	var err error
	service, err = execution.New(filepath.Join(t.TempDir(), "control"), func(ctx context.Context, e execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		m := newConversationFileMaterializer(workspace, service, e, execution.NewBudget(execution.TaskBytes))
		path := filepath.Join(workspace, "conversation-files", manifest.ID, manifest.Name)
		err := m.read(ctx, path)
		if err == nil {
			err = m.read(ctx, path)
		}
		result <- err
		return execution.Output{Content: "done"}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	e := execution.Envelope{SchemaVersion: 2, OwnerID: "owner", ClientID: "client", DeploymentID: "deployment", InstallationID: "11111111-1111-4111-8111-111111111111", ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444", Messages: []execution.Message{{ID: "fixture-message-35-1", Role: "user", Content: "before", Files: []execution.File{manifest}}, {ID: "fixture-message-35-2", Role: "user", Content: "read it"}}}
	if err = service.Bind(e.OwnerID, e.ClientID, e.InstallationID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	if _, err = service.Submit(t.Context(), e, execution.Digest(raw)); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for {
		status, err := service.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.PendingInputs) == 1 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("materialization did not request a file")
		}
		time.Sleep(time.Millisecond)
	}
	if err = service.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, manifest.ID, manifest.SHA256, data); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	service.Wait()
	got, err := os.ReadFile(filepath.Join(workspace, "conversation-files", manifest.ID, manifest.Name))
	if err != nil || string(got) != string(data) {
		t.Fatalf("read %q: %v", got, err)
	}
}

func TestMaterializedConversationFileRejectsLaterTampering(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "conversation-files", "id", "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	file := execution.File{ID: "id", Name: "report.md", Size: 4, SHA256: execution.Digest([]byte("safe"))}
	m := &conversationFileMaterializer{byPath: map[string]execution.File{path: file}, ready: map[string]bool{file.ID: true}}
	if err := os.WriteFile(path, []byte("evil"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.read(t.Context(), path); err == nil {
		t.Fatal("changed materialized bytes were accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "outside"), path); err != nil {
		t.Fatal(err)
	}
	if err := m.read(t.Context(), path); err == nil {
		t.Fatal("symlink was accepted")
	}
}
