package agent

import (
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
)

func workflowResultMessage(run app.AgentRun, result *app.WorkflowResult, fallback string, createdAt time.Time) app.Message {
	if result == nil {
		return app.Message{SessionID: run.SessionID, RunID: run.ID, Role: "assistant", Content: fallback, CreatedAt: createdAt}
	}
	content, attachments := delivery.ProjectWebMessageContent(result.Content, fallback)
	return app.Message{
		SessionID:   run.SessionID,
		RunID:       run.ID,
		Role:        "assistant",
		Content:     content,
		CreatedAt:   createdAt,
		Attachments: attachments,
	}
}
