package toolhub

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type fakeDocumentOCR struct {
	markdown string
}

func (fakeDocumentOCR) Enabled() bool { return true }

func (f fakeDocumentOCR) Parse(_ context.Context, input documentocr.Request) (documentocr.Result, error) {
	if len(input.Content) == 0 || !strings.HasPrefix(input.ContentType, "image/") {
		return documentocr.Result{}, errors.New("invalid fake OCR input")
	}
	return documentocr.Result{Markdown: f.markdown, Model: "ATH-MaaS/OvisOCR2", InferenceMS: 7}, nil
}

func (fakeDocumentOCR) Close() error { return nil }

func TestFilesReadRegistersEmbeddedImagesAndUsesFastSemantics(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "evidence.png")
	writeEmbeddedImageDocumentFixtures(t, root, imagePath)
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	for _, name := range []string{"image.docx", "image.xlsx", "image.pptx"} {
		t.Run(name, func(t *testing.T) {
			result, err := hub.Execute(context.Background(), "files.read", map[string]any{
				"path": name, "image_analysis": "all", "image_question": "Summarize the visual evidence.",
			}, "session", "run")
			if err != nil {
				t.Fatal(err)
			}
			output := result.Output.(map[string]any)
			documentValue := output["document"].(map[string]any)
			if documentValue["representation_version"] != "structured_document_v1" {
				t.Fatalf("unexpected representation: %#v", documentValue)
			}
			enrichment := documentValue["enrichment"].(map[string]any)
			if enrichment["schema_version"] != "document_enrichment_v1" {
				t.Fatalf("unexpected enrichment schema: %#v", enrichment)
			}
			assets := enrichment["assets"].(map[string]any)
			images := documentAnySlice(assets["images"])
			if len(images) != 1 {
				t.Fatalf("expected one registered image, got %#v", images)
			}
			image := images[0].(map[string]any)
			if len(stringArg(image, "sha256", "")) != 64 || !strings.HasPrefix(stringArg(image, "artifact_ref", ""), "artifact://") {
				t.Fatalf("image identity or artifact is missing: %#v", image)
			}
			if _, leaked := image["resource_key"]; leaked {
				t.Fatalf("internal resource key leaked into structured output: %#v", image)
			}
			semantic := image["semantic"].(map[string]any)
			if semantic["status"] != "succeeded" || semantic["model_lane"] != "fast" || semantic["untrusted"] != true {
				t.Fatalf("image semantic did not use the Fast evidence contract: %#v", semantic)
			}
			pipeline := documentValue["pipeline"].(map[string]any)
			bundle := pipeline["context_bundle"].(map[string]any)
			if !hasContextCategory(documentAnySlice(bundle["context_segments"]), "image_semantic") {
				t.Fatalf("bounded image semantic context was not assembled: %#v", bundle)
			}
		})
	}
}

func TestFilesReadTargetedImageModeSkipsUnrelatedImageButStoresArtifact(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "evidence.png")
	writeEmbeddedImageDocumentFixtures(t, root, imagePath)
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "files.read", map[string]any{
		"path": "image.docx", "image_analysis": "targeted", "image_target_paths": []any{"document.p[99]"},
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	documentValue := result.Output.(map[string]any)["document"].(map[string]any)
	enrichment := documentValue["enrichment"].(map[string]any)
	assets := enrichment["assets"].(map[string]any)
	image := documentAnySlice(assets["images"])[0].(map[string]any)
	semantic := image["semantic"].(map[string]any)
	if semantic["status"] != "skipped" || semantic["model_lane"] != "fast" || !strings.HasPrefix(stringArg(image, "artifact_ref", ""), "artifact://") {
		t.Fatalf("unrelated image should be stored but not analyzed: %#v", image)
	}
}

func TestFilesReadBlocksWhenRequiredImageEvidenceIsUnavailable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("text only"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := configtest.MustLoadDefault()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "files.read", map[string]any{
		"path": "note.txt", "image_analysis": "all", "image_required": true,
	}, "session", "run")
	if err == nil || !strings.Contains(err.Error(), "required image evidence") {
		t.Fatalf("missing required image evidence did not block the read: %v", err)
	}
}

func TestFilesReadPPTXCapturesEmptyShapesCapacityAndCompanions(t *testing.T) {
	root := t.TempDir()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
from pptx.dml.color import RGBColor
from pptx.enum.shapes import MSO_AUTO_SHAPE_TYPE
from pptx.util import Inches, Pt
root = Path(__import__("sys").argv[1])
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[6])
band = slide.shapes.add_shape(MSO_AUTO_SHAPE_TYPE.RECTANGLE, Inches(1.5), Inches(2), Inches(4.5), Inches(.6))
band.fill.solid()
band.fill.fore_color.rgb = RGBColor(22, 101, 52)
band.line.fill.background()
label = slide.shapes.add_textbox(Inches(1.7), Inches(2.08), Inches(1.2), Inches(.35))
label.text_frame.paragraphs[0].add_run().text = "定位"
label.text_frame.paragraphs[0].runs[0].font.size = Pt(16.5)
body = slide.shapes.add_textbox(Inches(3.2), Inches(2.08), Inches(6), Inches(.35))
body.text_frame.paragraphs[0].add_run().text = "读取完整结构后定位目标内容"
body.text_frame.paragraphs[0].runs[0].font.size = Pt(16.5)
marker = slide.shapes.add_textbox(Inches(8.5), Inches(6.8), Inches(1), Inches(.3))
marker.text_frame.paragraphs[0].add_run().text = "课程 · 2/3"
prs.save(root / "layout.pptx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create PPTX layout fixture: %v\n%s", err, output)
	}
	cfg := configtest.MustLoadDefault()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "layout.pptx"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	structured := result.Output.(map[string]any)["document"].(map[string]any)
	enrichment := structured["enrichment"].(map[string]any)
	layout := enrichment["layout"].(map[string]any)
	shapes := documentAnySlice(layout["shapes"])
	if len(shapes) != 4 {
		t.Fatalf("expected every PPTX shape including the empty band, got %#v", shapes)
	}
	band := shapes[0].(map[string]any)
	if stringArg(band, "text", "missing") != "" || stringArg(band["fill"].(map[string]any), "color", "") != "#166534" || stringArg(band, "companion_role", "") != "background" {
		t.Fatalf("empty background shape was not structurally retained: %#v", band)
	}
	body := shapes[2].(map[string]any)
	style := body["text_style"].(map[string]any)
	if stringArg(body, "companion_role", "") != "body" || intArg(style, "single_line_capacity_visual_units", 0) <= 0 {
		t.Fatalf("body capacity or companion role is missing: %#v", body)
	}
	groups := documentAnySlice(layout["companion_groups"])
	if len(groups) != 1 || intArg(groups[0].(map[string]any), "background_shape_index", 0) != 1 || intArg(groups[0].(map[string]any), "body_shape_index", 0) != 3 {
		t.Fatalf("expected a high-confidence label/body band group: %#v", groups)
	}
	markers := documentAnySlice(layout["page_markers"])
	warnings := documentAnySlice(enrichment["warnings"])
	if len(markers) != 1 || len(warnings) == 0 {
		t.Fatalf("page marker mismatch was not exposed as evidence: markers=%#v warnings=%#v", markers, warnings)
	}
}

func TestPDFExtractTextReportsScannedContentAsUnsupported(t *testing.T) {
	root := t.TempDir()
	writePDFBlankFixture(t, root, "blank.pdf", 1)
	cfg := configtest.MustLoadDefault()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "pdf.extract_text", map[string]any{"path": "blank.pdf"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["scanned_unsupported"] != true || output["content"] != "" {
		t.Fatalf("blank/scanned PDF boundary was not explicit: %#v", output)
	}
}

func TestPDFExtractTextUsesOvisOCR2ForScannedPage(t *testing.T) {
	root := t.TempDir()
	writePDFBlankFixture(t, root, "scanned.pdf", 1)
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore()).WithDocumentOCRAdapter(fakeDocumentOCR{markdown: "# Invoice\n\nTotal: 42"})

	result, err := hub.Execute(context.Background(), "pdf.extract_text", map[string]any{"path": "scanned.pdf"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["scanned_unsupported"] != false || !strings.Contains(stringArg(output, "content", ""), "Total: 42") {
		t.Fatalf("scanned PDF OCR was not promoted to document content: %#v", output)
	}
	documentValue := output["document"].(map[string]any)
	stats := documentValue["stats"].(map[string]any)
	pages := documentAnySlice(documentValue["pages"])
	blocks := documentAnySlice(documentValue["blocks"])
	if intArg(stats, "ocr_pages", 0) != 1 || stats["complete"] != true || len(pages) != 1 || len(blocks) != 1 || !strings.Contains(stringArg(pages[0].(map[string]any), "text", ""), "Invoice") || !strings.Contains(stringArg(blocks[0].(map[string]any), "text", ""), "Total") {
		t.Fatalf("OCR page evidence did not retain stable PDF structure: %#v", documentValue)
	}
}

func TestPDFExtractTextKeepsOCRContentWithinRequestedLimit(t *testing.T) {
	root := t.TempDir()
	writePDFBlankFixture(t, root, "scanned.pdf", 1)
	cfg := configtest.MustLoadDefault()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore()).WithDocumentOCRAdapter(fakeDocumentOCR{markdown: strings.Repeat("recognized text ", 20)})

	_, err := hub.Execute(context.Background(), "pdf.extract_text", map[string]any{"path": "scanned.pdf", "max_bytes": 32}, "session", "run")
	if !document.IsErrorCode(err, document.CodeStrategyDeferred) {
		t.Fatalf("oversized OCR content did not preserve the document read limit: %v", err)
	}
}

func hasContextCategory(values []any, category string) bool {
	for _, value := range values {
		item, ok := documentAnyMap(value)
		if ok && stringArg(item, "category", "") == category {
			return true
		}
	}
	return false
}

func writeEmbeddedImageDocumentFixtures(t *testing.T, root, imagePath string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from docx import Document
from docx.shared import Inches
from PIL import Image, ImageDraw
from pptx import Presentation
root = Path(__import__("sys").argv[1])
image = __import__("sys").argv[2]
canvas = Image.new("RGB", (320, 180), "white")
draw = ImageDraw.Draw(canvas)
draw.rectangle((20, 20, 300, 160), outline="black", width=3)
draw.text((50, 75), "Visual evidence", fill="black")
canvas.save(image, format="PNG")
doc = Document()
paragraph = doc.add_paragraph("Visual evidence")
paragraph.add_run().add_picture(image, width=Inches(2))
doc.save(root / "image.docx")
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[6])
slide.shapes.add_picture(image, Inches(1), Inches(1), width=Inches(3))
prs.save(root / "image.pptx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, imagePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create Office image fixtures: %v\n%s", err, output)
	}
	nodeScript := `
const ExcelJS = require("exceljs");
(async () => {
  const root = process.argv[1];
  const image = process.argv[2];
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet("Data");
  sheet.addRow(["Visual evidence"]);
  const imageId = workbook.addImage({ filename: image, extension: "png" });
  sheet.addImage(imageId, "A2:D8");
  await workbook.xlsx.writeFile(root + "/image.xlsx");
})().catch(error => { console.error(error && error.stack || error); process.exit(1); });
	`
	cmd = exec.Command(documentNodeBinary(), "-e", nodeScript, root, imagePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create XLSX image fixture: %v\n%s", err, output)
	}
}
