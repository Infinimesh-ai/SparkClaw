package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func writeRetrievalBook(t *testing.T, root string) {
	t.Helper()
	var book strings.Builder
	for chapter, topic := range []string{"物理层", "数据链路层", "网络层", "运输层", "应用层"} {
		fmt.Fprintf(&book, "# 第%d章 %s\n\n", chapter+1, topic)
		for section := 1; section <= 8; section++ {
			fmt.Fprintf(&book, "## %d.%d %s专题%d\n\n", chapter+1, section, topic, section)
			for paragraph := 0; paragraph < 12; paragraph++ {
				book.WriteString(strings.Repeat("本节通过实例分析协议的工作过程，并比较不同方案的取舍。", 6))
				if topic == "运输层" && section == 4 && paragraph == 2 {
					book.WriteString("慢开始算法让拥塞窗口按指数增长，直到达到慢开始门限 ssthresh。")
				}
				book.WriteString("\n\n")
			}
		}
	}
	if book.Len() <= document.SmallExtractedMaxBytes {
		t.Fatalf("fixture is %d bytes; it must exceed the complete-read limit", book.Len())
	}
	if err := os.WriteFile(filepath.Join(root, "book.md"), []byte(book.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentReadRetrievesPassagesOfALargeDocument(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	writeRetrievalBook(t, session.WorkspaceRoot)

	result, err := runtime.HandleMessage(context.Background(), session.ID, "Summarize what book.md says about 慢开始")
	if err != nil {
		t.Fatal(err)
	}
	tools := []string{}
	var rangeCall app.ToolCall
	for _, call := range testListToolCalls(st, session.ID) {
		if call.RunID != result.Run.ID {
			continue
		}
		tools = append(tools, call.Tool)
		if call.Tool == "document.read_range" {
			rangeCall = call
		}
	}
	if strings.Join(tools, ",") != "document.outline,document.search,document.read_range" {
		t.Fatalf("retrieval did not read, search and read a range: %v", tools)
	}
	output, _ := anyMap(rangeCall.Result)
	if !strings.Contains(stringValue(output["content"]), "慢开始门限") {
		t.Fatalf("the range read missed the slow-start passage: %.300s", stringValue(output["content"]))
	}
	if result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusSucceeded ||
		result.Run.Workflow.Plan.ProfileRevision != 7 || result.Message.Content != "Mock workflow answer grounded in the completed document evidence." {
		t.Fatalf("retrieval did not finish through the finalizer: workflow=%#v answer=%q", result.Run.Workflow, result.Message.Content)
	}
	events, err := st.EventsAfter(context.Background(), session.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	progress := 0
	for _, event := range events {
		if call, ok := event.Payload.(app.ToolCall); ok && event.Type == "tool_call.started" && call.Tool == "document.outline" && call.Progress == "正在建立文档索引" {
			progress++
		}
	}
	if progress != 1 {
		t.Fatalf("index build progress events = %d, want 1", progress)
	}
	projection, err := runtime.workflowFinalEvidence(context.Background(), result.Run, testListToolCalls(st, session.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projection.modelPayload(), "慢开始门限") {
		t.Fatal("finalization evidence does not contain the retrieved passage")
	}
}

func TestDocumentEditRefusesALargeDocument(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	writeRetrievalBook(t, session.WorkspaceRoot)
	before, err := os.ReadFile(filepath.Join(session.WorkspaceRoot, "book.md"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := runtime.HandleMessage(context.Background(), session.ID, "把 book.md 里的“物理层”替换成“物理层次”")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message.Content, "这个文档太大，暂不支持直接修改") {
		t.Fatalf("large-document edit reply = %q", result.Message.Content)
	}
	after, _ := os.ReadFile(filepath.Join(session.WorkspaceRoot, "book.md"))
	if string(after) != string(before) {
		t.Fatal("the large document was modified")
	}
	for _, call := range testListToolCalls(st, session.ID) {
		if call.RunID == result.Run.ID && call.Tool == "text.replace_text" {
			t.Fatalf("an editor ran on a large document: %#v", call)
		}
	}
}

func TestDocumentReadSummarizesAWholeLargeDocumentBySection(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	writeRetrievalBook(t, session.WorkspaceRoot)

	result, err := runtime.HandleMessage(context.Background(), session.ID, "把 book.md 全文总结一下")
	if err != nil {
		t.Fatal(err)
	}
	tools := []string{}
	for _, call := range testListToolCalls(st, session.ID) {
		if call.RunID == result.Run.ID {
			tools = append(tools, call.Tool)
		}
	}
	if strings.Join(tools, ",") != "document.outline,document.summarize" || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("whole-document request did not summarize by section: tools=%v status=%v", tools, result.Run.Workflow.Status)
	}
	projection, err := runtime.workflowFinalEvidence(context.Background(), result.Run, testListToolCalls(st, session.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projection.modelPayload(), "## 第5章 应用层") {
		t.Fatal("finalization evidence does not contain the section summaries")
	}
}

func TestFailedRetrievalQueryLetsTheStageQueryAgain(t *testing.T) {
	call := app.ToolCall{ID: "tc_search", Tool: "document.search", Capability: app.ToolCapabilityDocumentSearch, Status: app.ToolCallStatusFailed, Error: `section "n0006" is not in this document outline`}
	outcome := adaptDocumentRetrievalOutcome(call, documentReadNodeID)
	assessment := documentReadProfileV5{}.Assess(nil, outcome)
	if assessment.Status != app.AssessmentNeedsMoreEvidence {
		t.Fatalf("a failed retrieval query blocked the node: %+v", assessment)
	}
}
