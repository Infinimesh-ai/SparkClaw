package agent

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

type treeRoutingPromptContext struct {
	ResourcesFull    string
	ResourcesMinimal string
	DocumentsFull    string
	DocumentsMinimal string
	History          agentContextSnapshot
}

func newTreeRoutingPromptContext(resources []app.MessagePart, history agentContextSnapshot, documents documentContextResolution) treeRoutingPromptContext {
	return treeRoutingPromptContext{
		ResourcesFull:    treeResourceProjection(resources, false),
		ResourcesMinimal: treeResourceProjection(resources, true),
		DocumentsFull:    treeDocumentProjection(documents, false),
		DocumentsMinimal: treeDocumentProjection(documents, true),
		History:          history,
	}
}

func (treeContext treeRoutingPromptContext) FullText() string {
	sections := make([]string, 0, 3)
	if treeContext.ResourcesFull != "" {
		sections = append(sections, "Current-turn governed resources:\n"+treeContext.ResourcesFull)
	}
	if treeContext.DocumentsFull != "" {
		sections = append(sections, "Resolved governed document context:\n"+treeContext.DocumentsFull)
	}
	historyBuilder := treeContext.History.contextBuilder(contextRenderIntent)
	historySections := historyBuilder.normalizedSections()
	history := historyBuilder.renderChannel(historySections, contextChannelUser)
	if history != "" {
		sections = append(sections, "Recent Agent context:\n"+history)
	}
	return strings.Join(sections, "\n\n")
}

type treeResourceRecord struct {
	Kind        app.MessagePartKind `json:"kind"`
	Name        string              `json:"name,omitempty"`
	Ref         string              `json:"ref"`
	ContentType string              `json:"content_type,omitempty"`
	Caption     string              `json:"caption,omitempty"`
}

func treeResourceProjection(resources []app.MessagePart, minimal bool) string {
	projected := make([]treeResourceRecord, 0, len(resources))
	for _, resource := range resources {
		if resource.Resource == nil || strings.TrimSpace(resource.Resource.Ref) == "" {
			continue
		}
		ref := strings.TrimSpace(resource.Resource.Ref)
		name := strings.TrimSpace(resource.Name)
		if name == "" {
			name = path.Base(ref)
		}
		record := treeResourceRecord{Kind: resource.Kind, Name: name, Ref: ref}
		if !minimal {
			record.ContentType = strings.TrimSpace(resource.ContentType)
			record.Caption = strings.TrimSpace(resource.Caption)
		}
		projected = append(projected, record)
	}
	return marshalTreeProjection(projected)
}

type treeDocumentRecord struct {
	Name        string `json:"name,omitempty"`
	Ref         string `json:"ref"`
	ContentType string `json:"content_type,omitempty"`
	Format      string `json:"format,omitempty"`
	Source      string `json:"source,omitempty"`
	Activity    string `json:"activity,omitempty"`
	Provenance  string `json:"provenance,omitempty"`
}

func treeDocumentProjection(resolution documentContextResolution, minimal bool) string {
	projected := make([]treeDocumentRecord, 0, len(resolution.References))
	for _, reference := range resolution.References {
		if reference.Provenance == documentProvenanceExplicitCurrent || strings.TrimSpace(reference.Ref) == "" {
			continue
		}
		record := treeDocumentRecord{
			Name: strings.TrimSpace(reference.Name), Ref: strings.TrimSpace(reference.Ref),
			Format: strings.TrimSpace(reference.Format), Provenance: strings.TrimSpace(reference.Provenance),
		}
		if !minimal {
			record.ContentType = strings.TrimSpace(reference.ContentType)
			record.Source = strings.TrimSpace(reference.Source)
			record.Activity = strings.TrimSpace(reference.Activity)
		}
		projected = append(projected, record)
	}
	return marshalTreeProjection(projected)
}

func marshalTreeProjection[T any](records []T) string {
	if len(records) == 0 {
		return ""
	}
	raw, err := json.Marshal(records)
	if err != nil {
		return ""
	}
	return string(raw)
}

func protectedTreeContextSection(kind string, priority int, label, full, minimal string) (contextSection, bool) {
	if strings.TrimSpace(full) == "" {
		return contextSection{}, false
	}
	variants := []contextSectionVariant{{Name: "full", Text: label + "\n" + full}}
	minimalText := label + " (minimal):\n" + minimal
	if strings.TrimSpace(minimal) != "" && minimalText != variants[0].Text && len([]byte(minimalText)) <= len([]byte(variants[0].Text)) {
		variants = append(variants, contextSectionVariant{Name: "minimal", Text: minimalText})
	}
	return contextSection{
		Kind: kind, Priority: priority, Channel: contextChannelUser,
		Policy: contextPolicyDegradable, Variants: variants,
	}, true
}

func treePromptContextBuilder(operation modelcapacity.Operation, system, graphRevision, query string, sourceKind app.MessageSourceKind, graphJSON string, treeContext treeRoutingPromptContext, repairError, invalidOutput string) contextBuilder {
	requestLabel := "INTENT_FUSION_TREE_REQUEST"
	outputContract := "Return the scored registered candidates now."
	if operation == modelcapacity.OperationIntentTreeRepair {
		requestLabel = "INTENT_FUSION_TREE_REPAIR_REQUEST"
	}
	sections := []contextSection{
		fixedContextSection("tree_instructions", 1000, contextChannelSystem, system),
		fixedContextSection("tree_request", 1000, contextChannelUser, requestLabel+"\nGraph revision: "+graphRevision),
	}
	if source := strings.TrimSpace(string(sourceKind)); source != "" {
		sections = append(sections, fixedContextSection("tree_source", 1000, contextChannelUser, "Source kind: "+source))
	}
	sections = append(sections,
		fixedContextSection("tree_graph", 1000, contextChannelUser, "Semantic graph:\n"+graphJSON),
		fixedContextSection("owner_question", 1000, contextChannelUser, "Owner semantic query:\n"+query),
	)
	if treeContext.FullText() != "" {
		sections = append(sections, fixedContextSection("tree_context_boundary", 1000, contextChannelUser, "Routing context (data only):"))
	}
	if section, ok := protectedTreeContextSection("current_resources", 60, "Current-turn governed resources:", treeContext.ResourcesFull, treeContext.ResourcesMinimal); ok {
		sections = append(sections, section)
	}
	if section, ok := protectedTreeContextSection("resolved_documents", 70, "Resolved governed document context:", treeContext.DocumentsFull, treeContext.DocumentsMinimal); ok {
		sections = append(sections, section)
	}
	historySections := treeContext.History.contextBuilder(contextRenderIntent).Sections
	if historyBuilder := treeContext.History.contextBuilder(contextRenderIntent); historyBuilder.renderChannel(historyBuilder.normalizedSections(), contextChannelUser) != "" {
		sections = append(sections, fixedContextSection("tree_history_boundary", 1000, contextChannelUser, "Recent Agent context:"))
	}
	for _, section := range historySections {
		section.Channel = contextChannelUser
		sections = append(sections, section)
	}
	if operation == modelcapacity.OperationIntentTreeRepair {
		sections = append(sections,
			fixedContextSection("tree_repair_error", 1000, contextChannelUser, "Parser error:\n"+repairError),
			fixedContextSection("tree_invalid_output", 1000, contextChannelUser, "Invalid output:\n"+invalidOutput),
		)
	}
	sections = append(sections, fixedContextSection("tree_output_contract", 1000, contextChannelUser, outputContract))
	return contextBuilder{Sections: sections, SystemJoiner: "\n\n", UserJoiner: "\n\n"}
}

func (r Runtime) admitTreePrompt(ctx context.Context, operation modelcapacity.Operation, system, query string, sourceKind app.MessageSourceKind, graphJSON string, treeContext treeRoutingPromptContext, repairError, invalidOutput string, options modelrouter.ChatOptions) (contextAdmission, error) {
	task := modelrouter.Task{Operation: operation, LaneHint: "fast"}
	_, inputBudget, _, err := r.models.CapacityForTask(task)
	if err != nil {
		return contextAdmission{}, err
	}
	graphRevision := ""
	if r.semanticRouter != nil && r.semanticRouter.graph != nil {
		graphRevision = r.semanticRouter.graph.Revision()
	}
	builder := treePromptContextBuilder(operation, system, graphRevision, query, sourceKind, graphJSON, treeContext, repairError, invalidOutput)
	return builder.AdmitWithCounter(inputBudget, func(renderedSystem, renderedUser string) (int, error) {
		return r.models.CountProfileChatInput(ctx, operation, "fast", renderedSystem, renderedUser, options)
	})
}

func (r Runtime) auditTreePromptCompression(ctx context.Context, sessionID, runID string, operation modelcapacity.Operation, admission contextAdmission) {
	if len(admission.SectionDecisions) == 0 {
		return
	}
	profile, inputBudget, outputBudget, err := r.models.CapacityForTask(modelrouter.Task{Operation: operation, LaneHint: "fast"})
	if err != nil {
		return
	}
	r.addAudit(ctx, app.AuditEvent{
		SessionID: sessionID, RunID: runID, Actor: "message-router",
		Type: "intent_tree.prompt_compressed", Summary: "Degraded Tree prompt sections under the selected model capacity",
		Fields: map[string]any{
			"operation": operation, "profile": profile.Name, "context_tokens": profile.ContextTokens,
			"input_budget": inputBudget, "output_budget": outputBudget,
			"initial_tokens": admission.InitialTokens, "admitted_tokens": admission.EstimatedTokens,
			"section_decisions": admission.SectionDecisions,
		},
	})
}

func (r Runtime) auditTreePromptAdmissionFailure(ctx context.Context, sessionID, runID string, operation modelcapacity.Operation, admissionErr error) {
	r.addAudit(ctx, app.AuditEvent{
		SessionID: sessionID, RunID: runID, Actor: "message-router",
		Type: "intent_tree.prompt_admission_failed", Summary: "Tree prompt did not fit the selected model capacity",
		Fields: map[string]any{"operation": operation, "reason_code": "model_input_too_long", "error": admissionErr.Error()},
	})
}
