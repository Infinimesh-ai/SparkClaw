package toolhub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestValidateInputChecksRequiredTypesAndArrayItems(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())

	if err := hub.Validate("email.send", map[string]any{
		"to":      []any{"owner@example.test"},
		"subject": "SparkClaw checklist",
		"body":    "Ready.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Validate("email.send", map[string]any{
		"to":      []any{},
		"subject": "SparkClaw checklist",
		"body":    "Ready.",
	}); err == nil || !strings.Contains(err.Error(), "arguments.to must have at least 1 item") {
		t.Fatalf("expected minItems validation error, got %v", err)
	}
	if err := hub.Validate("email.send", map[string]any{
		"to":      []any{"owner@example.test", 42},
		"subject": "SparkClaw checklist",
		"body":    "Ready.",
	}); err == nil || !strings.Contains(err.Error(), "arguments.to[1] must be string") {
		t.Fatalf("expected item type validation error, got %v", err)
	}
	if err := hub.Validate("email.send", map[string]any{
		"to":      []any{"owner@example.test"},
		"subject": 99,
		"body":    "Ready.",
	}); err == nil || !strings.Contains(err.Error(), "arguments.subject must be string") {
		t.Fatalf("expected subject type validation error, got %v", err)
	}
}

func TestValidateInputSupportsJSONDecodedSchemaForms(t *testing.T) {
	def := app.ToolDefinition{
		Name: "test.schema",
		InputSchema: map[string]any{
			"type":                 "object",
			"required":             []any{"mode", "count"},
			"additionalProperties": false,
			"properties": map[string]any{
				"mode":  map[string]any{"enum": []any{"fast", "deep"}},
				"count": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(3)},
			},
		},
	}

	if err := validateInput(def, map[string]any{"mode": "fast", "count": float64(2)}); err != nil {
		t.Fatal(err)
	}
	if err := validateInput(def, map[string]any{"mode": "slow", "count": float64(2)}); err == nil || !strings.Contains(err.Error(), "arguments.mode must be one of [fast, deep]") {
		t.Fatalf("expected enum validation error, got %v", err)
	}
	if err := validateInput(def, map[string]any{"mode": "fast", "count": float64(2.5)}); err == nil || !strings.Contains(err.Error(), "arguments.count must be integer") {
		t.Fatalf("expected integer validation error, got %v", err)
	}
	if err := validateInput(def, map[string]any{"mode": "fast", "count": float64(4)}); err == nil || !strings.Contains(err.Error(), "arguments.count must be <= 3") {
		t.Fatalf("expected maximum validation error, got %v", err)
	}
	if err := validateInput(def, map[string]any{"mode": "fast", "count": float64(2), "extra": true}); err == nil || !strings.Contains(err.Error(), "arguments.extra is not allowed") {
		t.Fatalf("expected additionalProperties validation error, got %v", err)
	}
}

func TestValidateInputAllowsVerifierMetadataForApprovalArguments(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())

	err := hub.Validate("shell.exec_sandboxed", map[string]any{
		"command": "ls -la",
		"_verifier": app.VerifierDecision{
			Verdict: "ask_user",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDefaultDefinitionsExposeOutputSchemas(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	defs := hub.Definitions()
	if len(defs) == 0 {
		t.Fatal("expected default definitions")
	}
	for _, def := range defs {
		if len(def.InputSchema) == 0 {
			t.Fatalf("%s missing input schema", def.Name)
		}
		if len(def.OutputSchema) == 0 {
			t.Fatalf("%s missing output schema", def.Name)
		}
		if def.Risk == "" || def.TimeoutMS <= 0 || def.Sandbox == "" || def.Audit == "" {
			t.Fatalf("%s missing required contract metadata: %#v", def.Name, def)
		}
	}
}

func TestValidateOutputNormalizesStructResults(t *testing.T) {
	def := app.ToolDefinition{
		Name: "memory.write_candidate",
		OutputSchema: objectSchema([]string{"id", "created_at"}, map[string]any{
			"id":         stringSchema(),
			"created_at": stringSchema(),
		}),
	}

	err := validateOutput(def, app.MemoryCandidate{
		ID:        "mem_test",
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateOutputRejectsContractMismatch(t *testing.T) {
	def := app.ToolDefinition{
		Name: "files.read",
		OutputSchema: objectSchema([]string{"bytes"}, map[string]any{
			"bytes": integerSchema(),
		}),
	}

	err := validateOutput(def, map[string]any{"bytes": "not-a-number"})
	if err == nil || !strings.Contains(err.Error(), "files.read output schema violation") {
		t.Fatalf("expected output schema violation, got %v", err)
	}
}

func TestExecuteValidatesFilesReadOutput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("stable output contract"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": path}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["content"] != "stable output contract" {
		t.Fatalf("unexpected output: %#v", out)
	}
}
