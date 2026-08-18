package toolhub

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// Every operation the toolhub provider registry can execute must have a
// matching preservation policy in the document package: Pipeline.Edit and
// ValidatePreservation fail closed on unregistered pairs, so a mismatch
// that used to silently skip verification is now a runtime error — catch
// it here instead.
func TestDocumentProviderOperationsHavePreservationPolicies(t *testing.T) {
	registry := toolhubDocumentProviderRegistry()
	for _, spec := range app.DocumentFormatOperationSpecs() {
		provider, ok := registry.formats[spec.Format]
		if !ok {
			t.Errorf("format %q is absent from the ToolHub provider registry", spec.Format)
			continue
		}
		if !document.HasFormatPolicy(spec.Format) {
			t.Errorf("format %q has an operation provider but no document format policy", spec.Format)
		}
		if len(provider.Operations) != len(spec.Operations) {
			t.Errorf("format %q provider operation count=%d want=%d", spec.Format, len(provider.Operations), len(spec.Operations))
		}
		for _, operation := range spec.Operations {
			if _, ok := provider.Operations[operation.Name]; !ok {
				t.Errorf("canonical ToolHub operation %s:%s is missing", spec.Format, operation.Name)
			}
			if !document.HasOperationPolicy(spec.Format, operation.Name) {
				t.Errorf("operation %s:%s is executable but has no preservation policy; edits would fail closed at runtime", spec.Format, operation.Name)
			}
		}
	}
}
