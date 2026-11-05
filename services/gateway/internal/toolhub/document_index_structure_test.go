package toolhub

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestTextStructurePreservesBlankLinesUTF8CRLFAndModelIndependentIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.md")
	text := "#  Heading  \r\n\r\n　ＡＢＣ\tline\r\n" + strings.Repeat("🙂", 20000) + "\r\n\r\n  tail  "
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := configtest.MustLoadDefault()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	hub := New(cfg, fileStore)
	defer hub.Close()
	index, _, err := hub.ensureDocumentIndex(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	var original strings.Builder
	for _, node := range index.Nodes {
		original.WriteString(node.Text)
	}
	if original.String() != text {
		t.Fatal("text adapter changed whitespace, UTF-8 boundary or Markdown source")
	}
	cfg.Model.Fast.ContextTokens = 8192
	other := New(cfg, fileStore)
	defer other.Close()
	second, _, err := other.ensureDocumentIndex(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if second.Manifest.StructureGeneration != index.Manifest.StructureGeneration || second.Nodes[0].ID != index.Nodes[0].ID {
		t.Fatal("model capacity changed canonical identity")
	}
	result, err := hub.documentReadRange(t.Context(), map[string]any{"path": path, "all": true, "max_bytes": 1800, "source_sha256": index.Manifest.SourceSHA256, "structure_generation": index.Manifest.StructureGeneration}, "session", "run")
	if err != nil || result.Output.(map[string]any)["next_cursor"] == "" {
		t.Fatalf("bounded uniform read unavailable %v", err)
	}
}

func TestOfficeStructureRetainsNativeWhitespaceDuplicateCellsAndFormulaTypes(t *testing.T) {
	root := t.TempDir()
	docxPath := filepath.Join(root, "source.docx")
	python := `from docx import Document
import sys
doc=Document()
doc.add_paragraph('  Ａ\tB\n  C  ')
t=doc.add_table(rows=2,cols=3)
t.cell(0,0).text='Revenue (USD)'
t.cell(0,1).text='same'
t.cell(0,2).text='same'
t.cell(1,0).text='12'
t.cell(1,1).merge(t.cell(1,2)).text='merged'
doc.save(sys.argv[1])`
	command := exec.CommandContext(t.Context(), documentPythonBinary(), "-c", python, docxPath)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture %v %s", err, raw)
	}
	units := []docindex.Unit{}
	_, err := documentIndexExtractor{}.Extract(t.Context(), docxPath, "docx", func(unit docindex.Unit) error { units = append(units, unit); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if units[0].Text != "  Ａ\tB\n  C  " || units[1].Text != "Revenue (USD)\tsame\tsame" || units[2].Values[2]["is_merge_alias"] != true {
		t.Fatalf("DOCX text or merged cell semantics changed: %+v", units)
	}
	xlsxPath := filepath.Join(root, "source.xlsx")
	javascript := `const ExcelJS=require('exceljs');(async()=>{const w=new ExcelJS.Workbook();const s=w.addWorksheet('Data',{state:'hidden'});s.addRow(['Name','Revenue (USD)','Note']);s.addRow(['  Ａ\tB  ',{formula:'SUM(B3:B4)',result:12},'x'.repeat(2000)]);s.getRow(2).hidden=true;s.getCell('B2').numFmt='0.00';s.getCell('B2').note='cached result';s.mergeCells('A3:A4');s.getCell('A3').value='merged';await w.xlsx.writeFile(process.argv[1]);})().catch(e=>{console.error(e);process.exit(1)});`
	command = exec.CommandContext(t.Context(), documentNodeBinary(), "-e", javascript, xlsxPath)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture %v %s", err, raw)
	}
	hub := newRetrievalTestHub(t, root)
	index, _, err := hub.ensureDocumentIndex(context.Background(), xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	var cellID string
	longFound := false
	for _, node := range index.Nodes {
		if len(node.Values) == 0 {
			continue
		}
		value := node.Values[0]
		if value["address"] == "B2" {
			cellID = node.ID
			if value["formula"] != "SUM(B3:B4)" || value["cached_result"] != float64(12) || value["row_hidden"] != true || value["sheet_state"] != "hidden" || value["formula_result_recalculated"] != false {
				t.Fatalf("typed formula lost: %+v", value)
			}
		}
		if value["address"] == "C2" {
			longFound = len(node.Text) == 2000
		}
	}
	if cellID == "" || !longFound {
		t.Fatal("typed cell or long source content was omitted")
	}
	read, err := index.ReadRange(docindex.RangeRequest{NodeIDs: []string{cellID}, MaxBytes: 8192})
	if err != nil {
		t.Fatal(err)
	}
	foundHeader := false
	for _, node := range read.Dependencies {
		if node.Text == "Revenue (USD)" {
			foundHeader = true
		}
	}
	if !foundHeader {
		t.Fatal("cell read omitted its contextual column label/unit")
	}
}

func TestOfficeStructureRejectsExpandedPackageBeforeParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compressed.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(entry, zeroIndexReader{}, indexMaxPackageEntryBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Size() > 1<<20 {
		t.Fatal("fixture did not demonstrate compressed expansion")
	}
	emitted := false
	_, err = documentIndexExtractor{}.Extract(t.Context(), path, "docx", func(docindex.Unit) error { emitted = true; return nil })
	var pipeline *document.PipelineError
	if emitted || !errors.As(err, &pipeline) || !strings.Contains(pipeline.Detail, "expanded package") {
		t.Fatalf("expanded package bypassed resource admission: %v", err)
	}
}

type zeroIndexReader struct{}

func (zeroIndexReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
