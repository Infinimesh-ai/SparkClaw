package agent

import (
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

func (r Runtime) recordMessageDocuments(session app.Session, message app.Message) {
	if r.store == nil {
		return
	}
	for _, attachment := range message.Attachments {
		path := normalizeGovernedDocumentPath(attachment.RelPath)
		if path == "" {
			continue
		}
		record, ok := r.documentRecordByPath(session.ID, path)
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
		record = enrichDocumentRecordFromWorkspace(record, r.workspaceRootForSession(session.ID))
		r.store.SaveDocumentRecord(record)
	}
}

func (r Runtime) confirmDocumentRecord(sessionID, runID string, reference documentContextReference, preflight documentPreflight) app.DocumentRecord {
	record, ok := r.documentRecordByIDOrPath(reference.DocumentID, sessionID, preflight.InputRef)
	if !ok {
		record = app.DocumentRecord{
			ID:           app.NewID("doc"),
			OwnerID:      r.ownerIDForSession(sessionID),
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
	record = enrichDocumentRecordFromWorkspace(record, r.workspaceRootForSession(sessionID))
	return r.store.SaveDocumentRecord(record)
}

func (r Runtime) recordDocumentToolActivity(call app.ToolCall) {
	if r.store == nil || !toolCallCompleted(call) || !isDocumentContextCall(call) {
		return
	}
	workspaceRoot := r.workspaceRootForSession(call.SessionID)
	if call.Capability == app.ToolCapabilityDocumentEdit || toolCallProducesDocumentOutput(call, workspaceRoot) {
		r.recordDocumentEditOutputs(call)
		return
	}
	path := normalizeGovernedDocumentPath(firstNonEmptyString(
		strings.TrimSpace(stringValue(call.Arguments["path"])),
		toolResultDocumentPath(call.Result),
	))
	if path == "" {
		return
	}
	record, ok := r.documentRecordByPath(call.SessionID, path)
	if !ok {
		record = app.DocumentRecord{
			ID:           app.NewID("doc"),
			OwnerID:      r.ownerIDForSession(call.SessionID),
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
	record = enrichDocumentRecordFromWorkspace(record, r.workspaceRootForSession(call.SessionID))
	r.store.SaveDocumentRecord(record)
}

func (r Runtime) recordDocumentEditOutputs(call app.ToolCall) {
	inputPath := normalizeGovernedDocumentPath(strings.TrimSpace(stringValue(call.Arguments["path"])))
	parent, _ := r.documentRecordByPath(call.SessionID, inputPath)
	for _, outputPath := range documentOutputPaths(call, r.workspaceRootForSession(call.SessionID)) {
		record, ok := r.documentRecordByPath(call.SessionID, outputPath)
		if !ok {
			record = app.DocumentRecord{
				ID:           app.NewID("doc"),
				OwnerID:      r.ownerIDForSession(call.SessionID),
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
		record = enrichDocumentRecordFromWorkspace(record, r.workspaceRootForSession(call.SessionID))
		r.store.SaveDocumentRecord(record)
	}
}

func (r Runtime) documentRecordByIDOrPath(documentID, sessionID, path string) (app.DocumentRecord, bool) {
	if documentID != "" {
		if record, ok := r.store.GetDocumentRecord(documentID); ok && record.SessionID == sessionID {
			return record, true
		}
	}
	return r.documentRecordByPath(sessionID, path)
}

func (r Runtime) documentRecordByPath(sessionID, path string) (app.DocumentRecord, bool) {
	path = normalizeGovernedDocumentPath(path)
	if r.store == nil || path == "" {
		return app.DocumentRecord{}, false
	}
	for _, record := range r.store.ListDocumentRecords("", sessionID, 1000) {
		if normalizeGovernedDocumentPath(record.GovernedPath) == path {
			return record, true
		}
	}
	return app.DocumentRecord{}, false
}

func (r Runtime) ownerIDForSession(sessionID string) string {
	if r.store != nil {
		if session, ok := r.store.GetSession(sessionID); ok {
			return firstNonEmptyString(strings.TrimSpace(session.OwnerID), app.DefaultOwnerID)
		}
	}
	return app.DefaultOwnerID
}

func (r Runtime) workspaceRootForSession(sessionID string) string {
	if r.store != nil {
		if session, ok := r.store.GetSession(sessionID); ok && strings.TrimSpace(session.WorkspaceRoot) != "" {
			return session.WorkspaceRoot
		}
	}
	if r.tools != nil {
		return r.tools.Config().Workspaces.DefaultRoot
	}
	return ""
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
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".txt", ".md", ".csv", ".json", ".yaml", ".yml":
		return app.DocumentFormatText
	case ".docx":
		return app.DocumentFormatDOCX
	case ".xlsx":
		return app.DocumentFormatXLSX
	case ".pptx":
		return app.DocumentFormatPPTX
	case ".pdf":
		return app.DocumentFormatPDF
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(contentType, "text/"):
		return app.DocumentFormatText
	case contentType == "application/pdf":
		return app.DocumentFormatPDF
	case strings.HasPrefix(contentType, "image/"):
		return app.DocumentFormatImage
	case strings.Contains(contentType, "wordprocessingml"):
		return app.DocumentFormatDOCX
	case strings.Contains(contentType, "spreadsheetml"):
		return app.DocumentFormatXLSX
	case strings.Contains(contentType, "presentationml"):
		return app.DocumentFormatPPTX
	default:
		return ""
	}
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
