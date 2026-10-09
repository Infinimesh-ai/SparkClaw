package toolhub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestExecutionScopeExactToolPermissionsAndCurrentAuthority(t *testing.T) {
	parent := New(config.Default(), store.NewMemoryStore())
	t.Cleanup(func() { _ = parent.Close() })
	scoped, local, session := executionScopeFixture(t, parent, nil)
	resources := *scoped.resources
	allow := []string{"files.read"}
	resources.AllowedTools = allow
	checks := 0
	revoked := false
	resources.AuthorizeTool = func(ctx context.Context, name string) error {
		checks++
		if name != "files.read" {
			t.Fatal(name)
		}
		if revoked {
			return errors.New("authorization revoked")
		}
		return ctx.Err()
	}
	scoped, err := parent.WithExecutionScope(local, resources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scoped.Close() })
	allow[0] = "files.write_draft"
	if definitions := scoped.Definitions(); len(definitions) != 1 || definitions[0].Name != "files.read" {
		t.Fatal("allowlist was widened", definitions)
	}
	if err := os.WriteFile(filepath.Join(resources.WorkspaceRoot, "note.txt"), []byte("permitted content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.Execute(t.Context(), "files.read", map[string]any{"path": "note.txt"}, session.ID, "run"); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatal("dispatch skipped authority check")
	}
	revoked = true
	if _, err := scoped.Execute(t.Context(), "files.read", map[string]any{"path": "note.txt"}, session.ID, "run-after-approval"); app.ToolErrorCodeFrom(err) != app.ToolErrorResourceUnavailable || checks != 2 {
		t.Fatal("later dispatch bypassed revocation", err)
	}
	for _, definition := range parent.Definitions() {
		if definition.Name == "files.read" {
			continue
		}
		if _, err := scoped.Execute(t.Context(), definition.Name, nil, session.ID, "run"); app.ToolErrorCodeFrom(err) != app.ToolErrorResourceUnavailable {
			t.Error(definition.Name, err)
		}
	}
	resources.AllowedTools = []string{}
	empty, err := parent.WithExecutionScope(local, resources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = empty.Close() })
	if len(empty.Definitions()) != 0 {
		t.Fatal("empty explicit allowlist became unrestricted")
	}
}
