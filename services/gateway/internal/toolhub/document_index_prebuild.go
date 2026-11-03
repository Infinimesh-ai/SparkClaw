package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// PrebuildDocumentIndex queues a background, text-only index build for an
// uploaded document (docs/large-document-context-design.md section 4.3). It
// never blocks, shows nothing to the owner and ignores failures: a document
// that fits the complete-read path keeps no index, and a read builds any
// missing index on demand.
func (h *ToolHub) PrebuildDocumentIndex(path string) {
	if h == nil || h.documentIndex == nil {
		return
	}
	metadata, err := document.InspectFile(context.Background(), h.cfg.Workspaces.DefaultRoot, path)
	if err != nil || !documentIndexFormatSupported(metadata.Format) {
		return
	}
	if metadata.Format == app.DocumentFormatText && metadata.Size <= int64(h.documentExtractionLimit()) {
		// Plain text extracts to its own size, so a complete read fits.
		return
	}
	h.documentIndex.Enqueue(docindex.Prebuild{
		Target:           docindex.Target{WorkspaceRoot: h.cfg.Workspaces.DefaultRoot, RelPath: metadata.Relative, Format: metadata.Format},
		SmallSourceBytes: document.SmallFileMaxBytes, SmallTextBytes: h.documentExtractionLimit(),
	})
}
