package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type provisionedWorkflowEvidence struct {
	Text                      string
	CompactText               string
	MinimalText               string
	Bytes                     int
	ArchivedBytes             int
	SourceEventIDs            []string
	DerivedAssertionIDs       []string
	Coverage                  workflowEvidenceProjectionCoverage
	RuntimeBindingManifestRef string
}

type archivedToolObservation struct {
	Tool      string          `json:"tool"`
	RunID     string          `json:"run_id"`
	SessionID string          `json:"session_id"`
	Output    json.RawMessage `json:"output"`
}

func (r Runtime) provisionWorkflowEvidence(ctx context.Context, run app.AgentRun, requirements []workflowEvidenceRequirement) (provisionedWorkflowEvidence, error) {
	if len(requirements) == 0 {
		return provisionedWorkflowEvidence{}, nil
	}
	stageLimit := r.workflowStageEvidenceLimit()
	remaining := stageLimit
	ownerRequest := requestContentForRun(r.store.ListMessages(run.SessionID), run)
	sections := make([]string, 0, len(requirements))
	compactSections := make([]string, 0, len(requirements))
	minimalSections := make([]string, 0, len(requirements))
	providedBytes := 0
	archivedBytes := 0
	sourceEventIDs := []string{}
	derivedAssertionIDs := []string{}
	coverage := workflowEvidenceProjectionCoverage{
		Source: workflowCoverageComplete, Target: workflowCoverageComplete,
		Claim: workflowCoverageNotRequired, Candidate: workflowCoverageNotRequired,
		Transition: workflowCoverageNotRequired, Presentation: workflowCoverageNotRequired,
		CompleteForConsumer: true,
	}
	formatPolicy := agentDocumentFormatPolicy{}
	if run.Workflow != nil {
		formatPolicy, _ = registeredAgentDocumentFormatPolicies().policyForRoute(run.Workflow.Route)
	}
	for _, requirement := range requirements {
		if remaining <= 0 {
			if requirement.Optional {
				continue
			}
			return provisionedWorkflowEvidence{}, errors.New("required workflow evidence exceeds the stage evidence budget")
		}
		call, ref, err := r.resolveWorkflowEvidenceCall(run, requirement)
		if err != nil {
			if requirement.Optional {
				continue
			}
			return provisionedWorkflowEvidence{}, err
		}
		output, artifactBytes, err := r.readArchivedToolObservation(ctx, run, call)
		if err != nil {
			if requirement.Optional {
				continue
			}
			return provisionedWorkflowEvidence{}, err
		}
		evidencePolicy := formatPolicy
		if outputMap, ok := outputAsMap(output); ok {
			if resultPolicy, registered := registeredAgentDocumentFormatPolicies().policyForResult(call, outputMap); registered {
				evidencePolicy = resultPolicy
			}
		}
		limit := requirement.MaxBytes
		if limit <= 0 || limit > remaining {
			limit = remaining
		}
		text, sliceErr := sliceWorkflowEvidenceForRun(run, call.Tool, output, requirement.Mode, limit, ownerRequest)
		if sliceErr != nil {
			if requirement.Optional {
				continue
			}
			return provisionedWorkflowEvidence{}, sliceErr
		}
		if strings.TrimSpace(text) == "" {
			if requirement.Optional {
				continue
			}
			return provisionedWorkflowEvidence{}, errors.New("required workflow evidence produced an empty slice")
		}
		used := len([]byte(text))
		if used > remaining {
			return provisionedWorkflowEvidence{}, errors.New("workflow evidence slicer exceeded the stage evidence budget")
		}
		if evidencePolicy.ValidateEvidenceSlice != nil {
			if err := evidencePolicy.ValidateEvidenceSlice(text); err != nil {
				return provisionedWorkflowEvidence{}, err
			}
		}
		sections = append(sections, formatProvisionedEvidenceSection(ref, call.Tool, requirement.Mode, text))
		compactLimit := min(limit, max(512, limit/2))
		minimalLimit := min(compactLimit, max(256, limit/4))
		compactText, _ := sliceWorkflowEvidenceForRun(run, call.Tool, output, requirement.Mode, compactLimit, ownerRequest)
		minimalText, _ := sliceWorkflowEvidenceForRun(run, call.Tool, output, requirement.Mode, minimalLimit, ownerRequest)
		if evidencePolicy.ValidateEvidenceSlice != nil {
			if err := evidencePolicy.ValidateEvidenceSlice(compactText); err != nil {
				compactText = text
			}
		}
		if evidencePolicy.ValidateEvidenceSlice != nil {
			if err := evidencePolicy.ValidateEvidenceSlice(minimalText); err != nil {
				minimalText = compactText
			}
		}
		if strings.TrimSpace(compactText) != "" {
			compactSections = append(compactSections, formatProvisionedEvidenceSection(ref, call.Tool, requirement.Mode, compactText))
		}
		if strings.TrimSpace(minimalText) != "" {
			minimalSections = append(minimalSections, formatProvisionedEvidenceSection(ref, call.Tool, requirement.Mode, minimalText))
		}
		providedBytes += used
		archivedBytes += artifactBytes
		sourceEventIDs = appendUniqueString(sourceEventIDs, firstNonEmptyString(call.ObservationRef, call.ID))
		remaining -= used
		auditFields := map[string]any{
			"source_ref": ref, "tool_call_id": call.ID, "tool": call.Tool, "mode": requirement.Mode,
			"provisioned_bytes": used, "total_artifact_bytes": artifactBytes,
		}
		if evidencePolicy.ProjectEvidenceAudit != nil {
			for key, value := range evidencePolicy.ProjectEvidenceAudit(text) {
				auditFields[key] = value
			}
		}
		coverage = mergeWorkflowEvidenceProjectionCoverage(coverage, workflowCoverageForProvisionedEvidence(call.Tool, output, text, auditFields))
		if run.Workflow != nil && len(run.Workflow.ActiveNodeIDs) == 1 {
			if state, ok := run.Workflow.Nodes[run.Workflow.ActiveNodeIDs[0]]; ok {
				for _, ref := range state.OutcomeRefs {
					if ref.Kind == "browser_transition" || ref.Kind == "browser_presentation_equivalence" {
						derivedAssertionIDs = appendUniqueString(derivedAssertionIDs, ref.Ref)
					}
				}
			}
		}
		r.store.AddAudit(app.AuditEvent{
			SessionID: run.SessionID,
			RunID:     run.ID,
			Actor:     "runtime",
			Type:      "workflow_step.evidence_provisioned",
			Summary:   "Provisioned persisted evidence for the active workflow stage",
			Fields:    auditFields,
		})
	}
	if len(sections) == 0 {
		return provisionedWorkflowEvidence{}, nil
	}
	return provisionedWorkflowEvidence{
		Text:                      strings.Join(sections, "\n\n"),
		CompactText:               strings.Join(compactSections, "\n\n"),
		MinimalText:               strings.Join(minimalSections, "\n\n"),
		Bytes:                     providedBytes,
		ArchivedBytes:             archivedBytes,
		SourceEventIDs:            sourceEventIDs,
		DerivedAssertionIDs:       derivedAssertionIDs,
		Coverage:                  coverage,
		RuntimeBindingManifestRef: workflowProjectionBindingManifestRef(run, activeWorkflowNodeID(run.Workflow), activeWorkflowScopeRevision(run.Workflow)),
	}, nil
}

func activeWorkflowNodeID(state *app.WorkflowState) app.WorkflowNodeID {
	if state == nil || len(state.ActiveNodeIDs) != 1 {
		return ""
	}
	return state.ActiveNodeIDs[0]
}

func activeWorkflowScopeRevision(state *app.WorkflowState) int {
	if state == nil {
		return 0
	}
	return state.Nodes[activeWorkflowNodeID(state)].ScopeRevision
}

func workflowCoverageForProvisionedEvidence(tool string, output any, text string, fields map[string]any) workflowEvidenceProjectionCoverage {
	coverage := workflowEvidenceProjectionCoverage{
		Source: workflowCoverageComplete, Target: workflowCoverageComplete,
		Claim: workflowCoverageNotRequired, Candidate: workflowCoverageNotRequired,
		Transition: workflowCoverageNotRequired, Presentation: workflowCoverageNotRequired,
		CompleteForConsumer: strings.TrimSpace(text) != "",
	}
	if outputMap, ok := outputAsMap(output); ok {
		if (tool == "files.read" || tool == "pdf.extract_text" || tool == "images.inspect") && !fileReadComplete(outputMap) {
			coverage.Source = workflowCoveragePartial
			coverage.CompleteForConsumer = false
			coverage.Omissions = append(coverage.Omissions, "source_read_incomplete")
		}
		if tool == "browser.snapshot" {
			coverage.Source = workflowCoverageComplete
			coverage.Target = workflowCoverageBounded
			coverage.Candidate = workflowCoverageBounded
		}
	}
	if complete, ok := fields["selection_complete"].(bool); ok && !complete {
		coverage.Target = workflowCoveragePartial
		coverage.CompleteForConsumer = false
		coverage.Omissions = append(coverage.Omissions, "required_target_omitted")
	}
	for _, key := range []string{"omitted_sheets", "omitted_rows", "omitted_cells"} {
		if intLikeValue(fields[key]) > 0 {
			coverage.Omissions = appendUniqueString(coverage.Omissions, key)
		}
	}
	return coverage
}

func mergeWorkflowEvidenceProjectionCoverage(left, right workflowEvidenceProjectionCoverage) workflowEvidenceProjectionCoverage {
	merged := left
	merged.Source = leastCompleteWorkflowCoverage(left.Source, right.Source)
	merged.Target = leastCompleteWorkflowCoverage(left.Target, right.Target)
	merged.Claim = leastCompleteWorkflowCoverage(left.Claim, right.Claim)
	merged.Candidate = leastCompleteWorkflowCoverage(left.Candidate, right.Candidate)
	merged.Transition = leastCompleteWorkflowCoverage(left.Transition, right.Transition)
	merged.Presentation = leastCompleteWorkflowCoverage(left.Presentation, right.Presentation)
	merged.CompleteForConsumer = left.CompleteForConsumer && right.CompleteForConsumer
	for _, omission := range right.Omissions {
		merged.Omissions = appendUniqueString(merged.Omissions, omission)
	}
	return merged
}

func leastCompleteWorkflowCoverage(left, right string) string {
	rank := map[string]int{
		workflowCoverageUnknown: 0, workflowCoveragePartial: 1, workflowCoverageBounded: 2,
		workflowCoverageComplete: 3, workflowCoverageNotRequired: 4,
	}
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if rank[right] < rank[left] {
		return right
	}
	return left
}

func (r Runtime) workflowStageEvidenceLimit() int {
	stageLimit := r.tools.Config().Runtime.StageEvidenceMaxBytes
	if stageLimit <= 0 {
		return 8000
	}
	return stageLimit
}

func sliceWorkflowEvidenceForRun(run app.AgentRun, tool string, output any, mode workflowEvidenceSliceMode, maxBytes int, ownerRequest string) (string, error) {
	if tool == "browser.snapshot" && mode == workflowEvidenceStructured {
		if projection, handled := browserWorkflowEvidenceProjection(run, output, maxBytes); handled {
			if strings.TrimSpace(projection) == "" {
				return "", errors.New("required browser transition evidence exceeds the stage evidence budget")
			}
			return projection, nil
		}
	}
	if run.Workflow != nil {
		if policy, ok := registeredAgentDocumentFormatPolicies().policyForRoute(run.Workflow.Route); ok && policy.SliceWorkflowEvidence != nil {
			return policy.SliceWorkflowEvidence(run, tool, output, mode, maxBytes, ownerRequest)
		}
	}
	return slicePersistedToolEvidenceForRequest(tool, output, mode, maxBytes, ownerRequest), nil
}

func formatProvisionedEvidenceSection(ref, tool string, mode workflowEvidenceSliceMode, text string) string {
	return fmt.Sprintf("source=%s tool=%s mode=%s bytes=%d\n%s", ref, tool, mode, len([]byte(text)), text)
}

func (r Runtime) resolveWorkflowEvidenceCall(run app.AgentRun, requirement workflowEvidenceRequirement) (app.ToolCall, string, error) {
	if run.Workflow == nil {
		return app.ToolCall{}, "", errors.New("workflow state is unavailable for evidence provisioning")
	}
	candidateIDs := []string{}
	if requirement.SourceNodeID != "" {
		state, ok := run.Workflow.Nodes[requirement.SourceNodeID]
		if !ok || state.Status != app.WorkflowNodeSucceeded {
			return app.ToolCall{}, "", fmt.Errorf("required evidence source node %s is not completed", requirement.SourceNodeID)
		}
		candidateIDs = append(candidateIDs, state.ToolCallIDs...)
	} else if requirement.ResourceKind != "" {
		for _, nodeID := range run.Workflow.ActiveNodeIDs {
			state := run.Workflow.Nodes[nodeID]
			for _, ref := range state.OutcomeRefs {
				if ref.Kind == requirement.ResourceKind && strings.TrimSpace(ref.Provenance) != "" {
					candidateIDs = append(candidateIDs, ref.Provenance)
				}
			}
		}
	} else {
		return app.ToolCall{}, "", errors.New("workflow evidence requirement has no source")
	}

	var selected app.ToolCall
	for _, callID := range candidateIDs {
		call, ok := r.store.GetToolCall(callID)
		if !ok || !toolCallCompleted(call) || call.RunID != run.ID || call.SessionID != run.SessionID || strings.TrimSpace(call.ObservationRef) == "" {
			continue
		}
		if selected.ID == "" || toolCallCompletionTime(call).After(toolCallCompletionTime(selected)) {
			selected = call
		}
	}
	if selected.ID == "" {
		return app.ToolCall{}, "", errors.New("required workflow evidence has no persisted observation reference")
	}
	return selected, selected.ObservationRef, nil
}

func toolCallCompletionTime(call app.ToolCall) time.Time {
	if call.CompletedAt != nil {
		return *call.CompletedAt
	}
	return call.StartedAt
}

func (r Runtime) readArchivedToolObservation(ctx context.Context, run app.AgentRun, call app.ToolCall) (any, int, error) {
	if r.artifacts == nil {
		return nil, 0, errors.New("artifact store is unavailable")
	}
	object, ok := r.store.FindArtifactObjectByURI(call.ObservationRef, run.SessionID, run.ID)
	if !ok {
		return nil, 0, errors.New("persisted workflow evidence is outside the active run")
	}
	raw, err := r.artifacts.Get(ctx, object.Key)
	if err != nil {
		return nil, 0, fmt.Errorf("read workflow evidence artifact: %w", err)
	}
	var archived archivedToolObservation
	if err := json.Unmarshal(raw, &archived); err != nil {
		return nil, 0, errors.New("workflow evidence artifact is invalid")
	}
	if archived.Tool != call.Tool || archived.RunID != run.ID || archived.SessionID != run.SessionID || len(archived.Output) == 0 {
		return nil, 0, errors.New("workflow evidence artifact does not match its persisted tool call")
	}
	var output any
	if err := json.Unmarshal(archived.Output, &output); err != nil {
		return nil, 0, errors.New("workflow evidence output is invalid")
	}
	return output, object.Bytes, nil
}
