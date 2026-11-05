package toolhub

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// writeScannedPDF writes blank (text-less) pages with optional bookmarks.
func writeScannedPDF(t *testing.T, root, name string, pages int, outline [][2]any) {
	t.Helper()
	if outline == nil {
		outline = [][2]any{}
	}
	rawOutline, _ := json.Marshal(outline)
	script := `
import json, sys
from pathlib import Path
from pypdf import PdfWriter
writer = PdfWriter()
for _ in range(int(sys.argv[3])):
    writer.add_blank_page(width=300, height=400)
for title, page in json.loads(sys.argv[4]):
    writer.add_outline_item(title, page - 1)
with open(Path(sys.argv[1]) / sys.argv[2], "wb") as f:
    writer.write(f)
`
	if out, err := exec.Command(documentPythonBinary(), "-c", script, root, name, fmt.Sprint(pages), string(rawOutline)).CombinedOutput(); err != nil {
		t.Fatalf("create scanned pdf fixture: %v\n%s", err, out)
	}
}

func newScannedPDFHub(t *testing.T, root string, ocr bool) *ToolHub {
	t.Helper()
	cfg := configtest.MustLoadDefault()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())
	if ocr {
		hub = hub.WithDocumentOCRAdapter(fakeDocumentOCR{markdown: "TCP 慢开始算法让拥塞窗口指数增长，达到慢开始门限后转为拥塞避免。"})
	}
	t.Cleanup(func() { _ = hub.Close() })
	return hub
}

func indexManifestForTest(t *testing.T, hub *ToolHub, path string) docindex.Manifest {
	t.Helper()
	index, _, err := hub.ensureDocumentIndex(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return index.Manifest
}

func pageRange(first, last int) []int {
	pages := []int{}
	for page := first; page <= last; page++ {
		pages = append(pages, page)
	}
	return pages
}

func TestScannedPDFBeyondTheReadBudgetMovesToRetrievalAndRecognizesSelectedSections(t *testing.T) {
	root := t.TempDir()
	writeScannedPDF(t, root, "scan.pdf", 20, [][2]any{{"第1章 概述", 1}, {"第2章 运输层与拥塞控制", 11}})
	hub := newScannedPDFHub(t, root, true)

	read, err := hub.Execute(context.Background(), "pdf.extract_text", map[string]any{"path": "scan.pdf"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	if read.Output.(map[string]any)["retrieval_required"] != true {
		t.Fatalf("a scanned PDF beyond the OCR budget stayed a partial read: %#v", read.Output.(map[string]any)["coverage_status"])
	}
	manifest := indexManifestForTest(t, hub, root+"/scan.pdf")
	if !slices.Equal(manifest.OCRPages, pageRange(1, 8)) || !slices.Equal(manifest.UnindexedPages, pageRange(9, 20)) {
		t.Fatalf("the read's recognized pages did not seed the index: ocr=%v unindexed=%v", manifest.OCRPages, manifest.UnindexedPages)
	}

	search, err := hub.Execute(context.Background(), "document.search", map[string]any{"path": "scan.pdf", "keywords": []any{"拥塞控制"}}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	sections := search.Output.(map[string]any)["sections"].([]any)
	chapter := sections[0].(map[string]any)
	if chapter["title"] != "第2章 运输层与拥塞控制" || intArg(chapter, "page", 0) != 11 || intArg(chapter, "page_end", 0) != 20 {
		t.Fatalf("title search did not locate the scanned chapter: %#v", sections)
	}
	if got := len(indexManifestForTest(t, hub, root+"/scan.pdf").OCRPages); got != 8 {
		t.Fatalf("search on an outlined scan recognized pages: %d", got)
	}

	rangeRead, err := hub.Execute(context.Background(), "document.read_range", map[string]any{"path": "scan.pdf", "section": chapter["id"]}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := rangeRead.Output.(map[string]any)
	if !strings.Contains(output["content"].(string), "慢开始门限") || fmt.Sprint(output["unrecognized_pages"]) != "[19 20]" {
		t.Fatalf("section read did not recognize one batch and report the rest: unrecognized=%v content=%.120s", output["unrecognized_pages"], output["content"])
	}
	if got := indexManifestForTest(t, hub, root+"/scan.pdf").OCRPages; !slices.Equal(got, append(pageRange(1, 8), pageRange(11, 18)...)) {
		t.Fatalf("recognized pages after the section read = %v", got)
	}
}

func TestScannedPDFWithoutOutlineIsRecognizedWholeBeforeSearch(t *testing.T) {
	root := t.TempDir()
	writeScannedPDF(t, root, "notes.pdf", 12, nil)
	hub := newScannedPDFHub(t, root, true)
	if _, err := hub.Execute(context.Background(), "pdf.extract_text", map[string]any{"path": "notes.pdf"}, "session", "run"); err != nil {
		t.Fatal(err)
	}
	search, err := hub.Execute(context.Background(), "document.search", map[string]any{"path": "notes.pdf", "keywords": []any{"慢开始"}}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := search.Output.(map[string]any)
	if len(output["hits"].([]any)) == 0 || len(output["unrecognized_pages"].([]int)) != 0 {
		t.Fatalf("an outline-less scan within the upload limit was not recognized whole: %#v", output)
	}
}

func TestScannedSectionReadWithoutOCRNamesTheUnrecognizedPages(t *testing.T) {
	root := t.TempDir()
	writeScannedPDF(t, root, "scan.pdf", 12, [][2]any{{"第1章 概述", 1}, {"第2章 网络层", 7}})
	hub := newScannedPDFHub(t, root, false)
	read, err := hub.Execute(context.Background(), "pdf.extract_text", map[string]any{"path": "scan.pdf"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	if read.Output.(map[string]any)["retrieval_required"] == true {
		t.Fatal("without OCR a small scanned PDF has no budget-omitted pages to retrieve")
	}
	index, metadata, err := hub.ensureDocumentIndex(context.Background(), root+"/scan.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, unrecognized, err := hub.recognizeDocumentPages(context.Background(), metadata, index, pageRange(7, 12))
	if err != nil || !slices.Equal(unrecognized, pageRange(7, 12)) {
		t.Fatalf("pages without an OCR adapter were not reported unrecognized: %v %v", unrecognized, err)
	}
	result, err := hub.Execute(context.Background(), "document.read_range", map[string]any{"path": "scan.pdf", "section": index.Outline[1].ID}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["read_complete"] != false || fmt.Sprint(output["unrecognized_pages"]) != "[7 8 9 10 11 12]" {
		t.Fatalf("scanned-page gaps were hidden: %#v", output)
	}
}
