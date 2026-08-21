package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func (r Runtime) recordMessageDocuments(ctx context.Context, session app.Session, message app.Message) error {
	if r.store == nil {
		return nil
	}
	for _, attachment := range message.Attachments {
		path := normalizeGovernedDocumentPath(attachment.RelPath)
		if path == "" {
			continue
		}
		record, ok, err := r.documentRecordByPath(ctx, session.ID, path)
		if err != nil {
			return err
		}
		if !ok {
			record = app.DocumentRecord{
				ID:           app.NewID("doc"),
				OwnerID:      firstNonEmptyString(session.OwnerID, app.DefaultOwnerID),
				SessionID:    session.ID,
				GovernedPath: path,
				CreatedAt:    message.CreatedAt,
			}
		}
		record.Name = firstNonEmptyString(strings.TrimSpace(attachment.Name), filepath.Base(filepath.FromSlash(path)))
		record.ContentType = strings.TrimSpace(attachment.ContentType)
		record.Format = firstNonEmptyString(record.Format, documentFormatFromMetadata(record.Name, record.ContentType))
		record.SizeBytes = int64(attachment.Bytes)
		record.SHA256 = strings.TrimSpace(attachment.SHA256)
		record.Status = app.DocumentStatusAvailable
		record.Source = app.DocumentSourceAttachment
		record.SourceMessageID = message.ID
		record.LastActivity = app.DocumentActivityAttached
		record.LastActivityID = message.ID
		record.LastActivityAt = message.CreatedAt
		workspaceRoot := strings.TrimSpace(session.WorkspaceRoot)
		if workspaceRoot == "" && r.tools != nil {
			workspaceRoot = r.tools.Config().Workspaces.DefaultRoot
		}
		record = enrichDocumentRecordFromWorkspace(record, workspaceRoot)
		if _, err := r.store.SaveDocumentRecord(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (r Runtime) confirmDocumentRecord(ctx context.Context, sessionID, runID string, reference documentContextReference, preflight documentPreflight) (app.DocumentRecord, error) {
	ownerID, err := r.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	workspaceRoot, err := r.workspaceRootForSession(ctx, sessionID)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	record, ok, err := r.documentRecordByIDOrPath(ctx, reference.DocumentID, sessionID, preflight.InputRef)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	if !ok {
		record = app.DocumentRecord{
			ID:           app.NewID("doc"),
			OwnerID:      ownerID,
			SessionID:    sessionID,
			GovernedPath: preflight.InputRef,
			Source:       app.DocumentSourceWorkspace,
		}
	}
	record.GovernedPath = preflight.InputRef
	record.Name = firstNonEmptyString(strings.TrimSpace(reference.Name), record.Name, filepath.Base(filepath.FromSlash(preflight.InputRef)))
	record.ContentType = firstNonEmptyString(strings.TrimSpace(reference.ContentType), record.ContentType)
	record.Format = preflight.Format
	record.Status = app.DocumentStatusAvailable
	if record.Source == "" {
		record.Source = app.DocumentSourceWorkspace
	}
	record.SourceRunID = runID
	record.LastActivity = app.DocumentActivityConfirmed
	record.LastActivityID = runID
	record.LastActivityAt = time.Now().UTC()
	record = enrichDocumentRecordFromWorkspace(record, workspaceRoot)
	return r.store.SaveDocumentRecord(ctx, record)
}

func (r Runtime) recordDocumentToolActivity(ctx context.Context, call app.ToolCall) error {
	if r.store == nil || !toolCallCompleted(call) || !isDocumentContextCall(call) {
		return nil
	}
	ownerID, err := r.ownerIDForSession(ctx, call.SessionID)
	if err != nil {
		return err
	}
	workspaceRoot, err := r.workspaceRootForSession(ctx, call.SessionID)
	if err != nil {
		return err
	}
	if call.Capability == app.ToolCapabilityDocumentEdit || toolCallProducesDocumentOutput(call, workspaceRoot) {
		return r.recordDocumentEditOutputs(ctx, call, ownerID, workspaceRoot)
	}
	path := normalizeGovernedDocumentPath(firstNonEmptyString(
		strings.TrimSpace(stringValue(call.Arguments["path"])),
		toolResultDocumentPath(call.Result),
	))
	if path == "" {
		return nil
	}
	record, ok, err := r.documentRecordByPath(ctx, call.SessionID, path)
	if err != nil {
		return err
	}
	if !ok {
		record = app.DocumentRecord{
			ID:           app.NewID("doc"),
			OwnerID:      ownerID,
			SessionID:    call.SessionID,
			GovernedPath: path,
			Name:         filepath.Base(filepath.FromSlash(path)),
			Source:       app.DocumentSourceWorkspace,
		}
	}
	record.SourceRunID = call.RunID
	record.SourceToolCallID = call.ID
	record.LastActivity = app.DocumentActivityRead
	record.LastActivityID = call.ID
	record.LastActivityAt = completedToolCallTime(call)
	record.Status = app.DocumentStatusAvailable
	record = enrichDocumentRecordFromWorkspace(record, workspaceRoot)
	_, err = r.store.SaveDocumentRecord(ctx, record)
	return err
}

func (r Runtime) recordDocumentEditOutputs(ctx context.Context, call app.ToolCall, ownerID, workspaceRoot string) error {
	inputPath := normalizeGovernedDocumentPath(strings.TrimSpace(stringValue(call.Arguments["path"])))
	parent, _, err := r.documentRecordByPath(ctx, call.SessionID, inputPath)
	if err != nil {
		return err
	}
	for _, outputPath := range documentOutputPaths(call, workspaceRoot) {
		record, ok, err := r.documentRecordByPath(ctx, call.SessionID, outputPath)
		if err != nil {
			return err
		}
		if !ok {
			record = app.DocumentRecord{
				ID:           app.NewID("doc"),
				OwnerID:      ownerID,
				SessionID:    call.SessionID,
				GovernedPath: outputPath,
				Name:         filepath.Base(filepath.FromSlash(outputPath)),
				Source:       app.DocumentSourceToolOutput,
			}
		}
		record.ParentDocumentID = parent.ID
		record.Source = app.DocumentSourceToolOutput
		record.SourceRunID = call.RunID
		record.SourceToolCallID = call.ID
		record.LastActivity = app.DocumentActivityEdited
		record.LastActivityID = call.ID
		record.LastActivityAt = completedToolCallTime(call)
		record.Status = app.DocumentStatusAvailable
		record = enrichDocumentRecordFromWorkspace(record, workspaceRoot)
		if _, err := r.store.SaveDocumentRecord(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (r Runtime) documentRecordByIDOrPath(ctx context.Context, documentID, sessionID, path string) (app.DocumentRecord, bool, error) {
	if documentID != "" {
		record, ok, err := r.store.GetDocumentRecord(ctx, documentID)
		if err != nil {
			return app.DocumentRecord{}, false, err
		}
		if ok && record.SessionID == sessionID {
			return record, true, nil
		}
	}
	return r.documentRecordByPath(ctx, sessionID, path)
}

func (r Runtime) documentRecordByPath(ctx context.Context, sessionID, path string) (app.DocumentRecord, bool, error) {
	path = normalizeGovernedDocumentPath(path)
	if r.store == nil || path == "" {
		return app.DocumentRecord{}, false, nil
	}
	records, err := r.store.ListDocumentRecords(ctx, "", sessionID, 1000)
	if err != nil {
		return app.DocumentRecord{}, false, err
	}
	for _, record := range records {
		if normalizeGovernedDocumentPath(record.GovernedPath) == path {
			return record, true, nil
		}
	}
	return app.DocumentRecord{}, false, nil
}

func (r Runtime) ownerIDForSession(ctx context.Context, sessionID string) (string, error) {
	if r.store != nil {
		session, ok, err := r.store.GetSession(ctx, sessionID)
		if err != nil {
			return "", err
		}
		if ok {
			return firstNonEmptyString(strings.TrimSpace(session.OwnerID), app.DefaultOwnerID), nil
		}
	}
	return app.DefaultOwnerID, nil
}

func (r Runtime) workspaceRootForSession(ctx context.Context, sessionID string) (string, error) {
	if r.store != nil {
		session, ok, err := r.store.GetSession(ctx, sessionID)
		if err != nil {
			return "", err
		}
		if ok && strings.TrimSpace(session.WorkspaceRoot) != "" {
			return session.WorkspaceRoot, nil
		}
	}
	if r.tools != nil {
		return r.tools.Config().Workspaces.DefaultRoot, nil
	}
	return "", nil
}

func enrichDocumentRecordFromWorkspace(record app.DocumentRecord, workspaceRoot string) app.DocumentRecord {
	fullPath, ok := governedDocumentFullPath(workspaceRoot, record.GovernedPath)
	if !ok {
		return record
	}
	info, err := os.Stat(fullPath)
	if err != nil || !info.Mode().IsRegular() {
		return record
	}
	record.SizeBytes = info.Size()
	if detected, err := document.DetectFormat(fullPath); err == nil {
		record.Format = detected
	}
	if record.ContentType == "" {
		record.ContentType = strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(fullPath))))
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return record
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err == nil {
		record.SHA256 = hex.EncodeToString(hash.Sum(nil))
	}
	return record
}

func governedDocumentFullPath(workspaceRoot, governedPath string) (string, bool) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil || root == "" {
		return "", false
	}
	path := filepath.Clean(filepath.FromSlash(strings.TrimSpace(governedPath)))
	if path == "." || path == "" || filepath.IsAbs(path) {
		return "", false
	}
	fullPath, err := filepath.Abs(filepath.Join(root, path))
	if err != nil || fullPath == root || !strings.HasPrefix(fullPath, root+string(os.PathSeparator)) {
		return "", false
	}
	return fullPath, true
}

func documentFormatFromMetadata(name, contentType string) string {
	return document.InferFormatFromMetadata(name, contentType)
}

func documentOutputPaths(call app.ToolCall, workspaceRoot string) []string {
	output, _ := anyMap(call.Result)
	candidates := make([]string, 0)
	for _, raw := range anySlice(output["outputs"]) {
		if item, ok := anyMap(raw); ok {
			candidates = append(candidates, firstNonEmptyString(
				strings.TrimSpace(stringValue(item["output_path"])),
				strings.TrimSpace(stringValue(item["path"])),
				strings.TrimSpace(stringValue(item["rel_path"])),
			))
			continue
		}
		candidates = append(candidates, strings.TrimSpace(stringValue(raw)))
	}
	candidates = append(candidates,
		strings.TrimSpace(stringValue(output["output_path"])),
		strings.TrimSpace(stringValue(call.Arguments["output_path"])),
	)
	seen := map[string]bool{}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		path := normalizeDocumentOutputPath(workspaceRoot, candidate)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

func toolCallProducesDocumentOutput(call app.ToolCall, workspaceRoot string) bool {
	return len(documentOutputPaths(call, workspaceRoot)) > 0 && call.Capability != app.ToolCapabilityDocumentRead
}

func toolResultDocumentPath(result any) string {
	output, _ := anyMap(result)
	return firstNonEmptyString(
		strings.TrimSpace(stringValue(output["rel_path"])),
		strings.TrimSpace(stringValue(output["path"])),
	)
}

func completedToolCallTime(call app.ToolCall) time.Time {
	if call.CompletedAt != nil {
		return *call.CompletedAt
	}
	return call.StartedAt
}

func normalizeGovernedDocumentPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "<nil>" {
		return ""
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if cleaned == "." {
		return ""
	}
	return filepath.ToSlash(cleaned)
}

func normalizeDocumentOutputPath(workspaceRoot, value string) string {
	path := normalizeGovernedDocumentPath(value)
	if path == "" || !filepath.IsAbs(filepath.FromSlash(path)) {
		return path
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return ""
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return ""
	}
	absolute, err := filepath.Abs(filepath.FromSlash(path))
	if err != nil {
		return ""
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return ""
	}
	return normalizeGovernedDocumentPath(relative)
}
