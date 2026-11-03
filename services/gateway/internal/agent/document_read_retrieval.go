package agent

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// document.read revision 5 keeps the direct complete read for documents that
// fit and adds a retrieval stage for documents too large to read whole
// (docs/large-document-context-design.md section 4.8).
const (
	documentReadStage           = "read_by_type"
	documentRetrievalStage      = "retrieve"
	documentReadNodeID          = app.WorkflowNodeID("document_read")
	documentRetrievalMaxQueries = 4
	// documentReadMaxAttempts covers the read, the bounded searches or
	// outline listings, and the final range read.
	documentReadMaxAttempts = documentRetrievalMaxQueries + 2

	documentOutlineResourceKind = "document_outline"
	documentSearchResourceKind  = "document_search"
	documentRangeResourceKind   = "document_range"
)

func documentRetrievalScope() app.CapabilityScope {
	return app.CapabilityScope{Requirements: []app.CapabilityRequirement{
		{Name: app.ToolCapabilityDocumentOutline},
		{Name: app.ToolCapabilityDocumentSearch},
		{Name: app.ToolCapabilityDocumentReadRange},
	}, MaterializeAll: true}
}

func documentReadNode(format string) app.WorkflowNode {
	retrieval := documentRetrievalScope()
	pathBinding := func(capability string) app.ArgumentBinding {
		return app.ArgumentBinding{Capability: capability, Argument: "path", ResourceKind: "path", Source: app.ArgumentBindingRouteSlot, SourceKey: "target_ref"}
	}
	return app.WorkflowNode{
		ID: documentReadNodeID, InitialStage: documentReadStage, DependsOn: []app.WorkflowNodeID{"confirm_document_target"},
		Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Read the exact governed path with its detected format, retrieving passages when it is too large to read whole", Completion: app.CompletionEvidence},
		InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
			Name: app.ToolCapabilityDocumentRead, Qualifiers: map[string]string{app.CapabilityQualifierFormat: format},
		}}},
		Transitions: []app.ScopeTransition{
			{
				ID: "large_document_retrieval", On: app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalRetrievalRequired}},
				NextStage: documentRetrievalStage, Replace: &retrieval, MaxActivations: 1,
			},
			{
				ID: "retrieval_query", On: app.TransitionPredicate{
					OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalResultsAvailable, app.OutcomeSignalNoResults},
					Assessments:    []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence},
				},
				NextStage: documentRetrievalStage, Replace: &retrieval, MaxActivations: documentRetrievalMaxQueries,
			},
		},
		StageCapabilities: []app.StageCapabilityRule{
			{Stage: documentReadStage, Capabilities: []string{app.ToolCapabilityDocumentRead}},
			{Stage: documentRetrievalStage, Capabilities: []string{app.ToolCapabilityDocumentOutline, app.ToolCapabilityDocumentSearch, app.ToolCapabilityDocumentReadRange}},
		},
		ArgumentBindings: []app.ArgumentBinding{
			pathBinding(app.ToolCapabilityDocumentRead), pathBinding(app.ToolCapabilityDocumentOutline),
			pathBinding(app.ToolCapabilityDocumentSearch), pathBinding(app.ToolCapabilityDocumentReadRange),
		},
		AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: documentReadMaxAttempts,
	}
}

// DirectStage runs the format reader directly; only the retrieval stage
// asks the step model to choose queries.
func (documentReadProfile) DirectStage(state *app.WorkflowState) bool {
	if state == nil || len(state.ActiveNodeIDs) != 1 || state.ActiveNodeIDs[0] != documentReadNodeID {
		return false
	}
	return state.Nodes[documentReadNodeID].Stage == documentReadStage
}

func (documentReadProfile) DirectStageArguments(*app.WorkflowState) map[string]any { return nil }

func (documentReadProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	switch {
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable):
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "document_content_available"
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalRetrievalRequired):
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "document_retrieval_required"
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalResultsAvailable), containsOutcomeSignal(outcome.Signals, app.OutcomeSignalNoResults):
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "document_retrieval_query_completed"
	default:
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "document_read_failed"
	}
	return assessment
}

func (documentReadProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	stageContext := workflowStageContextForState(state, "inspect", "workspace", "workspace", "", "Dispatched by the document.read workflow contract.")
	if state.Nodes[documentReadNodeID].Stage != documentRetrievalStage {
		return stageContext
	}
	stageContext.Reason = documentRetrievalInstruction
	stageContext.EvidenceRequirements = []workflowEvidenceRequirement{
		{ResourceKind: documentOutlineResourceKind, Mode: workflowEvidenceStructured, Optional: true},
		{ResourceKind: documentSearchResourceKind, Mode: workflowEvidenceStructured, Optional: true},
	}
	return stageContext
}

const documentRetrievalInstruction = "Large-document retrieval stage: the document is too large to read whole, and its outline and latest search results are provisioned below. " +
	"Turn the owner request into 2-6 search keywords, including Chinese and English synonyms and the key terms it names, and call document.search; pass section with an outline node ID to search one chapter. " +
	"Then call document.read_range once with the chunk_ids (at most 12) that answer the request; neighbouring context is added automatically. " +
	"For a request about a whole chapter, call document.read_range with that chapter's section ID instead. " +
	"In a scanned document pages have no text until read: when search returns sections but no chunks, call document.read_range with the best section ID or a page range; its scanned pages are recognized first, at most 8 per call. " +
	"Use document.outline only when searches miss. Search at most three times; after that, read the best chunks you have."
