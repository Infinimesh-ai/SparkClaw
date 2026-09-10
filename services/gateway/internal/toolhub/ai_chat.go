package toolhub

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/aichatexport"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type AIChatExporter interface {
	Export(context.Context, string, string, string) (aichatexport.Receipt, error)
}

func (h *ToolHub) WithAIChatExporter(exporter AIChatExporter) *ToolHub {
	h.aiChatExporter = exporter
	return h
}
func aiChatDefinition() app.ToolDefinition {
	return app.ToolDefinition{Name: "ai_chat.export", Description: "Export one explicit AI conversation using the installed RevivalStack userscript; persist original JSON in the session workspace and return a receipt, not conversation text.",
		InputSchema:  strictObjectSchema([]string{"provider", "url"}, map[string]any{"provider": map[string]any{"type": "string", "enum": []any{"chatgpt", "claude", "gemini", "grok"}}, "url": stringSchema()}),
		OutputSchema: strictObjectSchema([]string{"status", "path", "manifest_path", "sha256", "bytes", "message_count", "provider", "source_url", "coverage"}, map[string]any{"status": stringSchema(), "path": stringSchema(), "manifest_path": stringSchema(), "sha256": stringSchema(), "bytes": integerSchema(), "message_count": integerSchema(), "provider": stringSchema(), "source_url": stringSchema(), "coverage": stringSchema()}),
		Risk:         app.RiskDraft, Idempotent: false, TimeoutMS: 95000, Sandbox: "forbidden", Audit: "always"}
}
func aiChatRegistration() toolRegistration {
	return workflowRegistration(toolRegistration{enabled: browserAutomationEnabled, run: argsSessionContext((*ToolHub).aiChatExport)}, "ai_chat.export", nil, app.OutcomeAdapterGeneric, "Save one AI conversation's original userscript JSON into workspace.", "Use in ai_chat provider branches with an explicit conversation URL.", "Not for sending prompts, public research, summaries, memory processing or batch export.", app.ToolEffectExternalRead, app.ToolEffectWorkspaceWrite)
}
func (h *ToolHub) aiChatExport(ctx context.Context, args map[string]any, sessionID string) (Result, error) {
	if h.aiChatExporter == nil {
		return Result{}, errors.New("ai_chat_exporter_unavailable")
	}
	if h.store == nil || sessionID == "" {
		return Result{}, errors.New("ai_chat_session_required")
	}
	session, ok, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	if !ok || session.WorkspaceRoot == "" {
		return Result{}, errors.New("ai_chat_workspace_missing")
	}
	receipt, err := h.aiChatExporter.Export(ctx, session.WorkspaceRoot, stringArg(args, "provider", ""), stringArg(args, "url", ""))
	if err != nil {
		return Result{}, err
	}
	return Result{Output: receipt}, nil
}
