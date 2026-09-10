package toolhub

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/aichatexport"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"testing"
)

type fakeChatExporter struct {
	root  string
	calls int
}

func (f *fakeChatExporter) Export(_ context.Context, root, provider, url string) (aichatexport.Receipt, error) {
	f.root = root
	f.calls++
	return aichatexport.Receipt{Status: "saved", Path: "ai-chat-exports/test/conversation.json", Provider: provider, SourceURL: url}, nil
}
func TestAIChatUsesSessionWorkspaceAndRejectsCallerPaths(t *testing.T) {
	st := store.NewMemoryStore()
	root := t.TempDir()
	session := storetest.MustCreateSessionWithScope(t, st, "ai_chat", "owner", root, "web", false)
	exporter := &fakeChatExporter{}
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	h := New(cfg, st).WithAIChatExporter(exporter)
	t.Cleanup(func() { _ = h.Close() })
	args := map[string]any{"provider": "chatgpt", "url": "https://chatgpt.com/c/test"}
	if _, err := h.Execute(t.Context(), "ai_chat.export", args, session.ID, "run"); err != nil {
		t.Fatal(err)
	}
	if exporter.root != root || exporter.calls != 1 {
		t.Fatal("wrong workspace")
	}
	args["workspace_root"] = "/tmp/attacker"
	if _, err := h.Execute(t.Context(), "ai_chat.export", args, session.ID, "run"); err == nil {
		t.Fatal("caller path accepted")
	}
	if exporter.calls != 1 {
		t.Fatal("invalid arguments reached exporter")
	}
}
