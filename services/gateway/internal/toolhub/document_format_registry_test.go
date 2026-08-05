package toolhub

import (
	"reflect"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

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
