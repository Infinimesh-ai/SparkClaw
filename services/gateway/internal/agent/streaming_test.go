package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestConversationAnswerStreamsItsOnlyFinalGeneration(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()

	events := []StreamEvent{}
	result, err := runtime.HandleMessageStream(
		context.Background(),
		session.ID,
		"法国的首都是什么？\nMOCK_CONVERSATION_RESPONSE:巴黎。",
		func(event StreamEvent) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got := streamedText(events); got != result.Message.Content || got != "巴黎。" {
		t.Fatalf("streamed answer = %q, final message = %q", got, result.Message.Content)
	}
	assertSingleFinalGeneration(t, testListModelCalls(st, session.ID, result.Run.ID), "workflow_answer")
	assertStreamSpan(t, events, session.ID, result.Run.ID, "workflow_answer")
}

func TestDocumentAnswerStreamsWorkflowFinalizerWithoutRenderer(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	path := filepath.Join(session.WorkspaceRoot, "stream-note.txt")
	if err := os.WriteFile(path, []byte("SparkClaw streams the final workflow answer once.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	events := []StreamEvent{}
	result, err := runtime.HandleMessageStream(
		context.Background(),
		session.ID,
		`Summarize stream-note.txt
MOCK_WORKFLOW_FINAL_RESPONSE:{"type":"final","answer":"The workflow answer was streamed once."}`,
		func(event StreamEvent) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got := streamedText(events); got != result.Message.Content || got != "The workflow answer was streamed once." {
		t.Fatalf("streamed answer = %q, final message = %q", got, result.Message.Content)
	}
	assertSingleFinalGeneration(t, testListModelCalls(st, session.ID, result.Run.ID), "workflow_final_answer")
	assertStreamSpan(t, events, session.ID, result.Run.ID, "workflow_final_answer")
}

func streamedText(events []StreamEvent) string {
	var answer strings.Builder
	for _, event := range events {
		if event.Type == "text_delta" {
			answer.WriteString(event.Text)
		}
	}
	return answer.String()
}

func assertSingleFinalGeneration(t *testing.T, calls []app.ModelCall, operation string) {
	t.Helper()
	count := 0
	for _, call := range calls {
		if call.Operation == "final_answer_stream" {
			t.Fatalf("obsolete final renderer was called: %#v", calls)
		}
		if call.Operation == operation {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%s call count = %d, want 1: %#v", operation, count, calls)
	}
}

func assertStreamSpan(t *testing.T, events []StreamEvent, sessionID, runID, spanID string) {
	t.Helper()
	deltas := 0
	done := 0
	for _, event := range events {
		if event.SessionID != sessionID || event.RunID != runID || event.SpanID != spanID {
			t.Fatalf("stream event has incorrect identity: %#v", event)
		}
		switch event.Type {
		case "text_delta":
			deltas++
		case "done":
			done++
		default:
			t.Fatalf("unexpected stream event: %#v", event)
		}
	}
	if deltas == 0 || done != 1 {
		t.Fatalf("stream events have %d deltas and %d done events: %#v", deltas, done, events)
	}
}
