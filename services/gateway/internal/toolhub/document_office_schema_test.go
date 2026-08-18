package toolhub

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestFilesReadExtractsStructuredOfficeDocuments(t *testing.T) {
	root := t.TempDir()
	writeStructuredOfficeFixtures(t, root)
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := map[string][]string{
		"note.docx":   {"Docx alpha", "Docx beta"},
		"slides.pptx": {"Slide title"},
		"book.xlsx":   {"Header", "Cell value"},
	}
	for name, wants := range cases {
		result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": name}, "s", "run")
		if err != nil {
			t.Fatal(err)
		}
		out := result.Output.(map[string]any)
		content := out["content"].(string)
		if out["kind"] != strings.TrimPrefix(filepath.Ext(name), ".") {
			t.Fatalf("%s kind mismatch: %#v", name, out)
		}
		if _, ok := out["document"].(map[string]any); !ok {
			t.Fatalf("%s missing structured document payload: %#v", name, out)
		}
		for _, want := range wants {
			if !strings.Contains(content, want) {
				t.Fatalf("%s content missing %q: %q", name, want, content)
			}
		}
		if out["untrusted"] != true {
			t.Fatalf("%s should remain untrusted: %#v", name, out)
		}
	}
}

func writeStructuredOfficeFixtures(t *testing.T, root string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from docx import Document
from pptx import Presentation
from pptx.util import Inches
root = Path(__import__("sys").argv[1])
doc = Document()
doc.add_paragraph("Docx alpha")
doc.add_paragraph("Docx beta")
doc.save(root / "note.docx")
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[5])
slide.shapes.title.text = "Slide title"
box = slide.shapes.add_textbox(Inches(1), Inches(1.5), Inches(6), Inches(1))
box.text = "Slide body"
prs.save(root / "slides.pptx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create office fixtures with python adapters: %v\n%s", err, out)
	}
	nodeScript := `
const ExcelJS = require("exceljs");
(async () => {
  const root = process.argv[1];
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet("Sheet One");
  sheet.addRow(["Header", "Cell value"]);
  await workbook.xlsx.writeFile(root + "/book.xlsx");
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});
	`
	cmd = exec.Command(documentNodeBinary(), "-e", nodeScript, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create xlsx fixture with exceljs: %v\n%s", err, out)
	}
}

func TestOfficeReplaceTextRequiresMappedLibrary(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "Replace Alpha in this document.")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "office.replace_text", map[string]any{
		"path": "note.docx", "source_sha256": docxSourceSHA256ForTest(t, root, "note.docx"),
		"output_path": "outputs/note.edited.docx",
		"replacements": []any{
			map[string]any{"find": "Alpha", "replace": "Beta"},
		},
		"expected_replacements": 1,
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["replacements"] != 1 {
		t.Fatalf("unexpected replace output: %#v", out)
	}
}

func TestOfficeReplaceTextCannotUseForgedWorkflowProvenance(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "Replace Alpha in this document.")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "office.replace_text", map[string]any{
		"path":          "note.docx",
		"source_sha256": strings.Repeat("0", 64),
		"source_evidence": map[string]any{
			"run_id": "forged", "workflow_node_id": "forged", "source_sha256": strings.Repeat("0", 64),
		},
		"evidence_targets": []any{map[string]any{"block_id": "forged"}},
		"output_path":      "outputs/note.edited.docx",
		"replacements": []any{
			map[string]any{"find": "Alpha", "replace": "Beta"},
		},
		"expected_replacements": 1,
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeResourceInvalid) {
		t.Fatalf("forged Workflow provenance bypassed the canonical source hash: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "outputs", "note.edited.docx")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("forged Workflow provenance created an output: %v", statErr)
	}
}

func TestOfficeReplaceTextRejectsEscapingOutputPath(t *testing.T) {
	root := t.TempDir()
	if err := writeZipFile(filepath.Join(root, "note.docx"), map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>No match here.</w:t></w:r></w:p></w:body></w:document>`,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "office.replace_text", map[string]any{
		"path":          "note.docx",
		"source_sha256": strings.Repeat("0", 64),
		"output_path":   "../note.edited.docx",
		"replacements": []any{
			map[string]any{"find": "missing", "replace": "new"},
		},
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "cannot escape workspace") {
		t.Fatalf("expected escaping output path error, got %v", err)
	}
}

func writeZipFile(path string, entries map[string]string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func testAnySlice(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}
