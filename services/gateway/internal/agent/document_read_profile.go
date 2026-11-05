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

// Revision 6 owns its plan and assessment. Persisted r4/r5 plans continue to
// use documentReadProfileV5, including their original direct-read behavior.
type documentReadProfile struct{}

func (documentReadProfile) ID() app.WorkflowID           { return app.WorkflowDocumentRead }
func (documentReadProfile) Revision() int                { return 6 }
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
	stage.EvidenceRequirements = []workflowEvidenceRequirement{
		{ResourceKind: documentOutlineResourceKind, Mode: workflowEvidenceStructured, Optional: true},
		{ResourceKind: documentSearchResourceKind, Mode: workflowEvidenceStructured, Optional: true},
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
