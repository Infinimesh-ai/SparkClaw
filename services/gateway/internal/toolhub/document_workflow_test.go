package toolhub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestDocumentWorkflowReadsEverySmallFormatIntoStableLocations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("# Stable heading\nTarget sentence"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDocxFixture(t, root, "note.docx", "Stable heading\nTarget sentence")
	writeXlsxFixture(t, root, "book.xlsx")
	writePptxFixture(t, root, "deck.pptx")
	writePDFBlankFixture(t, root, "pages.pdf", 2)
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())

	for _, test := range []struct {
		path       string
		format     string
		entityName string
	}{
		{path: "note.md", format: "text", entityName: "sections"},
		{path: "note.docx", format: "docx", entityName: "paragraphs"},
		{path: "book.xlsx", format: "xlsx", entityName: "sheets"},
		{path: "deck.pptx", format: "pptx", entityName: "slides"},
		{path: "pages.pdf", format: "pdf", entityName: "pages"},
	} {
		t.Run(test.format, func(t *testing.T) {
			first := executeDocumentRead(t, hub, test.path)
			second := executeDocumentRead(t, hub, test.path)
			firstDocument := first["document"].(map[string]any)
			secondDocument := second["document"].(map[string]any)
			if first["truncated"] != false || firstDocument["representation_version"] != "structured_document_v1" || firstDocument["format"] != test.format {
				t.Fatalf("unexpected structured read: %#v", first)
			}
			if firstDocument["id"] == "" || firstDocument["id"] != secondDocument["id"] {
				t.Fatalf("document ID is not stable: first=%#v second=%#v", firstDocument["id"], secondDocument["id"])
			}
			blocks := testAnySlice(firstDocument["blocks"])
			secondBlocks := testAnySlice(secondDocument["blocks"])
			if len(blocks) == 0 || len(secondBlocks) != len(blocks) || blocks[0].(map[string]any)["id"] != secondBlocks[0].(map[string]any)["id"] {
				t.Fatalf("stable blocks are missing: first=%#v second=%#v", blocks, secondBlocks)
			}
			entities := testAnySlice(firstDocument[test.entityName])
			if len(entities) == 0 || entities[0].(map[string]any)["id"] == "" {
				t.Fatalf("%s IDs are missing: %#v", test.entityName, entities)
			}
		})
	}
}

func TestDocumentWorkflowAppliesConstrainedSmallFormatEditsToCopies(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "Original sentence")
	writeXlsxFixture(t, root, "book.xlsx")
	writePptxFixture(t, root, "deck.pptx")
	writePDFBlankFixture(t, root, "pages.pdf", 2)
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())

	for _, test := range []struct {
		name        string
		input       string
		output      string
		tool        string
		args        map[string]any
		wantText    string
		wantChanged int
	}{
		{
			name: "docx", input: "note.docx", output: "outputs/note.docx", tool: "office.replace_text", wantText: "Updated sentence", wantChanged: 1,
			args: map[string]any{"replacements": []any{map[string]any{"find": "Original sentence", "replace": "Updated sentence"}}, "expected_replacements": 1},
		},
		{
			name: "xlsx", input: "book.xlsx", output: "outputs/book.xlsx", tool: "xlsx.update_cell", wantText: "Approved", wantChanged: 1,
			args: map[string]any{"sheet": "Sheet1", "cell": "C2", "value": "Approved"},
		},
		{
			name: "pptx", input: "deck.pptx", output: "outputs/deck.pptx", tool: "office.replace_text", wantText: "Updated body", wantChanged: 1,
			args: map[string]any{"replacements": []any{map[string]any{"find": "First body", "replace": "Updated body"}}, "expected_replacements": 1},
		},
		{
			name: "pdf", input: "pages.pdf", output: "outputs/pages.pdf", tool: "pdf.transform", wantChanged: 1,
			args: map[string]any{"operation": "rotate_pages", "pages": []any{1}, "rotation": 90},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputPath := filepath.Join(root, test.input)
			before, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			args := cloneTestMap(test.args)
			args["path"] = test.input
			args["output_path"] = test.output
			result, err := hub.Execute(context.Background(), test.tool, args, "session", "run")
			if err != nil {
				t.Fatal(err)
			}
			output := result.Output.(map[string]any)
			summary, ok := output["change_summary"].(map[string]any)
			if !ok || intArg(summary, "changed", 0) != test.wantChanged || intArg(summary, "matched", 0) == 0 || summary["original_unchanged"] != true || stringArg(summary, "document_id", "") == "" {
				t.Fatalf("change summary is incomplete: %#v", output["change_summary"])
			}
			after, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("document editor modified the original")
			}
			read := executeDocumentRead(t, hub, test.output)
			if test.wantText != "" && !strings.Contains(read["content"].(string), test.wantText) {
				t.Fatalf("edited copy is missing %q: %#v", test.wantText, read)
			}
		})
	}
}

func TestDocumentWorkflowRejectsMissingAmbiguousAndUnsupportedInputs(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "ambiguous.docx", "Repeated target\nRepeated target")
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), bytes.Repeat([]byte{'x'}, int(document.SmallFileMaxBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())

	baseArgs := map[string]any{
		"path": "ambiguous.docx", "replacements": []any{map[string]any{"find": "Repeated target", "replace": "Updated"}},
	}
	missing := cloneTestMap(baseArgs)
	missing["output_path"] = "outputs/missing.docx"
	missing["replacements"] = []any{map[string]any{"find": "Absent target", "replace": "Updated"}}
	if _, err := hub.Execute(context.Background(), "office.replace_text", missing, "session", "run"); !document.IsErrorCode(err, document.CodeTargetNotFound) {
		t.Fatalf("missing edit target did not fail explicitly: %v", err)
	}
	ambiguous := cloneTestMap(baseArgs)
	ambiguous["output_path"] = "outputs/ambiguous.docx"
	if _, err := hub.Execute(context.Background(), "office.replace_text", ambiguous, "session", "run"); !document.IsErrorCode(err, document.CodeTargetAmbiguous) {
		t.Fatalf("ambiguous edit target did not fail explicitly: %v", err)
	}
	multiple := cloneTestMap(baseArgs)
	multiple["output_path"] = "outputs/multiple.docx"
	multiple["expected_replacements"] = 2
	result, err := hub.Execute(context.Background(), "office.replace_text", multiple, "session", "run")
	if err != nil || result.Output.(map[string]any)["replacements"] != 2 {
		t.Fatalf("explicit multi-match edit failed: result=%#v err=%v", result, err)
	}
	if _, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "binary.bin"}, "session", "run"); !document.IsErrorCode(err, document.CodeFormatUnsupported) {
		t.Fatalf("unsupported format did not fail explicitly: %v", err)
	}
	if _, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "large.txt"}, "session", "run"); !document.IsErrorCode(err, document.CodeStrategyDeferred) {
		t.Fatalf("oversized document did not return deferred: %v", err)
	}
}

func newDocumentWorkflowHub(t *testing.T, root string, state store.Store) *ToolHub {
	t.Helper()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, state)
	t.Cleanup(func() { _ = hub.Close() })
	return hub
}

func executeDocumentRead(t *testing.T, hub *ToolHub, path string) map[string]any {
	t.Helper()
	result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": path}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	return result.Output.(map[string]any)
}

func cloneTestMap(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
