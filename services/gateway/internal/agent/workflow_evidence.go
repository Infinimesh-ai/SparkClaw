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
	Text        string
	CompactText string
	MinimalText string
	Bytes       int
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
		Text:        strings.Join(sections, "\n\n"),
		CompactText: strings.Join(compactSections, "\n\n"),
		MinimalText: strings.Join(minimalSections, "\n\n"),
		Bytes:       providedBytes,
	}, nil
}

func (r Runtime) workflowStageEvidenceLimit() int {
	stageLimit := r.tools.Config().Runtime.StageEvidenceMaxBytes
	if stageLimit <= 0 {
		return 8000
	}
	return stageLimit
}

func sliceWorkflowEvidenceForRun(run app.AgentRun, tool string, output any, mode workflowEvidenceSliceMode, maxBytes int, ownerRequest string) (string, error) {
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
	var object app.ArtifactObject
	for _, candidate := range r.store.ListArtifactObjects(0) {
		if candidate.URI == call.ObservationRef && candidate.SessionID == run.SessionID && candidate.RunID == run.ID {
			object = candidate
			break
		}
	}
	if object.URI == "" {
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
