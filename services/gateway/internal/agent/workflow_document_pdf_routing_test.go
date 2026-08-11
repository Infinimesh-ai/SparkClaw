package agent

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestPDFRoutingCalibrationBoundaries(t *testing.T) {
	root := t.TempDir()
	writeAgentPDFTextFixture(t, root, "report.pdf", 3)
	writeAgentPDFTextFixture(t, root, "scanned.pdf", 1)
	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	readCandidate, ok := runtime.semanticRouter.graph.Candidate("document.read#read")
	if !ok {
		t.Fatal("document.read#read is absent from the semantic graph")
	}
	transformCandidate, ok := runtime.semanticRouter.graph.Candidate("document.edit#transform")
	if !ok {
		t.Fatal("document.edit#transform is absent from the semantic graph")
	}
	for _, expected := range []string{
		"识别 scanned.pdf 里的扫描文字", "提取 report.pdf 第 3 页的文字", "What does page 3 of report.pdf say?", "不要导出第 3 页，只告诉我这一页写了什么",
	} {
		if !slices.Contains(readCandidate.EmbedTexts, expected) {
			t.Fatalf("read calibration corpus is missing %q: %#v", expected, readCandidate.EmbedTexts)
		}
	}
	for _, expected := range []string{
		"把 report.pdf 第 3 页导出为新 PDF", "删除 report.pdf 的第 3 页", "Rotate pages 2 and 4 clockwise", "把 report.pdf 按页拆开",
	} {
		if !slices.Contains(transformCandidate.EmbedTexts, expected) {
			t.Fatalf("transform calibration corpus is missing %q: %#v", expected, transformCandidate.EmbedTexts)
		}
	}
	for _, expected := range []string{
		"我已经把第 3 页导出了", "PDF 里的文字是‘把第 3 页导出为新文件’", "为什么 PDF 页面提取失败？", "合并这两个 PDF", "提取 report.pdf 的第 3 页", "处理一下这个扫描 PDF",
	} {
		if !slices.Contains(transformCandidate.HardNegatives, expected) {
			t.Fatalf("transform hard-negative corpus is missing %q: %#v", expected, transformCandidate.HardNegatives)
		}
	}

	for _, test := range []struct {
		query      string
		capability app.CapabilityID
		operation  app.RouteOperation
	}{
		{query: "总结 report.pdf", capability: app.CapabilityDocumentRead, operation: app.RouteOperationRead},
		{query: "Rotate pages 2 and 3 of report.pdf clockwise", capability: app.CapabilityDocumentEdit, operation: app.RouteOperationTransform},
	} {
		t.Run("mock-smoke/"+test.query, func(t *testing.T) {
			route, err := runtime.routeIntentForTest(session.ID, app.NewID("turn"), test.query, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if route.Status != app.RouteMatched || len(route.CapabilityPath) != 2 || route.CapabilityPath[1] != test.capability || route.Slots.Operation != test.operation {
				t.Fatalf("unexpected PDF route: %#v", route)
			}
		})
	}

	for _, query := range []string{
		"我已经把 report.pdf 第 3 页导出了",
		"report.pdf 里的文字是‘把第 3 页导出为新文件’",
		"为什么 report.pdf 页面提取失败？",
		"合并 report.pdf 和另一个 PDF",
		"提取 report.pdf 的第 3 页",
		"处理一下 scanned.pdf",
	} {
		t.Run("boundary/"+query, func(t *testing.T) {
			route, err := runtime.routeIntentForTest(session.ID, app.NewID("turn"), query, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if route.Status == app.RouteMatched && len(route.CapabilityPath) == 2 && route.CapabilityPath[1] == app.CapabilityDocumentEdit && route.Slots.Operation == app.RouteOperationTransform {
				t.Fatalf("boundary statement authorized a PDF transform: %#v", route)
			}
		})
	}
}

func TestPDFTransformWorkflowRoutesApprovesExecutesAndRereads(t *testing.T) {
	root := t.TempDir()
	writeAgentPDFTextFixture(t, root, "report.pdf", 3)
	original, err := os.ReadFile(filepath.Join(root, "report.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	goal := "Transform report.pdf into a new PDF copy containing only page 2"
	user := st.AddMessage(app.Message{SessionID: session.ID, Role: "user", Content: goal, CreatedAt: time.Now().UTC()})
	routing, err := runtime.routeIntent(context.Background(), session.ID, user.ID, goal)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit || route.Slots.Operation != app.RouteOperationTransform {
		t.Fatalf("page export did not route to PDF transform: %#v fusion=%#v", route, routing.Fusion)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "received", Risk: app.RiskReversible, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != "pdf.extract_text" {
		t.Fatalf("PDF edit did not begin with governed read: %#v", visibleToolNames(dispatch.Tools))
	}

	readCall, approval, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "pdf.extract_text", Args: map[string]any{"path": "report.pdf"}, WorkflowID: app.WorkflowDocumentEdit,
		WorkflowNodeID: "document_locate_evidence", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})
	if approval != nil || !toolCallCompleted(readCall) {
		t.Fatalf("governed PDF read failed: call=%#v approval=%#v", readCall, approval)
	}
	definition, _ := runtime.tools.Definition(readCall.Tool)
	outcome, err := adaptWorkflowOutcome(definition, readCall)
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := st.GetRun(dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(storedRun.Workflow, outcome)
	if changed, err := applyWorkflowOutcome(&storedRun, outcome, assessment); err != nil || !changed {
		t.Fatalf("PDF read did not activate operation selection: changed=%t err=%v", changed, err)
	}
	selectedDefinition, _ := runtime.tools.Definition("pdf.transform")
	selectedEntry := app.ToolDirectoryEntryID("")
	for _, capability := range selectedDefinition.Capabilities {
		if capability.Qualifiers[app.CapabilityQualifierOperation] == "extract_pages" {
			selectedEntry = directoryEntryID(selectedDefinition, capability)
			break
		}
	}
	storedRun.Workflow.Route.Slots.Query += mockWorkflowDecisionSelectedResponse(selectedEntry)
	st.SaveRun(storedRun)
	if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &storedRun, dispatch.Profile); err != nil || !changed {
		t.Fatalf("extract_pages operation selection failed: changed=%t err=%v", changed, err)
	}
	stageContext := dispatch.Profile.StageContext(storedRun.Workflow)
	tools, err := runtime.materializeActiveWorkflowTools(context.Background(), storedRun, runtime.workflowActorRef(session.ID), &stageContext)
	if err != nil || len(tools) != 1 || tools[0].Name != "pdf.transform" {
		t.Fatalf("PDF transform did not materialize: tools=%#v err=%v", visibleToolNames(tools), err)
	}
	for _, runtimeBound := range []string{"operation", "path", "output_path"} {
		if containsString(toolDefinitionPropertyNames(tools[0].InputSchema), runtimeBound) ||
			containsString(toolDefinitionRequiredArgs(tools[0].InputSchema), runtimeBound) {
			t.Fatalf("model-visible PDF transform leaked runtime-bound %s: %#v", runtimeBound, tools[0].InputSchema)
		}
	}
	if !containsString(stageContext.SemanticVariables, "pdf.transform.pages") {
		t.Fatalf("PDF editor stage did not retain its semantic page variable: %#v", stageContext.SemanticVariables)
	}
	if calls := countModelCalls(st.ListModelCalls(session.ID, storedRun.ID), "workflow_operation_selection", documentWorkflowModelLane); calls != 1 {
		t.Fatalf("ambiguous PDF transform family used %d bounded operation-selection calls, want 1", calls)
	}

	editCall, editApproval, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "pdf.transform",
		Args: map[string]any{
			"operation": "delete_pages", "path": "model-input.pdf", "pages": []any{2}, "output_path": "model-output.pdf",
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if editApproval == nil || editCall.Status != "approval_pending" || editCall.Arguments["operation"] != "extract_pages" ||
		editCall.Arguments["path"] != "report.pdf" || editCall.Arguments["output_path"] != "report-sparkclaw-edit.pdf" {
		t.Fatalf("PDF transform did not enter approval with frozen paths: call=%#v approval=%#v", editCall, editApproval)
	}
	storedRun, _ = st.GetRun(storedRun.ID)
	storedRun.State = "approval_pending"
	st.SaveRun(storedRun)
	st.SaveModelCall(app.ModelCall{
		ID: app.NewID("mcall"), SessionID: session.ID, RunID: storedRun.ID, Operation: "workflow_step_2", Status: "completed", StartedAt: time.Now().UTC(),
	})
	resolved, err := st.ResolveApproval(editApproval.ID, "approved", "fixture owner approved PDF page export")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "completed_after_approval" {
		t.Fatalf("approved PDF transform did not execute: call=%#v err=%v", executed, err)
	}
	result, resumed, err := runtime.ResumeRunAfterApproval(context.Background(), session.ID, storedRun.ID)
	if err != nil || !resumed || result.Run.State != "completed" {
		t.Fatalf("PDF workflow did not complete after approval: resumed=%t result=%#v err=%v", resumed, result, err)
	}

	outputPath := filepath.Join(root, "report-sparkclaw-edit.pdf")
	readResult, err := runtime.tools.Execute(context.Background(), "pdf.extract_text", map[string]any{"path": outputPath}, session.ID, "reread_pdf_output")
	if err != nil {
		t.Fatal(err)
	}
	readOutput := readResult.Output.(map[string]any)
	if readOutput["read_complete"] != true || !strings.Contains(stringValue(readOutput["content"]), "page 2") || strings.Contains(stringValue(readOutput["content"]), "page 1") {
		t.Fatalf("exported PDF did not reread as the selected page: %#v", readOutput)
	}
	current, err := os.ReadFile(filepath.Join(root, "report.pdf"))
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("approved PDF transform modified the original: %v", err)
	}
}

func writeAgentPDFTextFixture(t *testing.T, root, name string, pages int) {
	t.Helper()
	script := `
from pathlib import Path
from pypdf import PdfWriter
from pypdf.generic import DecodedStreamObject, DictionaryObject, NameObject
import sys
root, name, count = Path(sys.argv[1]), sys.argv[2], int(sys.argv[3])
writer = PdfWriter()
for index in range(1, count + 1):
    page = writer.add_blank_page(width=240, height=200)
    font = DictionaryObject({NameObject("/Type"):NameObject("/Font"), NameObject("/Subtype"):NameObject("/Type1"), NameObject("/BaseFont"):NameObject("/Helvetica")})
    page[NameObject("/Resources")] = DictionaryObject({NameObject("/Font"):DictionaryObject({NameObject("/F1"):writer._add_object(font)})})
    stream = DecodedStreamObject()
    stream.set_data(("BT /F1 12 Tf 36 160 Td (Agent routing PDF page %d contains complete searchable fixture text for verification) Tj ET" % index).encode("ascii"))
    page.replace_contents(stream)
with open(root / name, "wb") as output:
    writer.write(output)
`
	cmd := exec.Command("python3", "-c", script, root, name, strconv.Itoa(pages))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create agent PDF fixture: %v\n%s", err, output)
	}
}
