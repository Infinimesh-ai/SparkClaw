package toolhub

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// ExtractCommittedDocument reuses the registered deterministic readers. The
// caller must authorize and verify the committed source before calling. It
// performs no model enrichment, editing, tool selection or external delivery.
func (h *ToolHub) ExtractCommittedDocument(ctx context.Context, path string, maxBytes int) (document.ReadResult, error) {
	return h.readDocumentWorkflow(ctx, path, maxBytes, document.EnrichmentOptions{ImageAnalysis: "none"})
}
