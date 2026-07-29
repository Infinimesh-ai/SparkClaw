package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDocumentEditBindsCurrentDOCXParagraphEvidenceBeforeApproval(t *testing.T) {
	root := t.TempDir()
	inputRef := "report-sparkclaw-edit-2.docx"
	outputRef := "report-sparkclaw-edit-3.docx"
	paragraphs := make([]string, 25)
	for index := range paragraphs {
		paragraphs[index] = "Supporting paragraph"
	}
	paragraphs[24] = "Current reflection paragraph"
	writeDOCXParagraphFixture(t, filepath.Join(root, inputRef), paragraphs)

	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	goal := "重新扩写 report-sparkclaw-edit-2.docx 第25段心得与体会到400字"
	route, err := runtime.routeIntentForTest(session.ID, "turn", goal, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Slots.TargetRef != inputRef || route.Slots.OutputRef != outputRef || route.Facts["output_path"] != outputRef {
		t.Fatalf("follow-up DOCX edit did not freeze the numbered input/output family: %#v", route)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	readCall, approval, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "files.read", Args: map[string]any{"path": inputRef}, WorkflowID: app.WorkflowDocumentEdit,
		WorkflowNodeID: documentLocateEvidenceNodeID, ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})
	if approval != nil || !toolCallCompleted(readCall) {
		t.Fatalf("DOCX localization read did not complete: call=%#v approval=%#v", readCall, approval)
	}
	readDefinition, _ := runtime.tools.Definition(readCall.Tool)
	outcome, err := adaptWorkflowOutcome(readDefinition, readCall)
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := st.GetRun(dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(storedRun.Workflow, outcome)
	if changed, err := applyWorkflowOutcome(&storedRun, outcome, assessment); err != nil || !changed {
		t.Fatalf("DOCX localization evidence did not activate operation selection: changed=%t err=%v", changed, err)
	}
	st.SaveRun(storedRun)

	editorDefinition, ok := runtime.tools.Definition("docx.replace_paragraph")
	if !ok {
		t.Fatal("docx.replace_paragraph is not registered")
	}
	decisionState := storedRun.Workflow.Nodes["select_edit_operation"]
	selectedEntry := app.ToolDirectoryEntryID("")
	for _, capability := range editorDefinition.Capabilities {
		if capability.Qualifiers[app.CapabilityQualifierOperation] == "replace_paragraph" &&
			matchesAnyRequirement(capability, decisionState.CurrentScope.Requirements) {
			selectedEntry = directoryEntryID(editorDefinition, capability)
			break
		}
	}
	if selectedEntry == "" {
		t.Fatal("docx.replace_paragraph is outside the operation-selection scope")
	}
	storedRun.Workflow.Route.Slots.Query += "\nMOCK_OPERATION_SELECTION_RESPONSE:{\"entry_id\":\"" + string(selectedEntry) + "\"}"
	st.SaveRun(storedRun)
	if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &storedRun, dispatch.Profile); err != nil || !changed {
		t.Fatalf("DOCX paragraph operation was not selected: changed=%t err=%v", changed, err)
	}
	hint := dispatch.Profile.Hint(storedRun.Workflow)
	editTools, err := runtime.materializeActiveWorkflowTools(context.Background(), storedRun, runtime.workflowActorRef(session.ID), &hint)
	if err != nil {
		t.Fatal(err)
	}
	if len(editTools) != 1 || editTools[0].Name != "docx.replace_paragraph" ||
		!containsString(toolDefinitionRequiredArgs(editTools[0].InputSchema), "source_hash") {
		t.Fatalf("materialized model-visible DOCX editor does not require source_hash: %#v", editTools)
	}
	storedRun, _ = st.GetRun(storedRun.ID)

	readResult, _ := anyMap(readCall.Result)
	document, _ := anyMap(readResult["document"])
	evidence, ok := matchDOCXParagraphEvidence(documentAnySliceFromAny(document["evidence_blocks"]), docxParagraphTarget{Index: 25})
	if !ok || evidence.SourceHash == "" {
		t.Fatalf("localization read omitted paragraph 25 evidence: %#v", readCall.Result)
	}
	replacement := strings.TrimSpace(strings.Repeat("Expanded reflection. ", 24))
	conflictingCall, conflictingApproval, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "docx.replace_paragraph",
		Args: map[string]any{
			"path": "model-invented-input.docx", "output_path": "model-invented-output.docx",
			"paragraph_index": 25, "source_hash": "sha1:stale-model-evidence", "text": replacement,
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if conflictingApproval != nil || conflictingCall.Status != "blocked" ||
		!strings.Contains(conflictingCall.Error, "source_hash conflicts with current workflow localization evidence") {
		t.Fatalf("conflicting model source_hash was not blocked before approval: call=%#v approval=%#v", conflictingCall, conflictingApproval)
	}
	if approvals := st.ListApprovals(""); len(approvals) != 0 {
		t.Fatalf("conflicting model source_hash created an owner approval: %#v", approvals)
	}

	editCall, editApproval, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "docx.replace_paragraph",
		Args: map[string]any{
			"path": "model-invented-input.docx", "output_path": "model-invented-output.docx",
			"paragraph_index": 25, "text": replacement,
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if editApproval == nil || editCall.Status != "approval_pending" {
		t.Fatalf("evidence-bound DOCX edit did not enter approval: call=%#v approval=%#v", editCall, editApproval)
	}
	if editCall.Arguments["path"] != inputRef || editCall.Arguments["output_path"] != outputRef ||
		editApproval.Arguments["path"] != inputRef || editApproval.Arguments["output_path"] != outputRef {
		t.Fatalf("evidence binding changed the frozen numbered paths: call=%#v approval=%#v", editCall.Arguments, editApproval.Arguments)
	}
	if editCall.Arguments["source_hash"] != evidence.SourceHash || editApproval.Arguments["source_hash"] != evidence.SourceHash {
		t.Fatalf("current localization source_hash was not bound before approval: call=%#v approval=%#v evidence=%#v", editCall.Arguments, editApproval.Arguments, evidence)
	}

	resolved, err := st.ResolveApproval(editApproval.ID, "approved", "approved synthetic DOCX regression edit")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "completed_after_approval" || strings.Contains(executed.Error, "preflight evidence") {
		t.Fatalf("evidence-bound DOCX edit failed after approval: call=%#v err=%v", executed, err)
	}
	outputRead, err := runtime.tools.Execute(context.Background(), "files.read", map[string]any{"path": outputRef}, session.ID, storedRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	output, _ := anyMap(outputRead.Output)
	if !strings.Contains(stringValue(output["content"]), "Expanded reflection.") ||
		strings.Contains(stringValue(output["content"]), "Current reflection paragraph") {
		t.Fatalf("approved DOCX replacement did not update paragraph 25: %#v", output)
	}
	inputRead, err := runtime.tools.Execute(context.Background(), "files.read", map[string]any{"path": inputRef}, session.ID, storedRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := anyMap(inputRead.Output)
	if !strings.Contains(stringValue(input["content"]), "Current reflection paragraph") {
		t.Fatalf("approved DOCX edit changed the frozen input: %#v", input)
	}
}

func TestDocumentEditBlocksDOCXParagraphWithoutDependencyEvidence(t *testing.T) {
	root := t.TempDir()
	writeTestOfficePackage(t, filepath.Join(root, "report.docx"), "word/document.xml")
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	route, err := runtime.routeIntentForTest(session.ID, "turn", "Replace a paragraph in report.docx", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := advanceDocumentEditToEditor(t, runtime, st, dispatch, "report.docx", "docx.replace_paragraph", "replace_paragraph")
	st.SaveToolCall(app.ToolCall{
		ID: "tc_unrelated_docx_read", SessionID: session.ID, RunID: storedRun.ID, Tool: "files.read", Status: "completed",
		Arguments: map[string]any{"path": "report.docx"},
		Result: map[string]any{
			"rel_path": "report.docx",
			"document": map[string]any{
				"format": app.DocumentFormatDOCX,
				"evidence_blocks": []any{map[string]any{
					"text": "Unrelated evidence", "sourceHash": "sha1:unrelated",
					"location": map[string]any{"paragraphIndex": 25, "path": "document.p[25]"},
				}},
			},
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})

	call, approval, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "docx.replace_paragraph",
		Args: map[string]any{
			"path": "model-input.docx", "output_path": "model-output.docx",
			"paragraph_index": 25, "text": "Replacement",
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if approval != nil || call.Status != "failed" ||
		!strings.Contains(call.Error, `requires "source_hash"`) {
		t.Fatalf("DOCX edit without dependency evidence was not blocked before approval: call=%#v approval=%#v", call, approval)
	}
	if call.Arguments["path"] != "report.docx" || call.Arguments["output_path"] != "report-sparkclaw-edit.docx" {
		t.Fatalf("failed evidence binding changed frozen paths: %#v", call.Arguments)
	}
	if approvals := st.ListApprovals(""); len(approvals) != 0 {
		t.Fatalf("invalid DOCX evidence created an owner approval: %#v", approvals)
	}
	if _, err := os.Stat(filepath.Join(runtime.tools.Config().Workspaces.DefaultRoot, "report-sparkclaw-edit.docx")); !os.IsNotExist(err) {
		t.Fatalf("invalid DOCX evidence wrote an output before approval: %v", err)
	}
}

func writeDOCXParagraphFixture(t *testing.T, path string, paragraphs []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, paragraph := range paragraphs {
		var escaped bytes.Buffer
		if err := xml.EscapeText(&escaped, []byte(paragraph)); err != nil {
			t.Fatal(err)
		}
		body.WriteString("<w:p><w:r><w:t>")
		body.Write(escaped.Bytes())
		body.WriteString("</w:t></w:r></w:p>")
	}
	entries := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `<w:sectPr/></w:body></w:document>`,
		"word/styles.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
</w:styles>`,
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		entry, createErr := archive.Create(name)
		if createErr == nil {
			_, createErr = entry.Write([]byte(content))
		}
		if createErr != nil {
			_ = archive.Close()
			_ = file.Close()
			t.Fatal(createErr)
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
