package toolhub

import (
	"bytes"
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

func TestPDFTransformToolsWriteNewVersions(t *testing.T) {
	root := t.TempDir()
	writePDFReadFixture(t, root, "first.pdf", "native", 3)
	original, err := os.ReadFile(filepath.Join(root, "first.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := []struct {
		name      string
		args      map[string]any
		wantPages int
	}{
		{
			name: "extract_pages",
			args: map[string]any{
				"path":        "first.pdf",
				"operation":   "extract_pages",
				"pages":       []any{1, 3},
				"output_path": "outputs/extracted.pdf",
			},
			wantPages: 2,
		},
		{
			name: "delete_pages",
			args: map[string]any{
				"path":        "first.pdf",
				"operation":   "delete_pages",
				"pages":       []any{2},
				"output_path": "outputs/deleted.pdf",
			},
			wantPages: 2,
		},
		{
			name: "rotate_pages",
			args: map[string]any{
				"path":        "first.pdf",
				"operation":   "rotate_pages",
				"pages":       []any{1},
				"rotation":    90,
				"output_path": "outputs/rotated.pdf",
			},
			wantPages: 3,
		},
		{
			name: "split",
			args: map[string]any{
				"path":        "first.pdf",
				"operation":   "split",
				"output_path": "outputs/split.pdf",
			},
			wantPages: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := hub.Execute(context.Background(), "pdf.transform", tc.args, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			if out["pages"] != tc.wantPages {
				t.Fatalf("unexpected pages for %s: %#v", tc.name, out)
			}
			changeSummary := out["change_summary"].(map[string]any)
			if changeSummary["original_unchanged"] != true {
				t.Fatalf("transform did not report an unchanged original: %#v", changeSummary)
			}
			if tc.name == "split" {
				outputs := out["outputs"].([]string)
				if len(outputs) != tc.wantPages {
					t.Fatalf("split should return one output per page: %#v", out)
				}
				for _, path := range outputs {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("split output missing: %v", err)
					}
					read := executePDFReadFixture(t, hub, path)
					if read["read_complete"] != true || len(documentAnySlice(read["document"].(map[string]any)["pages"])) != 1 {
						t.Fatalf("split output did not re-read completely: %#v", read)
					}
				}
				if out["output_path"] != outputs[0] {
					t.Fatalf("split primary output must name an existing typed resource: %#v", out)
				}
				return
			}
			outputPath := out["output_path"].(string)
			if _, err := os.Stat(outputPath); err != nil {
				t.Fatalf("expected output file: %v", err)
			}
			read := executePDFReadFixture(t, hub, outputPath)
			pages := documentAnySlice(read["document"].(map[string]any)["pages"])
			if read["read_complete"] != true || len(pages) != tc.wantPages {
				t.Fatalf("transform output did not re-read completely: %#v", read)
			}
			if tc.name == "extract_pages" && (!strings.Contains(stringArg(read, "content", ""), "page 1") || !strings.Contains(stringArg(read, "content", ""), "page 3") || strings.Contains(stringArg(read, "content", ""), "page 2")) {
				t.Fatalf("extracted pages lost source order or selected the wrong page: %#v", read)
			}
			if tc.name == "delete_pages" && strings.Contains(stringArg(read, "content", ""), "page 2") {
				t.Fatalf("deleted page remained in output: %#v", read)
			}
			if tc.name == "rotate_pages" && intArg(pages[0].(map[string]any), "rotation", 0) != 90 {
				t.Fatalf("rotated page did not retain the requested angle: %#v", pages[0])
			}
		})
	}
	current, err := os.ReadFile(filepath.Join(root, "first.pdf"))
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("PDF transforms modified the original: %v", err)
	}
}

func TestPDFTransformRejectsInvalidOperationContracts(t *testing.T) {
	root := t.TempDir()
	writePDFBlankFixture(t, root, "first.pdf", 3)
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())
	base := map[string]any{"path": "first.pdf", "operation": "extract_pages", "pages": []any{1}, "output_path": "outputs/result.pdf"}

	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "merge removed", mutate: func(args map[string]any) { args["operation"] = "merge" }, want: "must be one of"},
		{name: "duplicate page", mutate: func(args map[string]any) { args["pages"] = []any{1, 1} }, want: "duplicate page 1"},
		{name: "zero page", mutate: func(args map[string]any) { args["pages"] = []any{0} }, want: "must be >= 1"},
		{name: "fractional page", mutate: func(args map[string]any) { args["pages"] = []any{1.5} }, want: "must be integer"},
		{name: "unrelated rotation", mutate: func(args map[string]any) { args["rotation"] = 90 }, want: "does not accept rotation"},
		{name: "missing rotation", mutate: func(args map[string]any) { args["operation"] = "rotate_pages" }, want: "rotation must be one of"},
		{name: "invalid rotation", mutate: func(args map[string]any) { args["operation"], args["rotation"] = "rotate_pages", 360 }, want: "must be one of"},
		{name: "split pages", mutate: func(args map[string]any) { args["operation"] = "split" }, want: "does not accept pages"},
		{name: "inputs removed", mutate: func(args map[string]any) { args["inputs"] = []any{"other.pdf"} }, want: "is not allowed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := map[string]any{}
			for key, value := range base {
				args[key] = value
			}
			test.mutate(args)
			if err := hub.Validate("pdf.transform", args); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid PDF transform contract was accepted: %v", err)
			}
		})
	}
}

func TestPDFTransformRejectsOutOfRangePage(t *testing.T) {
	root := t.TempDir()
	writePDFBlankFixture(t, root, "first.pdf", 1)
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "pdf.transform", map[string]any{
		"path":        "first.pdf",
		"operation":   "extract_pages",
		"pages":       []any{2},
		"output_path": "outputs/bad.pdf",
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeTargetNotFound) {
		t.Fatalf("expected typed page target error, got %v", err)
	}
}

func writePDFBlankFixture(t *testing.T, root, name string, pages int) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pypdf import PdfWriter
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
pages = int(__import__("sys").argv[3])
writer = PdfWriter()
for _ in range(pages):
    writer.add_blank_page(width=200, height=200)
with open(root / name, "wb") as f:
    writer.write(f)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name, fmt.Sprint(pages))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create pdf fixture: %v\n%s", err, out)
	}
}
