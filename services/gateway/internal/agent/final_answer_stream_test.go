package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

func TestFinalAnswerMarkdownStreamsBraceAndCodeFenceBeforeDone(t *testing.T) {
	for _, first := range []string{"{", "```go\n", " ", "你好😀"} {
		t.Run(first, func(t *testing.T) {
			var events []StreamEvent
			s := workflowFinalAnswerStream{format: FinalAnswerMarkdown, forward: func(e modelrouter.ModelStreamEvent) error { events = append(events, e); return nil }}
			if err := s.emit(StreamEvent{Type: "text_delta", Text: first}); err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Text != first {
				t.Fatal("first answer bytes waited for classification/done")
			}
			long := strings.Repeat("正文", 6000)
			if err := s.emit(StreamEvent{Type: "text_delta", Text: long}); err != nil {
				t.Fatal(err)
			}
			if streamedText(events) != first+long {
				t.Fatal("long answer truncated")
			}
			for _, typ := range []string{"reasoning_delta", "tool_call_delta"} {
				_ = s.emit(StreamEvent{Type: typ, Text: "private"})
			}
			if len(events) != 2 {
				t.Fatal("internal event reached answer")
			}
		})
	}
}

func TestFinalAnswerStructuredWaitsForFullStrictValidation(t *testing.T) {
	for _, raw := range []string{`{"type":"final","answer":"visible"}`, `{"type":"final","answer":"visible","reasoning":"private"}`, `{"type":"final","answer":"visible","answer":"other"}`, `{"type":"action","answer":"private"}`, `{"type":"final","answer":"\ud800"}`, "```json\n{\"type\":\"final\",\"answer\":\"visible\"}\n```", `{"type":"final","answer":"visible"} trailing`} {
		t.Run(raw, func(t *testing.T) {
			var events []StreamEvent
			s := workflowFinalAnswerStream{format: FinalAnswerStructured, forward: func(e modelrouter.ModelStreamEvent) error { events = append(events, e); return nil }}
			for _, r := range raw {
				if err := s.emit(StreamEvent{Type: "text_delta", Text: string(r)}); err != nil {
					t.Fatal(err)
				}
			}
			if len(events) != 0 {
				t.Fatal("structured field leaked before validation")
			}
			err := s.emit(StreamEvent{Type: "done"})
			if raw == `{"type":"final","answer":"visible"}` {
				if err != nil || streamedText(events) != "visible" || len(events) != 2 {
					t.Fatal(events, err)
				}
			} else if err == nil || len(events) != 0 {
				t.Fatal("invalid wrapper published", events, err)
			}
		})
	}
}

func TestRuntimeExplicitMarkdownUsesProductionFinalAnswerStream(t *testing.T) {
	// The existing mock marker consumes one line; multiline fences are covered
	// by the incremental emitter test above.
	for _, answer := range []string{"{ordinary brace text}", "```go fmt.Println(\"你好\") ```"} {
		t.Run(answer, func(t *testing.T) {
			runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
			defer closeRuntime()
			var events []StreamEvent
			starts := 0
			ctx := WithFinalAnswerStart(WithFinalAnswerFormat(t.Context(), FinalAnswerMarkdown), func() { starts++ })
			result, err := runtime.HandleMessageStream(ctx, session.ID, "Say hello.\nMOCK_CONVERSATION_RESPONSE:"+answer, func(e StreamEvent) error {
				if starts != 1 {
					t.Error("answer text escaped before generation start")
				}
				if e.Type == "done" && streamedText(events) == "" {
					t.Error("production final stream buffered until done")
				}
				events = append(events, e)
				return nil
			})
			if err != nil || streamedText(events) != answer || result.Message.Content != answer {
				t.Fatalf("production content %q final %q error %v", streamedText(events), result.Message.Content, err)
			}
			assertSingleFinalGeneration(t, testListModelCalls(st, session.ID, result.Run.ID), "workflow_answer")
		})
	}
}
