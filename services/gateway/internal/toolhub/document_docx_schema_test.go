package toolhub

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestFilesReadReturnsFullDocxWithLocations(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("docx-line-%03d", i+1)
	}
	writeDocxFixture(t, root, "large.docx", strings.Join(lines, "\n"))
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	first, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "large.docx"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	firstOut := first.Output.(map[string]any)
	firstContent := firstOut["content"].(string)
	if firstOut["kind"] != "docx" || firstOut["truncated"] != false || !strings.Contains(firstContent, "docx-line-001") || !strings.Contains(firstContent, "docx-line-012") {
		t.Fatalf("unexpected docx full-read output: %#v", firstOut)
	}
	if _, ok := firstOut["document"].(map[string]any); !ok {
		t.Fatalf("docx read should preserve structured document payload: %#v", firstOut)
	}
	firstDocument := firstOut["document"].(map[string]any)
	if firstDocument["schema_version"] != "document_read_v1" || firstDocument["source"] != "python_docx" {
		t.Fatalf("docx should use unified document schema: %#v", firstDocument)
	}
	strategy := firstDocument["strategy"].(map[string]any)
	if strategy["mode"] != "full" || strategy["complete"] != true {
		t.Fatalf("docx should use unified full-read strategy metadata: %#v", strategy)
	}
	pipeline := firstDocument["pipeline"].(map[string]any)
	pipelineStrategy := pipeline["strategy"].(map[string]any)
	if pipeline["status"] != "succeeded" || pipelineStrategy["strategy"] != "small_direct" || pipelineStrategy["context_mode"] != "full_text" {
		t.Fatalf("docx should enter the small-document pipeline: %#v", pipeline)
	}
	paragraphs := firstDocument["paragraphs"].([]any)
	if len(paragraphs) != 12 {
		t.Fatalf("expected all docx paragraphs, got %#v", firstDocument)
	}
	evidenceBlocks := testAnySlice(firstDocument["evidence_blocks"])
	if len(evidenceBlocks) != 12 {
		t.Fatalf("expected docx evidence blocks, got %#v", firstDocument)
	}
	firstBlock := evidenceBlocks[0].(map[string]any)
	if firstBlock["blockId"] != "document.p[1]" || firstBlock["documentId"] != "large.docx" || firstBlock["fileType"] != "docx" || firstBlock["sourceHash"] == "" {
		t.Fatalf("unexpected evidence block identity: %#v", firstBlock)
	}
	location := firstBlock["location"].(map[string]any)
	if intArg(location, "paragraphIndex", 0) != 1 {
		t.Fatalf("evidence block should expose normalized paragraphIndex: %#v", firstBlock)
	}
}

func TestFilesReadDocxLocationDistinguishesTableCells(t *testing.T) {
	root := t.TempDir()
	pythonScript := `
from pathlib import Path
from docx import Document
root = Path(__import__("sys").argv[1])
doc = Document()
doc.add_paragraph("Before table")
table = doc.add_table(rows=1, cols=2)
table.cell(0, 0).text = "Cell A"
table.cell(0, 1).text = "Cell B"
doc.add_paragraph("After table")
doc.save(root / "table.docx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create table docx fixture: %v\n%s", err, out)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "files.read", map[string]any{
		"path": "table.docx",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	document := out["document"].(map[string]any)
	blocks := document["blocks"].([]any)
	var tableLocation map[string]any
	for _, value := range blocks {
		block := value.(map[string]any)
		if block["text"] == "Cell A" {
			tableLocation = block["location"].(map[string]any)
			break
		}
	}
	if tableLocation == nil {
		t.Fatalf("missing table cell block in docx read output: %#v", document)
	}
	if tableLocation["block_type"] != "table_cell" ||
		intArg(tableLocation, "paragraph_index", -1) != 0 ||
		intArg(tableLocation, "table_index", 0) != 1 ||
		intArg(tableLocation, "row_index", 0) != 1 ||
		intArg(tableLocation, "cell_index", 0) != 1 {
		t.Fatalf("unexpected table cell location: %#v", tableLocation)
	}
	evidenceBlocks := testAnySlice(document["evidence_blocks"])
	foundCellAnchor := false
	for _, value := range evidenceBlocks {
		block := value.(map[string]any)
		if block["text"] != "Cell A" {
			continue
		}
		foundCellAnchor = true
		if block["type"] != "table_cell" {
			t.Fatalf("table cell evidence block should keep type: %#v", block)
		}
		location := block["location"].(map[string]any)
		if location["tableId"] != "table_1" || intArg(location, "rowIndex", 0) != 1 || intArg(location, "columnIndex", 0) != 1 {
			t.Fatalf("table cell evidence block should normalize location: %#v", block)
		}
	}
	if !foundCellAnchor {
		t.Fatalf("missing table cell evidence block: %#v", evidenceBlocks)
	}
}

func TestDocxParagraphToolsAcceptReadLocation(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "First paragraph\nSecond paragraph")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	read, err := hub.Execute(context.Background(), "files.read", map[string]any{
		"path": "note.docx",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	readOut := read.Output.(map[string]any)
	document := readOut["document"].(map[string]any)
	blocks := document["blocks"].([]any)
	if len(blocks) < 2 {
		t.Fatalf("expected full read blocks, got %#v", document)
	}
	location := blocks[1].(map[string]any)["location"].(map[string]any)
	sourceSHA := document["metadata"].(map[string]any)["sha256"].(string)

	result, err := hub.Execute(context.Background(), "docx.replace_paragraph", map[string]any{
		"path": "note.docx", "source_sha256": sourceSHA,
		"location": location, "old_text": "Second paragraph", "source_hash": sourceHash("Second paragraph"),
		"text": "Replaced by location", "output_path": "outputs/location-replaced.docx",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if intArg(out, "paragraph_index", 0) != 2 {
		t.Fatalf("location should resolve paragraph 2: %#v", out)
	}
	edited, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": out["output_path"]}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	content := edited.Output.(map[string]any)["content"].(string)
	if !strings.Contains(content, "Replaced by location") || strings.Contains(content, "Second paragraph") {
		t.Fatalf("location replacement did not apply to expected paragraph: %q", content)
	}

	_, err = hub.Execute(context.Background(), "docx.replace_paragraph", map[string]any{
		"path": "note.docx", "source_sha256": sourceSHA,
		"location": location, "old_text": "Wrong paragraph", "source_hash": sourceHash("Second paragraph"),
		"text": "Should not be written", "output_path": "outputs/location-mismatch.docx",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "old_text mismatch") {
		t.Fatalf("expected old_text preflight mismatch, got %v", err)
	}
}

func TestDocxParagraphToolsRejectTableCellLocation(t *testing.T) {
	root := t.TempDir()
	pythonScript := `
from pathlib import Path
from docx import Document
root = Path(__import__("sys").argv[1])
doc = Document()
table = doc.add_table(rows=1, cols=1)
table.cell(0, 0).text = "Cell A"
doc.save(root / "table.docx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create table docx fixture: %v\n%s", err, out)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	read, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "table.docx"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	document := read.Output.(map[string]any)["document"].(map[string]any)
	blocks := document["blocks"].([]any)
	location := blocks[0].(map[string]any)["location"].(map[string]any)
	_, err = hub.Execute(context.Background(), "docx.delete_paragraph", map[string]any{
		"path": "table.docx", "source_sha256": docxSourceSHA256ForTest(t, root, "table.docx"),
		"location": location, "old_text": "Cell A", "source_hash": sourceHash("Cell A"), "output_path": "outputs/deleted.docx",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "only top-level paragraph locations are currently editable") {
		t.Fatalf("expected table cell location rejection, got %v", err)
	}
}

func TestDocxParagraphToolsWriteNewVersions(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "First paragraph\nSecond paragraph\nThird paragraph")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())
	sourceSHA := docxSourceSHA256ForTest(t, root, "note.docx")

	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{
			tool: "docx.replace_paragraph",
			args: map[string]any{
				"path":            "note.docx",
				"source_sha256":   sourceSHA,
				"paragraph_index": 2,
				"old_text":        "Second paragraph",
				"source_hash":     sourceHash("Second paragraph"),
				"text":            "Replaced second paragraph",
				"output_path":     "outputs/replaced.docx",
			},
			want: "Replaced second paragraph",
		},
		{
			tool: "docx.insert_paragraph",
			args: map[string]any{
				"path":            "note.docx",
				"source_sha256":   sourceSHA,
				"paragraph_index": 1,
				"position":        "after",
				"old_text":        "First paragraph",
				"source_hash":     sourceHash("First paragraph"),
				"text":            "Inserted after first",
				"output_path":     "outputs/inserted.docx",
			},
			want: "Inserted after first",
		},
		{
			tool: "docx.delete_paragraph",
			args: map[string]any{
				"path":            "note.docx",
				"source_sha256":   sourceSHA,
				"paragraph_index": 2,
				"old_text":        "Second paragraph",
				"source_hash":     sourceHash("Second paragraph"),
				"output_path":     "outputs/deleted.docx",
			},
			want: "First paragraph",
		},
		{
			tool: "docx.set_text_style",
			args: map[string]any{
				"path":                 "note.docx",
				"source_sha256":        sourceSHA,
				"paragraph_index":      1,
				"old_text":             "First paragraph",
				"source_hash":          sourceHash("First paragraph"),
				"before_format_sha256": "direct-toolhub-preflight",
				"style":                map[string]any{"builtin_style": "Heading 1", "bold": true, "font_size_pt": 18},
				"output_path":          "outputs/styled.docx",
			},
			want: "First paragraph",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result, err := hub.Execute(context.Background(), tc.tool, tc.args, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			outputPath := out["output_path"].(string)
			if outputPath == filepath.Join(root, "note.docx") {
				t.Fatalf("tool overwrote input: %#v", out)
			}
			if _, err := os.Stat(outputPath); err != nil {
				t.Fatalf("expected output file: %v", err)
			}
			read, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": outputPath}, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			content := read.Output.(map[string]any)["content"].(string)
			if !strings.Contains(content, tc.want) {
				t.Fatalf("edited docx missing %q: %q", tc.want, content)
			}
		})
	}
}

func TestDocxParagraphToolRejectsOutOfRangeParagraph(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "Only paragraph")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "docx.delete_paragraph", map[string]any{
		"path":            "note.docx",
		"source_sha256":   docxSourceSHA256ForTest(t, root, "note.docx"),
		"paragraph_index": 99,
		"old_text":        "Only paragraph",
		"source_hash":     sourceHash("Only paragraph"),
		"output_path":     "outputs/deleted.docx",
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeTargetNotFound) {
		t.Fatalf("expected typed paragraph target error, got %v", err)
	}
}

func writeDocxFixture(t *testing.T, root, name, text string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from docx import Document
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
text = __import__("sys").argv[3]
doc = Document()
for part in text.split("\n"):
    doc.add_paragraph(part)
doc.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name, text)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create docx fixture: %v\n%s", err, out)
	}
}
