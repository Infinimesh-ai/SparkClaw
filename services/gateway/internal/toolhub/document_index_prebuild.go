package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// PrebuildDocumentIndex queues a background, text-only index build for an
// uploaded document (docs/large-document-context-design.md section 4.3). It
// never blocks, shows nothing to the owner and ignores failures: a document
// keeps its reusable structure, and a read builds any missing index on demand.
func (h *ToolHub) PrebuildDocumentIndex(path string) {
	if h == nil || h.documentIndex == nil {
		return
	}
	metadata, err := document.InspectFile(context.Background(), h.cfg.Workspaces.DefaultRoot, path)
	if err != nil || !documentIndexFormatSupported(metadata.Format) {
		return
	}
	h.documentIndex.Enqueue(docindex.Prebuild{
		Target: func() docindex.Target {
			target := h.documentIndexTarget(metadata)
			target.StructureVersion = document.StructureSchemaVersion
			return target
		}(),
	})
}
