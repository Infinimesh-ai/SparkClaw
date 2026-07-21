package agent

import (
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func workflowResultMessage(run app.AgentRun, result *app.WorkflowResult, fallback string, createdAt time.Time) app.Message {
	content, attachments := workflowResultMessagePayload(result, fallback)
	return app.Message{
		SessionID:   run.SessionID,
		RunID:       run.ID,
		Role:        "assistant",
		Content:     content,
		CreatedAt:   createdAt,
		Attachments: attachments,
	}
}

func workflowResultMessagePayload(result *app.WorkflowResult, fallback string) (string, []app.MessageAttachment) {
	if result == nil {
		return fallback, nil
	}
	textParts := make([]string, 0, len(result.Content.Parts))
	attachments := make([]app.MessageAttachment, 0, len(result.Content.Parts))
	for _, part := range result.Content.Parts {
		if part.Kind == app.MessagePartText {
			if text := strings.TrimSpace(part.Text); text != "" {
				textParts = append(textParts, text)
			}
			continue
		}
		if part.Kind != app.MessagePartImage && part.Kind != app.MessagePartAudio && part.Kind != app.MessagePartFile {
			continue
		}
		relPath := ""
		if part.Resource != nil && part.Resource.Kind == "workspace_file" {
			relPath = strings.TrimSpace(part.Resource.Ref)
		}
		if relPath == "" {
			continue
		}
		name := strings.TrimSpace(part.Name)
		if name == "" {
			segments := strings.Split(strings.TrimSuffix(relPath, "/"), "/")
			name = segments[len(segments)-1]
		}
		attachments = append(attachments, app.MessageAttachment{
			ArtifactID:  part.ArtifactID,
			Name:        name,
			RelPath:     relPath,
			URI:         "workspace://" + relPath,
			ContentType: part.ContentType,
			Bytes:       part.Bytes,
			Width:       part.Width,
			Height:      part.Height,
			SHA256:      part.SHA256,
			Source:      "workflow_result",
			Caption:     part.Caption,
		})
	}
	content := strings.Join(textParts, "\n\n")
	if content == "" && len(attachments) == 0 {
		content = fallback
	}
	return content, attachments
}
