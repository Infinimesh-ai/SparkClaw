package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func scopeClarificationTest(t *testing.T, r Runtime, st *store.MemoryStore, session app.Session, signer testClarificationSigner, id, request, text string, record *clarification.Record) Runtime {
	t.Helper()
	messages := storetest.MustListMessages(t, st, session.ID)
	messages = append(messages, app.Message{ID: id, SessionID: session.ID, Role: "user", Content: text, CreatedAt: time.Now()})
	snapshot := ClarificationSnapshot(messages)
	digest, err := clarification.ContextDigest(snapshot, "web")
	if err != nil {
		t.Fatal(err)
	}
	var records []clarification.Record
	if record != nil {
		records = []clarification.Record{*record}
	}
	return r.WithClarificationScope(signer, clarification.Binding{OwnerID: session.OwnerID, ClientID: "desktop", InstallationID: "installation", ConversationID: session.ID}, request, records).WithClarificationContext(id, digest).WithClarificationSnapshot(snapshot, "web")
}
func TestPureDeliveryHistoricalFileBypassesScoringAndVersionsOutput(t *testing.T) {
	root := t.TempDir()
	input := []byte("original file\n")
	rel := "conversation-files/original/report.md"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), input, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "report.md"), []byte("existing output"), 0600); err != nil {
		t.Fatal(err)
	}
	r, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	_, err := st.AddMessage(t.Context(), app.Message{ID: "prior", SessionID: session.ID, Role: "assistant", Content: "文件已准备好。", CreatedAt: time.Now().Add(-time.Minute), Attachments: []app.MessageAttachment{{ArtifactID: "original", Name: "report.md", RelPath: rel, Source: "conversation_result", SHA256: clarification.TextDigest(string(input)), Bytes: len(input), ContentType: "text/markdown"}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.HandleMessage(t.Context(), session.ID, "把刚才的文件发给我")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "completed" || result.RouteDecision == nil || result.RouteDecision.Slots.Operation != app.RouteOperationPublish {
		t.Fatalf("pure delivery did not complete: %+v %s", result.RouteDecision, result.Message.Content)
	}
	if result.WorkflowResult == nil || len(result.WorkflowResult.Content.Parts) != 1 || result.WorkflowResult.Content.Parts[0].Resource.Ref != "report-2.md" {
		t.Fatalf("output=%+v", result.WorkflowResult)
	}
	for _, call := range testListModelCalls(st, session.ID, result.Run.ID) {
		if call.Operation == "intent_tree_graph" || call.Operation == "intent_tree_graph_repair" || call.Operation == "intent_embedding" {
			t.Fatalf("pure delivery called scoring: %+v", call)
		}
	}
	preserved, _ := os.ReadFile(filepath.Join(root, rel))
	copy, _ := os.ReadFile(filepath.Join(root, "report-2.md"))
	if string(preserved) != string(input) || string(copy) != string(input) {
		t.Fatal("publication did not preserve input bytes")
	}
}
func TestSignedFileSelectionRunsOriginalRequestOnlyOnce(t *testing.T) {
	root := t.TempDir()
	r, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	attachments := make([]app.MessageAttachment, 0, 2)
	for _, id := range []string{"file_a", "file_b"} {
		rel := "conversation-files/" + id + "/report.md"
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(id), 0400); err != nil {
			t.Fatal(err)
		}
		attachments = append(attachments, app.MessageAttachment{ArtifactID: id, Name: "report.md", RelPath: rel, Source: "conversation_result", SHA256: clarification.TextDigest(id), Bytes: len(id), ContentType: "text/markdown"})
	}
	_, err := st.AddMessage(t.Context(), app.Message{ID: "files", SessionID: session.ID, Role: "assistant", Content: "两个文件。", CreatedAt: time.Now().Add(-time.Minute), Attachments: attachments})
	if err != nil {
		t.Fatal(err)
	}
	signer := testClarificationSigner{key: make([]byte, 32), claims: &sync.Map{}}
	scoped := scopeClarificationTest(t, r, st, session, signer, "original_request", "req1", "把刚才的文件发给我", nil)
	first, err := scoped.HandleMessageWithAttachmentsIdempotent(t.Context(), session.ID, "original_request", "run1", "把刚才的文件发给我", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Clarification == nil || first.Clarification.SchemaVersion != 2 || len(first.Clarification.Options) != 2 || !strings.Contains(first.Message.Content, "1. report.md") {
		t.Fatalf("missing signed file choice: %+v %s", first.Clarification, first.Message.Content)
	}
	next := scopeClarificationTest(t, r, st, session, signer, "choice", "req2", "2", first.Clarification)
	second, err := next.HandleMessageWithAttachmentsIdempotent(t.Context(), session.ID, "choice", "run2", "2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.State != "completed" || len(second.Message.Attachments) != 1 {
		t.Fatalf("selection did not execute original: %s %+v", second.Message.Content, second.RouteDecision)
	}
	got, _ := os.ReadFile(filepath.Join(root, second.Message.Attachments[0].RelPath))
	if string(got) != "file_b" {
		t.Fatalf("selected wrong original: %q", got)
	}
	again, err := next.HandleMessageWithAttachmentsIdempotent(context.Background(), session.ID, "choice", "run2", "2", nil)
	if err != nil || again.Message.ID != second.Message.ID {
		t.Fatalf("same request replay changed result: %v", err)
	}
	selected := &clarificationSelection{record: *first.Clarification, option: first.Clarification.Options[0]}
	conflicting := r.WithClarificationScope(signer, next.clarification.binding, "other", nil)
	if conflicting.claimClarification(selected) == nil {
		t.Fatal("another request reused the choice")
	}
}

func TestPureDeliveryCurrentAttachmentCreatesIndependentOutput(t *testing.T) {
	for _, query := range []string{"把这个文件发给我", "把刚才的文件发给我"} {
		t.Run(query, func(t *testing.T) {
			root := t.TempDir()
			data := []byte("current original")
			if err := os.WriteFile(filepath.Join(root, "note.md"), data, 0600); err != nil {
				t.Fatal(err)
			}
			r, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
			defer closeRuntime()
			result, err := r.HandleMessageWithAttachments(t.Context(), session.ID, query, []app.MessageAttachment{{ArtifactID: "input", Name: "note.md", RelPath: "note.md", ContentType: "text/markdown", SHA256: clarification.TextDigest(string(data)), Bytes: len(data)}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Run.State != "completed" || len(result.Message.Attachments) != 1 || result.Message.Attachments[0].RelPath != "note-2.md" {
				t.Fatalf("current input disappeared from outputs: %+v", result)
			}
			original, _ := os.ReadFile(filepath.Join(root, "note.md"))
			copy, _ := os.ReadFile(filepath.Join(root, "note-2.md"))
			if string(original) != string(data) || string(copy) != string(data) {
				t.Fatal("current file publication changed bytes")
			}
		})
	}
}

func TestPureDeliveryIncompleteCurrentFileSetCannotSelectSurvivingPeer(t *testing.T) {
	root := t.TempDir()
	data := []byte("surviving original")
	if err := os.WriteFile(filepath.Join(root, "note.md"), data, 0600); err != nil {
		t.Fatal(err)
	}
	r, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	ctx := WithSourceMessageFilesUnavailable(t.Context(), true)
	result, err := r.HandleMessageWithAttachments(ctx, session.ID, "把这个文件发给我", []app.MessageAttachment{{ArtifactID: "input", Name: "note.md", RelPath: "note.md", ContentType: "text/markdown", SHA256: clarification.TextDigest(string(data)), Bytes: len(data)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.Attachments) != 0 || !strings.Contains(result.Message.Content, "重新附加") {
		t.Fatalf("incomplete group silently selected surviving peer: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "note-2.md")); !os.IsNotExist(err) {
		t.Fatal("incomplete file set created a delivery copy")
	}
}

func TestConversationFileSplitCopiesAnExplicitCurrentAttachment(t *testing.T) {
	root := t.TempDir()
	data := []byte("reattached current file")
	if err := os.WriteFile(filepath.Join(root, "note.md"), data, 0600); err != nil {
		t.Fatal(err)
	}
	r, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	run := app.AgentRun{SessionID: session.ID}
	documents := documentContextResolution{References: []documentContextReference{{HandleID: "reattached", Name: "note.md", Ref: "note.md", SHA256: clarification.TextDigest(string(data)), Size: len(data)}}}
	routing, err := r.routePureDelivery(t.Context(), run, "把这个文件发给我", requestSplit{DeliveryMode: "current_route", DeliveryObjectKind: "conversation_file"}, DeliveryDirective{}, nil, nil, app.MessageSourceWeb, invocationHistory{}, documents, nil, "")
	if err != nil || routing.Publication == nil || len(routing.Publication.Parts) != 1 {
		t.Fatalf("current file was not bound: %+v %v", routing, err)
	}
	part, err := r.governWorkflowRequestPart(t.Context(), run, routing.Publication.Parts[0])
	if err != nil || part.Resource.Ref != "note-2.md" {
		t.Fatalf("conversation_file split reused an input excluded from result collection: %+v %v", part, err)
	}
	copy, err := os.ReadFile(filepath.Join(root, part.Resource.Ref))
	if err != nil || string(copy) != string(data) {
		t.Fatalf("independent result bytes differ: %v", err)
	}
}
