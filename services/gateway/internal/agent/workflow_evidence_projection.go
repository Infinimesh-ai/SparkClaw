package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const workflowEvidenceProjectionRecordSchema = "workflow_evidence_projection_record_v1"

const (
	workflowCoverageComplete    = "complete"
	workflowCoverageBounded     = "bounded"
	workflowCoveragePartial     = "partial"
	workflowCoverageNotRequired = "not_required"
	workflowCoverageUnknown     = "unknown"
)

type workflowEvidenceProjectionConsumer struct {
	WorkflowID            app.WorkflowID     `json:"workflow_id,omitempty"`
	NodeID                app.WorkflowNodeID `json:"node_id,omitempty"`
	Stage                 string             `json:"stage"`
	SemanticVariable      string             `json:"semantic_variable"`
	ConsumerSchemaVersion string             `json:"consumer_schema_version"`
}

type workflowEvidenceProjectionCoverage struct {
	Source              string   `json:"source_coverage"`
	Target              string   `json:"target_coverage"`
	Claim               string   `json:"claim_coverage"`
	Candidate           string   `json:"candidate_coverage"`
	Transition          string   `json:"transition_coverage"`
	Presentation        string   `json:"presentation_coverage"`
	CompleteForConsumer bool     `json:"complete_for_consumer"`
	Omissions           []string `json:"omissions,omitempty"`
}

type workflowEvidenceProjectionRecord struct {
	ProjectionID              string                             `json:"projection_id"`
	ProjectionSchemaVersion   string                             `json:"projection_schema_version"`
	SourceEventIDs            []string                           `json:"source_event_ids,omitempty"`
	DerivedAssertionIDs       []string                           `json:"derived_assertion_ids,omitempty"`
	Consumer                  workflowEvidenceProjectionConsumer `json:"consumer"`
	Coverage                  workflowEvidenceProjectionCoverage `json:"coverage"`
	ModelPayloadDigest        string                             `json:"model_payload_digest"`
	ModelPayloadBytes         int                                `json:"model_payload_bytes"`
	ArchivedBytes             int                                `json:"archived_bytes"`
	RuntimeBindingManifestRef string                             `json:"runtime_binding_manifest_ref,omitempty"`
	CandidateCount            int                                `json:"candidate_count,omitempty"`
	SelectedItemCount         int                                `json:"selected_item_count,omitempty"`
	RepairAttempt             int                                `json:"repair_attempt,omitempty"`
	ValidationErrorCodes      []string                           `json:"validation_error_codes,omitempty"`
	Reused                    bool                               `json:"reused,omitempty"`
	CreatedAt                 time.Time                          `json:"created_at"`
}

type workflowEvidenceProjectionInput struct {
	Payload                   string
	SourceEventIDs            []string
	DerivedAssertionIDs       []string
	Consumer                  workflowEvidenceProjectionConsumer
	Coverage                  workflowEvidenceProjectionCoverage
	ArchivedBytes             int
	RuntimeBindingManifestRef string
	CandidateCount            int
	SelectedItemCount         int
	RepairAttempt             int
	ValidationErrorCodes      []string
	Reused                    bool
	ModelOperation            string
	Step                      int
}

func (r Runtime) recordWorkflowEvidenceProjection(ctx context.Context, run app.AgentRun, input workflowEvidenceProjectionInput) workflowEvidenceProjectionRecord {
	payloadDigest := sha256.Sum256([]byte(input.Payload))
	record := workflowEvidenceProjectionRecord{
		ProjectionID:              app.NewID("evidence_projection"),
		ProjectionSchemaVersion:   workflowEvidenceProjectionRecordSchema,
		SourceEventIDs:            uniqueNonEmptyStrings(input.SourceEventIDs),
		DerivedAssertionIDs:       uniqueNonEmptyStrings(input.DerivedAssertionIDs),
		Consumer:                  input.Consumer,
		Coverage:                  normalizeWorkflowProjectionCoverage(input.Coverage, input.Payload),
		ModelPayloadDigest:        hex.EncodeToString(payloadDigest[:]),
		ModelPayloadBytes:         len([]byte(input.Payload)),
		ArchivedBytes:             input.ArchivedBytes,
		RuntimeBindingManifestRef: input.RuntimeBindingManifestRef,
		CandidateCount:            input.CandidateCount,
		SelectedItemCount:         input.SelectedItemCount,
		RepairAttempt:             input.RepairAttempt,
		ValidationErrorCodes:      uniqueNonEmptyStrings(input.ValidationErrorCodes),
		Reused:                    input.Reused,
		CreatedAt:                 time.Now().UTC(),
	}
	fields := map[string]any{
		"projection_id":                record.ProjectionID,
		"projection_schema_version":    record.ProjectionSchemaVersion,
		"source_event_ids":             record.SourceEventIDs,
		"derived_assertion_ids":        record.DerivedAssertionIDs,
		"workflow_id":                  record.Consumer.WorkflowID,
		"node_id":                      record.Consumer.NodeID,
		"stage":                        record.Consumer.Stage,
		"semantic_variable":            record.Consumer.SemanticVariable,
		"consumer_schema_version":      record.Consumer.ConsumerSchemaVersion,
		"source_coverage":              record.Coverage.Source,
		"target_coverage":              record.Coverage.Target,
		"claim_coverage":               record.Coverage.Claim,
		"candidate_coverage":           record.Coverage.Candidate,
		"transition_coverage":          record.Coverage.Transition,
		"presentation_coverage":        record.Coverage.Presentation,
		"complete_for_consumer":        record.Coverage.CompleteForConsumer,
		"omissions":                    record.Coverage.Omissions,
		"model_payload_digest":         record.ModelPayloadDigest,
		"model_payload_bytes":          record.ModelPayloadBytes,
		"archived_bytes":               record.ArchivedBytes,
		"runtime_binding_manifest_ref": record.RuntimeBindingManifestRef,
		"candidate_count":              record.CandidateCount,
		"selected_item_count":          record.SelectedItemCount,
		"repair_attempt":               record.RepairAttempt,
		"validation_error_codes":       record.ValidationErrorCodes,
		"reused":                       record.Reused,
		"model_operation":              input.ModelOperation,
		"step":                         input.Step,
	}
	r.addAudit(ctx, app.AuditEvent{
		SessionID: run.SessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "workflow.evidence_projection.created",
		Summary:   "Created a consumer-scoped model evidence projection",
		Fields:    fields,
	})

	return record
}

func workflowEvidenceConsumerForStage(run app.AgentRun, stage workflowStageContext) workflowEvidenceProjectionConsumer {
	semanticVariable := workflowStageSemanticVariable(stage)
	consumerSchema := "workflow_stage_semantic_output_v1"
	if strings.HasPrefix(stage.Reason, "workflow_stage: ") && strings.HasPrefix(browserActiveStage(run.Workflow), browserStageAssessGoalPrefix) {
		consumerSchema = "browser_goal_verdict_v1"
	}
	workflowID := stage.WorkflowID
	if run.Workflow != nil {
		workflowID = run.Workflow.Plan.ProfileID
	}
	return workflowEvidenceProjectionConsumer{
		WorkflowID: workflowID, NodeID: stage.WorkflowNodeID,
		Stage:            browserOrWorkflowStage(run.Workflow, stage.WorkflowNodeID),
		SemanticVariable: semanticVariable, ConsumerSchemaVersion: consumerSchema,
	}
}

func workflowStageSemanticVariable(stage workflowStageContext) string {
	switch {
	case stage.Capability == app.ToolCapabilityBrowserGoalAssess:
		return "browser_goal_verdict"
	case stage.Capability == app.ToolCapabilityBrowserClick:
		return "browser_control_candidate"
	case stage.Capability == app.ToolCapabilityDocumentEdit:
		return "document_mutation_arguments"
	case len(stage.SemanticVariables) == 1:
		return stage.SemanticVariables[0]
	case len(stage.SemanticVariables) > 1:
		return "tool_invocation_arguments"
	default:
		return "workflow_stage_output"
	}
}

func browserOrWorkflowStage(state *app.WorkflowState, nodeID app.WorkflowNodeID) string {
	if state == nil {
		return "workflow_stage"
	}
	if node, ok := state.Nodes[nodeID]; ok && strings.TrimSpace(node.Stage) != "" {
		return node.Stage
	}
	return "workflow_stage"
}

func workflowProjectionBindingManifestRef(run app.AgentRun, nodeID app.WorkflowNodeID, scopeRevision int) string {
	if run.ID == "" {
		return ""
	}
	return "workflow_state:" + run.ID + ":" + string(nodeID) + ":" + strconv.Itoa(scopeRevision)
}

func normalizeWorkflowProjectionCoverage(coverage workflowEvidenceProjectionCoverage, payload string) workflowEvidenceProjectionCoverage {
	if coverage.Source == "" {
		coverage.Source = workflowCoverageNotRequired
	}
	if coverage.Target == "" {
		coverage.Target = workflowCoverageNotRequired
	}
	if coverage.Claim == "" {
		coverage.Claim = workflowCoverageNotRequired
	}
	if coverage.Candidate == "" {
		coverage.Candidate = workflowCoverageNotRequired
	}
	if coverage.Transition == "" {
		coverage.Transition = workflowCoverageNotRequired
	}
	if coverage.Presentation == "" {
		coverage.Presentation = workflowCoverageNotRequired
	}
	if strings.TrimSpace(payload) == "" && coverage.Source != workflowCoverageNotRequired {
		coverage.CompleteForConsumer = false
		coverage.Omissions = appendUniqueString(coverage.Omissions, "model_payload_empty")
	}
	return coverage
}

func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !containsString(out, value) {
			out = append(out, value)
		}
	}
	return out
}
