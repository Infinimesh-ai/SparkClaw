package toolhub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

func recordToolAuditEvidence(ctx context.Context, event app.AuditEvent) {
	if _, enabled := executionevidence.ScopeFromContext(ctx); !enabled {
		return
	}
	payload, err := executionevidence.CaptureJSON(context.WithoutCancel(ctx), "audit", event.Type, event)
	if err != nil {
		_ = executionevidence.MarkGap(ctx, "audit_capture_failed", event.Type)
		return
	}
	if err := executionevidence.Record(ctx, executionevidence.Event{Kind: "audit", Status: "observed", PayloadIDs: []string{payload.ID}, Data: map[string]any{"audit_type": event.Type}}); err != nil {
		_ = executionevidence.MarkGap(ctx, "audit_event_failed", event.Type)
	}
}

func beginToolEvidence(ctx context.Context, name string, args map[string]any) (context.Context, func(Result, error)) {
	if _, enabled := executionevidence.ScopeFromContext(ctx); !enabled {
		return ctx, nil
	}
	id := app.NewID("tool_dispatch")
	started := time.Now()
	record := func(event executionevidence.Event, value any) {
		persist := context.WithoutCancel(ctx)
		payload, err := executionevidence.CaptureJSON(persist, "tool", name, value)
		if err != nil {
			_ = executionevidence.MarkGap(persist, "tool_capture_failed", name)
			return
		}
		event.PayloadIDs = []string{payload.ID}
		if err := executionevidence.Record(persist, event); err != nil {
			_ = executionevidence.MarkGap(persist, "tool_event_failed", name)
		}
	}
	record(executionevidence.Event{ID: id, Kind: "tool.dispatch", SpanID: id, Status: "started"}, map[string]any{"tool": name, "arguments": args})
	child := executionevidence.WithParent(ctx, id)
	return child, func(result Result, err error) {
		status, code := "completed", ""
		if err != nil {
			status, code = "failed", string(app.ToolErrorCodeFrom(err))
		}
		record(executionevidence.Event{Kind: "tool.observation", ParentID: id, SpanID: id, Status: status, ErrorCode: code, DurationMS: time.Since(started).Milliseconds()},
			map[string]any{"tool": name, "output": result.Output, "archive_output": result.ArchiveOutput, "error_code": code})
	}
}

// Called only after document admission. An unbound ordinary execution performs
// no additional file access; capture errors never change its business outcome.
func (h *ToolHub) recordDocumentEvidenceSource(ctx context.Context, metadata document.Metadata) {
	recordDocumentEvidenceFile(ctx, h.cfg.Workspaces.DefaultRoot, metadata, "source")
}

func CaptureDocumentOutputEvidence(ctx context.Context, root, path string) {
	if _, enabled := executionevidence.ScopeFromContext(ctx); !enabled {
		return
	}
	metadata, err := document.InspectFile(ctx, root, path)
	if err != nil {
		_ = executionevidence.MarkGap(ctx, "output_unavailable", "document output could not be snapshotted")
		return
	}
	recordDocumentEvidenceFile(ctx, root, metadata, "output")
}

func recordDocumentEvidenceFile(ctx context.Context, rootPath string, metadata document.Metadata, kind string) {
	if _, enabled := executionevidence.ScopeFromContext(ctx); !enabled {
		return
	}
	gap := func(code string) {
		_ = executionevidence.MarkGap(ctx, code, "document "+kind+" snapshot unavailable or changed")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		gap("source_open_failed")
		return
	}
	defer root.Close()
	rel := filepath.FromSlash(metadata.Relative)
	if !filepath.IsLocal(rel) {
		gap("source_scope_invalid")
		return
	}
	current := ""
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			gap("source_scope_invalid")
			return
		}
	}
	file, err := root.Open(rel)
	if err != nil {
		gap("source_open_failed")
		return
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != metadata.Size || !before.ModTime().Equal(metadata.ModifiedAt) {
		gap("source_changed")
		return
	}
	payload, err := executionevidence.Capture(ctx, kind, filepath.Base(rel), metadata.ContentType, file)
	if err != nil {
		gap("source_capture_failed")
		return
	}
	after, statErr := file.Stat()
	valid := statErr == nil && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && payload.Bytes == metadata.Size
	valid = valid && (metadata.SHA256 == "" || metadata.SHA256 == payload.SHA256)
	status := "captured"
	if !valid {
		status = "version_mismatch"
		gap("source_changed")
	}
	if err := executionevidence.Record(ctx, executionevidence.Event{Kind: "document." + kind, Status: status,
		PayloadIDs: []string{payload.ID}, Data: map[string]any{"source": metadata, "snapshot_sha256": payload.SHA256, "version_verified": valid}}); err != nil {
		gap("source_event_failed")
	}
}
