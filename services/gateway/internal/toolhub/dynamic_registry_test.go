package toolhub

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestReplaceDynamicToolsRegistersExecutesAndRefreshesOneSource(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	defer hub.Close()
	registration := DynamicToolRegistration{
		Definition: app.ToolDefinition{
			Name: "mcp.fixture.read", Description: "fixture", InputSchema: schema("object", []string{"id"}, map[string]any{"id": stringSchema()}),
			Risk: app.RiskRead, TimeoutMS: 5000, Audit: "always", Sandbox: "forbidden",
		},
		RemoteName: "read",
		Execute: func(_ context.Context, args map[string]any, sessionID, runID string) (Result, error) {
			return Result{Output: map[string]any{"id": args["id"], "session": sessionID, "run": runID}}, nil
		},
	}
	if err := hub.ReplaceDynamicTools("fixture", []DynamicToolRegistration{registration}); err != nil {
		t.Fatal(err)
	}
	origin, ok := hub.DynamicToolOrigin("mcp.fixture.read")
	if !ok || origin.Source != "fixture" || origin.RemoteName != "read" {
		t.Fatalf("origin = %#v, %t", origin, ok)
	}
	result, err := hub.Execute(t.Context(), "mcp.fixture.read", map[string]any{"id": "one"}, "session-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["id"] != "one" || output["session"] != "session-1" || output["run"] != "run-1" {
		t.Fatalf("unexpected output: %#v", output)
	}

	registration.Definition.Name = "mcp.fixture.second"
	registration.RemoteName = "second"
	if err := hub.ReplaceDynamicTools("fixture", []DynamicToolRegistration{registration}); err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.Definition("mcp.fixture.read"); ok {
		t.Fatal("refresh retained a stale dynamic tool")
	}
	if _, ok := hub.Definition("mcp.fixture.second"); !ok {
		t.Fatal("refresh omitted the replacement dynamic tool")
	}
	if err := hub.ReplaceDynamicTools("fixture", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.Definition("mcp.fixture.second"); ok {
		t.Fatal("clearing a source retained its tool")
	}
}

func TestReplaceDynamicToolsRejectsStaticAndCrossSourceCollisions(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	defer hub.Close()
	makeRegistration := func(name string) DynamicToolRegistration {
		return DynamicToolRegistration{
			Definition: app.ToolDefinition{Name: name, InputSchema: schema("object", nil, nil), Risk: app.RiskRead},
			RemoteName: "remote", Execute: func(context.Context, map[string]any, string, string) (Result, error) { return Result{}, nil },
		}
	}
	if err := hub.ReplaceDynamicTools("fixture", []DynamicToolRegistration{makeRegistration("files.search")}); err == nil {
		t.Fatal("static tool collision was accepted")
	}
	if err := hub.ReplaceDynamicTools("one", []DynamicToolRegistration{makeRegistration("mcp.shared.tool")}); err != nil {
		t.Fatal(err)
	}
	if err := hub.ReplaceDynamicTools("two", []DynamicToolRegistration{makeRegistration("mcp.shared.tool")}); err == nil {
		t.Fatal("cross-source collision was accepted")
	}
}
