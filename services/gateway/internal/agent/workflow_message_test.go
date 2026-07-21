package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestWorkflowResultMessagePayloadProjectsChannelNeutralParts(t *testing.T) {
	result := &app.WorkflowResult{Content: app.MessageContent{Parts: []app.MessagePart{
		{ID: "text-1", Kind: app.MessagePartText, Text: "Completed."},
		{ID: "image-1", Kind: app.MessagePartImage, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "media/card.png"}, Name: "card.png", ContentType: "image/png", Width: 900, Height: 1200},
		{ID: "audio-1", Kind: app.MessagePartAudio, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "media/reply.mp3"}, ContentType: "audio/mpeg"},
		{ID: "file-1", Kind: app.MessagePartFile, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "outputs/report.pdf"}, ContentType: "application/pdf"},
	}}}

	content, attachments := workflowResultMessagePayload(result, "fallback")
	if content != "Completed." || len(attachments) != 3 {
		t.Fatalf("unexpected message projection: content=%q attachments=%#v", content, attachments)
	}
	if attachments[0].RelPath != "media/card.png" || attachments[0].URI != "workspace://media/card.png" ||
		attachments[0].Source != "workflow_result" || attachments[0].Width != 900 || attachments[0].Height != 1200 {
		t.Fatalf("image metadata was not preserved: %#v", attachments[0])
	}
	if attachments[1].Name != "reply.mp3" || attachments[2].Name != "report.pdf" {
		t.Fatalf("resource names were not derived from workspace paths: %#v", attachments)
	}
}

func TestWorkflowResultMessagePayloadUsesFallbackOnlyWithoutDeliverableParts(t *testing.T) {
	content, attachments := workflowResultMessagePayload(&app.WorkflowResult{}, "fallback")
	if content != "fallback" || len(attachments) != 0 {
		t.Fatalf("unexpected empty result projection: content=%q attachments=%#v", content, attachments)
	}

	content, attachments = workflowResultMessagePayload(&app.WorkflowResult{Content: app.MessageContent{Parts: []app.MessagePart{{
		ID: "image-1", Kind: app.MessagePartImage, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "media/card.png"}, ContentType: "image/png",
	}}}}, "fallback")
	if content != "" || len(attachments) != 1 {
		t.Fatalf("attachment-only result should not duplicate fallback text: content=%q attachments=%#v", content, attachments)
	}
}
