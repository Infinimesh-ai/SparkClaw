package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestDocumentEditBindsCurrentXLSXRowEvidenceBeforeApproval(t *testing.T) {
	root := t.TempDir()
	inputRef := "ledger.xlsx"
	outputRef := "ledger-sparkclaw-edit.xlsx"
	writeAgentXLSXFixture(t, filepath.Join(root, inputRef))

	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	goal := "Update row 2 in ledger.xlsx with Beta and 50"
	route, err := runtime.routeIntentForTest(session.ID, "turn", goal, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || len(route.CapabilityPath) < 2 || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
		route.Facts["document_format"] != app.DocumentFormatXLSX || route.Slots.TargetRef != inputRef || route.Slots.OutputRef != outputRef {
		t.Fatalf("XLSX row update did not freeze the expected document route: %#v", route)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}

	readCall, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "files.read", Args: map[string]any{"path": inputRef}, WorkflowID: app.WorkflowDocumentEdit,
		WorkflowNodeID: documentLocateEvidenceNodeID, ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})
	if approval != nil || !toolCallCompleted(readCall) {
		t.Fatalf("XLSX localization read did not complete: call=%#v approval=%#v", readCall, approval)
	}
	readDefinition, _ := runtime.tools.Definition(readCall.Tool)
	outcome, err := adaptWorkflowOutcome(readDefinition, readCall)
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := testGetRun(st, dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(storedRun.Workflow, outcome)
	if changed, applyErr := applyWorkflowOutcome(&storedRun, outcome, assessment); applyErr != nil || !changed {
		t.Fatalf("XLSX localization evidence did not activate operation selection: changed=%t err=%v", changed, applyErr)
	}
	testSaveRun(st, storedRun)

	editorDefinition, ok := runtime.tools.Definition("xlsx.update_row")
	if !ok {
		t.Fatal("xlsx.update_row is not registered")
	}
	decisionState := storedRun.Workflow.Nodes["select_edit_operation"]
	selectedEntry := app.ToolDirectoryEntryID("")
	for _, capability := range editorDefinition.Capabilities {
		if capability.Qualifiers[app.CapabilityQualifierOperation] == "update_row" &&
			matchesAnyRequirement(capability, decisionState.CurrentScope.Requirements) {
			selectedEntry = directoryEntryID(editorDefinition, capability)
			break
		}
	}
	if selectedEntry == "" {
		t.Fatal("xlsx.update_row is outside the operation-selection scope")
	}
	storedRun.Workflow.Route.Slots.Query += mockWorkflowDecisionSelectedResponse(selectedEntry)
	testSaveRun(st, storedRun)
	if _, changed, resolveErr := runtime.resolveActiveWorkflowDecisions(context.Background(), &storedRun, dispatch.Profile); resolveErr != nil || !changed {
		t.Fatalf("XLSX update_row operation was not selected: changed=%t err=%v", changed, resolveErr)
	}
	stageContext := dispatch.Profile.StageContext(storedRun.Workflow)
	editTools, err := runtime.materializeActiveWorkflowTools(context.Background(), storedRun, runtime.workflowActorRef(storedRun), &stageContext)
	if err != nil {
		t.Fatal(err)
	}
	if !exactVisibleToolNames(editTools, "xlsx.update_row", "observation.read") {
		t.Fatalf("operation selection exposed the wrong XLSX editor: %#v", visibleToolNames(editTools))
	}
	for _, runtimeBound := range []string{"path", "output_path", "operation", "source_sha256", "source_row_hash"} {
		if containsString(toolDefinitionRequiredArgs(editTools[0].InputSchema), runtimeBound) ||
			containsString(toolDefinitionPropertyNames(editTools[0].InputSchema), runtimeBound) {
			t.Fatalf("model-visible XLSX editor exposes runtime-bound %s: %#v", runtimeBound, editTools[0].InputSchema)
		}
	}
	for _, semantic := range []string{"sheet", "row", "values"} {
		if !containsString(toolDefinitionRequiredArgs(editTools[0].InputSchema), semantic) {
			t.Fatalf("model-visible XLSX editor lost semantic argument %s: %#v", semantic, editTools[0].InputSchema)
		}
	}
	for _, semantic := range []string{"xlsx.update_row.sheet", "xlsx.update_row.row", "xlsx.update_row.values"} {
		if !containsString(stageContext.SemanticVariables, semantic) {
			t.Fatalf("XLSX editor stage did not declare semantic variable %s: %#v", semantic, stageContext.SemanticVariables)
		}
	}
	registeredEditor, _ := runtime.tools.Definition("xlsx.update_row")
	for _, runtimeBound := range []string{"path", "output_path", "source_sha256", "source_row_hash"} {
		if !containsString(toolDefinitionRequiredArgs(registeredEditor.InputSchema), runtimeBound) {
			t.Fatalf("registered XLSX editor lost execution argument %s: %#v", runtimeBound, registeredEditor.InputSchema)
		}
	}
	storedRun, _ = testGetRun(st, storedRun.ID)

	evidence, ok, err := runtime.currentXLSXEditEvidence(t.Context(), storedRun, "update_row", map[string]any{
		"path": inputRef, "sheet": "data", "row": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || evidence.SourceSHA256 == "" || evidence.TargetHash == "" || evidence.Sheet != "Data" {
		t.Fatalf("localization read omitted canonical XLSX row evidence: %#v", evidence)
	}
	conflictingCall, conflictingApproval, _, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "xlsx.update_row",
		Args: map[string]any{
			"path": "model-invented.xlsx", "output_path": "model-output.xlsx", "sheet": "data", "row": 2,
			"source_row_hash": "sha256:stale-model-evidence", "values": []any{"Beta", 50},
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if conflictingApproval != nil || conflictingCall.Status != "blocked" ||
		!strings.Contains(conflictingCall.Error, "source_row_hash conflicts with current workflow localization evidence") {
		t.Fatalf("conflicting XLSX row evidence was not blocked before approval: call=%#v approval=%#v", conflictingCall, conflictingApproval)
	}
	if approvals := st.ListApprovals(""); len(approvals) != 0 {
		t.Fatalf("conflicting XLSX evidence created an owner approval: %#v", approvals)
	}

	editCall, editApproval, _, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "xlsx.update_row",
		Args: map[string]any{
			"path": "model-invented.xlsx", "output_path": "model-output.xlsx", "sheet": "data", "row": 2,
			"values": []any{"Beta", 50},
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if editApproval == nil || editCall.Status != "approval_pending" {
		t.Fatalf("evidence-bound XLSX edit did not enter approval: call=%#v approval=%#v", editCall, editApproval)
	}
	for key, want := range map[string]any{
		"path": inputRef, "output_path": outputRef, "sheet": "Data",
		"source_sha256": evidence.SourceSHA256, "source_row_hash": evidence.TargetHash,
	} {
		if editCall.Arguments[key] != want || editApproval.Arguments[key] != want {
			t.Fatalf("XLSX evidence was not bound before approval for %s: call=%#v approval=%#v", key, editCall.Arguments, editApproval.Arguments)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, outputRef)); !os.IsNotExist(statErr) {
		t.Fatalf("XLSX output existed before approval: %v", statErr)
	}

	resolved, err := st.ResolveApproval(editApproval.ID, "approved", "approved synthetic XLSX regression edit")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "completed_after_approval" {
		t.Fatalf("evidence-bound XLSX edit failed after approval: call=%#v err=%v", executed, err)
	}
	executedOutput, ok := anyMap(executed.Result)
	if !ok || executedOutput["package_preservation"] != "verified" {
		t.Fatalf("approved XLSX user path omitted verified package preservation: %#v", executed.Result)
	}
	changeSummary, ok := anyMap(executedOutput["change_summary"])
	if !ok || changeSummary["package_preservation"] != "verified" || len(documentAnySliceFromAny(changeSummary["target_deltas"])) != 2 {
		t.Fatalf("approved XLSX user path omitted typed target deltas: %#v", executedOutput)
	}
	outputRead, err := runtime.tools.Execute(context.Background(), "files.read", map[string]any{"path": outputRef}, session.ID, storedRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAgentXLSXRow(t, outputRead.Output, "Beta", float64(50), "B2*2")
	inputRead, err := runtime.tools.Execute(context.Background(), "files.read", map[string]any{"path": inputRef}, session.ID, storedRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAgentXLSXRow(t, inputRead.Output, "Alpha", float64(40), "B2*2")
}

func TestDocumentEditBlocksUnverifiedXLSXPackageFeatureBeforeApproval(t *testing.T) {
	root := t.TempDir()
	inputRef := "table.xlsx"
	outputRef := "table-sparkclaw-edit.xlsx"
	writeAgentXLSXTableFixture(t, filepath.Join(root, inputRef))
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	route, err := runtime.routeIntentForTest(session.ID, "turn", "Update Data!B2 in table.xlsx to 55", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	storedRun, tools := advanceDocumentEditToEditor(t, runtime, st, dispatch, inputRef, "xlsx.update_cell", "update_cell")
	if !exactVisibleToolNames(tools, "xlsx.update_cell", "observation.read") {
		t.Fatalf("XLSX table fixture did not reach the selected editor: %#v", visibleToolNames(tools))
	}
	readOutput := replaceAgentXLSXLocateEvidence(t, runtime, st, storedRun, inputRef)
	documentMap, _ := anyMap(readOutput["document"])
	contentScope, _ := anyMap(documentMap["content_scope"])
	coverage, _ := anyMap(contentScope["package_coverage"])
	if coverage["status"] != "partial" || coverage["mutation_supported"] != false {
		t.Fatalf("read-only unsupported package coverage is missing: %#v", coverage)
	}
	call, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name:       "xlsx.update_cell",
		Args:       map[string]any{"path": inputRef, "output_path": outputRef, "sheet": "Data", "cell": "B2", "value": 55},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if approval != nil || call.Status != "blocked" || !strings.Contains(call.Error, "tables") {
		t.Fatalf("unverified XLSX table feature reached approval: call=%#v approval=%#v", call, approval)
	}
	if approvals := st.ListApprovals(""); len(approvals) != 0 {
		t.Fatalf("unverified XLSX package created an owner approval: %#v", approvals)
	}
	if _, statErr := os.Stat(filepath.Join(root, outputRef)); !os.IsNotExist(statErr) {
		t.Fatalf("unverified XLSX package left an output: %v", statErr)
	}
}

func TestDocumentEditRejectsWorkbookChangedAfterXLSXLocalizationThroughPipeline(t *testing.T) {
	root := t.TempDir()
	inputRef := "ledger.xlsx"
	outputRef := "ledger-sparkclaw-edit.xlsx"
	inputPath := filepath.Join(root, inputRef)
	writeAgentXLSXFixture(t, inputPath)
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	route, err := runtime.routeIntentForTest(session.ID, "turn", "Update Data!B2 in ledger.xlsx to 55", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := advanceDocumentEditToEditor(t, runtime, st, dispatch, inputRef, "xlsx.update_cell", "update_cell")
	replaceAgentXLSXLocateEvidence(t, runtime, st, storedRun, inputRef)
	mutateAgentXLSXFixture(t, inputPath)

	call, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name:       "xlsx.update_cell",
		Args:       map[string]any{"path": inputRef, "output_path": outputRef, "sheet": "Data", "cell": "B2", "value": 55},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if approval == nil || call.Status != "approval_pending" {
		t.Fatalf("evidence-bound XLSX edit did not reach approval: call=%#v approval=%#v", call, approval)
	}
	resolved, err := st.ResolveApproval(approval.ID, "approved", "approve stale-source regression")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "failed_after_approval" || !strings.Contains(strings.ToLower(executed.Error), "stale") {
		t.Fatalf("Pipeline did not reject the stale XLSX source: call=%#v err=%v", executed, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, outputRef)); !os.IsNotExist(statErr) {
		t.Fatalf("physically stale XLSX evidence left an output: %v", statErr)
	}
}

func writeAgentXLSXFixture(t *testing.T, path string) {
	t.Helper()
	script := `
const ExcelJS = require("exceljs");
(async () => {
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet("Data");
  sheet.addRow(["Name", "Score", "Total", "Note"]);
  sheet.addRow(["Alpha", 40, {formula: "B2*2", result: 80}, "Keep me"]);
  sheet.getCell("B2").numFmt = "0.00";
  sheet.mergeCells("A4:B4");
  sheet.getCell("A4").value = "Merged footer";
  await workbook.xlsx.writeFile(process.argv[1]);
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});`
	cmd := exec.Command("node", "-e", script, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create agent XLSX fixture: %v\n%s", err, output)
	}
}

func writeAgentXLSXTableFixture(t *testing.T, path string) {
	t.Helper()
	script := `
const ExcelJS = require("exceljs");
(async () => {
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet("Data");
  sheet.addRows([["Name", "Score"], ["Alpha", 40], ["Bravo", 60]]);
  sheet.addTable({name: "Scores", ref: "A1", headerRow: true, totalsRow: false, columns: [{name: "Name"}, {name: "Score"}], rows: [["Alpha", 40], ["Bravo", 60]]});
  await workbook.xlsx.writeFile(process.argv[1]);
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});`
	cmd := exec.Command("node", "-e", script, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create agent XLSX table fixture: %v\n%s", err, output)
	}
}

func mutateAgentXLSXFixture(t *testing.T, path string) {
	t.Helper()
	script := `
const ExcelJS = require("exceljs");
(async () => {
  const workbook = new ExcelJS.Workbook();
  await workbook.xlsx.readFile(process.argv[1]);
  workbook.getWorksheet("Data").getCell("D3").value = "external change";
  await workbook.xlsx.writeFile(process.argv[1]);
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});`
	cmd := exec.Command("node", "-e", script, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mutate agent XLSX fixture: %v\n%s", err, output)
	}
}

func replaceAgentXLSXLocateEvidence(t *testing.T, runtime Runtime, st store.RunRepository, run app.AgentRun, inputRef string) map[string]any {
	t.Helper()
	read, err := runtime.tools.Execute(context.Background(), "files.read", map[string]any{"path": inputRef}, run.SessionID, run.ID)
	if err != nil {
		t.Fatalf("read-only XLSX inspection failed: %v", err)
	}
	locateState := run.Workflow.Nodes[documentLocateEvidenceNodeID]
	readCall, ok := testGetToolCall(st, locateState.ToolCallIDs[0])
	if !ok {
		t.Fatal("XLSX locate call is missing")
	}
	readCall.Result = read.Output
	readCall.Status = "completed"
	readCall.Error = ""
	testSaveToolCall(st, readCall)
	output, ok := anyMap(read.Output)
	if !ok {
		t.Fatalf("XLSX read output is not structured: %#v", read.Output)
	}
	return output
}

func assertAgentXLSXRow(t *testing.T, raw any, wantA string, wantB float64, wantFormula string) {
	t.Helper()
	result, ok := anyMap(raw)
	if !ok {
		t.Fatalf("XLSX read output is not structured: %#v", raw)
	}
	document, ok := anyMap(result["document"])
	if !ok {
		t.Fatalf("XLSX read omitted document evidence: %#v", result)
	}
	sheet, ok := matchXLSXSheetEvidence(documentAnySliceFromAny(document["sheets"]), "Data")
	if !ok {
		t.Fatalf("XLSX read omitted Data sheet: %#v", document)
	}
	a2, okA := matchXLSXCellEvidence(sheet, "A2")
	b2, okB := matchXLSXCellEvidence(sheet, "B2")
	c2, okC := matchXLSXCellEvidence(sheet, "C2")
	if !okA || !okB || !okC || cleanOptionalString(a2["raw_value"]) != wantA || b2["raw_value"] != wantB || cleanOptionalString(c2["formula"]) != wantFormula {
		t.Fatalf("XLSX row evidence mismatch: A2=%#v B2=%#v C2=%#v", a2, b2, c2)
	}
}
