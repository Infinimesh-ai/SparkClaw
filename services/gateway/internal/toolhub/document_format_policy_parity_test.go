package toolhub

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// Every operation the toolhub provider registry can execute must have a
// matching preservation policy in the document package: Pipeline.Edit and
// ValidatePreservation fail closed on unregistered pairs, so a mismatch
// that used to silently skip verification is now a runtime error — catch
// it here instead.
func TestDocumentProviderOperationsHavePreservationPolicies(t *testing.T) {
	registry := toolhubDocumentProviderRegistry()
	for format, provider := range registry.formats {
		if !document.HasFormatPolicy(format) {
			t.Errorf("format %q has an operation provider but no document format policy", format)
		}
		for operation := range provider.Operations {
			if !document.HasOperationPolicy(format, operation) {
				t.Errorf("operation %s:%s is executable but has no preservation policy; edits would fail closed at runtime", format, operation)
			}
		}
	}
}
