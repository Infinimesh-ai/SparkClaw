package toolhub

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type pdfFixtureOCR struct {
	markdown string
	err      error
}

func (pdfFixtureOCR) Enabled() bool { return true }

func (adapter pdfFixtureOCR) Parse(_ context.Context, request documentocr.Request) (documentocr.Result, error) {
	if adapter.err != nil {
		return documentocr.Result{}, adapter.err
	}
	if len(request.Content) == 0 || request.ContentType != "image/jpeg" {
		return documentocr.Result{}, errors.New("fixture OCR received invalid rendered page")
	}
	return documentocr.Result{Markdown: adapter.markdown, Model: "fixture-ocr", InferenceMS: 3}, nil
}

func (pdfFixtureOCR) Close() error { return nil }

func TestPDFReadCoverageForNativePDF(t *testing.T) {
	root := t.TempDir()
	writePDFReadFixture(t, root, "native.pdf", "native", 2)
	hub := newPDFReadFixtureHub(root).WithDocumentOCRAdapter(pdfFixtureOCR{markdown: "must not be used"})

	output := executePDFReadFixture(t, hub, "native.pdf")
	if output["read_complete"] != true || output["coverage_status"] != "complete" || len(documentAnySlice(output["missing_page_indexes"])) != 0 {
		t.Fatalf("native PDF coverage is not complete: %#v", output)
	}
	documentValue := output["document"].(map[string]any)
	for _, raw := range documentAnySlice(documentValue["pages"]) {
		page := raw.(map[string]any)
		quality := page["native_text_quality"].(map[string]any)
		if page["text_status"] != "native" || page["text_source"] != "native" || quality["classification"] != "usable" || quality["version"] != "pdf_native_text_quality_v1" {
			t.Fatalf("native page classification is unstable: %#v", page)
		}
	}
}

func TestPDFReadCoverageForScannedAndMixedPDF(t *testing.T) {
	for _, test := range []struct {
		name       string
		kind       string
		wantSource string
		wantBlocks int
	}{
		{name: "scanned", kind: "scanned", wantSource: "ocr", wantBlocks: 1},
		{name: "mixed", kind: "mixed", wantSource: "native+ocr", wantBlocks: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writePDFReadFixture(t, root, test.name+".pdf", test.kind, 1)
			hub := newPDFReadFixtureHub(root).WithDocumentOCRAdapter(pdfFixtureOCR{markdown: "# OCR evidence\n\nRecognized total: 42"})

			output := executePDFReadFixture(t, hub, test.name+".pdf")
			if output["read_complete"] != true || output["coverage_status"] != "complete" || !strings.Contains(stringArg(output, "content", ""), "Recognized total: 42") {
				t.Fatalf("OCR PDF coverage is not complete: %#v", output)
			}
			documentValue := output["document"].(map[string]any)
			page := documentAnySlice(documentValue["pages"])[0].(map[string]any)
			if page["text_status"] != "ocr_succeeded" || page["text_source"] != test.wantSource || page["ocr_preprocessing_version"] != "pdf_page_render_v1" || page["ocr_provenance_ref"] == "" {
				t.Fatalf("OCR page provenance is incomplete: %#v", page)
			}
			blocks := documentAnySlice(documentValue["blocks"])
			if len(blocks) != test.wantBlocks {
				t.Fatalf("unexpected native/OCR block count: %#v", blocks)
			}
			if test.kind == "mixed" && (!strings.Contains(stringArg(page, "native_text", ""), "Header") || !strings.Contains(stringArg(page, "ocr_text", ""), "OCR evidence")) {
				t.Fatalf("mixed page did not retain both evidence sources: %#v", page)
			}
		})
	}
}

func TestPDFReadCoverageReportsOCRDisabledAndFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		adapter    documentocr.Adapter
		wantStatus string
	}{
		{name: "disabled", wantStatus: "ocr_disabled"},
		{name: "failed", adapter: pdfFixtureOCR{err: errors.New("fixture OCR failure")}, wantStatus: "ocr_failed"},
		{name: "trivial", adapter: pdfFixtureOCR{markdown: " "}, wantStatus: "ocr_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writePDFReadFixture(t, root, "scanned.pdf", "scanned", 1)
			hub := newPDFReadFixtureHub(root)
			if test.adapter != nil {
				hub = hub.WithDocumentOCRAdapter(test.adapter)
			}

			output := executePDFReadFixture(t, hub, "scanned.pdf")
			page := documentAnySlice(output["document"].(map[string]any)["pages"])[0].(map[string]any)
			if output["read_complete"] != false || output["coverage_status"] != "unavailable" || page["text_status"] != test.wantStatus || intArg(output["page_status_counts"].(map[string]any), test.wantStatus, 0) != 1 {
				t.Fatalf("OCR unavailability was not explicit: %#v", output)
			}
		})
	}
}

func TestPDFReadCoverageReportsPageBudgetOmission(t *testing.T) {
	root := t.TempDir()
	writePDFReadFixture(t, root, "nine-pages.pdf", "scanned", 9)
	hub := newPDFReadFixtureHub(root).WithDocumentOCRAdapter(pdfFixtureOCR{markdown: "Recognized fixture page"})

	output := executePDFReadFixture(t, hub, "nine-pages.pdf")
	missing := documentAnySlice(output["missing_page_indexes"])
	counts := output["page_status_counts"].(map[string]any)
	pages := documentAnySlice(output["document"].(map[string]any)["pages"])
	if output["read_complete"] != false || output["coverage_status"] != "partial" || len(missing) != 1 || intArg(map[string]any{"value": missing[0]}, "value", 0) != 9 || intArg(counts, "ocr_succeeded", 0) != 8 || intArg(counts, "budget_omitted", 0) != 1 {
		t.Fatalf("OCR page budget coverage is incorrect: %#v", output)
	}
	if pages[8].(map[string]any)["text_status"] != "budget_omitted" {
		t.Fatalf("ninth page was not marked as budget omitted: %#v", pages[8])
	}
}

func newPDFReadFixtureHub(root string) *ToolHub {
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	return New(cfg, store.NewMemoryStore())
}

func executePDFReadFixture(t *testing.T, hub *ToolHub, path string) map[string]any {
	t.Helper()
	result, err := hub.Execute(context.Background(), "pdf.extract_text", map[string]any{"path": path}, "fixture-session", "fixture-run")
	if err != nil {
		t.Fatal(err)
	}
	return result.Output.(map[string]any)
}

func writePDFReadFixture(t *testing.T, root, name, kind string, pages int) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from PIL import Image, ImageDraw
from pypdf import PdfReader, PdfWriter
from pypdf.generic import DecodedStreamObject, DictionaryObject, NameObject
import sys

root, name, kind, page_count = Path(sys.argv[1]), sys.argv[2], sys.argv[3], int(sys.argv[4])
path = root / name

def add_text_layer(page, writer, text):
    resources = page.get("/Resources")
    if resources is None:
        resources = DictionaryObject()
        page[NameObject("/Resources")] = resources
    elif hasattr(resources, "get_object"):
        resources = resources.get_object()
    font = DictionaryObject({
        NameObject("/Type"): NameObject("/Font"),
        NameObject("/Subtype"): NameObject("/Type1"),
        NameObject("/BaseFont"): NameObject("/Helvetica"),
    })
    fonts = resources.get("/Font")
    if fonts is None:
        fonts = DictionaryObject()
        resources[NameObject("/Font")] = fonts
    elif hasattr(fonts, "get_object"):
        fonts = fonts.get_object()
    fonts[NameObject("/F1")] = writer._add_object(font)
    stream = DecodedStreamObject()
    stream.set_data(("BT /F1 12 Tf 36 160 Td (" + text + ") Tj ET").encode("ascii"))
    page.replace_contents(stream)

if kind == "native":
    writer = PdfWriter()
    for index in range(1, page_count + 1):
        page = writer.add_blank_page(width=240, height=200)
        add_text_layer(page, writer, "Normal native PDF page %d contains enough deterministic searchable text for complete extraction" % index)
    with open(path, "wb") as output:
        writer.write(output)
else:
    images = []
    for index in range(1, page_count + 1):
        image = Image.new("RGB", (480, 360), "white")
        draw = ImageDraw.Draw(image)
        draw.rectangle((20, 20, 460, 340), outline="black", width=3)
        draw.text((50, 150), "Scanned fixture page %d" % index, fill="black")
        images.append(image)
    images[0].save(path, "PDF", save_all=True, append_images=images[1:], resolution=144.0)
    if kind == "mixed":
        reader = PdfReader(path)
        writer = PdfWriter()
        for page in reader.pages:
            writer.add_page(page)
        add_text_layer(writer.pages[0], writer, "Header 1")
        with open(path, "wb") as output:
            writer.write(output)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name, kind, strconv.Itoa(pages))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create PDF read fixture: %v\n%s", err, output)
	}
}
