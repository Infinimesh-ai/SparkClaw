package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestDOCXEditBilingualRouteAndOperationSelectionMatrix(t *testing.T) {
	cases := []struct {
		name, request, tool, operation string
	}{
		{"replace_text_en", "Edit report.docx and replace every exact occurrence of First paragraph", "office.replace_text", "replace_text"},
		{"replace_text_zh", "编辑 report.docx，替换“First paragraph”的每一处准确文本", "office.replace_text", "replace_text"},
		{"replace_paragraph_en", "Rewrite paragraph 2 in report.docx with a corrected summary", "docx.replace_paragraph", "replace_paragraph"},
		{"replace_paragraph_zh", "改写 report.docx 第2段的总结内容", "docx.replace_paragraph", "replace_paragraph"},
		{"insert_paragraph_en", "Insert a new paragraph after paragraph 1 in report.docx", "docx.insert_paragraph", "insert_paragraph"},
		{"insert_paragraph_zh", "在 report.docx 第1段后插入一个新段落", "docx.insert_paragraph", "insert_paragraph"},
		{"delete_paragraph_en", "Delete paragraph 2 from report.docx but keep the file", "docx.delete_paragraph", "delete_paragraph"},
		{"delete_paragraph_zh", "删除 report.docx 第2段，但保留文档文件", "docx.delete_paragraph", "delete_paragraph"},
		{"set_text_style_en", "Edit paragraph 1 in report.docx and make its text bold", "docx.set_text_style", "set_text_style"},
		{"set_text_style_zh", "编辑 report.docx 第1段并将文字设为粗体", "docx.set_text_style", "set_text_style"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeDOCXParagraphFixture(t, filepath.Join(root, "report.docx"), []string{"First paragraph", "Second paragraph"})
			runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
			defer closeRuntime()

			route, err := runtime.routeIntentForTest(session.ID, "turn", tc.request, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if route.Status != app.RouteMatched || len(route.CapabilityPath) < 2 || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
				route.Slots.Query != tc.request || route.Facts["document_format"] != app.DocumentFormatDOCX || route.Facts["document_operation"] != "" {
				t.Fatalf("bilingual DOCX case did not route to the format-bounded edit workflow: %#v", route)
			}
			dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
				ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
			}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
			if err != nil {
				t.Fatal(err)
			}
			dispatch.Run = advanceDocumentEditToDecision(t, runtime, st, dispatch, route.Slots.TargetRef)
			selectedEntry := docxEvaluationEntryID(t, runtime, dispatch.Run, tc.tool, tc.operation)
			dispatch.Run.Workflow.Route.Slots.Query += mockWorkflowDecisionSelectedResponse(selectedEntry)
			testSaveRun(st, dispatch.Run)

			if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &dispatch.Run, dispatch.Profile); err != nil || !changed {
				t.Fatalf("operation selection failed: changed=%t err=%v", changed, err)
			}
			decision := dispatch.Run.Workflow.Nodes["select_edit_operation"]
			if decision.Status != app.WorkflowNodeSucceeded || len(decision.OutcomeRefs) != 1 ||
				decision.OutcomeRefs[0].Ref != string(selectedEntry) ||
				decision.OutcomeRefs[0].Attributes[app.CapabilityQualifierOperation] != tc.operation {
				t.Fatalf("operation selection did not persist the expected frozen entry: %#v", decision)
			}
			if countModelCalls(testListModelCalls(st, session.ID, dispatch.Run.ID), "workflow_operation_selection", documentWorkflowModelLane) != 1 {
				t.Fatalf("multi-operation DOCX decision did not use exactly one bounded selection call")
			}
		})
	}
}

func TestDOCXEditRoutingConfusionPairsStayOutsideEdit(t *testing.T) {
	cases := []struct {
		name, request string
		expected      app.CapabilityID
	}{
		{"read_en", "Summarize report.docx without changing it", app.CapabilityDocumentRead},
		{"read_zh", "总结 report.docx，不要修改内容", app.CapabilityDocumentRead},
		{"delete_file_en", "Delete report.docx from the workspace", ""},
		{"delete_file_zh", "从工作区删除 report.docx 文件", ""},
		{"create_en", "Create a new Word document", ""},
		{"create_zh", "新建一个 Word 文档", ""},
		{"browser_en", "Click the Bold button on the current Word Online page", app.CapabilityBrowserInteraction},
		{"browser_zh", "点击当前 Word 在线页面的粗体按钮", app.CapabilityBrowserInteraction},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeDOCXParagraphFixture(t, filepath.Join(root, "report.docx"), []string{"First paragraph"})
			runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
			defer closeRuntime()
			route, err := runtime.routeIntentForTest(session.ID, "turn", tc.request, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if len(route.CapabilityPath) > 1 && route.CapabilityPath[1] == app.CapabilityDocumentEdit {
				t.Fatalf("confusion case entered document.edit: %#v", route)
			}
			if tc.expected != "" && (len(route.CapabilityPath) < 2 || route.CapabilityPath[1] != tc.expected) {
				t.Fatalf("confusion case resolved to the wrong capability: %#v", route)
			}
		})
	}
}

func TestDOCXUnsupportedTargetsBlockWithoutApproval(t *testing.T) {
	for _, tc := range []struct{ name, request string }{
		{"header_en", "Edit the header text in report.docx"},
		{"table_zh", "编辑 report.docx 并合并表格单元格"},
		{"footnote_en", "Edit report.docx and add a footnote to paragraph 1"},
		{"tracked_change_zh", "编辑 report.docx，把第1段改成修订模式的跟踪更改"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeDOCXParagraphFixture(t, filepath.Join(root, "report.docx"), []string{"First paragraph"})
			runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
			defer closeRuntime()
			route, err := runtime.routeIntentForTest(session.ID, "turn", tc.request, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if route.Status != app.RouteMatched || len(route.CapabilityPath) < 2 || route.CapabilityPath[1] != app.CapabilityDocumentEdit {
				t.Fatalf("unsupported content mutation did not reach explicit operation selection: %#v", route)
			}
			dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
				ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
			}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
			if err != nil {
				t.Fatal(err)
			}
			dispatch.Run = advanceDocumentEditToDecision(t, runtime, st, dispatch, route.Slots.TargetRef)
			dispatch.Run.Workflow.Route.Slots.Query += mockWorkflowDecisionNoMatchResponse()
			testSaveRun(st, dispatch.Run)
			if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &dispatch.Run, dispatch.Profile); err != nil || !changed {
				t.Fatalf("unsupported selection did not reach a terminal block: changed=%t err=%v", changed, err)
			}
			decision := dispatch.Run.Workflow.Nodes["select_edit_operation"]
			if dispatch.Run.Workflow.Status != app.WorkflowStatusBlocked || decision.Status != app.WorkflowNodeBlocked ||
				decision.LastAssessment == nil || decision.LastAssessment.ReasonCode != "no_registered_editor_matches" ||
				len(decision.OutcomeRefs) != 0 {
				t.Fatalf("unsupported target did not fail closed: %#v", decision)
			}
			if len(storetest.MustListApprovals(t, st, "")) != 0 || len(toolCallsForRun(testListToolCalls(st, session.ID), dispatch.Run.ID)) != 1 {
				t.Fatalf("unsupported target created mutation state: approvals=%#v calls=%#v", storetest.MustListApprovals(t, st, ""), testListToolCalls(st, session.ID))
			}
		})
	}
}

func TestRealFastDOCXSectionImprovementSelection(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_DOCX_OPERATION_EVAL") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_DOCX_OPERATION_EVAL=1 to call the configured Fast model")
	}
	runtime, _, _, dispatch := newDocumentDecisionFixture(t, "完善 report.docx 中的心得与体会")
	node, ok := workflowPlanNode(dispatch.Run.Workflow.Plan, "select_edit_operation")
	if !ok {
		t.Fatal("decision node is missing")
	}
	if got := dispatch.Run.Workflow.Route.Slots.Query; got != "完善 report.docx 中的心得与体会" {
		t.Fatalf("document route lost the owner request before operation selection: %q", got)
	}
	state := dispatch.Run.Workflow.Nodes[node.ID]
	view, err := runtime.exposure.Search(t.Context(), app.ExposureRequest{
		RunID: dispatch.Run.ID, WorkflowID: dispatch.Run.Workflow.Plan.ProfileID, NodeID: node.ID,
		ScopeRevision: state.ScopeRevision, ActorRef: runtime.workflowActorRef(dispatch.Run), Limit: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	view.Entries = scopeDocumentDirectoryEntries(dispatch.Run.Workflow.Route, view.Entries)
	candidateProjection, bindings, err := buildWorkflowDecisionCandidateProjection(view.Entries)
	if err != nil {
		t.Fatal(err)
	}
	output := syntheticDOCXDecisionOutput(25)
	if observationPath := os.Getenv("SPARKCLAW_DOCX_OBSERVATION_PATH"); observationPath != "" {
		raw, err := os.ReadFile(observationPath)
		if err != nil {
			t.Fatal(err)
		}
		var observation struct {
			Output map[string]any `json:"output"`
		}
		if err := json.Unmarshal(raw, &observation); err != nil {
			t.Fatal(err)
		}
		output = observation.Output
	} else {
		document, _ := anyMap(output["document"])
		blocks := documentAnySliceFromAny(document["evidence_blocks"])
		heading, _ := anyMap(blocks[23])
		heading["type"] = "heading"
		heading["text"] = "五、心得与体会"
		paragraph, _ := anyMap(blocks[24])
		paragraph["text"] = "本次实验以航空订票系统为对象，完成了 Selenium 自动化测试实践。"
	}
	dispatch.Run.Workflow.Route.Slots.Query = "完善心得与体会"
	evidence := projectDOCXDecisionEvidence(output, dispatch.Run.Workflow.Route, view.Entries, 8000)
	system, user := workflowDecisionSelectionPromptWithLimit(
		dispatch.Run, dispatch.Profile, node, candidateProjection, evidence, 8000,
	)
	cfg, err := config.Load(filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = false
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	chat, err := modelrouter.New(cfg).ChatWithProfile(ctx, documentWorkflowModelLane, system, user)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Mock {
		t.Fatal("real DOCX operation eval resolved to the mock model")
	}
	selection, err := parseWorkflowDecisionSelection(chat.Content)
	if err != nil {
		t.Fatalf("Fast returned invalid selection %q: %v", chat.Content, err)
	}
	entryID := bindings[selection.CandidateID]
	entry, ok := directoryViewEntry(view, entryID)
	if !ok || entry.Capability.Qualifiers[app.CapabilityQualifierOperation] != app.DocumentOperationReplaceParagraph {
		t.Fatalf("Fast selected %q (%q), want %q; response=%s evidence=%s", entryID,
			entry.Capability.Qualifiers[app.CapabilityQualifierOperation], app.DocumentOperationReplaceParagraph, chat.Content, evidence)
	}
}

func TestDOCXEditFileStoreEndToEndRereadsAndVerifiesPreservation(t *testing.T) {
	testRoot := t.TempDir()
	workspace := filepath.Join(testRoot, "workspace")
	statePath := filepath.Join(testRoot, "state", "sparkclaw-state.json")
	artifactPath := filepath.Join(testRoot, "artifacts")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(workspace, "report.docx")
	outputPath := filepath.Join(workspace, "report-sparkclaw-edit.docx")
	writeDOCXParagraphFixture(t, inputPath, []string{"First paragraph", "Second paragraph"})
	inputBefore, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}

	cfg := agentTestConfig()
	cfg.State.Backend = "file"
	cfg.State.Path = statePath
	cfg.Workspaces.DefaultRoot = workspace
	cfg.Workspaces.Allowlist = []string{workspace}
	cfg.Storage.TraceDir = filepath.Join(testRoot, "traces")
	cfg.Storage.ArtifactDir = artifactPath
	st, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	session := storetest.MustCreateSessionWithScope(t, st, "file-backed DOCX evaluation", app.DefaultOwnerID, workspace, "web", false)
	hub := toolhub.New(cfg, st)
	defer func() { _ = hub.Close() }()
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)

	request := "Edit paragraph 1 in report.docx and make its text bold"
	user := storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: request, CreatedAt: time.Now().UTC()})
	route, err := runtime.routeIntentForTest(session.ID, user.ID, request, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || len(route.CapabilityPath) < 2 || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
		route.Slots.TargetRef != "report.docx" || route.Slots.OutputRef != "report-sparkclaw-edit.docx" {
		t.Fatalf("real DOCX request did not freeze its input/output route: %#v", route)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "received", Risk: app.RiskReversible, StartedAt: time.Now().UTC(),
		MessageContext: &app.MessageRunContext{
			OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
			ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource}, Route: route,
		},
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	readCall, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "files.read", Args: map[string]any{"path": "report.docx"}, WorkflowID: app.WorkflowDocumentEdit,
		WorkflowNodeID: documentLocateEvidenceNodeID, ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})
	if approval != nil || !toolCallCompleted(readCall) {
		t.Fatalf("real DOCX localization read failed: call=%#v approval=%#v", readCall, approval)
	}
	definition, _ := runtime.tools.Definition(readCall.Tool)
	outcome, err := adaptWorkflowOutcome(definition, readCall)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := testGetRun(st, dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(run.Workflow, outcome)
	if changed, err := applyWorkflowOutcome(&run, outcome, assessment); err != nil || !changed {
		t.Fatalf("real DOCX read did not activate selection: changed=%t err=%v", changed, err)
	}
	selectedEntry := docxEvaluationEntryID(t, runtime, run, "docx.set_text_style", "set_text_style")
	run.Workflow.Route.Slots.Query += mockWorkflowDecisionSelectedResponse(selectedEntry)
	testSaveRun(st, run)
	if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &run, dispatch.Profile); err != nil || !changed {
		t.Fatalf("real DOCX operation selection failed: changed=%t err=%v", changed, err)
	}
	stageContext := dispatch.Profile.StageContext(run.Workflow)
	editTools, err := runtime.materializeActiveWorkflowTools(context.Background(), run, runtime.workflowActorRef(run), &stageContext)
	if err != nil || !exactVisibleToolNames(editTools, "docx.set_text_style", "observation.read") {
		t.Fatalf("selected DOCX style editor did not materialize: tools=%#v err=%v", visibleToolNames(editTools), err)
	}

	editCall, editApproval, _, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{
		Name: "docx.set_text_style", Args: map[string]any{
			"paragraph_index": 1, "style": map[string]any{"bold": true},
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if editApproval == nil || editCall.Status != "approval_pending" {
		t.Fatalf("real DOCX mutation did not wait for approval: call=%#v approval=%#v", editCall, editApproval)
	}
	if editCall.Arguments["path"] != "report.docx" || editCall.Arguments["output_path"] != "report-sparkclaw-edit.docx" ||
		cleanOptionalString(editCall.Arguments["source_sha256"]) == "" || cleanOptionalString(editCall.Arguments["before_format_sha256"]) == "" {
		t.Fatalf("approval omitted frozen path/version/format evidence: %#v", editCall.Arguments)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("DOCX output existed before approval: %v", err)
	}
	run, _ = testGetRun(st, run.ID)
	run.State = "approval_pending"
	testSaveRun(st, run)
	testSaveModelCall(st, app.ModelCall{
		ID: app.NewID("mcall"), SessionID: session.ID, RunID: run.ID,
		Operation: "workflow_step_2", Status: "completed", StartedAt: time.Now().UTC(),
	})

	resolved, err := st.ResolveApproval(t.Context(), editApproval.ID, "approved", "owner approved file-backed DOCX style edit")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "completed_after_approval" {
		t.Fatalf("approved real DOCX mutation failed: call=%#v err=%v", executed, err)
	}
	executedResult, _ := anyMap(executed.Result)
	changeSummary, _ := anyMap(executedResult["change_summary"])
	if changeSummary["high_level_preservation"] != "verified" || !boolValue(changeSummary["original_unchanged"]) ||
		changeSummary["operation"] != "set_text_style" {
		t.Fatalf("real DOCX mutation omitted preservation evidence: %#v", changeSummary)
	}
	outputRead, err := runtime.tools.Execute(context.Background(), "files.read", map[string]any{"path": "report-sparkclaw-edit.docx"}, session.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	output, _ := anyMap(outputRead.Output)
	document, _ := anyMap(output["document"])
	paragraphs := documentAnySliceFromAny(document["paragraphs"])
	if len(paragraphs) == 0 {
		t.Fatalf("output reread omitted DOCX paragraphs: %#v", output)
	}
	firstParagraph, _ := anyMap(paragraphs[0])
	runs := documentAnySliceFromAny(firstParagraph["runs"])
	if len(runs) == 0 {
		t.Fatalf("output reread omitted DOCX run evidence: %#v", firstParagraph)
	}
	for _, value := range runs {
		runEvidence, _ := anyMap(value)
		if strings.TrimSpace(stringValue(runEvidence["text"])) != "" && !boolValue(runEvidence["effective_bold"]) {
			t.Fatalf("output reread did not prove bold style: %#v", firstParagraph)
		}
	}
	inputAfter, err := os.ReadFile(inputPath)
	if err != nil || string(inputAfter) != string(inputBefore) {
		t.Fatalf("approved output-copy edit changed the input: err=%v", err)
	}
	result, resumed, err := runtime.ResumeRunAfterApproval(context.Background(), session.ID, run.ID)
	if err != nil || !resumed || result.Run.State != "completed" || result.WorkflowResult == nil ||
		result.WorkflowResult.Status != app.WorkflowResultSucceeded || len(result.Message.Attachments) != 1 ||
		result.Message.Attachments[0].RelPath != "report-sparkclaw-edit.docx" {
		t.Fatalf("approved DOCX workflow did not resume with its output copy: resumed=%t result=%#v err=%v", resumed, result, err)
	}
	if !hasAgentAuditType(mustAgentListAudit(t, st, session.ID), "workflow.decision_resolved") ||
		!hasAgentAuditType(mustAgentListAudit(t, st, session.ID), "workflow_step.evidence_provisioned") {
		t.Fatalf("real DOCX path omitted decision/evidence audit records")
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("default file-backed state was not persisted: %v", err)
	}
	reloaded, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	persistedRun, ok := testGetRun(reloaded, run.ID)
	documentRecords := mustListAgentDocumentRecords(t, reloaded, session.OwnerID, session.ID, 10)
	if !ok || persistedRun.State != "completed" || len(storetest.MustListApprovals(t, reloaded, "approved")) != 1 ||
		len(documentRecords) < 2 {
		t.Fatalf("file-backed reload lost DOCX workflow state: run=%#v approvals=%#v documents=%#v", persistedRun, storetest.MustListApprovals(t, reloaded, "approved"), documentRecords)
	}
}

func docxEvaluationEntryID(t *testing.T, runtime Runtime, run app.AgentRun, tool, operation string) app.ToolDirectoryEntryID {
	t.Helper()
	definition, ok := runtime.tools.Definition(tool)
	if !ok {
		t.Fatalf("DOCX evaluation tool %q is unavailable", tool)
	}
	state := run.Workflow.Nodes["select_edit_operation"]
	for _, capability := range definition.Capabilities {
		if capability.Qualifiers[app.CapabilityQualifierOperation] == operation && matchesAnyRequirement(capability, state.CurrentScope.Requirements) {
			return directoryEntryID(definition, capability)
		}
	}
	t.Fatalf("DOCX evaluation operation %q is outside the frozen directory scope", operation)
	return ""
}
