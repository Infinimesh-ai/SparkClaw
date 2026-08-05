package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestDocumentReadPreflightDispatchesOnlyCompatibleReader(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestOfficePackage(t, filepath.Join(root, "report.docx"), "word/document.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report.xlsx"), "xl/workbook.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report.pptx"), "ppt/presentation.xml")
	writeTinyPNG(t, filepath.Join(root, "screen.png"))

	for _, test := range []struct {
		path, format, tool string
	}{
		{path: "note.txt", format: app.DocumentFormatText, tool: "files.read"},
		{path: "report.docx", format: app.DocumentFormatDOCX, tool: "files.read"},
		{path: "report.xlsx", format: app.DocumentFormatXLSX, tool: "files.read"},
		{path: "report.pptx", format: app.DocumentFormatPPTX, tool: "files.read"},
		{path: "report.pdf", format: app.DocumentFormatPDF, tool: "pdf.extract_text"},
		{path: "screen.png", format: app.DocumentFormatImage, tool: "images.inspect"},
	} {
		t.Run(test.format, func(t *testing.T) {
			runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
			defer closeRuntime()
			route, err := runtime.routeIntentForTest(session.ID, "turn", "Summarize "+test.path, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentRead || route.Facts["document_format"] != test.format {
				t.Fatalf("read preflight chose the wrong format: %#v", route)
			}
			run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
			dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
			if err != nil {
				t.Fatal(err)
			}
			if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != test.tool {
				t.Fatalf("read stage exposed an incompatible reader: %#v", visibleToolNames(dispatch.Tools))
			}
			node := dispatch.Run.Workflow.Nodes["document_read"]
			if node.Stage != "read_by_type" || node.ScopeRevision != 1 || len(node.CurrentScope.Requirements) != 1 ||
				node.CurrentScope.Requirements[0].Qualifiers[app.CapabilityQualifierFormat] != test.format {
				t.Fatalf("read operation node was not frozen: %#v", node)
			}
			confirmation := dispatch.Run.Workflow.Nodes["confirm_document_target"]
			if confirmation.Status != app.WorkflowNodeSucceeded || confirmation.Attempts != 1 ||
				len(confirmation.OutcomeRefs) != 1 ||
				confirmation.OutcomeRefs[0].Attributes["path"] != test.path ||
				confirmation.OutcomeRefs[0].Attributes["format"] != test.format {
				t.Fatalf("document target confirmation was not persisted before reader exposure: %#v", confirmation)
			}
		})
	}
}

func TestDocumentEditPreflightDispatchesFormatThenSelectsCompatibleEditor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("Original text"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestOfficePackage(t, filepath.Join(root, "report.docx"), "word/document.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report.xlsx"), "xl/workbook.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report.pptx"), "ppt/presentation.xml")
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		request, format, operation, tool, output string
	}{
		{request: "Replace text in note.txt", format: app.DocumentFormatText, operation: "replace_text", tool: "text.replace_text", output: "note-sparkclaw-edit.txt"},
		{request: "Replace a paragraph in report.docx", format: app.DocumentFormatDOCX, operation: "replace_paragraph", tool: "docx.replace_paragraph", output: "report-sparkclaw-edit.docx"},
		{request: "Update a cell in report.xlsx", format: app.DocumentFormatXLSX, operation: "update_cell", tool: "xlsx.update_cell", output: "report-sparkclaw-edit.xlsx"},
		{request: "Append a row to report.xlsx", format: app.DocumentFormatXLSX, operation: "append_row", tool: "xlsx.append_row", output: "report-sparkclaw-edit.xlsx"},
		{request: "Insert a row in report.xlsx", format: app.DocumentFormatXLSX, operation: "insert_row", tool: "xlsx.insert_row", output: "report-sparkclaw-edit.xlsx"},
		{request: "Delete a row in report.xlsx", format: app.DocumentFormatXLSX, operation: "delete_row", tool: "xlsx.delete_row", output: "report-sparkclaw-edit.xlsx"},
		{request: "Replace text in report.pptx", format: app.DocumentFormatPPTX, operation: "replace_text", tool: "pptx.replace_text", output: "report-sparkclaw-edit.pptx"},
		{request: "Improve slide 3 in report.pptx", format: app.DocumentFormatPPTX, operation: "update_slide", tool: "pptx.update_slide", output: "report-sparkclaw-edit.pptx"},
		{request: "Rotate pages in report.pdf", format: app.DocumentFormatPDF, operation: "rotate_pages", tool: "pdf.transform", output: "report-sparkclaw-edit.pdf"},
	} {
		t.Run(test.format+"/"+test.operation, func(t *testing.T) {
			runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
			defer closeRuntime()
			route, err := runtime.routeIntentForTest(session.ID, "turn", test.request, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
				route.Facts["document_format"] != test.format || route.Facts["output_path"] != test.output || route.Facts["document_operation"] != "" {
				t.Fatalf("edit preflight did not freeze only its format and copy resources: %#v", route)
			}
			run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
			dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
			if err != nil {
				t.Fatal(err)
			}
			reader := "files.read"
			if test.format == app.DocumentFormatPDF {
				reader = "pdf.extract_text"
			}
			if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != reader {
				t.Fatalf("edit read stage exposed an incompatible reader: %#v", visibleToolNames(dispatch.Tools))
			}
			dispatch.Run, dispatch.Tools = advanceDocumentEditToEditor(t, runtime, st, dispatch, route.Slots.TargetRef, test.tool, test.operation)
			if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != test.tool {
				t.Fatalf("edit stage exposed an incompatible editor: %#v", visibleToolNames(dispatch.Tools))
			}
			node := dispatch.Run.Workflow.Nodes["document_edit"]
			if node.Stage != "edit_by_type" || node.ScopeRevision != 1 || len(node.CurrentScope.Requirements) != 1 ||
				node.CurrentScope.Requirements[0].Qualifiers[app.CapabilityQualifierFormat] != test.format ||
				node.CurrentScope.Requirements[0].Qualifiers[app.CapabilityQualifierOperation] != "" {
				t.Fatalf("edit node scope was not frozen: %#v", node)
			}
			decision := dispatch.Run.Workflow.Nodes["select_edit_operation"]
			if decision.Status != app.WorkflowNodeSucceeded || len(decision.OutcomeRefs) != 1 ||
				decision.OutcomeRefs[0].Kind != "tool_directory_entry" ||
				decision.OutcomeRefs[0].Attributes[app.CapabilityQualifierFormat] != test.format ||
				decision.OutcomeRefs[0].Attributes[app.CapabilityQualifierOperation] != test.operation {
				t.Fatalf("edit operation decision was not persisted with exact qualifiers: %#v", decision)
			}
		})
	}
}

func TestDocumentPreflightRejectsExtensionSignatureMismatchAndAllowsTextEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fake.pdf"), []byte("not a PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := preflightDocumentPath(root, "fake.pdf", false); err == nil {
		t.Fatal("mismatched PDF signature passed preflight")
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if preflight, err := preflightDocumentPath(root, "note.txt", true); err != nil || preflight.Format != app.DocumentFormatText || preflight.OutputRef != "note-sparkclaw-edit.txt" {
		t.Fatalf("text edit preflight failed: preflight=%#v err=%v", preflight, err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := preflightDocumentPath(root, "linked/outside.txt", false); err == nil {
		t.Fatal("workspace path traversed a symlinked parent")
	}
}

func TestDocumentEditPreflightAllocatesNextAvailableOutputCopy(t *testing.T) {
	root := t.TempDir()
	writeTestOfficePackage(t, filepath.Join(root, "report.docx"), "word/document.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report-sparkclaw-edit.docx"), "word/document.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report-sparkclaw-edit-2.docx"), "word/document.xml")

	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	route, err := runtime.routeIntentForTest(session.ID, "turn", "Replace a paragraph in report.docx", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || route.Slots.OutputRef != "report-sparkclaw-edit-3.docx" ||
		route.Facts["output_path"] != "report-sparkclaw-edit-3.docx" {
		t.Fatalf("document edit did not route to the next output copy: %#v", route)
	}
}

func TestDocumentEditRejectsOperationContradictingMaterializedQualifier(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	route, err := runtime.routeIntentForTest(session.ID, "turn", "Rotate pages in report.pdf", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Run, dispatch.Tools = advanceDocumentEditToEditor(t, runtime, st, dispatch, route.Slots.TargetRef, "pdf.transform", "rotate_pages")
	definition, ok := runtime.tools.Definition("pdf.transform")
	if !ok {
		t.Fatal("pdf.transform is unavailable")
	}
	plan := toolPlan{
		Name: "pdf.transform", WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1,
		Capability: app.ToolCapabilityDocumentEdit,
		Args:       map[string]any{"path": "report.pdf", "output_path": "report-sparkclaw-edit.pdf", "operation": "rotate_pages"},
	}
	if err := runtime.validateWorkflowToolPlan(context.Background(), dispatch.Run.ID, plan, definition); err != nil {
		t.Fatalf("matching PDF operation was rejected: %v", err)
	}
	plan.Args["operation"] = "delete_pages"
	if err := runtime.validateWorkflowToolPlan(context.Background(), dispatch.Run.ID, plan, definition); err == nil {
		t.Fatal("PDF operation escaped the materialized rotate_pages qualifier")
	}
}

func TestDocumentEditUsesSingleGovernedAttachmentPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "uploads", "report.docx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestOfficePackage(t, path, "word/document.xml")
	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	projection := strings.Join([]string{
		"修改并完善附件文档中五、心得与体会标题下的正文。必须依据结构化读取结果，仅定位并替换 document.p[25]，保持其他内容不变，写入新 DOCX 副本。", "", "Attached files for this user turn:",
		"- original-display-name.docx path=uploads/report.docx content_type=application/vnd.openxmlformats-officedocument.wordprocessingml.document bytes=128 media_kind=file",
	}, "\n")
	routing, err := runtime.routeIntentOutputForTest(session.ID, "turn", projection)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
		route.Slots.TargetRef != "uploads/report.docx" || route.Facts["document_operation"] != "" {
		t.Fatalf("attached document edit did not freeze its unique governed path: %#v", route)
	}
}

func TestDocumentContentMutationRoutesToEditR6ThenSelectsXLSXEditor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "uploads", "people.xlsx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestOfficePackage(t, path, "xl/workbook.xml")
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	projection := strings.Join([]string{
		"文档末尾新增学号 3，姓名为张七的人", "", "Attached files for this user turn:",
		"- people.xlsx path=uploads/people.xlsx content_type=application/vnd.openxmlformats-officedocument.spreadsheetml.sheet bytes=6791",
	}, "\n")
	routing, err := runtime.routeIntentOutputForTest(session.ID, "turn", projection)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
		route.Facts["document_format"] != app.DocumentFormatXLSX || route.Facts["document_operation"] != "" {
		t.Fatalf("XLSX content mutation did not route to format-bounded document.edit r6: route=%#v fusion=%+v", route, routing.Fusion)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.Profile.Revision() != 6 || len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != "files.read" {
		t.Fatalf("XLSX edit did not start in document.edit r6 evidence stage: profile=%d tools=%#v", dispatch.Profile.Revision(), visibleToolNames(dispatch.Tools))
	}
	dispatch.Run, dispatch.Tools = advanceDocumentEditToEditor(t, runtime, st, dispatch, route.Slots.TargetRef, "xlsx.append_row", "append_row")
	if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != "xlsx.append_row" {
		t.Fatalf("XLSX content mutation exposed the wrong editor: %#v", visibleToolNames(dispatch.Tools))
	}
	if !hasModelCallOperation(st.ListModelCalls(session.ID, dispatch.Run.ID), "workflow_operation_selection", documentWorkflowModelLane) ||
		!hasAgentAuditType(st.ListAudit(session.ID), "workflow.decision_resolved") {
		t.Fatalf("XLSX editor was not selected through the explicit document decision node")
	}
}

func TestXLSXMutationSynonymsDoNotFreezeConcreteOperations(t *testing.T) {
	root := t.TempDir()
	writeTestOfficePackage(t, filepath.Join(root, "people.xlsx"), "xl/workbook.xml")
	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	for _, request := range []string{
		"在 people.xlsx 末尾新增学号 3 和姓名张七",
		"给 people.xlsx 添加学号 4",
		"在 people.xlsx 增加一行记录",
		"在 people.xlsx 指定位置插入一行",
		"删除 people.xlsx 中指定的一行",
	} {
		route, err := runtime.routeIntentForTest(session.ID, "turn", request, agentContextSnapshot{})
		if err != nil {
			t.Fatal(err)
		}
		if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
			route.Facts["document_format"] != app.DocumentFormatXLSX || route.Facts["document_operation"] != "" {
			t.Fatalf("XLSX mutation synonym escaped generic document.edit r6 routing for %q: %#v", request, route)
		}
	}
}

func TestDocumentRoutingKeepsReadOnlyAndFileLifecycleOutsideEditR2(t *testing.T) {
	root := t.TempDir()
	writeTestOfficePackage(t, filepath.Join(root, "people.xlsx"), "xl/workbook.xml")
	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	read, err := runtime.routeIntentForTest(session.ID, "read", "读取 people.xlsx 的内容", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if read.Status != app.RouteMatched || read.CapabilityPath[1] != app.CapabilityDocumentRead {
		t.Fatalf("read-only document request left document.read: %#v", read)
	}
	for _, request := range []string{"删除 people.xlsx", "新建 people.xlsx"} {
		route, err := runtime.routeIntentForTest(session.ID, "lifecycle", request, agentContextSnapshot{})
		if err != nil {
			t.Fatal(err)
		}
		if route.Status == app.RouteMatched && len(route.CapabilityPath) > 1 && route.CapabilityPath[1] == app.CapabilityDocumentEdit {
			t.Fatalf("file lifecycle request entered document.edit r6 for %q: %#v", request, route)
		}
	}
}

func TestImageAttachmentUsesDocumentReadOnlyForDirectAnalysis(t *testing.T) {
	root := t.TempDir()
	writeTinyPNG(t, filepath.Join(root, "media", "screen.png"))
	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	attachment := "\n\nAttached files for this user turn:\n- screen.png path=media/screen.png content_type=image/png bytes=74 media_kind=image"

	directRouting, err := runtime.routeIntentOutputForTest(session.ID, "direct", "这张图片什么内容"+attachment)
	if err != nil {
		t.Fatal(err)
	}
	direct := directRouting.Route
	if direct.Status != app.RouteMatched || direct.CapabilityPath[1] != app.CapabilityDocumentRead || direct.Facts["document_format"] != app.DocumentFormatImage {
		t.Fatalf("direct image analysis did not enter document.read: route=%#v fusion=%+v", direct, directRouting.Fusion)
	}
	for _, request := range []string{"把这张图片发送到微信", "这张图片里的新闻是真的吗，帮我联网查证"} {
		route, err := runtime.routeIntentForTest(session.ID, "other", request+attachment, agentContextSnapshot{})
		if err != nil {
			t.Fatal(err)
		}
		if route.Status == app.RouteMatched && len(route.CapabilityPath) > 1 && route.CapabilityPath[1] == app.CapabilityDocumentRead {
			t.Fatalf("non-analysis image request entered document.read for %q: %#v", request, route)
		}
	}
}

func TestUnsupportedDocumentContentMutationStillRoutesToEditR2(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	route, err := runtime.routeIntentForTest(session.ID, "turn", "修改 report.pdf 中的文字内容", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
		route.Facts["document_format"] != app.DocumentFormatPDF || route.Facts["document_operation"] != "" {
		t.Fatalf("unsupported PDF content mutation degraded outside document.edit r6: %#v", route)
	}
}

func advanceDocumentEditToEditor(t *testing.T, runtime Runtime, st *store.MemoryStore, dispatch matchedWorkflowDispatch, inputPath, selectedTool, selectedOperation string) (app.AgentRun, []app.ToolDefinition) {
	t.Helper()
	dispatch.Run = advanceDocumentEditToDecision(t, runtime, st, dispatch, inputPath)
	selectedDefinition, ok := runtime.tools.Definition(selectedTool)
	if !ok {
		t.Fatalf("selected test editor %q is unavailable", selectedTool)
	}
	selectedEntry := app.ToolDirectoryEntryID("")
	state := dispatch.Run.Workflow.Nodes["select_edit_operation"]
	for _, capability := range selectedDefinition.Capabilities {
		if !matchesAnyRequirement(capability, state.CurrentScope.Requirements) ||
			selectedOperation != "" && capability.Qualifiers[app.CapabilityQualifierOperation] != selectedOperation {
			continue
		}
		selectedEntry = directoryEntryID(selectedDefinition, capability)
		break
	}
	if selectedEntry == "" {
		t.Fatalf("selected test editor %q operation %q is outside the edit scope", selectedTool, selectedOperation)
	}
	dispatch.Run.Workflow.Route.Slots.Query += "\nMOCK_OPERATION_SELECTION_RESPONSE:{\"entry_id\":\"" + string(selectedEntry) + "\"}"
	st.SaveRun(dispatch.Run)
	if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &dispatch.Run, dispatch.Profile); err != nil || !changed {
		t.Fatalf("document operation decision did not resolve: changed=%t err=%v", changed, err)
	}
	stageContext := dispatch.Profile.StageContext(dispatch.Run.Workflow)
	tools, err := runtime.materializeActiveWorkflowTools(context.Background(), dispatch.Run, runtime.workflowActorRef(dispatch.Run.SessionID), &stageContext)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed, ok := st.GetRun(dispatch.Run.ID); ok {
		dispatch.Run = refreshed
	}
	return dispatch.Run, tools
}

func advanceDocumentEditToDecision(t *testing.T, runtime Runtime, st *store.MemoryStore, dispatch matchedWorkflowDispatch, inputPath string) app.AgentRun {
	t.Helper()
	if len(dispatch.Tools) != 1 {
		t.Fatalf("document edit read stage is not singular: %#v", visibleToolNames(dispatch.Tools))
	}
	definition := dispatch.Tools[0]
	call := app.ToolCall{
		ID: "tc_document_read_" + string(dispatch.Run.Workflow.Route.Slots.Format), SessionID: dispatch.Run.SessionID, RunID: dispatch.Run.ID,
		Tool: definition.Name, Risk: app.RiskRead, Status: "completed", Arguments: map[string]any{"path": inputPath},
		Result:     map[string]any{"path": inputPath, "rel_path": inputPath, "content": "structured evidence"},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_locate_evidence", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	}
	call.ObservationRef = store.ArchiveToolObservation(context.Background(), st, runtime.artifacts, call, call.Result)
	if call.ObservationRef == "" {
		t.Fatal("document decision fixture failed to archive its read evidence")
	}
	call.ObservationSummary = adaptToolResult(toolResultAdapterInput{
		Call: call, Output: call.Result, ObservationRef: call.ObservationRef,
		MaxBytes: runtime.tools.Config().Runtime.ObservationSummaryMaxBytes,
	})
	st.SaveToolCall(call)
	outcome, err := adaptWorkflowOutcome(definition, call)
	if err != nil {
		t.Fatal(err)
	}
	assessment := dispatch.Profile.Assess(dispatch.Run.Workflow, outcome)
	changed, err := applyWorkflowOutcome(&dispatch.Run, outcome, assessment)
	if err != nil || !changed {
		t.Fatalf("document read did not activate the operation decision: changed=%t assessment=%#v err=%v", changed, assessment, err)
	}
	st.SaveRun(dispatch.Run)
	return dispatch.Run
}

func TestParseWorkflowDecisionSelectionRejectsUnknownAndTrailingFields(t *testing.T) {
	selection, err := parseWorkflowDecisionSelection(`{"entry_id":"entry_allowed"}`)
	if err != nil || selection.EntryID != "entry_allowed" {
		t.Fatalf("valid directory selection was rejected: selection=%#v err=%v", selection, err)
	}
	for _, raw := range []string{
		`{"entry_id":"entry_allowed","tool":"xlsx.append_row"}`,
		`{"entry_id":"entry_allowed"}{"entry_id":"entry_other"}`,
	} {
		if _, err := parseWorkflowDecisionSelection(raw); err == nil {
			t.Fatalf("invalid directory selection was accepted: %s", raw)
		}
	}
}

func newDocumentDispatchRuntime(t *testing.T, root string) (Runtime, *store.MemoryStore, app.Session, func()) {
	t.Helper()
	cfg := agentTestConfig()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.ArtifactDir = filepath.Join(root, ".artifacts")
	st := store.NewMemoryStore()
	session := st.CreateSessionWithScope("document dispatch", app.DefaultOwnerID, root, "web", false)
	hub := toolhub.New(cfg, st)
	return NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil), st, session, func() { _ = hub.Close() }
}
