package agent

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"testing"
)

func TestAIChatFourSiblingBranches(t *testing.T) {
	c := capability.MustDefaultCatalog()
	registry := defaultWorkflowProfileRegistry()
	for provider, url := range map[string]string{"chatgpt": "https://chatgpt.com/c/abc", "claude": "https://claude.ai/chat/abc", "gemini": "https://gemini.google.com/app/abc", "grok": "https://grok.com/c/abc"} {
		route := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: c.Revision(), CapabilityPath: []app.CapabilityID{"ai_chat", app.CapabilityID("ai_chat." + provider)}, Slots: app.RouteSlots{Operation: app.RouteOperationRead, TargetKind: "url", TargetRef: url}}
		if _, err := registry.Resolve(c, route, "turn"); err != nil {
			t.Fatalf("%s: %v", provider, err)
		}
		p := aiChatProfile{provider: provider}
		if p.DirectStageArguments(nil)["provider"] != provider {
			t.Fatal("provider binding lost")
		}
		route.Slots.TargetRef = "https://example.com/"
		if _, err := registry.Resolve(c, route, "turn"); err == nil {
			t.Fatal("foreign URL accepted")
		}
	}
}
func TestAIChatRequiresSavedFileEvidence(t *testing.T) {
	p := aiChatProfile{provider: "chatgpt"}
	if p.Assess(nil, app.ToolOutcome{Status: app.ToolCallStatusCompleted}).Status == app.AssessmentComplete {
		t.Fatal("empty success accepted")
	}
	out := adaptGenericWorkflowOutcome(app.ToolCall{ID: "export", Status: app.ToolCallStatusCompleted, Result: map[string]any{"path": "ai-chat-exports/test/conversation.json"}}, "export")
	if p.Assess(nil, out).Status != app.AssessmentComplete {
		t.Fatal("receipt rejected")
	}
}
