package document

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDocumentFormatPoliciesRegisterLifecycleAndOperations(t *testing.T) {
	for _, spec := range app.DocumentFormatOperationSpecs() {
		policy, ok := registeredDocumentFormatPolicies.format(spec.Format)
		if !ok || policy.NormalizationSource == "" {
			t.Fatalf("format policy %q is missing or incomplete: %#v", spec.Format, policy)
		}
		expectedOperations := make([]string, 0, len(spec.Operations))
		for _, operation := range spec.Operations {
			expectedOperations = append(expectedOperations, operation.Name)
			if _, ok := registeredDocumentFormatPolicies.operation(spec.Format, operation.Name); !ok {
				t.Errorf("preservation policy %s:%s is not registered", spec.Format, operation.Name)
			}
		}
		registered := make([]string, 0, len(policy.Operations))
		for operation := range policy.Operations {
			registered = append(registered, operation)
		}
		for _, operation := range registered {
			if !slices.Contains(expectedOperations, operation) {
				t.Errorf("unexpected preservation policy %s:%s", spec.Format, operation)
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

func TestRegisteredDocumentFormatPoliciesRejectCatalogMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]documentFormatPolicy)
		want   string
	}{
		{name: "missing operation", mutate: func(policies []documentFormatPolicy) {
			delete(policies[0].Operations, app.DocumentOperationReplaceText)
		}, want: app.DocumentFormatText + ":" + app.DocumentOperationReplaceText},
		{name: "extra operation", mutate: func(policies []documentFormatPolicy) {
			policies[0].Operations["rewrite"] = preservationPolicy{}
		}, want: app.DocumentFormatText + ":rewrite"},
		{name: "missing format", mutate: func(policies []documentFormatPolicy) {
			policies[0].Format = "retired"
		}, want: `canonical format policy "text" is missing`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policies := documentFormatPolicies()
			test.mutate(policies)
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatal("canonical document policy mismatch did not panic")
				}
				if message := fmt.Sprint(recovered); !strings.Contains(message, test.want) {
					t.Fatalf("canonical document policy panic %q does not identify %q", message, test.want)
				}
			}()
			_ = newRegisteredDocumentFormatPolicyRegistry(policies...)
		})
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

func TestDocumentFormatPolicyRegistrySupportsSyntheticOperation(t *testing.T) {
	registry := newDocumentFormatPolicyRegistry(documentFormatPolicy{
		Format: "synthetic", NormalizationSource: "fixture",
		Operations: map[string]preservationPolicy{"rewrite": {CheckUnchangedContent: true}},
	})
	policy, ok := registry.operation("synthetic", "rewrite")
	if !ok || !policy.CheckUnchangedContent {
		t.Fatalf("synthetic format operation was not registered: %#v", policy)
	}
	if _, ok := registry.operation("synthetic", "missing"); ok {
		t.Fatal("unknown synthetic operation unexpectedly resolved")
	}
}

func TestDocumentFormatPolicyRegistryRejectsDuplicateFormats(t *testing.T) {
	policy := documentFormatPolicy{Format: "synthetic"}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate document format policy did not fail closed")
		}
	}()
	_ = newDocumentFormatPolicyRegistry(policy, policy)
}

func TestSharedDocumentPipelineDoesNotNameFormatPolicies(t *testing.T) {
	for _, name := range []string{"contract.go", "normalize.go", "enrichment.go", "preservation.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"DocumentFormatDOCX", "DocumentFormatXLSX", "DocumentFormatPPTX", "DocumentFormatPDF"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("shared document file %s names %s instead of using the policy registry", name, forbidden)
			}
		}
	}
}
