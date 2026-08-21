package toolhub

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

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

func TestToolHubUsesSessionWorkspaceRoot(t *testing.T) {
	globalRoot := t.TempDir()
	userA := filepath.Join(globalRoot, "users", "a")
	userB := filepath.Join(globalRoot, "users", "b")
	if err := os.MkdirAll(userA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userA, "note.txt"), []byte("alpha workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userB, "note.txt"), []byte("beta workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = globalRoot
	cfg.Workspaces.Allowlist = []string{globalRoot}
	st := store.NewMemoryStore()
	sessionA := storetest.MustCreateSessionWithScope(t, st, "A", "owner-a", userA, "weixin", true)
	sessionB := storetest.MustCreateSessionWithScope(t, st, "B", "owner-b", userB, "weixin", true)
	hub := New(cfg, st)

	read := func(sessionID string) string {
		t.Helper()
		result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "note.txt"}, sessionID, "run")
		if err != nil {
			t.Fatal(err)
		}
		output, _ := result.Output.(map[string]any)
		content, _ := output["content"].(string)
		return content
	}
	if got := read(sessionA.ID); !strings.Contains(got, "alpha workspace") {
		t.Fatalf("session A read wrong workspace content: %q", got)
	}
	if got := read(sessionB.ID); !strings.Contains(got, "beta workspace") {
		t.Fatalf("session B read wrong workspace content: %q", got)
	}
}

func writeTestJPEG(path string, width, height int) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 180, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return jpeg.Encode(file, img, &jpeg.Options{Quality: 85})
}

func TestValidateInputAllowsVerifierMetadataForApprovalArguments(t *testing.T) {
	definition := app.ToolDefinition{
		Name: "strict.approval", InputSchema: strictObjectSchema([]string{"command"}, map[string]any{"command": stringSchema()}),
	}
	err := validateInput(definition, map[string]any{
		"command": "ls -la",
		"_verifier": app.VerifierDecision{
			Verdict: "ask_user",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateInput(definition, map[string]any{"command": "ls -la", "invented": true}); err == nil {
		t.Fatal("strict approval schema accepted non-verifier metadata")
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

func TestFilesReadReturnsFullTextUntilMaxBytes(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 520)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%03d", i+1)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	first, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "large.txt"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	firstOut := first.Output.(map[string]any)
	firstContent := firstOut["content"].(string)
	if firstOut["truncated"] != false || !strings.Contains(firstContent, "line-001") || !strings.Contains(firstContent, "line-520") {
		t.Fatalf("expected full small text read, got %#v", firstOut)
	}
	textDocument := firstOut["document"].(map[string]any)
	if textDocument["schema_version"] != "document_read_v1" || textDocument["format"] != "text" {
		t.Fatalf("text read should use unified document envelope: %#v", textDocument)
	}
	textStrategy := textDocument["strategy"].(map[string]any)
	if textStrategy["mode"] != "full" || textStrategy["complete"] != true {
		t.Fatalf("text read should report full strategy: %#v", textStrategy)
	}
	textPipeline := textDocument["pipeline"].(map[string]any)
	textPipelineStrategy := textPipeline["strategy"].(map[string]any)
	if textPipeline["status"] != "succeeded" || textPipelineStrategy["strategy"] != "small_direct" || textPipelineStrategy["context_mode"] != "full_text" {
		t.Fatalf("text read should enter the small-document pipeline: %#v", textPipeline)
	}
	textIndex := textPipeline["index"].(map[string]any)
	if textIndex["index_status"] != "skipped" {
		t.Fatalf("small text read should skip retrieval index: %#v", textIndex)
	}
	_, err = hub.Execute(context.Background(), "files.read", map[string]any{
		"path":      "large.txt",
		"max_bytes": 80,
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeStrategyDeferred) || !strings.Contains(err.Error(), "limit=80") {
		t.Fatalf("limited small-file read must defer instead of truncating, got %v", err)
	}
}

func TestResolvePathAcceptsAllowedMacAbsolutePathMissingLeadingSlash(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	missingSlash := strings.TrimPrefix(filepath.Join(root, "note.txt"), string(os.PathSeparator))
	got, err := hub.resolvePath(missingSlash)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "note.txt") {
		t.Fatalf("unexpected normalized path: %q", got)
	}
}
