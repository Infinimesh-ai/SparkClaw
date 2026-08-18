package toolhub

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestXlsxStructureToolsWriteNewVersions(t *testing.T) {
	root := t.TempDir()
	writeXlsxFixture(t, root, "book.xlsx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := []struct {
		tool     string
		args     map[string]any
		contains []string
		wantRow  int
	}{
		{
			tool: "xlsx.update_cell",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"cell":        "B2",
				"value":       99,
				"output_path": "outputs/cell.xlsx",
			},
			contains: []string{"99"},
		},
		{
			tool: "xlsx.insert_row",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"row":         2,
				"position":    "after",
				"values":      []any{"Inserted", 77, "New"},
				"output_path": "outputs/inserted.xlsx",
			},
			contains: []string{"Inserted", "77", "New"},
		},
		{
			tool: "xlsx.delete_row",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"row":         2,
				"output_path": "outputs/deleted.xlsx",
			},
			contains: []string{"Bob", "92"},
		},
		{
			tool: "xlsx.update_row",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"row":         2,
				"values":      []any{"Updated", 66, "Changed"},
				"output_path": "outputs/row.xlsx",
			},
			contains: []string{"Updated", "66", "Changed"},
		},
		{
			tool: "xlsx.append_row",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"values":      []any{"Appended", 55, "Done"},
				"output_path": "outputs/appended.xlsx",
			},
			contains: []string{"Appended", "55", "Done"},
			wantRow:  4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			evidence := executeDocumentRead(t, hub, "book.xlsx")
			operation := strings.TrimPrefix(tc.tool, "xlsx.")
			bound := xlsxBoundTestArgs(t, evidence, stringArg(tc.args, "sheet", ""), operation, intArg(tc.args, "row", 0), stringArg(tc.args, "cell", ""))
			for key, value := range bound {
				tc.args[key] = value
			}
			result, err := hub.Execute(context.Background(), tc.tool, tc.args, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			if tc.wantRow > 0 && intArg(out, "row", 0) != tc.wantRow {
				t.Fatalf("appended XLSX row ignored the structured content boundary: got=%#v want=%d", out["row"], tc.wantRow)
			}
			outputPath := out["output_path"].(string)
			if outputPath == filepath.Join(root, "book.xlsx") {
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
			for _, want := range tc.contains {
				if !strings.Contains(content, want) {
					t.Fatalf("edited xlsx missing %q: %q", want, content)
				}
			}
		})
	}
}

func TestXlsxStructureToolRejectsMissingSheet(t *testing.T) {
	root := t.TempDir()
	writeXlsxFixture(t, root, "book.xlsx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	read := executeDocumentRead(t, hub, "book.xlsx")
	metadata := read["document"].(map[string]any)["metadata"].(map[string]any)
	_, err := hub.Execute(context.Background(), "xlsx.update_cell", map[string]any{
		"path":             "book.xlsx",
		"source_sha256":    metadata["sha256"],
		"sheet":            "Missing",
		"cell":             "A1",
		"source_cell_hash": "sha256:unresolved",
		"value":            "x",
		"output_path":      "outputs/missing.xlsx",
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeTargetNotFound) {
		t.Fatalf("expected typed missing-sheet target error, got %v", err)
	}
}

func TestXlsxStructureToolRejectsInvalidCell(t *testing.T) {
	root := t.TempDir()
	writeXlsxFixture(t, root, "book.xlsx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	read := executeDocumentRead(t, hub, "book.xlsx")
	metadata := read["document"].(map[string]any)["metadata"].(map[string]any)
	_, err := hub.Execute(context.Background(), "xlsx.update_cell", map[string]any{
		"path":             "book.xlsx",
		"source_sha256":    metadata["sha256"],
		"sheet":            "Sheet1",
		"cell":             "bad",
		"source_cell_hash": "sha256:unresolved",
		"value":            "x",
		"output_path":      "outputs/bad.xlsx",
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeResourceInvalid) {
		t.Fatalf("expected invalid cell to fail trusted evidence validation, got %v", err)
	}
}

func writeXlsxFixture(t *testing.T, root, name string) {
	t.Helper()
	nodeScript := `
const ExcelJS = require("exceljs");
(async () => {
  const root = process.argv[1];
  const name = process.argv[2];
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet("Sheet1");
  sheet.addRow(["Name", "Score", "Status"]);
  sheet.addRow(["Alice", 88, "Ready"]);
  sheet.addRow(["Bob", 92, "Done"]);
  sheet.getCell("B10").font = { italic: true };
  await workbook.xlsx.writeFile(root + "/" + name);
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});
	`
	cmd := exec.Command(documentNodeBinary(), "-e", nodeScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create xlsx fixture: %v\n%s", err, out)
	}
}
