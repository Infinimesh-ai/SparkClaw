package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type agentDocumentRouteGrounding struct {
	Status app.RouteStatus
	Reason string
}

type orderedDocumentDecisionRule struct {
	Order int
	Text  string
}

type agentDocumentOperationPolicy struct {
	BindArguments      func(Runtime, app.AgentRun, map[string]any) map[string]any
	ValidateEvidence   func(context.Context, Runtime, app.AgentRun, string, map[string]any) error
	RevalidateApproved func(context.Context, Runtime, app.ToolCall) error
}

type agentDocumentFormatPolicy struct {
	Format                  string
	RouteOperations         []app.RouteOperation
	GroundRoute             func(string, app.RouteOperation, map[string]string) agentDocumentRouteGrounding
	ScopeDirectory          func(app.RouteDecision, []app.ToolDirectoryEntry) []app.ToolDirectoryEntry
	DecisionEvidence        func(Runtime, context.Context, app.AgentRun, app.WorkflowNode, []app.ToolDirectoryEntry) (string, error)
	SliceWorkflowEvidence   func(app.AgentRun, string, any, workflowEvidenceSliceMode, int, string) (string, error)
	ValidateEvidenceSlice   func(string) error
	ProjectEvidenceAudit    func(string) map[string]any
	MaterializeSchemas      func([]app.ToolDefinition, app.DirectoryView, []app.ToolDirectoryEntryID) []app.ToolDefinition
	ProjectReadCoverage     func(app.ToolCall, map[string]any) documentReadCoverage
	SliceStructuredEvidence func(map[string]any, int, string) string
	BuildReadEvidence       func(map[string]any, string, int) []toolEvidence
	DecisionRules           []orderedDocumentDecisionRule
	Operations              map[string]agentDocumentOperationPolicy
}

type agentDocumentFormatPolicyRegistry struct {
	formats map[string]agentDocumentFormatPolicy
	order   []string
}

func newAgentDocumentFormatPolicyRegistry(policies ...agentDocumentFormatPolicy) agentDocumentFormatPolicyRegistry {
	registry := agentDocumentFormatPolicyRegistry{formats: make(map[string]agentDocumentFormatPolicy, len(policies))}
	for _, policy := range policies {
		format := canonicalAgentDocumentKey(policy.Format)
		if format == "" {
			panic("agent: document format policy has an empty format")
		}
		if _, exists := registry.formats[format]; exists {
			panic(fmt.Sprintf("agent: duplicate document format policy %q", format))
		}
		policy.Format = format
		operations := make(map[string]agentDocumentOperationPolicy, len(policy.Operations))
		for operation, operationPolicy := range policy.Operations {
			operation = canonicalAgentDocumentKey(operation)
			if operation == "" {
				panic(fmt.Sprintf("agent: %s document policy has an empty operation", format))
			}
			if _, exists := operations[operation]; exists {
				panic(fmt.Sprintf("agent: duplicate document operation policy %s:%s", format, operation))
			}
			operations[operation] = operationPolicy
		}
		policy.Operations = operations
		registry.formats[format] = policy
		registry.order = append(registry.order, format)
	}
	return registry
}

func canonicalAgentDocumentKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (r agentDocumentFormatPolicyRegistry) format(format string) (agentDocumentFormatPolicy, bool) {
	policy, ok := r.formats[canonicalAgentDocumentKey(format)]
	return policy, ok
}

func (r agentDocumentFormatPolicyRegistry) operation(format, operation string) (agentDocumentOperationPolicy, bool) {
	formatPolicy, ok := r.format(format)
	if !ok {
		return agentDocumentOperationPolicy{}, false
	}
	policy, ok := formatPolicy.Operations[canonicalAgentDocumentKey(operation)]
	return policy, ok
}

func (r agentDocumentFormatPolicyRegistry) allowsRouteOperation(format string, operation app.RouteOperation) bool {
	policy, ok := r.format(format)
	return ok && slices.Contains(policy.RouteOperations, operation)
}

func (r agentDocumentFormatPolicyRegistry) policyForRoute(route app.RouteDecision) (agentDocumentFormatPolicy, bool) {
	format := firstNonEmptyString(route.Facts["document_format"], route.Slots.Format)
	return r.format(format)
}

func (r agentDocumentFormatPolicyRegistry) policyForResult(call app.ToolCall, output map[string]any) (agentDocumentFormatPolicy, bool) {
	document, _ := anyMap(output["document"])
	format := canonicalAgentDocumentKey(firstNonEmptyString(document["format"], output["kind"]))
	if policy, ok := r.format(format); ok {
		return policy, true
	}
	if call.Tool == "pdf.extract_text" {
		return r.format(app.DocumentFormatPDF)
	}
	return agentDocumentFormatPolicy{}, false
}

func (r agentDocumentFormatPolicyRegistry) decisionRules() []string {
	rules := commonDocumentDecisionRules()
	for _, format := range r.order {
		rules = append(rules, r.formats[format].DecisionRules...)
	}
	sort.SliceStable(rules, func(left, right int) bool { return rules[left].Order < rules[right].Order })
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.Text)
	}
	return out
}

var (
	agentDocumentPoliciesOnce sync.Once
	agentDocumentPolicies     agentDocumentFormatPolicyRegistry
)

func registeredAgentDocumentFormatPolicies() agentDocumentFormatPolicyRegistry {
	agentDocumentPoliciesOnce.Do(func() {
		agentDocumentPolicies = newAgentDocumentFormatPolicyRegistry(
			textAgentDocumentPolicy(),
			docxAgentDocumentPolicy(),
			xlsxAgentDocumentPolicy(),
			pptxAgentDocumentPolicy(),
			pdfAgentDocumentPolicy(),
			imageAgentDocumentPolicy(),
		)
	})
	return agentDocumentPolicies
}

func textAgentDocumentPolicy() agentDocumentFormatPolicy {
	return agentDocumentFormatPolicy{
		Format: app.DocumentFormatText, RouteOperations: []app.RouteOperation{app.RouteOperationEdit},
		Operations: map[string]agentDocumentOperationPolicy{"replace_text": {}},
	}
}

func docxAgentDocumentPolicy() agentDocumentFormatPolicy {
	operations := map[string]agentDocumentOperationPolicy{}
	for _, operation := range []string{"replace_text", "replace_paragraph", "insert_paragraph", "delete_paragraph", "set_text_style"} {
		operation := operation
		operations[operation] = agentDocumentOperationPolicy{
			BindArguments: func(runtime Runtime, run app.AgentRun, args map[string]any) map[string]any {
				return runtime.bindDOCXMutationEvidence(run, operation, args)
			},
			ValidateEvidence: func(_ context.Context, runtime Runtime, run app.AgentRun, toolName string, args map[string]any) error {
				return runtime.validateDOCXMutationEvidence(run, toolName, operation, args)
			},
			RevalidateApproved: func(ctx context.Context, runtime Runtime, call app.ToolCall) error {
				return runtime.revalidateApprovedDOCXMutation(ctx, call, operation)
			},
		}
	}
	return agentDocumentFormatPolicy{
		Format: app.DocumentFormatDOCX, RouteOperations: []app.RouteOperation{app.RouteOperationEdit},
		DecisionEvidence: func(runtime Runtime, ctx context.Context, run app.AgentRun, node app.WorkflowNode, entries []app.ToolDirectoryEntry) (string, error) {
			return runtime.workflowDOCXDecisionEvidence(ctx, run, node, entries)
		},
		DecisionRules: []orderedDocumentDecisionRule{{Order: 50, Text: "For DOCX, the listed editors currently mutate body paragraph text or style only. Return an empty entry_id for table-cell, header, footer, footnote, endnote, text-box, comment, field, drawing, tracked-change, or other unsupported targets unless one eligible entry explicitly advertises that target."}},
		Operations:    operations,
	}
}

func xlsxAgentDocumentPolicy() agentDocumentFormatPolicy {
	operations := map[string]agentDocumentOperationPolicy{}
	for _, operation := range []string{"replace_text", "update_cell", "insert_row", "delete_row", "update_row", "append_row"} {
		operation := operation
		operations[operation] = agentDocumentOperationPolicy{
			BindArguments: func(runtime Runtime, run app.AgentRun, args map[string]any) map[string]any {
				return runtime.bindXLSXEditEvidence(run, operation, args)
			},
			ValidateEvidence: func(ctx context.Context, runtime Runtime, run app.AgentRun, _ string, args map[string]any) error {
				return runtime.validateXLSXEditEvidence(ctx, run, operation, args)
			},
		}
	}
	return agentDocumentFormatPolicy{
		Format: app.DocumentFormatXLSX, RouteOperations: []app.RouteOperation{app.RouteOperationEdit},
		ValidateEvidenceSlice: func(text string) error {
			if complete, handled := xlsxEvidenceSelectionState(text); handled && !complete {
				return errors.New("required XLSX target evidence exceeds the stage evidence budget")
			}
			return nil
		},
		ProjectEvidenceAudit: xlsxEvidenceAuditFields,
		SliceStructuredEvidence: func(output map[string]any, maxBytes int, ownerRequest string) string {
			return xlsxSheetEvidenceProjection(output, ownerRequest, maxBytes)
		},
		BuildReadEvidence: xlsxDocumentReadEvidence,
		DecisionRules: []orderedDocumentDecisionRule{
			{Order: 60, Text: "Treat the structured observation only as verification for mutation target and content the owner explicitly provided; never invent a missing target or new value from observed data. For a cell update, require the owner to supply the new value and either the exact cell address or a uniquely identifying existing record plus field; otherwise return no entry."},
			{Order: 70, Text: "For structured rows, change one explicit cell with a cell editor; change multiple supplied fields of one existing row with a row update; do not turn either request into a new row."},
			{Order: 80, Text: "Choose positional insertion only for a new row with an explicit before or after anchor, append only for a new row at the final structured boundary, and delete-row only for explicit removal of the complete row."},
			{Order: 90, Text: "Clearing a cell, removing exact matching text, deleting a column, deleting the workbook file, and an ambiguous target are not complete-row deletion requests; return no entry when no listed operation matches exactly."},
		},
		Operations: operations,
	}
}

func pptxAgentDocumentPolicy() agentDocumentFormatPolicy {
	operations := map[string]agentDocumentOperationPolicy{}
	for _, operation := range []string{"replace_text", "add_slide", "update_slide", "update_deck", "duplicate_slide", "delete_slide"} {
		operation := operation
		operations[operation] = agentDocumentOperationPolicy{
			BindArguments: func(runtime Runtime, run app.AgentRun, args map[string]any) map[string]any {
				return runtime.bindPPTXEditArguments(run, operation, args)
			},
			ValidateEvidence: func(_ context.Context, runtime Runtime, run app.AgentRun, _ string, args map[string]any) error {
				return runtime.validatePPTXEditEvidence(run, operation, args)
			},
		}
	}
	return agentDocumentFormatPolicy{
		Format: app.DocumentFormatPPTX, RouteOperations: []app.RouteOperation{app.RouteOperationEdit},
		GroundRoute: func(content string, operation app.RouteOperation, facts map[string]string) agentDocumentRouteGrounding {
			if operation != app.RouteOperationEdit {
				return agentDocumentRouteGrounding{}
			}
			grounded := groundPPTXEditScope(content)
			switch grounded.Scope {
			case pptxScopeUnsupported:
				return agentDocumentRouteGrounding{Status: app.RouteBlocked, Reason: grounded.Reason}
			case pptxScopeUnspecified:
				return agentDocumentRouteGrounding{Status: app.RouteClarify, Reason: grounded.Reason}
			default:
				facts[pptxScopeFact] = grounded.Scope
				if encoded := encodePPTXSlideIndexes(grounded.SlideIndexes); encoded != "" {
					facts[pptxSlideIndexesFact] = encoded
				}
				return agentDocumentRouteGrounding{}
			}
		},
		ScopeDirectory: scopePPTXDirectoryEntries,
		SliceWorkflowEvidence: func(run app.AgentRun, tool string, output any, mode workflowEvidenceSliceMode, maxBytes int, ownerRequest string) (string, error) {
			if mode != workflowEvidenceStructured || tool != "files.read" || run.Workflow == nil {
				return slicePersistedToolEvidenceForRequest(tool, output, mode, maxBytes, ownerRequest), nil
			}
			outputMap, ok := outputAsMap(output)
			if !ok {
				return slicePersistedToolEvidenceForRequest(tool, output, mode, maxBytes, ownerRequest), nil
			}
			document, hasDocument := anyMap(outputMap["document"])
			if !hasDocument || !strings.EqualFold(strings.TrimSpace(stringValue(document["format"])), app.DocumentFormatPPTX) ||
				strings.TrimSpace(run.Workflow.Route.Facts[pptxScopeFact]) == pptxScopeExactText {
				return slicePersistedToolEvidenceForRequest(tool, output, mode, maxBytes, ownerRequest), nil
			}
			return pptxTargetStructuredEvidence(
				outputMap,
				strings.TrimSpace(run.Workflow.Route.Facts[pptxScopeFact]),
				decodePPTXSlideIndexes(run.Workflow.Route.Facts[pptxSlideIndexesFact]),
				maxBytes,
			)
		},
		DecisionRules: []orderedDocumentDecisionRule{{Order: 110, Text: "For PPTX, obey the frozen scope: single_slide selects update_slide, whole_deck selects update_deck, exact_text selects replace_text, and structural selects only add_slide, duplicate_slide, or delete_slide."}},
		Operations:    operations,
	}
}

func pdfAgentDocumentPolicy() agentDocumentFormatPolicy {
	operations := map[string]agentDocumentOperationPolicy{}
	for _, operation := range []string{"extract_pages", "delete_pages", "rotate_pages", "split"} {
		operations[operation] = agentDocumentOperationPolicy{}
	}
	return agentDocumentFormatPolicy{
		Format: app.DocumentFormatPDF, RouteOperations: []app.RouteOperation{app.RouteOperationTransform},
		MaterializeSchemas:  materializePDFTransformSchemas,
		ProjectReadCoverage: projectPDFReadCoverage,
		Operations:          operations,
	}
}

func imageAgentDocumentPolicy() agentDocumentFormatPolicy {
	return agentDocumentFormatPolicy{Format: app.DocumentFormatImage, RouteOperations: []app.RouteOperation{app.RouteOperationEdit}}
}

func commonDocumentDecisionRules() []orderedDocumentDecisionRule {
	return []orderedDocumentDecisionRule{
		{Order: 10, Text: "Use the owner's requested content change and the completed structured observation to distinguish replacement, insertion, deletion, append, style, row, cell, slide, and page operations."},
		{Order: 20, Text: "Apply minimum-change semantics when the observation already contains the requested target: modify, improve, polish, complete, update, revise, or rewrite means replace/update that existing target, not insert or append another overlapping block."},
		{Order: 30, Text: "Apply the same semantics across languages: 完善、润色、优化或改写 an existing located paragraph means replace that paragraph, not no match and not insertion."},
		{Order: 40, Text: "Choose insert, add, or append only when the owner explicitly requests a new block, row, or slide, or when the structured observation shows that the requested target does not exist."},
		{Order: 100, Text: "Return no entry when the owner negates an edit, asks only to quote edit instructions, or requests troubleshooting without changing the document."},
	}
}

func agentDocumentOperationForPlan(run app.AgentRun, definition app.ToolDefinition, plan toolPlan) (agentDocumentOperationPolicy, string, string, bool) {
	if plan.WorkflowID != app.WorkflowDocumentEdit || run.Workflow == nil {
		return agentDocumentOperationPolicy{}, "", "", false
	}
	routeFormat := canonicalAgentDocumentKey(firstNonEmptyString(run.Workflow.Route.Slots.Format, run.Workflow.Route.Facts["document_format"]))
	for _, capability := range definition.Capabilities {
		format := canonicalAgentDocumentKey(capability.Qualifiers[app.CapabilityQualifierFormat])
		operation := canonicalAgentDocumentKey(capability.Qualifiers[app.CapabilityQualifierOperation])
		if capability.Name != plan.Capability || capability.Name != app.ToolCapabilityDocumentEdit || format == "" || operation == "" || format != routeFormat {
			continue
		}
		policy, ok := registeredAgentDocumentFormatPolicies().operation(format, operation)
		return policy, format, operation, ok
	}
	return agentDocumentOperationPolicy{}, "", "", false
}

func scopeDocumentDirectoryEntries(route app.RouteDecision, entries []app.ToolDirectoryEntry) []app.ToolDirectoryEntry {
	policy, ok := registeredAgentDocumentFormatPolicies().policyForRoute(route)
	if !ok || policy.ScopeDirectory == nil {
		return entries
	}
	return policy.ScopeDirectory(route, entries)
}

func materializeDocumentOperationSchemas(definitions []app.ToolDefinition, view app.DirectoryView, entryIDs []app.ToolDirectoryEntryID) []app.ToolDefinition {
	if len(entryIDs) != 1 {
		return definitions
	}
	for _, entry := range view.Entries {
		if entry.ID != entryIDs[0] {
			continue
		}
		policy, ok := registeredAgentDocumentFormatPolicies().format(entry.Capability.Qualifiers[app.CapabilityQualifierFormat])
		if !ok || policy.MaterializeSchemas == nil {
			return definitions
		}
		return policy.MaterializeSchemas(definitions, view, entryIDs)
	}
	return definitions
}

func projectDocumentReadCoverage(call app.ToolCall, output map[string]any) documentReadCoverage {
	policy, ok := registeredAgentDocumentFormatPolicies().policyForResult(call, output)
	if !ok || policy.ProjectReadCoverage == nil {
		return documentReadCoverage{}
	}
	return policy.ProjectReadCoverage(call, output)
}

func (r Runtime) revalidateApprovedDocumentOperation(ctx context.Context, call app.ToolCall, definition app.ToolDefinition) error {
	run, ok := r.store.GetRun(call.RunID)
	if !ok {
		return errors.New("approved DOCX mutation run is unavailable")
	}
	plan := toolPlan{
		Name: call.Tool, Args: call.Arguments, WorkflowID: call.WorkflowID, WorkflowNodeID: call.WorkflowNodeID,
		ScopeRevision: call.ScopeRevision, Capability: call.Capability,
	}
	policy, _, _, registered := agentDocumentOperationForPlan(run, definition, plan)
	if !registered || policy.RevalidateApproved == nil {
		return nil
	}
	return policy.RevalidateApproved(ctx, r, call)
}
