package agent

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	documentProvenanceExplicitCurrent = "explicit_current"
	documentProvenanceCurrentResource = "current_resource"
	documentProvenanceDocumentRecord  = "document_record"
	documentProvenanceRecentTool      = "recent_tool"
	documentProvenanceRecentMessage   = "recent_message"
)

type documentContextReference struct {
	DocumentID       string
	ParentDocumentID string
	Ref              string
	Name             string
	ContentType      string
	Format           string
	Source           string
	SourceID         string
	Activity         string
	Provenance       string
	ObservedAt       time.Time
}

type documentContextResolution struct {
	References []documentContextReference
}

// resolveExternalMCPDocumentContext only projects locators supplied in the
// current request. Recent-record and artifact lookup would disclose workspace
// names before the external AI's data-access request is approved.
func resolveExternalMCPDocumentContext(content string, resources []app.MessagePart) documentContextResolution {
	if paths := documentRoutePaths(content); len(paths) > 0 {
		references := make([]documentContextReference, 0, len(paths))
		for _, path := range paths {
			references = append(references, documentContextReference{
				Ref: path, Name: filepath.Base(filepath.FromSlash(path)), Provenance: documentProvenanceExplicitCurrent,
			})
		}
		return documentContextResolution{References: dedupeDocumentReferences(references)}
	}
	return documentContextResolution{References: documentReferencesFromMessageParts(resources)}
}

func (r Runtime) resolveDocumentContext(ctx context.Context, sessionID, runID, content string, resources []app.MessagePart) (documentContextResolution, error) {
	if paths := documentRoutePaths(content); len(paths) > 0 {
		references := make([]documentContextReference, 0, len(paths))
		for _, path := range paths {
			reference := documentContextReference{
				Ref: path, Name: filepath.Base(filepath.FromSlash(path)), Provenance: documentProvenanceExplicitCurrent,
			}
			if record, ok := r.documentRecordByPath(sessionID, path); ok {
				reference = documentReferenceFromRecord(record, documentProvenanceExplicitCurrent)
			}
			references = append(references, reference)
		}
		return documentContextResolution{References: dedupeDocumentReferences(references)}, nil
	}
	if references := documentReferencesFromMessageParts(resources); len(references) > 0 {
		for index := range references {
			if record, ok := r.documentRecordByPath(sessionID, references[index].Ref); ok {
				references[index] = documentReferenceFromRecord(record, documentProvenanceCurrentResource)
			}
		}
		return documentContextResolution{References: references}, nil
	}
	if r.store == nil {
		return documentContextResolution{}, nil
	}
	workspaceRoot, err := r.workspaceRootForSession(ctx, sessionID)
	if err != nil {
		return documentContextResolution{}, err
	}
	if references := recentDocumentRecordReferences(
		r.store.ListDocumentRecords("", sessionID, 100),
		workspaceRoot,
	); len(references) > 0 {
		return documentContextResolution{References: references}, nil
	}

	snapshot, err := r.buildAgentContextSnapshot(ctx, sessionID, runID, content)
	if err != nil {
		return documentContextResolution{}, err
	}
	storedToolCalls, err := r.store.ListToolCalls(ctx, sessionID)
	if err != nil {
		return documentContextResolution{}, err
	}
	toolReferences := recentDocumentToolReferences(storedToolCalls, workspaceRoot)
	messageReferences := recentDocumentMessageReferences(snapshot.Messages)
	switch {
	case len(toolReferences) == 0:
		return documentContextResolution{References: messageReferences}, nil
	case len(messageReferences) == 0:
		return documentContextResolution{References: toolReferences}, nil
	case documentReferencesObservedAt(messageReferences).After(documentReferencesObservedAt(toolReferences)):
		return documentContextResolution{References: messageReferences}, nil
	default:
		return documentContextResolution{References: toolReferences}, nil
	}
}

func recentDocumentRecordReferences(records []app.DocumentRecord, workspaceRoot string) []documentContextReference {
	if len(records) == 0 {
		return nil
	}
	activityID := strings.TrimSpace(records[0].LastActivityID)
	if activityID == "" {
		activityID = records[0].ID
	}
	references := make([]documentContextReference, 0)
	referenceIndexes := make(map[string]int)
	canonicalRecord := make(map[string]bool)
	for _, record := range records {
		candidateActivityID := strings.TrimSpace(record.LastActivityID)
		if candidateActivityID == "" {
			candidateActivityID = record.ID
		}
		if candidateActivityID != activityID {
			continue
		}
		rawPath := normalizeGovernedDocumentPath(record.GovernedPath)
		path := normalizeDocumentOutputPath(workspaceRoot, rawPath)
		if path == "" {
			continue
		}
		reference := documentReferenceFromRecord(record, documentProvenanceDocumentRecord)
		reference.Ref = path
		isCanonical := !filepath.IsAbs(filepath.FromSlash(rawPath))
		if index, exists := referenceIndexes[path]; exists {
			if isCanonical && !canonicalRecord[path] {
				references[index] = reference
				canonicalRecord[path] = true
			}
			continue
		}
		referenceIndexes[path] = len(references)
		canonicalRecord[path] = isCanonical
		references = append(references, reference)
	}
	return references
}

func documentReferenceFromRecord(record app.DocumentRecord, provenance string) documentContextReference {
	return documentContextReference{
		DocumentID:       record.ID,
		ParentDocumentID: record.ParentDocumentID,
		Ref:              record.GovernedPath,
		Name:             record.Name,
		ContentType:      record.ContentType,
		Format:           record.Format,
		Source:           record.Source,
		SourceID: firstNonEmptyString(
			record.SourceToolCallID,
			record.SourceMessageID,
			record.SourceRunID,
		),
		Activity:   record.LastActivity,
		Provenance: provenance,
		ObservedAt: record.LastActivityAt,
	}
}

func documentReferencesFromMessageParts(resources []app.MessagePart) []documentContextReference {
	references := make([]documentContextReference, 0, len(resources))
	for _, resource := range resources {
		if (resource.Kind != app.MessagePartFile && resource.Kind != app.MessagePartImage) ||
			resource.Resource == nil || resource.Resource.Kind != "workspace_file" {
			continue
		}
		references = append(references, documentContextReference{
			Ref:         resource.Resource.Ref,
			Name:        resource.Name,
			ContentType: resource.ContentType,
			Format:      documentFormatFromMetadata(resource.Name, resource.ContentType),
			Source:      app.DocumentSourceAttachment,
			SourceID:    resource.ID,
			Provenance:  documentProvenanceCurrentResource,
		})
	}
	return dedupeDocumentReferences(references)
}

func recentDocumentToolReferences(calls []app.ToolCall, workspaceRoot string) []documentContextReference {
	selectedIndex := -1
	selectedAt := time.Time{}
	for index, call := range calls {
		if !toolCallCompleted(call) || !isDocumentContextCall(call) {
			continue
		}
		if len(documentContextPathsFromToolCall(call, workspaceRoot)) == 0 {
			continue
		}
		observedAt := call.StartedAt
		if call.CompletedAt != nil {
			observedAt = *call.CompletedAt
		}
		if selectedIndex < 0 || observedAt.After(selectedAt) || observedAt.Equal(selectedAt) && index > selectedIndex {
			selectedIndex, selectedAt = index, observedAt
		}
	}
	if selectedIndex < 0 {
		return nil
	}
	selected := calls[selectedIndex]
	paths := documentContextPathsFromToolCall(selected, workspaceRoot)
	references := make([]documentContextReference, 0, len(paths))
	for _, path := range paths {
		references = append(references, documentContextReference{
			Ref: path, Name: filepath.Base(filepath.FromSlash(path)),
			Format: documentFormatFromMetadata(path, ""), Source: app.DocumentSourceToolOutput, SourceID: selected.ID,
			Activity: selected.Tool, Provenance: documentProvenanceRecentTool, ObservedAt: selectedAt,
		})
	}
	return dedupeDocumentReferences(references)
}

func documentContextPathsFromToolCall(call app.ToolCall, workspaceRoot string) []string {
	if outputs := documentOutputPaths(call, workspaceRoot); len(outputs) > 0 {
		return outputs
	}
	path := normalizeGovernedDocumentPath(firstNonEmptyString(
		strings.TrimSpace(stringValue(call.Arguments["path"])),
		toolResultDocumentPath(call.Result),
	))
	if path == "" {
		return nil
	}
	return []string{path}
}

func isDocumentContextCall(call app.ToolCall) bool {
	if call.Capability == app.ToolCapabilityDocumentRead || call.Capability == app.ToolCapabilityDocumentEdit {
		return true
	}
	tool := strings.TrimSpace(call.Tool)
	return tool == "files.read" || tool == "images.inspect" || tool == "pdf.extract_text" ||
		tool == "pdf.transform" || tool == "text.replace_text" || isDocumentContextTool(tool)
}

func recentDocumentMessageReferences(messages []app.Message) []documentContextReference {
	selectedIndex := -1
	selectedAt := time.Time{}
	var selected app.Message
	for index, message := range messages {
		if strings.TrimSpace(message.Role) != "user" || len(message.Attachments) == 0 {
			continue
		}
		if selectedIndex < 0 || message.CreatedAt.After(selectedAt) || message.CreatedAt.Equal(selectedAt) && index > selectedIndex {
			selectedIndex, selectedAt, selected = index, message.CreatedAt, message
		}
	}
	if selectedIndex < 0 {
		return nil
	}
	references := make([]documentContextReference, 0, len(selected.Attachments))
	for _, attachment := range selected.Attachments {
		references = append(references, documentContextReference{
			Ref:         attachment.RelPath,
			Name:        attachment.Name,
			ContentType: attachment.ContentType,
			Format:      documentFormatFromMetadata(attachment.Name, attachment.ContentType),
			Source:      app.DocumentSourceAttachment,
			SourceID:    selected.ID,
			Activity:    app.DocumentActivityAttached,
			Provenance:  documentProvenanceRecentMessage,
			ObservedAt:  selectedAt,
		})
	}
	return dedupeDocumentReferences(references)
}

func dedupeDocumentReferences(references []documentContextReference) []documentContextReference {
	seen := make(map[string]bool, len(references))
	out := make([]documentContextReference, 0, len(references))
	for _, reference := range references {
		reference.Ref = strings.TrimSpace(filepath.ToSlash(reference.Ref))
		if reference.Ref == "" || seen[reference.Ref] {
			continue
		}
		seen[reference.Ref] = true
		out = append(out, reference)
	}
	return out
}

func documentReferencesObservedAt(references []documentContextReference) time.Time {
	latest := time.Time{}
	for _, reference := range references {
		if reference.ObservedAt.After(latest) {
			latest = reference.ObservedAt
		}
	}
	return latest
}

func formatDocumentRoutingContext(resolution documentContextResolution) string {
	lines := make([]string, 0, len(resolution.References))
	for _, reference := range resolution.References {
		if reference.Provenance == documentProvenanceExplicitCurrent {
			continue
		}
		fields := []string{
			"path=" + quoteEpisodeField(reference.Ref, 240),
			"provenance=" + quoteEpisodeField(reference.Provenance, 40),
		}
		if documentID := strings.TrimSpace(reference.DocumentID); documentID != "" {
			fields = append(fields, "document_id="+quoteEpisodeField(documentID, 100))
		}
		if parentDocumentID := strings.TrimSpace(reference.ParentDocumentID); parentDocumentID != "" {
			fields = append(fields, "parent_document_id="+quoteEpisodeField(parentDocumentID, 100))
		}
		if name := strings.TrimSpace(reference.Name); name != "" {
			fields = append(fields, "name="+quoteEpisodeField(name, 160))
		}
		if contentType := strings.TrimSpace(reference.ContentType); contentType != "" {
			fields = append(fields, "content_type="+quoteEpisodeField(contentType, 120))
		}
		if format := strings.TrimSpace(reference.Format); format != "" {
			fields = append(fields, "format="+quoteEpisodeField(format, 40))
		}
		if source := strings.TrimSpace(reference.Source); source != "" {
			fields = append(fields, "source="+quoteEpisodeField(source, 60))
		}
		if sourceID := strings.TrimSpace(reference.SourceID); sourceID != "" {
			fields = append(fields, "source_id="+quoteEpisodeField(sourceID, 100))
		}
		if activity := strings.TrimSpace(reference.Activity); activity != "" {
			fields = append(fields, "recent_activity="+quoteEpisodeField(activity, 80))
		}
		lines = append(lines, "- "+strings.Join(fields, " "))
	}
	return strings.Join(lines, "\n")
}
