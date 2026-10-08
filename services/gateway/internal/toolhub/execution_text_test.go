package toolhub

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestTextOnlyExecutionScopeNeverExposesOrExecutesTools(t *testing.T) {
	cfg := config.Default()
	parent := New(cfg, store.NewMemoryStore())
	t.Cleanup(func() { _ = parent.Close() })
	local := store.NewMemoryStore()
	storage := cfg.Storage
	storage.ArtifactBackend, storage.ArtifactDir = "filesystem", t.TempDir()
	scoped, err := parent.WithExecutionScope(local, ExecutionResources{OwnerID: "owner", WorkspaceRoot: t.TempDir(), Artifacts: artifact.NewStore(storage), TextOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scoped.Close() })
	if len(parent.Definitions()) == 0 || len(scoped.Definitions()) != 0 {
		t.Fatal("text-only scope did not remove service-owned tool exposure")
	}
	for _, definition := range parent.Definitions() {
		if _, err := scoped.Execute(t.Context(), definition.Name, nil, "unused-session", "unused-run"); app.ToolErrorCodeFrom(err) != app.ToolErrorResourceUnavailable {
			t.Errorf("%s reached execution: %v", definition.Name, err)
		}
	}
	ran := false
	if err := parent.ReplaceDynamicTools("test", []DynamicToolRegistration{{Definition: app.ToolDefinition{Name: "test.external", InputSchema: strictObjectSchema(nil, nil)}, RemoteName: "external", Execute: func(context.Context, map[string]any, string, string) (Result, error) {
		ran = true
		return Result{}, nil
	}}}); err != nil {
		t.Fatal(err)
	}
	if len(scoped.Definitions()) != 0 {
		t.Fatal("dynamic tool became visible after registry refresh")
	}
	if _, err := scoped.Execute(t.Context(), "test.external", nil, "unused-session", "unused-run"); app.ToolErrorCodeFrom(err) != app.ToolErrorResourceUnavailable || ran {
		t.Fatalf("dynamic tool ran: %v", err)
	}
}
