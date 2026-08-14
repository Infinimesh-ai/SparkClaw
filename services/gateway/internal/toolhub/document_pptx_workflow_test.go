package toolhub

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestPPTXReaderExposesRichTextLayoutAndReadOnlyGroupEvidence(t *testing.T) {
	root := t.TempDir()
	writeRichPPTXWorkflowFixture(t, root, "rich-deck.pptx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())

	first := executeDocumentRead(t, hub, "rich-deck.pptx")
	second := executeDocumentRead(t, hub, "rich-deck.pptx")
	documentMap := first["document"].(map[string]any)
	if len(testAnySlice(documentMap["slides"])) != 10 {
		t.Fatalf("rich PPTX slide inventory is incomplete: %#v", documentMap["stats"])
	}
	rich := pptxTestBlock(t, documentMap, 1, 1, 0)
	structure := rich["format_metadata"].(map[string]any)["text_structure"].(map[string]any)
	paragraphs := testAnySlice(structure["paragraphs"])
	if structure["editable"] != true || len(paragraphs) != 2 {
		t.Fatalf("rich paragraph evidence is incomplete: %#v", structure)
	}
	firstParagraph := paragraphs[0].(map[string]any)
	runs := testAnySlice(firstParagraph["runs"])
	firstStyle := runs[0].(map[string]any)["style"].(map[string]any)
	linkStyle := runs[1].(map[string]any)["style"].(map[string]any)
	if firstParagraph["bullet"].(map[string]any)["state"] != "character" || firstStyle["bold"] != true ||
		linkStyle["italic"] != true || linkStyle["hyperlink"] != "https://example.com/details" ||
		intArg(paragraphs[1].(map[string]any), "level", -1) != 1 {
		t.Fatalf("rich run, bullet, hyperlink, or level evidence is incomplete: %#v", structure)
	}
	grouped := pptxTestBlock(t, documentMap, 1, 3, 1)
	if grouped["format_metadata"].(map[string]any)["editable"] != false {
		t.Fatalf("group-child text was exposed as editable: %#v", grouped)
	}
	late := pptxTestBlock(t, documentMap, 10, 1, 0)
	if late["text"] != "Late target" {
		t.Fatalf("late target slide was not retained: %#v", late)
	}
	firstLayout := pptxLayoutInventory(documentMap)
	secondLayout := pptxLayoutInventory(second["document"].(map[string]any))
	if len(firstLayout) == 0 || !reflect.DeepEqual(firstLayout, secondLayout) {
		t.Fatalf("layout inventory refs are absent or unstable: first=%#v second=%#v", firstLayout, secondLayout)
	}
}

func TestPPTXRunAwareReplacementAndShapeUpdatePreserveStyles(t *testing.T) {
	root := t.TempDir()
	writeRichPPTXWorkflowFixture(t, root, "rich-deck.pptx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	original := executeDocumentRead(t, hub, "rich-deck.pptx")["document"].(map[string]any)
	originalBlock := pptxTestBlock(t, original, 1, 1, 0)
	originalStructure := originalBlock["format_metadata"].(map[string]any)["text_structure"].(map[string]any)

	if _, err := hub.Execute(context.Background(), "pptx.replace_text", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/replaced.pptx", "expected_replacements": 1,
		"replacements": []any{map[string]any{"find": "Alpha linked", "replace": "Quarterly linked"}},
	}, "session", "run"); err != nil {
		t.Fatal(err)
	}
	replaced := executeDocumentRead(t, hub, "outputs/replaced.pptx")["document"].(map[string]any)
	assertPPTXRunStylesPreserved(t, originalStructure, pptxTestBlock(t, replaced, 1, 1, 0), "Quarterly linked")

	if _, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/exact-span.pptx", "slide_index": 1, "layout_policy": "preserve",
		"updates": []any{map[string]any{
			"shape_index": 1, "old_text": originalBlock["text"], "mode": "exact_span", "find": "Alpha linked", "text": "Quarterly linked",
		}},
	}, "session", "run"); err != nil {
		t.Fatal(err)
	}
	updated := executeDocumentRead(t, hub, "outputs/exact-span.pptx")["document"].(map[string]any)
	assertPPTXRunStylesPreserved(t, originalStructure, pptxTestBlock(t, updated, 1, 1, 0), "Quarterly linked")

	if _, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/ambiguous-span.pptx", "slide_index": 1, "layout_policy": "preserve",
		"updates": []any{map[string]any{
			"shape_index": 1, "old_text": originalBlock["text"], "mode": "exact_span", "find": "a", "text": "x",
		}},
	}, "session", "run"); err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("ambiguous same-paragraph exact_span was not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "outputs", "ambiguous-span.pptx")); !os.IsNotExist(err) {
		t.Fatalf("ambiguous exact_span left an output: %v", err)
	}
}

func TestPPTXSingleParagraphMultilineDefaultsToSoftBreak(t *testing.T) {
	root := t.TempDir()
	writeRichPPTXWorkflowFixture(t, root, "rich-deck.pptx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	original := executeDocumentRead(t, hub, "rich-deck.pptx")["document"].(map[string]any)
	soft := pptxTestBlock(t, original, 1, 2, 0)

	if _, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"),
		"output_path": "outputs/soft-break.pptx", "slide_index": 1, "layout_policy": "coordinated",
		"updates": []any{map[string]any{
			"shape_index": 2, "old_text": soft["text"], "mode": "rewrite_shape",
			"text": "Improved first line\nImproved second line",
		}},
	}, "session", "run"); err != nil {
		t.Fatalf("single-paragraph multiline update did not use its source-derived soft-break default: %v", err)
	}

	updated := executeDocumentRead(t, hub, "outputs/soft-break.pptx")["document"].(map[string]any)
	structure := pptxTestBlock(t, updated, 1, 2, 0)["format_metadata"].(map[string]any)["text_structure"].(map[string]any)
	paragraphs := testAnySlice(structure["paragraphs"])
	if len(paragraphs) != 1 || intArg(paragraphs[0].(map[string]any), "soft_breaks", 0) != 1 {
		t.Fatalf("single-paragraph multiline update changed paragraph structure: %#v", structure)
	}
}

func TestPPTXMutationRequiresCurrentSourceSHA256(t *testing.T) {
	root := t.TempDir()
	writeRichPPTXWorkflowFixture(t, root, "rich-deck.pptx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	base := map[string]any{
		"path": "rich-deck.pptx", "output_path": "outputs/source-bound.pptx", "slide_index": 10,
		"updates": []any{map[string]any{"shape_index": 1, "old_text": "Late target", "text": "Late revised"}},
	}
	if _, err := hub.Execute(context.Background(), "pptx.update_slide", cloneTestMap(base), "session", "run"); err == nil || !strings.Contains(err.Error(), "source_document_sha256") {
		t.Fatalf("missing PPTX source SHA was not rejected: %v", err)
	}
	mismatch := cloneTestMap(base)
	mismatch["source_document_sha256"] = strings.Repeat("0", 64)
	if _, err := hub.Execute(context.Background(), "pptx.update_slide", mismatch, "session", "run"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("stale PPTX source SHA was not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "outputs", "source-bound.pptx")); !os.IsNotExist(err) {
		t.Fatalf("rejected source-bound PPTX mutation left output: %v", err)
	}
}

func TestPPTXReplacementAdapterDoesNotRematchInsertedFindText(t *testing.T) {
	root := t.TempDir()
	writeRichPPTXWorkflowFixture(t, root, "rich-deck.pptx")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := runPythonAdapter(ctx, pptxAdapterScript, map[string]any{
		"path": filepath.Join(root, "rich-deck.pptx"), "output_path": filepath.Join(root, "replacement-adapter.pptx"),
		"replacements": []any{map[string]any{"find": "Alpha linked", "replace": "Alpha linked expanded"}},
	})
	if err != nil || intArg(out, "replacements", 0) != 1 {
		t.Fatalf("PPTX adapter rematched inserted find text or failed: output=%#v err=%v", out, err)
	}
}

func TestPPTXWholeDeckUpdateIsAtomicAndBounded(t *testing.T) {
	root := t.TempDir()
	writeRichPPTXWorkflowFixture(t, root, "rich-deck.pptx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	before, err := os.ReadFile(filepath.Join(root, "rich-deck.pptx"))
	if err != nil {
		t.Fatal(err)
	}
	read := executeDocumentRead(t, hub, "rich-deck.pptx")["document"].(map[string]any)
	rich := pptxTestBlock(t, read, 1, 1, 0)
	late := pptxTestBlock(t, read, 10, 1, 0)

	result, err := hub.Execute(context.Background(), "pptx.update_deck", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/deck-updated.pptx",
		"slide_updates": []any{
			map[string]any{"slide_index": 1, "layout_policy": "preserve", "updates": []any{map[string]any{
				"shape_index": 1, "old_text": rich["text"], "text": "First revised paragraph\nSecond revised paragraph", "break_mode": "paragraph",
			}}},
			map[string]any{"slide_index": 10, "layout_policy": "preserve", "updates": []any{map[string]any{
				"shape_index": 1, "old_text": late["text"], "text": "Late revised",
			}}},
		},
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if intArg(output, "updated_slides", 0) != 2 || intArg(output, "updated_shapes", 0) != 2 {
		t.Fatalf("whole-deck update summary is incomplete: %#v", output)
	}
	reread := executeDocumentRead(t, hub, "outputs/deck-updated.pptx")["document"].(map[string]any)
	if !strings.Contains(stringArg(pptxTestBlock(t, reread, 1, 1, 0), "text", ""), "First revised paragraph") ||
		pptxTestBlock(t, reread, 10, 1, 0)["text"] != "Late revised" {
		t.Fatalf("whole-deck output did not contain both atomic updates")
	}
	after, err := os.ReadFile(filepath.Join(root, "rich-deck.pptx"))
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatalf("whole-deck update modified its source: %v", err)
	}

	failedPath := filepath.Join(root, "outputs", "atomic-failure.pptx")
	_, err = hub.Execute(context.Background(), "pptx.update_deck", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/atomic-failure.pptx",
		"slide_updates": []any{
			map[string]any{"slide_index": 1, "updates": []any{map[string]any{"shape_index": 1, "old_text": rich["text"], "text": "Temporary"}}},
			map[string]any{"slide_index": 10, "updates": []any{map[string]any{"shape_index": 1, "old_text": "stale", "text": "Must fail"}}},
		},
	}, "session", "run")
	if err == nil {
		t.Fatal("stale whole-deck update unexpectedly succeeded")
	}
	if _, statErr := os.Stat(failedPath); !os.IsNotExist(statErr) {
		t.Fatalf("partial whole-deck output survived failure: %v", statErr)
	}

	tooMany := make([]any, 13)
	for index := range tooMany {
		tooMany[index] = map[string]any{"slide_index": index + 1, "updates": []any{map[string]any{"shape_index": 1, "old_text": "x", "text": "y"}}}
	}
	if _, err := hub.Execute(context.Background(), "pptx.update_deck", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/too-many.pptx", "slide_updates": tooMany,
	}, "session", "run"); err == nil || !strings.Contains(err.Error(), "at most 12") {
		t.Fatalf("whole-deck batch bound was not enforced: %v", err)
	}
}

func TestPPTXMaximumWholeDeckBatchCompletesWithinTimeout(t *testing.T) {
	root := t.TempDir()
	writeMaximumPPTXWorkflowFixture(t, root, "maximum-deck.pptx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(pptxEditTimeoutMS)*time.Millisecond)
	defer cancel()
	slideUpdates := make([]any, 0, document.PPTXWholeDeckMaxSlides)
	for index := 1; index <= document.PPTXWholeDeckMaxSlides; index++ {
		slideUpdates = append(slideUpdates, map[string]any{
			"slide_index": index, "layout_policy": "preserve", "updates": []any{map[string]any{
				"shape_index": 1, "old_text": "Slide " + strconv.Itoa(index), "text": "Revised " + strconv.Itoa(index),
			}},
		})
	}
	result, err := hub.Execute(ctx, "pptx.update_deck", map[string]any{
		"path": "maximum-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "maximum-deck.pptx"), "output_path": "outputs/maximum-updated.pptx", "slide_updates": slideUpdates,
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if intArg(output, "updated_slides", 0) != document.PPTXWholeDeckMaxSlides || intArg(output, "updated_shapes", 0) != document.PPTXWholeDeckMaxSlides {
		t.Fatalf("maximum whole-deck batch summary is incomplete: %#v", output)
	}
	reread := executeDocumentRead(t, hub, "outputs/maximum-updated.pptx")["document"].(map[string]any)
	if pptxTestBlock(t, reread, document.PPTXWholeDeckMaxSlides, 1, 0)["text"] != "Revised 12" {
		t.Fatalf("maximum whole-deck output did not preserve its last update")
	}

	tooManyShapes := make([]any, document.PPTXMaxUpdatedShapes+1)
	for index := range tooManyShapes {
		tooManyShapes[index] = map[string]any{"shape_index": index + 1, "old_text": "old", "text": "new"}
	}
	if err := validatePPTXEditArguments("update_slide", map[string]any{"updates": tooManyShapes}); err == nil || !strings.Contains(err.Error(), "64-shape") {
		t.Fatalf("PPTX shape bound was not enforced: %v", err)
	}
	if err := validatePPTXEditArguments("update_slide", map[string]any{"updates": []any{map[string]any{
		"shape_index": 1, "old_text": "old", "text": strings.Repeat("x", document.PPTXMaxReplacementBytes+1),
	}}}); err == nil || !strings.Contains(err.Error(), "32768-byte") {
		t.Fatalf("PPTX replacement byte bound was not enforced: %v", err)
	}
}

func TestPPTXTemplateAwareInsertionPreservesRelationshipsAndOrder(t *testing.T) {
	root := t.TempDir()
	writeRichPPTXWorkflowFixture(t, root, "rich-deck.pptx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	read := executeDocumentRead(t, hub, "rich-deck.pptx")["document"].(map[string]any)
	if !pptxHasLayoutRef(read, "layout:/ppt/slideLayouts/slideLayout2.xml") {
		t.Fatalf("fixture layout inventory omitted Title and Content layout: %#v", pptxLayoutInventory(read))
	}

	if _, err := hub.Execute(context.Background(), "pptx.add_slide", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/layout-added.pptx", "after_slide_index": 4,
		"layout_ref": "layout:/ppt/slideLayouts/slideLayout2.xml", "title": "Inserted title", "body": "Inserted body",
	}, "session", "run"); err != nil {
		t.Fatal(err)
	}
	layoutAdded := executeDocumentRead(t, hub, "outputs/layout-added.pptx")["document"].(map[string]any)
	inserted := testAnySlice(layoutAdded["slides"])[4].(map[string]any)
	if intArg(inserted, "index", 0) != 5 || !pptxSlideContains(inserted, "Inserted title") || !pptxSlideContains(inserted, "Inserted body") {
		t.Fatalf("layout-based slide was inserted at the wrong position or lost placeholders: %#v", inserted)
	}

	rich := pptxTestBlock(t, read, 1, 1, 0)
	if _, err := hub.Execute(context.Background(), "pptx.add_slide", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/template-added.pptx", "after_slide_index": 4,
		"template_slide_ref": "slide:1", "template_updates": []any{map[string]any{
			"shape_index": 1, "old_text": rich["text"], "mode": "exact_span", "find": "Alpha linked", "text": "Cloned linked",
		}},
	}, "session", "run"); err != nil {
		t.Fatal(err)
	}
	cloned := executeDocumentRead(t, hub, "outputs/template-added.pptx")["document"].(map[string]any)
	if !strings.Contains(stringArg(pptxTestBlock(t, cloned, 5, 1, 0), "text", ""), "Cloned linked") ||
		pptxTestBlock(t, cloned, 5, 3, 1)["text"] != "Grouped read only" ||
		!pptxEnrichmentHasSlideRecord(cloned, "charts", 5) || !pptxEnrichmentHasSlideRecord(cloned, "images", 5) ||
		!pptxEnrichmentHasAnnotation(cloned, "hyperlinks", 5, "https://example.com/details") {
		t.Fatalf("template clone did not preserve text, group, chart, image, or hyperlink relationships")
	}

	for _, test := range []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "stale layout", args: map[string]any{"layout_ref": "layout:/ppt/slideLayouts/stale.xml"}, want: "layout_ref is stale"},
		{name: "notes template", args: map[string]any{"template_slide_ref": "slide:2"}, want: "speaker notes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := cloneTestMap(test.args)
			args["path"] = "rich-deck.pptx"
			args["source_document_sha256"] = docxSourceSHA256ForTest(t, root, "rich-deck.pptx")
			args["output_path"] = "outputs/rejected-" + strings.ReplaceAll(test.name, " ", "-") + ".pptx"
			_, err := hub.Execute(context.Background(), "pptx.add_slide", args, "session", "run")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid template source was not rejected: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, args["output_path"].(string))); !os.IsNotExist(statErr) {
				t.Fatalf("rejected template operation left an output: %v", statErr)
			}
		})
	}
}

func TestPPTXExpiredDeadlineReturnsStableCodeWithoutOutput(t *testing.T) {
	root := t.TempDir()
	writeRichPPTXWorkflowFixture(t, root, "rich-deck.pptx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := hub.Execute(ctx, "pptx.update_slide", map[string]any{
		"path": "rich-deck.pptx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "rich-deck.pptx"), "output_path": "outputs/timed-out.pptx", "slide_index": 10,
		"updates": []any{map[string]any{"shape_index": 1, "old_text": "Late target", "text": "Late revised"}},
	}, "session", "run")
	if app.ToolErrorCodeFrom(err) != app.ToolErrorDocumentOperationTimeout || !document.IsErrorCode(err, document.CodeOperationTimeout) {
		t.Fatalf("PPTX timeout did not retain stable document/tool error codes: %v", err)
	}
	var pipelineErr *document.PipelineError
	if !errors.As(err, &pipelineErr) || pipelineErr.Stage != document.StageRead {
		t.Fatalf("PPTX timeout did not retain the failed reader stage: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "outputs", "timed-out.pptx")); !os.IsNotExist(statErr) {
		t.Fatalf("timed-out PPTX edit left an output: %v", statErr)
	}
}

func TestPPTXHungAdapterIsTerminatedAndClassified(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, adapterErr := runSubprocessAdapter(ctx, map[string]any{"operation": "test_hang"}, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, documentPythonBinary(), "-c", "import time; time.sleep(30)")
	})
	err := wrapPPTXToolError(ctx, adapterErr)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("hung PPTX adapter was not terminated promptly: %s", elapsed)
	}
	if app.ToolErrorCodeFrom(err) != app.ToolErrorDocumentOperationTimeout || !document.IsErrorCode(err, document.CodeOperationTimeout) {
		t.Fatalf("hung PPTX adapter did not map to stable timeout codes: %v", err)
	}
}

func TestPPTXDefinitionsUseOneEndToEndTimeout(t *testing.T) {
	if pptxEditTimeoutMS != 125000 {
		t.Fatalf("PPTX edit timeout = %d, want measured end-to-end budget 125000", pptxEditTimeoutMS)
	}
	for _, definition := range pptxToolDefinitions() {
		if definition.TimeoutMS != pptxEditTimeoutMS {
			t.Fatalf("PPTX definition %s has an independent timeout: %d", definition.Name, definition.TimeoutMS)
		}
	}
}

func writeRichPPTXWorkflowFixture(t *testing.T, root, name string) {
	t.Helper()
	const script = `
from pathlib import Path
import base64
from pptx import Presentation
from pptx.chart.data import ChartData
from pptx.dml.color import RGBColor
from pptx.enum.chart import XL_CHART_TYPE
from pptx.enum.text import PP_ALIGN
from pptx.oxml import parse_xml
from pptx.oxml.ns import nsdecls
from pptx.util import Inches, Pt

root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
image_path = root / "fixture.png"
image_path.write_bytes(base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2nGQAAAAASUVORK5CYII="))
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[6])
rich = slide.shapes.add_textbox(Inches(.6), Inches(.5), Inches(6), Inches(2.1))
tf = rich.text_frame
p = tf.paragraphs[0]
p.alignment = PP_ALIGN.LEFT
p.space_after = Pt(6)
p._p.get_or_add_pPr().insert(0, parse_xml('<a:buChar %s char="&#x2022;"/>' % nsdecls('a')))
r = p.add_run(); r.text = "Alpha "; r.font.name = "Aptos"; r.font.size = Pt(20); r.font.bold = True; r.font.color.rgb = RGBColor(196, 36, 48)
r = p.add_run(); r.text = "linked"; r.font.name = "Aptos"; r.font.size = Pt(18); r.font.italic = True; r.font.color.rgb = RGBColor(20, 92, 180); r.hyperlink.address = "https://example.com/details"
r = p.add_run(); r.text = " omega"; r.font.name = "Aptos"; r.font.size = Pt(16)
p2 = tf.add_paragraph(); p2.level = 1; p2.alignment = PP_ALIGN.RIGHT
r = p2.add_run(); r.text = "Second paragraph"; r.font.name = "Aptos"; r.font.size = Pt(14); r.font.color.rgb = RGBColor(32, 128, 72)
soft = slide.shapes.add_textbox(Inches(.6), Inches(2.8), Inches(4.5), Inches(.7))
p = soft.text_frame.paragraphs[0]
r = p.add_run(); r.text = "Line one"; r.font.size = Pt(15)
p.add_line_break()
r = p.add_run(); r.text = "Line two"; r.font.size = Pt(15); r.font.bold = True
group = slide.shapes.add_group_shape()
child = group.shapes.add_textbox(Inches(6.8), Inches(.7), Inches(2.4), Inches(.5))
child.text_frame.paragraphs[0].add_run().text = "Grouped read only"
chart_data = ChartData(); chart_data.categories = ["A", "B"]; chart_data.add_series("Series", (1, 2))
slide.shapes.add_chart(XL_CHART_TYPE.COLUMN_CLUSTERED, Inches(6.6), Inches(1.6), Inches(3), Inches(2.4), chart_data)
slide.shapes.add_picture(str(image_path), Inches(9.3), Inches(.6), width=Inches(.4), height=Inches(.4))
marker = slide.shapes.add_textbox(Inches(8.7), Inches(7), Inches(1), Inches(.25)); marker.text = "1 / 10"
for index in range(2, 11):
    current = prs.slides.add_slide(prs.slide_layouts[6])
    box = current.shapes.add_textbox(Inches(.7), Inches(.7), Inches(6), Inches(.7))
    box.text_frame.paragraphs[0].add_run().text = "Late target" if index == 10 else "Slide %d" % index
    footer = current.shapes.add_textbox(Inches(8.7), Inches(7), Inches(1), Inches(.25)); footer.text = "%d / 10" % index
    if index == 2:
        current.notes_slide.notes_text_frame.text = "Speaker note that must not be dropped"
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", script, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create rich PPTX workflow fixture: %v\n%s", err, out)
	}
}

func writeMaximumPPTXWorkflowFixture(t *testing.T, root, name string) {
	t.Helper()
	const script = `
from pathlib import Path
from pptx import Presentation
from pptx.util import Inches
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
for index in range(1, 13):
    slide = prs.slides.add_slide(prs.slide_layouts[6])
    box = slide.shapes.add_textbox(Inches(1), Inches(1), Inches(5), Inches(.7))
    box.text = "Slide %d" % index
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", script, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create maximum PPTX fixture: %v\n%s", err, out)
	}
}

func pptxTestBlock(t *testing.T, documentMap map[string]any, slideIndex, shapeIndex, groupChildIndex int) map[string]any {
	t.Helper()
	for _, value := range testAnySlice(documentMap["blocks"]) {
		block := value.(map[string]any)
		location := block["location"].(map[string]any)
		if intArg(location, "slide_index", 0) == slideIndex && intArg(location, "shape_index", 0) == shapeIndex &&
			intArg(location, "group_child_index", 0) == groupChildIndex && block["kind"] == "shape_text" {
			return block
		}
	}
	t.Fatalf("missing PPTX block slide=%d shape=%d group_child=%d", slideIndex, shapeIndex, groupChildIndex)
	return nil
}

func assertPPTXRunStylesPreserved(t *testing.T, before map[string]any, afterBlock map[string]any, wantText string) {
	t.Helper()
	after := afterBlock["format_metadata"].(map[string]any)["text_structure"].(map[string]any)
	beforeParagraphs := testAnySlice(before["paragraphs"])
	afterParagraphs := testAnySlice(after["paragraphs"])
	if len(beforeParagraphs) != len(afterParagraphs) || !strings.Contains(stringArg(afterBlock, "text", ""), wantText) {
		t.Fatalf("PPTX paragraph skeleton or expected text changed: %#v", afterBlock)
	}
	for paragraphIndex := range beforeParagraphs {
		beforeRuns := testAnySlice(beforeParagraphs[paragraphIndex].(map[string]any)["runs"])
		afterRuns := testAnySlice(afterParagraphs[paragraphIndex].(map[string]any)["runs"])
		if len(beforeRuns) != len(afterRuns) {
			t.Fatalf("run count changed in paragraph %d", paragraphIndex+1)
		}
		for runIndex := range beforeRuns {
			if !reflect.DeepEqual(beforeRuns[runIndex].(map[string]any)["style"], afterRuns[runIndex].(map[string]any)["style"]) {
				t.Fatalf("run style changed in paragraph %d run %d", paragraphIndex+1, runIndex+1)
			}
		}
	}
	firstRuns := testAnySlice(afterParagraphs[0].(map[string]any)["runs"])
	if strings.TrimSpace(stringArg(firstRuns[0].(map[string]any), "text", "")) == "" || strings.TrimSpace(stringArg(firstRuns[1].(map[string]any), "text", "")) == "" {
		t.Fatalf("cross-run replacement flattened the replacement into one run: %#v", firstRuns)
	}
}

func pptxLayoutInventory(documentMap map[string]any) []any {
	enrichment := documentMap["enrichment"].(map[string]any)
	layout := enrichment["layout"].(map[string]any)
	return testAnySlice(layout["layout_inventory"])
}

func pptxHasLayoutRef(documentMap map[string]any, ref string) bool {
	for _, value := range pptxLayoutInventory(documentMap) {
		if stringArg(value.(map[string]any), "layout_ref", "") == ref {
			return true
		}
	}
	return false
}

func pptxSlideContains(slide map[string]any, expected string) bool {
	for _, value := range testAnySlice(slide["items"]) {
		if strings.Contains(stringArg(value.(map[string]any), "text", ""), expected) {
			return true
		}
	}
	return false
}

func pptxEnrichmentHasSlideRecord(documentMap map[string]any, category string, slideIndex int) bool {
	enrichment := documentMap["enrichment"].(map[string]any)
	assets := enrichment["assets"].(map[string]any)
	for _, value := range testAnySlice(assets[category]) {
		location := value.(map[string]any)["location"].(map[string]any)
		if intArg(location, "slide_index", 0) == slideIndex {
			return true
		}
	}
	return false
}

func pptxEnrichmentHasAnnotation(documentMap map[string]any, category string, slideIndex int, target string) bool {
	enrichment := documentMap["enrichment"].(map[string]any)
	annotations := enrichment["annotations"].(map[string]any)
	for _, value := range testAnySlice(annotations[category]) {
		record := value.(map[string]any)
		location := record["location"].(map[string]any)
		if intArg(location, "slide_index", 0) == slideIndex && stringArg(record, "target", "") == target {
			return true
		}
	}
	return false
}
