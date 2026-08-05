package document

import (
	"slices"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDocumentFormatPoliciesRegisterLifecycleAndOperations(t *testing.T) {
	expectedOperations := map[string][]string{
		app.DocumentFormatText: {"replace_text"},
		app.DocumentFormatDOCX: {"replace_text", "replace_paragraph", "insert_paragraph", "delete_paragraph", "set_text_style"},
		app.DocumentFormatXLSX: {"replace_text", "update_cell", "insert_row", "delete_row", "update_row", "append_row"},
		app.DocumentFormatPPTX: {"replace_text", "add_slide", "update_slide", "update_deck", "duplicate_slide", "delete_slide"},
		app.DocumentFormatPDF:  {"extract_pages", "delete_pages", "rotate_pages", "split"},
	}
	for format, operations := range expectedOperations {
		policy, ok := registeredDocumentFormatPolicies.format(format)
		if !ok || policy.NormalizationSource == "" {
			t.Fatalf("format policy %q is missing or incomplete: %#v", format, policy)
		}
		for _, operation := range operations {
			if _, ok := registeredDocumentFormatPolicies.operation(format, operation); !ok {
				t.Errorf("preservation policy %s:%s is not registered", format, operation)
			}
		}
		registered := make([]string, 0, len(policy.Operations))
		for operation := range policy.Operations {
			registered = append(registered, operation)
		}
		for _, operation := range registered {
			if !slices.Contains(operations, operation) {
				t.Errorf("unexpected preservation policy %s:%s", format, operation)
			}
		}
	}

	xlsx, _ := registeredDocumentFormatPolicies.format(app.DocumentFormatXLSX)
	if xlsx.AfterRead == nil || xlsx.BeginEdit == nil || xlsx.FallbackBlocks == nil {
		t.Fatalf("XLSX lifecycle hooks are incomplete: %#v", xlsx)
	}
	pdf, _ := registeredDocumentFormatPolicies.format(app.DocumentFormatPDF)
	if pdf.AfterEnrich == nil || pdf.FallbackBlocks == nil {
		t.Fatalf("PDF lifecycle hooks are incomplete: %#v", pdf)
	}
}

func TestTextOutputExtensionUsesInspectedInputExtension(t *testing.T) {
	policy, ok := registeredDocumentFormatPolicies.format(app.DocumentFormatText)
	if !ok || policy.OutputExtension == nil {
		t.Fatal("text output extension policy is not registered")
	}
	if got := policy.OutputExtension(Metadata{Path: "/workspace/notes.MD"}); got != ".md" {
		t.Fatalf("text output extension = %q, want .md", got)
	}
}
