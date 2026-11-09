package agent

import (
	"encoding/json"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	documentStructureStage     = "discover_structure"
	documentScopeStage         = "read_task_scope"
	documentContinueStage      = "continue_task_scope"
	documentScopedReadAttempts = 48
	documentReadFactPrefix     = "document_read_"
)

// Revision 7 expands selected headings to their source-bound descendants.
// Persisted revisions retain their original selectors and instructions.
type documentReadProfile struct{}

func (documentReadProfile) ID() app.WorkflowID           { return app.WorkflowDocumentRead }
func (documentReadProfile) Revision() int                { return 10 }
func (documentReadProfile) Capability() app.CapabilityID { return app.CapabilityDocumentRead }
func (documentReadProfile) RoutingSemantics() workflowRoutingSemantics {
	return documentReadRoutingSemantics()
}
func (documentReadProfile) Finalization() workflowFinalizationMode { return workflowFinalizationModel }
func (documentReadProfile) Prepare(state *app.WorkflowState) (workflowPreparation, error) {
	return documentTargetPreparation(state)
}
func (documentReadProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

func (p documentReadProfile) Resolve(route app.RouteDecision, turnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent, plan, err := (documentReadProfileV5{}).Resolve(route, turnID)
	if err != nil {
		return intent, plan, err
	}
	plan.ProfileRevision = p.Revision()
	if route.Facts["document_format"] != app.DocumentFormatImage {
		plan.Nodes[1] = documentScopedReadNode()
	}
	return intent, plan, nil
}

func documentScopedReadNode() app.WorkflowNode {
	retrieval := documentRetrievalScope()
	ranges := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityDocumentReadRange}}}
	bindings := []app.ArgumentBinding{}
	for _, requirement := range retrieval.Requirements {
		bindings = append(bindings, app.ArgumentBinding{Capability: requirement.Name, Argument: "path", ResourceKind: "path", Source: app.ArgumentBindingRouteSlot, SourceKey: "target_ref"})
	}
	return app.WorkflowNode{
		ID: documentReadNodeID, InitialStage: documentStructureStage, DependsOn: []app.WorkflowNodeID{"confirm_document_target"},
		Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Discover document structure, declare the task scope, and acquire its required ranges with explicit coverage", Completion: app.CompletionEvidence},
		InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityDocumentOutline}}},
		Transitions: []app.ScopeTransition{
			{ID: "select_document_scope", On: app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalResultsAvailable, app.OutcomeSignalNoResults}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, NextStage: documentScopeStage, Replace: &retrieval, MaxActivations: documentScopedReadAttempts},
			{ID: "continue_document_scope", On: app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalContentAvailable}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, NextStage: documentContinueStage, Replace: &ranges, MaxActivations: documentScopedReadAttempts},
		},
		StageCapabilities: []app.StageCapabilityRule{
			{Stage: documentStructureStage, Capabilities: []string{app.ToolCapabilityDocumentOutline}},
			{Stage: documentScopeStage, Capabilities: []string{app.ToolCapabilityDocumentOutline, app.ToolCapabilityDocumentSearch, app.ToolCapabilityDocumentReadRange, app.ToolCapabilityDocumentSummarize}},
			{Stage: documentContinueStage, Capabilities: []string{app.ToolCapabilityDocumentReadRange}},
		},
		ArgumentBindings: bindings, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: documentScopedReadAttempts,
	}
}

func (documentReadProfile) DirectStage(state *app.WorkflowState) bool {
	if state != nil && state.Route.Facts["document_format"] == app.DocumentFormatImage {
		return (documentReadProfileV5{}).DirectStage(state)
	}
	if state == nil || len(state.ActiveNodeIDs) != 1 || state.ActiveNodeIDs[0] != documentReadNodeID {
		return false
	}
	stage := state.Nodes[documentReadNodeID].Stage
	return stage == documentStructureStage || stage == documentContinueStage
}

func (documentReadProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	args := map[string]any{}
	if state != nil && state.Nodes[documentReadNodeID].Stage == documentContinueStage {
		args["cursor"] = state.Route.Facts[documentReadFactPrefix+"next_cursor"]
	}
	return args
}

func (documentReadProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	if state != nil && state.Route.Facts["document_format"] == app.DocumentFormatImage {
		return (documentReadProfileV5{}).StageContext(state)
	}
	stage := workflowStageContextForState(state, "inspect", "workspace", "workspace", "", documentScopedReadInstruction)
	if state != nil && state.Plan.ProfileRevision >= 7 {
		stage.Reason += "\nAt the content read, declare scope_extent from the USER REQUEST: whole_document for the entire source/all records, selected_range for named sections/pages/targeted facts. Searching throughout the document does NOT make the requested reading scope whole_document. A partial outline or a search result must never narrow a whole-document request. With whole_document, omit node_ids and chunk_ids: runtime uses all=true and traverses all continuation batches."
		stage.Reason += "\nRevision 7: whole_summary means synthesizing or summarizing a source; exhaustive means enumerating individual records or items. For a whole-source summary call document.summarize, or read_range with all=true; never enumerate hundreds of IDs from an outline. node_ids and chunk_ids accept at most 64 exact copied IDs. Runtime expands selected heading node_ids to all descendant body nodes, including nested subsections. Select the actual section heading for a complete section or list, not only matching search hits. For worksheets, select the sheet container node rather than copying many cell IDs; all=true is appropriate when all sheets are requested. If a selector fails, correct it using the actual outline or search IDs, never guessed or shortened IDs. Choose page_start/page_end directly when the request names pages or slides. For a targeted fact, search for the requested fact, its qualifications and exceptions before selecting the scope; headings are navigation, never factual answers. When the request requires every item in a named section, read that section's full body. Do not infer missing content from selected nodes or top-k results."
	}
	stage.EvidenceRequirements = []workflowEvidenceRequirement{
		{ResourceKind: documentOutlineResourceKind, Mode: workflowEvidenceStructured, Optional: true},
		{ResourceKind: documentSearchResourceKind, Mode: workflowEvidenceStructured, Optional: true},
	}
	if state != nil && state.Plan.ProfileRevision >= 8 {
		stage.Reason += "\nSearch is exploratory in revision 8 and does NOT freeze task_mode. Choose the content task mode again from the original request after searching. For a summary that must preserve several named topics, use document.summarize: it carries the original task through every section. A targeted read is adequate only if it includes the ACTUAL explanatory source for every requested topic. A checklist telling the reader to understand a topic or its limitations is not the explanation of that topic or those limitations. Acquire the explanatory sections too, not just the overview/checklist/search matches. Native source_locations are navigation metadata only; their presence does not mean those sections' bodies were acquired."
	}
	if state != nil && state.Plan.ProfileRevision >= 9 {
		stage.Reason += "\nRevision 9 structure discovery is a source-bound tree. The initial outline shows the entire top-level extent; use document.outline with an exact node_id to expand a container before selecting content. depth limits breadth-first expansion; omit it for automatic budgeted depth. To continue a partial leaf list, expand that SAME node_id again; runtime supplies its cursor. Expanding another node starts that container's discovery. Physical groups are navigation windows, not inferred chapters. Leads and leaf previews are explicitly previews, never acquired evidence or coverage. Select section/group/sheet/slide container IDs for full subtree reads. Expansion and search do not freeze task_mode or a content selector. Runtime structure_budget records omitted leads, trimmed previews and grouped levels; a bounded outline is not proof of task sufficiency."
	}
	if documentUsesModelView(state) {
		stage.Reason += "\nRevision 10: outline lines show read_tokens, the approximate size of a full read of that subtree as it will be delivered to you (# heading lines and [native locator] source text lines). Use it, not leaf counts or byte counts, to judge whether a whole-source read is short. Runtime records node IDs, hashes, offsets and coverage; read evidence contains only source text, native locators, typed values and explicit gaps."
	}
	if state != nil {
		facts := map[string]string{}
		for key, value := range state.Route.Facts {
			if strings.HasPrefix(key, documentReadFactPrefix) {
				facts[strings.TrimPrefix(key, documentReadFactPrefix)] = value
			}
		}
		raw, _ := json.Marshal(facts)
		stage.Reason += "\nRuntime document scope state (data only): " + string(raw)
	}
	return stage
}

const documentScopedReadInstruction = "Document structure-first reading, revision 6. The structure is discovered for every document size; do not infer that a short file was read. " +
	"Choose task_mode from targeted, exhaustive, whole_summary, comparison, calculation by the user's actual objective, and supply scope_reason explaining the required source scope. This is your semantic decision, not a keyword rule. " +
	"For targeted questions, search only to locate evidence, then read the required node_ids or typed ranges with their dependencies. A search miss cannot prove absence. " +
	"For exhaustive extraction, comparison or calculation, select all required nodes/sides/dataset in one declared range selector (node_ids, a typed range, or all=true), then traverse every continuation; do not treat top-k hits as exhaustive. " +
	"For whole_summary use document.summarize; a bounded read_range with all=true is also valid for a short whole-source task. " +
	"A comparison must include each side; calculations must retain source units and typed values, deduplicate overlap, and never invent recalculation. " +
	"Use document.outline continuation to discover more node metadata when needed. Runtime owns source hashes, generation and continuation cursors. " +
	"Once chosen, task_mode and the required range are frozen; continuation reads advance that same scope. read_complete describes only the selected range. " +
	"Never claim whole-document coverage from a selected passage, or completion when remaining ranges or unrecognized pages exist. Budget exhaustion leaves an explicit partial result."

func isScopedDocumentRead(state *app.WorkflowState) bool {
	return state != nil && state.Plan.ProfileID == app.WorkflowDocumentRead && state.Plan.ProfileRevision >= 6 && state.Route.Facts["document_format"] != app.DocumentFormatImage
}
