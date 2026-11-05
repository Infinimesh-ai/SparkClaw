package agent

import (
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type workflowFinalEvidenceProjection struct {
	Evidence                  []string
	SourceEventIDs            []string
	DerivedAssertionIDs       []string
	ArchivedBytes             int
	Coverage                  workflowEvidenceProjectionCoverage
	RuntimeBindingManifestRef string
}

// documentContentCapability reports whether a call's archived output is
// document text for finalization: a complete read, or passages retrieved
// from an indexed large document.
func documentContentCapability(capability string) bool {
	return capability == app.ToolCapabilityDocumentRead || capability == app.ToolCapabilityDocumentReadRange ||
		capability == app.ToolCapabilityDocumentSummarize
}

func (projection workflowFinalEvidenceProjection) modelPayload() string {
	lines := make([]string, 0, len(projection.Evidence))
	for _, evidence := range projection.Evidence {
		lines = append(lines, "- "+evidence)
	}
	return strings.Join(lines, "\n")
}

func buildWorkflowFinalEvidenceProjection(
	run app.AgentRun,
	calls []app.ToolCall,
	observations []string,
	archivedBytesByCall map[string]int,
	maxEvidenceBytes ...int,
) workflowFinalEvidenceProjection {
	projection := workflowFinalEvidenceProjection{
		Coverage: workflowEvidenceProjectionCoverage{
			Source: workflowCoverageNotRequired, Target: workflowCoverageNotRequired,
			Claim: workflowCoverageBounded, Candidate: workflowCoverageNotRequired,
			Transition: workflowCoverageNotRequired, Presentation: workflowCoverageNotRequired,
			CompleteForConsumer: true,
		},
		RuntimeBindingManifestRef: "workflow_state:" + run.ID + ":finalization",
		DerivedAssertionIDs:       workflowFinalizationDerivedAssertionIDs(run.Workflow),
	}
	remaining := defaultWorkflowFinalEvidenceMaxBytes
	if len(maxEvidenceBytes) > 0 && maxEvidenceBytes[0] > 0 {
		remaining = maxEvidenceBytes[0]
	}
	documentEvidence := false
	remainingDocumentCalls := 0
	if isScopedDocumentRead(run.Workflow) {
		for _, call := range calls {
			if output, ok := anyMap(call.Result); ok && toolCallCompleted(call) && documentContentCapability(call.Capability) && firstNonEmptyString(output["content"], output["summary"], output["description"], output["text"]) != "" {
				remainingDocumentCalls++
			}
		}
	}
	retrieved := workflowDocumentRetrieval(calls) != ""
	for _, call := range calls {
		if !toolCallCompleted(call) || !documentContentCapability(call.Capability) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		if retrieved && boolLikeValue(result["retrieval_required"]) {
			// The outline pointer only routed to retrieval; the passages or
			// section summaries read after it carry their own coverage.
			continue
		}
		text := firstNonEmptyString(result["content"], result["summary"], result["description"], result["text"])
		if text == "" {
			continue
		}
		if !documentEvidence {
			projection.Coverage.Source = workflowCoverageComplete
			projection.Coverage.Claim = workflowCoverageComplete
			documentEvidence = true
		}
		projection.SourceEventIDs = appendUniqueString(projection.SourceEventIDs, firstNonEmptyString(call.ObservationRef, call.ID))
		projection.ArchivedBytes += archivedBytesByCall[call.ID]

		format := firstNonEmptyString(result["kind"])
		if document, ok := anyMap(result["document"]); ok {
			format = firstNonEmptyString(document["format"], format)
		}
		sourceComplete := fileReadComplete(result)
		if isScopedDocumentRead(run.Workflow) {
			sourceComplete = run.Workflow.Route.Facts[documentReadFactPrefix+"scope_status"] == "complete"
		}
		callBudget := remaining
		if remainingDocumentCalls > 0 {
			callBudget = remaining / remainingDocumentCalls
			remainingDocumentCalls--
		}
		projected := sampledDocumentEvidence(text, callBudget)
		modelTruncated := projected != text
		if isScopedDocumentRead(run.Workflow) {
			projected, modelTruncated = scopedDocumentFinalEvidence(result, text, callBudget)
		}
		remaining -= len([]byte(projected))
		limitationRequired := !sourceComplete || modelTruncated
		if !sourceComplete {
			projection.Coverage.Source = workflowCoveragePartial
			projection.Coverage.Omissions = appendUniqueString(projection.Coverage.Omissions, "source_read_incomplete")
		}
		if limitationRequired {
			projection.Coverage.Claim = workflowCoveragePartial
		}
		if modelTruncated {
			projection.Coverage.Omissions = appendUniqueString(projection.Coverage.Omissions, "finalizer_content_truncated")
		}

		header := "document_read"
		if format != "" {
			header += " format=" + format
		}
		header += " source_truncated=" + strconv.FormatBool(boolLikeValue(result["truncated"]))
		header += " model_evidence_truncated=" + strconv.FormatBool(modelTruncated)
		if modelTruncated {
			if isScopedDocumentRead(run.Workflow) {
				header += " model_evidence_projection=bounded_with_omissions"
			} else {
				header += " model_evidence_sampling=evenly_spaced_windows"
			}
		}
		header += " claim_coverage=" + map[bool]string{true: workflowCoveragePartial, false: workflowCoverageComplete}[limitationRequired]
		header += " limitation_required=" + strconv.FormatBool(limitationRequired)
		if coverage := projectDocumentReadCoverage(call, result); coverage.Applies {
			if isScopedDocumentRead(run.Workflow) {
				header += " per_call_coverage={" + coverage.manifest() + "}"
			} else {
				header += " " + coverage.manifest()
			}
		}
		projection.Evidence = append(projection.Evidence, header+"\ncontent:\n"+projected)
	}
	if len(projection.Evidence) > 0 {
		applyDocumentScopeToFinalProjection(run, &projection)
		return projection
	}

	projection.Coverage.Source = workflowCoverageComplete
	for index, observation := range observations {
		observation = strings.TrimSpace(observation)
		if observation == "" {
			continue
		}
		projected := boundedUTF8Prefix([]byte(observation), remaining)
		truncated := len([]byte(observation)) > len([]byte(projected))
		if truncated {
			projection.Coverage.Claim = workflowCoveragePartial
			projection.Coverage.Omissions = appendUniqueString(projection.Coverage.Omissions, "finalizer_observation_truncated")
		}
		if projected == "" {
			continue
		}
		projection.Evidence = append(projection.Evidence, projected)
		remaining -= len([]byte(projected))
		if call, ok := workflowCallForFinalObservation(calls, observation, index); ok {
			projection.SourceEventIDs = appendUniqueString(projection.SourceEventIDs, firstNonEmptyString(call.ObservationRef, call.ID))
			projection.ArchivedBytes += archivedBytesByCall[call.ID]
		}
	}
	if len(projection.Evidence) == 0 {
		projection.Coverage.Source = workflowCoverageNotRequired
	} else {
		limitationRequired := projection.Coverage.Claim == workflowCoveragePartial
		projection.Evidence[0] = "finalization_manifest claim_coverage=" + projection.Coverage.Claim +
			" limitation_required=" + strconv.FormatBool(limitationRequired) + "\ncontent:\n" + projection.Evidence[0]
	}
	if display, sourceEventIDs := clientScheduleDisplayEvidence(run, calls); display != "" {
		projection.Evidence = append([]string{display}, projection.Evidence...)
		for _, sourceEventID := range sourceEventIDs {
			projection.SourceEventIDs = appendUniqueString(projection.SourceEventIDs, sourceEventID)
		}
		projection.Coverage.Source = workflowCoverageComplete
	}
	applyDocumentScopeToFinalProjection(run, &projection)
	return projection
}

func clientScheduleDisplayEvidence(run app.AgentRun, calls []app.ToolCall) (string, []string) {
	if run.MessageContext == nil || strings.TrimSpace(run.MessageContext.ClientTimezone) == "" {
		return "", nil
	}
	clientTimezone := strings.TrimSpace(run.MessageContext.ClientTimezone)
	location, err := time.LoadLocation(clientTimezone)
	if err != nil {
		return "", nil
	}
	lines := []string{}
	sourceEventIDs := []string{}
	for _, call := range calls {
		if !toolCallCompleted(call) || call.Capability != app.ToolCapabilityScheduleManage {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		items := []any{result}
		if reminders := anySlice(result["reminders"]); len(reminders) > 0 {
			items = reminders
		}
		added := false
		for _, item := range items {
			reminder, ok := anyMap(item)
			if !ok {
				continue
			}
			dueTime, err := time.Parse(time.RFC3339, strings.TrimSpace(stringValue(reminder["due_time"])))
			if err != nil {
				continue
			}
			lines = append(lines, "schedule_client_display reminder_id="+strconv.Quote(strings.TrimSpace(stringValue(reminder["reminder_id"])))+
				" due_time="+strconv.Quote(dueTime.In(location).Format(time.RFC3339))+" timezone="+strconv.Quote(clientTimezone))
			added = true
		}
		if added {
			sourceEventIDs = appendUniqueString(sourceEventIDs, firstNonEmptyString(call.ObservationRef, call.ID))
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	return "schedule_display_manifest: Display schedule times using only these client-local due_time and timezone values.\n" + strings.Join(lines, "\n"), sourceEventIDs
}

func workflowCallForFinalObservation(calls []app.ToolCall, observation string, index int) (app.ToolCall, bool) {
	for _, call := range calls {
		if strings.TrimSpace(call.ObservationSummary) == observation {
			return call, true
		}
	}
	if index >= 0 && index < len(calls) && toolCallCompleted(calls[index]) {
		return calls[index], true
	}
	return app.ToolCall{}, false
}

func workflowFinalizationDerivedAssertionIDs(state *app.WorkflowState) []string {
	if state == nil {
		return nil
	}
	assertions := []string{}
	if state.Browser != nil && state.Browser.Result != nil {
		assertions = appendUniqueString(assertions, state.Browser.Result.PresentationAssertionID)
	}
	for _, node := range state.Nodes {
		for _, ref := range node.OutcomeRefs {
			switch ref.Kind {
			case "browser_transition", "browser_presentation_equivalence":
				assertions = appendUniqueString(assertions, ref.Ref)
			}
		}
	}
	return assertions
}
