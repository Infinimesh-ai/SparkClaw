package toolhub

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func TestOfficeMutationSchemasExposeOnlyCanonicalSourceHash(t *testing.T) {
	definitions := []app.ToolDefinition{officeReplaceTextDefinition()}
	definitions = append(definitions, docxToolDefinitions()...)
	definitions = append(definitions, xlsxToolDefinitions()...)
	definitions = append(definitions, pptxToolDefinitions()...)
	legacySourceHashArgument := "source_" + "document_sha256"

	for _, definition := range definitions {
		properties := schemaMap(definition.InputSchema["properties"])
		if _, ok := properties[app.DocumentSourceSHA256Argument]; !ok {
			t.Errorf("%s does not expose the canonical source hash: %#v", definition.Name, definition.InputSchema)
		}
		for _, forbidden := range []string{legacySourceHashArgument, "source_evidence", "evidence_targets"} {
			if _, ok := properties[forbidden]; ok {
				t.Errorf("%s exposes runtime-only or retired argument %q", definition.Name, forbidden)
			}
		}
		requiredCount := 0
		for _, name := range stringList(definition.InputSchema["required"]) {
			if name == app.DocumentSourceSHA256Argument {
				requiredCount++
			}
		}
		if requiredCount != 1 {
			t.Errorf("%s requires canonical source hash %d times, want exactly once", definition.Name, requiredCount)
		}
	}

	legacyOnly := map[string]any{
		"path": "note.docx", legacySourceHashArgument: strings.Repeat("0", 64),
		"replacements": []any{map[string]any{"find": "old", "replace": "new"}}, "output_path": "note-edited.docx",
	}
	if err := validateInput(officeReplaceTextDefinition(), legacyOnly); err == nil || !strings.Contains(err.Error(), app.DocumentSourceSHA256Argument) {
		t.Fatalf("retired source hash argument unexpectedly satisfied the hard-cutover schema: %v", err)
	}
}

func TestDocumentFormatCatalogDrivesPipelineAndToolCapabilities(t *testing.T) {
	registry := toolhubDocumentProviderRegistry()
	editors := registry.editors()
	registrations := documentToolRegistrations()

	for _, entry := range toolhubDocumentFormatCatalog() {
		provider := entry.Provider
		if _, ok := registry.provider(provider.Format); !ok {
			t.Fatalf("format %q is absent from the provider registry", provider.Format)
		}
		for operation, candidate := range provider.Operations {
			if editors[document.EditorKey(provider.Format, operation)] == nil {
				t.Fatalf("operation %s:%s has no pipeline editor", provider.Format, operation)
			}
			registration, ok := registrations[candidate.ToolName]
			if !ok {
				t.Fatalf("operation %s:%s references unknown tool %q", provider.Format, operation, candidate.ToolName)
			}
			if !hasDocumentCapability(registration, provider.Format, operation) {
				t.Fatalf("tool %q does not advertise canonical operation %s:%s", candidate.ToolName, provider.Format, operation)
			}
		}
	}

	filesRead := registrations["files.read"]
	gotFormats := make([]string, 0, len(filesRead.capabilities))
	for _, capability := range filesRead.capabilities {
		gotFormats = append(gotFormats, capability.Qualifiers[app.CapabilityQualifierFormat])
	}
	wantFormats := []string{app.DocumentFormatText, app.DocumentFormatDOCX, app.DocumentFormatXLSX, app.DocumentFormatPPTX}
	if !reflect.DeepEqual(gotFormats, wantFormats) {
		t.Fatalf("files.read formats are not catalog-derived: got %v want %v", gotFormats, wantFormats)
	}
}

func TestDocumentProviderRegistryRejectsDuplicateFormats(t *testing.T) {
	provider := documentFormatProvider{
		Format: "synthetic", Parser: document.ParserFunc(parseTextDocument),
		Operations: map[string]documentOperationProvider{
			"replace_text": {
				ToolName: "synthetic.replace", BuildTargets: exactTextTargets,
				Editor: document.EditorFunc(applyTextReplacement), SuccessStatus: "written",
			},
		},
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate document format provider did not fail closed")
		}
	}()
	_ = newDocumentProviderRegistry(provider, provider)
}

func TestRegisteredDocumentProvidersRejectCatalogMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]documentFormatProvider)
		want   string
	}{
		{name: "missing operation", mutate: func(providers []documentFormatProvider) {
			delete(providers[0].Operations, app.DocumentOperationReplaceText)
		}, want: app.DocumentFormatText + ":" + app.DocumentOperationReplaceText},
		{name: "extra operation", mutate: func(providers []documentFormatProvider) {
			providers[0].Operations["rewrite"] = providers[0].Operations[app.DocumentOperationReplaceText]
		}, want: app.DocumentFormatText + ":rewrite"},
		{name: "wrong order", mutate: func(providers []documentFormatProvider) {
			providers[0].OperationOrder = []string{"rewrite"}
		}, want: `operation order for "text"`},
		{name: "ambiguous error alias", mutate: func(providers []documentFormatProvider) {
			docx := providers[1].Operations[app.DocumentOperationReplaceText]
			docx.WrapError = wrapPPTXToolError
			providers[1].Operations[app.DocumentOperationReplaceText] = docx
		}, want: "ambiguous document error wrapper"},
		{name: "missing format", mutate: func(providers []documentFormatProvider) {
			providers[0].Format = "retired"
		}, want: `canonical document provider "text" is missing`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			providers := documentFormatProvidersFromCatalog()
			test.mutate(providers)
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatal("canonical ToolHub provider mismatch did not panic")
				}
				if message := fmt.Sprint(recovered); !strings.Contains(message, test.want) {
					t.Fatalf("canonical ToolHub provider panic %q does not identify %q", message, test.want)
				}
			}()
			_ = newRegisteredDocumentProviderRegistry(providers...)
		})
	}
}

func hasDocumentCapability(registration toolRegistration, format, operation string) bool {
	for _, capability := range registration.capabilities {
		if capability.Name == app.ToolCapabilityDocumentEdit &&
			capability.Qualifiers[app.CapabilityQualifierFormat] == format &&
			capability.Qualifiers[app.CapabilityQualifierOperation] == operation {
			return true
		}
	}
	return false
}
