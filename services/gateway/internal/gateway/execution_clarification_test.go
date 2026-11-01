package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

func TestExecutionClarificationContextSurvivesCurrentInputPromotion(t *testing.T) {
	file := execution.File{ID: "11111111-1111-4111-8111-111111111111", Name: "原件.md", Size: 1, SHA256: execution.Digest([]byte("x"))}
	envelope := execution.Envelope{Messages: []execution.Message{{ID: "history", Role: "assistant", Content: "earlier", FilesUnavailable: true}, {ID: "source", Role: "user", Content: "完善一下发给我"}}, InputFiles: []execution.File{file}}
	before, err := executionClarificationSnapshot(envelope)
	if err != nil {
		t.Fatal(err)
	}
	original, err := clarification.ContextDigest(before, string(app.MessageSourceWeb))
	if err != nil {
		t.Fatal(err)
	}
	// Selection replay moves the original task's input manifest into its owner
	// message. Newly appended turns are outside the original signed prefix.
	envelope.Messages[1].Files = []execution.File{file}
	envelope.InputFiles = nil
	envelope.Messages = append(envelope.Messages, execution.Message{ID: "question", Role: "assistant", Content: "1. 编辑", Clarification: &clarification.Record{ID: "ignored-metadata"}}, execution.Message{ID: "selection", Role: "user", Content: "1"})
	after, err := executionClarificationSnapshot(envelope)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := clarification.ContextDigest(after[:2], string(app.MessageSourceWeb))
	if err != nil || restored != original {
		t.Fatalf("promotion changed signed context: %s %s %v", original, restored, err)
	}
	if before[1].Files[0].Source != "conversation_attachment" || !before[0].FilesUnavailable {
		t.Fatal("ownership or missing marker lost")
	}
	envelope.Messages[0].FilesUnavailable = false
	changed, _ := executionClarificationSnapshot(envelope)
	digest, _ := clarification.ContextDigest(changed[:2], string(app.MessageSourceWeb))
	if digest == original {
		t.Fatal("missing marker not bound")
	}
}

func TestExecutionClarificationCurrentFileDeduplicatesOnlyMatchingMetadata(t *testing.T) {
	file := execution.File{ID: "11111111-1111-4111-8111-111111111111", Name: "original.md", Size: 1, SHA256: execution.Digest([]byte("x"))}
	envelope := execution.Envelope{Messages: []execution.Message{{ID: "source", Role: "user", Content: "edit", Files: []execution.File{file}}}, InputFiles: []execution.File{file}}
	snapshot, err := executionClarificationSnapshot(envelope)
	if err != nil || len(snapshot[0].Files) != 1 {
		t.Fatalf("duplicate=%+v %v", snapshot, err)
	}
	envelope.InputFiles[0].Name = "changed.md"
	if _, err := executionClarificationSnapshot(envelope); err == nil {
		t.Fatal("conflicting current ownership accepted")
	}
}

func TestConversationMaterializerWaitingReaderCanCancelIndependently(t *testing.T) {
	file := execution.File{ID: "file", Name: "original.md", Size: 1, SHA256: execution.Digest([]byte("x"))}
	path := "/ephemeral/conversation-files/file/original.md"
	pending := make(chan struct{})
	materializer := &conversationFileMaterializer{byPath: map[string]execution.File{path: file}, inflight: map[string]chan struct{}{file.ID: pending}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- materializer.read(ctx, path) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled reader=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader waited behind another upload despite cancellation")
	}
}
