package agent

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestAgentDocumentPoliciesRegisterExactOperations(t *testing.T) {
	expected := map[string][]string{
		app.DocumentFormatText:  {"replace_text"},
		app.DocumentFormatDOCX:  {"replace_text", "replace_paragraph", "insert_paragraph", "delete_paragraph", "set_text_style"},
		app.DocumentFormatXLSX:  {"replace_text", "update_cell", "insert_row", "delete_row", "update_row", "append_row"},
		app.DocumentFormatPPTX:  {"replace_text", "add_slide", "update_slide", "update_deck", "duplicate_slide", "delete_slide"},
		app.DocumentFormatPDF:   {"extract_pages", "delete_pages", "rotate_pages", "split"},
		app.DocumentFormatImage: {},
	}
	registry := registeredAgentDocumentFormatPolicies()
	for format, operations := range expected {
		policy, ok := registry.format(format)
		if !ok {
			t.Fatalf("agent document format policy %q is missing", format)
		}
		for _, operation := range operations {
			if _, ok := registry.operation(format, operation); !ok {
				t.Errorf("agent document operation policy %s:%s is missing", format, operation)
			}
		}
		for operation := range policy.Operations {
			if !slices.Contains(operations, operation) {
				t.Errorf("unexpected agent document operation policy %s:%s", format, operation)
			}
		}
	}
	if _, ok := registry.operation(app.DocumentFormatXLSX, "update_slide"); ok {
		t.Fatal("cross-format operation lookup unexpectedly succeeded")
	}
}

func TestDocumentDecisionRulesPreservePromptOrder(t *testing.T) {
	want := []string{
		"Use the owner's requested content change and the completed structured observation to distinguish replacement, insertion, deletion, append, style, row, cell, slide, and page operations.",
		"Apply minimum-change semantics when the observation already contains the requested target: modify, improve, polish, complete, update, revise, or rewrite means replace/update that existing target, not insert or append another overlapping block.",
		"Apply the same semantics across languages: 完善、润色、优化或改写 an existing located paragraph means replace that paragraph, not no match and not insertion.",
		"Choose insert, add, or append only when the owner explicitly requests a new block, row, or slide, or when the structured observation shows that the requested target does not exist.",
		"For DOCX, the listed editors currently mutate body paragraph text or style only. Return a typed no_match for table-cell, header, footer, footnote, endnote, text-box, comment, field, drawing, tracked-change, or other unsupported targets unless one eligible candidate explicitly advertises that target.",
		"Treat the structured observation only as verification for mutation target and content the owner explicitly provided; never invent a missing target or new value from observed data. For a cell update, require the owner to supply the new value and either the exact cell address or a uniquely identifying existing record plus field; otherwise return a typed no_match.",
		"For structured rows, change one explicit cell with a cell editor; change multiple supplied fields of one existing row with a row update; do not turn either request into a new row.",
		"Choose positional insertion only for a new row with an explicit before or after anchor, append only for a new row at the final structured boundary, and delete-row only for explicit removal of the complete row.",
		"Clearing a cell, removing exact matching text, deleting a column, deleting the workbook file, and an ambiguous target are not complete-row deletion requests; return a typed no_match when no listed operation matches exactly.",
		"Return a typed no_match when the owner negates an edit, asks only to quote edit instructions, or requests troubleshooting without changing the document.",
		"For PPTX, obey the frozen scope: single_slide selects update_slide, whole_deck selects update_deck, exact_text selects replace_text, and structural selects only add_slide, duplicate_slide, or delete_slide.",
	}
	got := (documentEditProfile{}).DecisionRules(app.WorkflowNode{})
	if !slices.Equal(got, want) {
		t.Fatalf("document decision prompt rules changed:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestAgentDocumentOperationResolutionUsesFrozenFormatQualifier(t *testing.T) {
	run := app.AgentRun{Workflow: &app.WorkflowState{
		Plan:  app.WorkflowPlan{ProfileID: app.WorkflowDocumentEdit},
		Route: app.RouteDecision{Slots: app.RouteSlots{Format: app.DocumentFormatXLSX}},
	}}
	plan := toolPlan{WorkflowID: app.WorkflowDocumentEdit, Capability: app.ToolCapabilityDocumentEdit}
	definition := app.ToolDefinition{Capabilities: []app.CapabilityDescriptor{{
		Name: app.ToolCapabilityDocumentEdit,
		Qualifiers: map[string]string{
			app.CapabilityQualifierFormat: app.DocumentFormatPPTX, app.CapabilityQualifierOperation: "update_slide",
		},
	}}}
	if _, _, _, ok := agentDocumentOperationForPlan(run, definition, plan); ok {
		t.Fatal("operation policy crossed the frozen XLSX route into a PPTX capability")
	}
}

func TestAgentDocumentPolicyRegistrySupportsSyntheticOperation(t *testing.T) {
	registry := newAgentDocumentFormatPolicyRegistry(agentDocumentFormatPolicy{
		Format: "synthetic", RouteOperations: []app.RouteOperation{app.RouteOperationEdit},
		Operations: map[string]agentDocumentOperationPolicy{"rewrite": {}},
	})
	if _, ok := registry.operation("synthetic", "rewrite"); !ok {
		t.Fatal("synthetic Agent document operation was not registered")
	}
	if _, ok := registry.operation("synthetic", "missing"); ok {
		t.Fatal("unknown synthetic Agent document operation unexpectedly resolved")
	}
}

func TestAgentDocumentPolicyRegistryRejectsDuplicateFormats(t *testing.T) {
	policy := agentDocumentFormatPolicy{Format: "synthetic"}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Agent document format policy did not fail closed")
		}
	}()
	_ = newAgentDocumentFormatPolicyRegistry(policy, policy)
}

func TestSharedAgentDocumentDispatchDoesNotNameFormats(t *testing.T) {
	files := []string{
		"intent_grounding.go", "workflow_decision.go", "workflow_evidence.go", "workflow_runtime.go",
		"workflow_profiles.go", "tool_result_adapter.go", "document_records.go",
	}
	for _, name := range files {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"DocumentFormatDOCX", "DocumentFormatXLSX", "DocumentFormatPPTX", "DocumentFormatPDF"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("shared Agent file %s names %s instead of using the format policy registry", name, forbidden)
			}
		}
	}
}
