package toolhub

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestDOCXSetTextStyleRoundTripsEveryRequestedProperty(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "style.docx", "Styled paragraph")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())
	sourceSHA := docxSourceSHA256ForTest(t, root, "style.docx")

	cases := []struct {
		name  string
		style map[string]any
	}{
		{name: "builtin", style: map[string]any{"builtin_style": "Heading 1"}},
		{name: "bold_false", style: map[string]any{"bold": false}},
		{name: "font_size", style: map[string]any{"font_size_pt": 17}},
		{name: "combined", style: map[string]any{"builtin_style": "Heading 2", "bold": true, "font_size_pt": 19}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join("outputs", tc.name+".docx")
			result, err := hub.Execute(context.Background(), "docx.set_text_style", map[string]any{
				"path": "style.docx", "paragraph_index": 1, "source_document_sha256": sourceSHA,
				"source_hash": sourceHash("Styled paragraph"), "old_text": "Styled paragraph",
				"before_format_sha256": "direct-toolhub-preflight", "style": tc.style, "output_path": output,
			}, "session", "run")
			if err != nil {
				t.Fatal(err)
			}
			written := result.Output.(map[string]any)["output_path"].(string)
			read := readDOCXDocument(t, root, written)
			paragraph := testAnySlice(read["paragraphs"])[0].(map[string]any)
			if paragraph["text"] != "Styled paragraph" {
				t.Fatalf("style edit changed text: %#v", paragraph)
			}
			runs := testAnySlice(paragraph["runs"])
			if len(runs) != 1 {
				t.Fatalf("style edit changed run structure: %#v", runs)
			}
			run := runs[0].(map[string]any)
			if value, ok := tc.style["builtin_style"]; ok && !strings.EqualFold(paragraph["style"].(string), value.(string)) {
				t.Fatalf("built-in style did not round-trip: %#v", paragraph)
			}
			if value, ok := tc.style["bold"]; ok && run["effective_bold"] != value {
				t.Fatalf("bold did not round-trip: %#v", run)
			}
			if value, ok := tc.style["font_size_pt"]; ok && intArg(run, "effective_font_size_pt", 0) != value.(int) {
				t.Fatalf("font size did not round-trip: %#v", run)
			}
		})
	}
}

func TestDOCXSetTextStyleRejectsInvalidContractBeforeWriting(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "style.docx", "Styled paragraph")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())
	sourceSHA := docxSourceSHA256ForTest(t, root, "style.docx")

	cases := []struct {
		name string
		args map[string]any
	}{
		{name: "empty_style", args: map[string]any{"path": "style.docx", "paragraph_index": 1, "style": map[string]any{}, "output_path": "outputs/empty.docx"}},
		{name: "missing_target", args: map[string]any{"path": "style.docx", "style": map[string]any{"bold": true}, "output_path": "outputs/missing.docx"}},
		{name: "conflicting_target", args: map[string]any{"path": "style.docx", "paragraph_index": 1, "location": map[string]any{"part": "document", "block_type": "paragraph", "paragraph_index": 2}, "style": map[string]any{"bold": true}, "output_path": "outputs/conflict.docx"}},
		{name: "size_out_of_range", args: map[string]any{"path": "style.docx", "paragraph_index": 1, "style": map[string]any{"font_size_pt": 201}, "output_path": "outputs/size.docx"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := cloneTestMap(tc.args)
			args["source_document_sha256"] = sourceSHA
			args["source_hash"] = sourceHash("Styled paragraph")
			args["old_text"] = "Styled paragraph"
			args["before_format_sha256"] = "direct-toolhub-preflight"
			_, err := hub.Execute(context.Background(), "docx.set_text_style", args, "session", "run")
			if err == nil {
				t.Fatal("expected invalid style contract to fail")
			}
			output := filepath.Join(root, tc.args["output_path"].(string))
			if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
				t.Fatalf("invalid style call left an output: %v", statErr)
			}
		})
	}
}

func docxSourceSHA256ForTest(t *testing.T, root, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("%x", digest)
}

func TestDOCXReadExposesRunsAndStoryCoverage(t *testing.T) {
	root := t.TempDir()
	pythonScript := `
from pathlib import Path
from docx import Document
from docx.shared import Pt
root = Path(__import__("sys").argv[1])
doc = Document()
paragraph = doc.add_paragraph()
first = paragraph.add_run("Alpha ")
first.bold = True
first.font.size = Pt(18)
second = paragraph.add_run("Beta")
second.italic = True
section = doc.sections[0]
section.header.paragraphs[0].text = "Confidential header"
section.footer.paragraphs[0].text = "Page footer"
doc.save(root / "stories.docx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create DOCX story fixture: %v\n%s", err, output)
	}
	document := readDOCXDocument(t, root, "stories.docx")
	paragraphs := testAnySlice(document["paragraphs"])
	if len(paragraphs) != 1 {
		t.Fatalf("expected one body paragraph, got %#v", paragraphs)
	}
	paragraph := paragraphs[0].(map[string]any)
	runs := testAnySlice(paragraph["runs"])
	if len(runs) != 2 {
		t.Fatalf("expected two stable runs, got %#v", runs)
	}
	first := runs[0].(map[string]any)
	if first["text"] != "Alpha " || first["bold"] != true || first["effective_bold"] != true || intArg(first, "font_size_pt", 0) != 18 {
		t.Fatalf("unexpected first-run formatting: %#v", first)
	}
	if intArg(first, "start", -1) != 0 || intArg(first, "end", 0) != 6 {
		t.Fatalf("unexpected first-run span: %#v", first)
	}

	enrichment := document["enrichment"].(map[string]any)
	coverage := enrichment["coverage"].(map[string]any)
	if coverage["content"] != "complete" {
		t.Fatalf("fully inventoried DOCX should report complete content: %#v", coverage)
	}
	storyParts := testAnySlice(enrichment["extensions"].(map[string]any)["story_parts"])
	wants := map[string]bool{"Confidential header": false, "Page footer": false}
	for _, value := range storyParts {
		story := value.(map[string]any)
		for _, blockValue := range testAnySlice(story["blocks"]) {
			block := blockValue.(map[string]any)
			if _, ok := wants[block["text"].(string)]; ok {
				wants[block["text"].(string)] = true
				location := block["location"].(map[string]any)
				if location["story_part"] == "" || location["path"] == "" {
					t.Fatalf("story block lacks a stable location: %#v", block)
				}
			}
		}
	}
	for text, found := range wants {
		if !found {
			t.Fatalf("missing %q in story-part evidence: %#v", text, storyParts)
		}
	}
}

func TestDOCXReadReportsTrackedChangesAsPartial(t *testing.T) {
	root := t.TempDir()
	pythonScript := `
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile
from docx import Document
root = Path(__import__("sys").argv[1])
path = root / "tracked.docx"
doc = Document()
doc.add_paragraph("Visible body")
doc.save(path)
with ZipFile(path, "r") as source:
    entries = {name: source.read(name) for name in source.namelist()}
xml = entries["word/document.xml"].decode("utf-8")
marker = '<w:ins w:id="1" w:author="SparkClaw"><w:r><w:t>Tracked insertion</w:t></w:r></w:ins>'
xml = xml.replace("</w:p>", marker + "</w:p>", 1)
entries["word/document.xml"] = xml.encode("utf-8")
with ZipFile(path, "w", ZIP_DEFLATED) as output:
    for name, value in entries.items():
        output.writestr(name, value)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create tracked DOCX fixture: %v\n%s", err, output)
	}
	document := readDOCXDocument(t, root, "tracked.docx")
	enrichment := document["enrichment"].(map[string]any)
	coverage := enrichment["coverage"].(map[string]any)
	if coverage["content"] != "partial" {
		t.Fatalf("tracked content must not report complete coverage: %#v", coverage)
	}
	scopes := coverage["content_scopes"].(map[string]any)
	if scopes["tracked_changes"] != "partial" || scopes["body"] != "partial" {
		t.Fatalf("tracked-change omission should identify the body scope: %#v", scopes)
	}
	omissions := testAnySlice(enrichment["extensions"].(map[string]any)["content_omissions"])
	found := false
	for _, value := range omissions {
		if value.(map[string]any)["reason"] == "tracked_changes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tracked-change omission is missing: %#v", omissions)
	}
}

func TestDOCXTextReplacementPreservesRunsRelationshipsAndUnsupportedSiblings(t *testing.T) {
	root := t.TempDir()
	writeDOCXRunPreservationFixture(t, root)
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())

	t.Run("single_run", func(t *testing.T) {
		output := executeDOCXTextReplacement(t, hub, root, "runs.docx", "outputs/single.docx", "solo-target", "replacement")
		paragraph := docxParagraphForTest(t, output, 1)
		runs := testAnySlice(paragraph["runs"])
		if len(runs) != 3 || runs[0].(map[string]any)["text"] != "Alpha " ||
			runs[1].(map[string]any)["text"] != "replacement" || runs[2].(map[string]any)["text"] != " omega" {
			t.Fatalf("single-run replacement changed sibling text or run structure: %#v", runs)
		}
		if runs[1].(map[string]any)["bold"] != true || runs[2].(map[string]any)["italic"] != true {
			t.Fatalf("single-run replacement changed run formatting: %#v", runs)
		}
	})

	t.Run("homogeneous_cross_run", func(t *testing.T) {
		output := executeDOCXTextReplacement(t, hub, root, "runs.docx", "outputs/cross.docx", "ss boundary", "span")
		runs := testAnySlice(docxParagraphForTest(t, output, 2)["runs"])
		if len(runs) != 3 || runs[0].(map[string]any)["text"] != "Crospan" ||
			runs[1].(map[string]any)["text"] != "" || runs[2].(map[string]any)["text"] != " tail" {
			t.Fatalf("homogeneous cross-run replacement did not retain run structure: %#v", runs)
		}
		if runs[0].(map[string]any)["bold"] != true || runs[1].(map[string]any)["bold"] != true ||
			runs[2].(map[string]any)["italic"] != true {
			t.Fatalf("homogeneous cross-run replacement changed formatting: %#v", runs)
		}
	})

	t.Run("mixed_format_rejected", func(t *testing.T) {
		outputRef := "outputs/mixed.docx"
		_, err := hub.Execute(context.Background(), "office.replace_text", map[string]any{
			"path": "runs.docx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "runs.docx"),
			"output_path": outputRef, "expected_replacements": 1,
			"replacements": []any{map[string]any{"find": "ed for", "replace": " across "}},
		}, "session", "run")
		if err == nil || !strings.Contains(err.Error(), "mixed run formatting") {
			t.Fatalf("mixed-format replacement did not fail closed: %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(root, outputRef)); !os.IsNotExist(statErr) {
			t.Fatalf("mixed-format failure left an output: %v", statErr)
		}
	})

	t.Run("linked_run", func(t *testing.T) {
		output := executeDOCXTextReplacement(t, hub, root, "runs.docx", "outputs/link.docx", "Linked target", "Linked update")
		enrichment := output["enrichment"].(map[string]any)
		hyperlinks := testAnySlice(enrichment["annotations"].(map[string]any)["hyperlinks"])
		if len(hyperlinks) != 1 || hyperlinks[0].(map[string]any)["text"] != "Linked update" ||
			hyperlinks[0].(map[string]any)["target"] != "https://example.com/report" {
			t.Fatalf("linked-run replacement lost hyperlink evidence: %#v", hyperlinks)
		}
		images := testAnySlice(enrichment["assets"].(map[string]any)["images"])
		if len(images) != 1 {
			t.Fatalf("linked-run replacement lost the unchanged image: %#v", images)
		}
		boundaries := map[string]bool{}
		for _, runValue := range testAnySlice(docxParagraphForTest(t, output, 4)["runs"]) {
			for _, boundary := range testAnySlice(runValue.(map[string]any)["boundaries"]) {
				boundaries[boundary.(string)] = true
			}
		}
		if !boundaries["hyperlink"] || !boundaries["field"] || !boundaries["drawing"] {
			t.Fatalf("linked-run replacement lost relationship/field/drawing boundaries: %#v", boundaries)
		}
	})
}

func TestDOCXParagraphReplacementPreservesPropertiesAndRejectsMixedRuns(t *testing.T) {
	root := t.TempDir()
	writeDOCXRunPreservationFixture(t, root)
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "docx.replace_paragraph", map[string]any{
		"path": "runs.docx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "runs.docx"),
		"paragraph_index": 5, "old_text": "Whole paragraph", "source_hash": sourceHash("Whole paragraph"),
		"text": "Rewritten paragraph", "output_path": "outputs/paragraph.docx",
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	written := result.Output.(map[string]any)["output_path"].(string)
	paragraph := docxParagraphForTest(t, readDOCXDocument(t, root, written), 5)
	if paragraph["style"] != "Heading 2" || intArg(paragraph["format"].(map[string]any), "left_indent", 0) == 0 ||
		intArg(paragraph["format"].(map[string]any), "space_after", 0) == 0 {
		t.Fatalf("whole-paragraph replacement changed paragraph properties: %#v", paragraph)
	}
	runs := testAnySlice(paragraph["runs"])
	if len(runs) != 2 || runs[0].(map[string]any)["text"] != "Rewritten paragraph" || runs[0].(map[string]any)["bold"] != true ||
		runs[1].(map[string]any)["text"] != "" || runs[1].(map[string]any)["bold"] != true {
		t.Fatalf("whole-paragraph replacement flattened homogeneous run formatting: %#v", runs)
	}

	mixedOutput := "outputs/paragraph-mixed.docx"
	_, err = hub.Execute(context.Background(), "docx.replace_paragraph", map[string]any{
		"path": "runs.docx", "source_document_sha256": docxSourceSHA256ForTest(t, root, "runs.docx"),
		"paragraph_index": 6, "old_text": "Mixed paragraph", "source_hash": sourceHash("Mixed paragraph"),
		"text": "Must not flatten", "output_path": mixedOutput,
	}, "session", "run")
	if err == nil || !strings.Contains(err.Error(), "homogeneous run formatting") {
		t.Fatalf("mixed whole-paragraph replacement did not fail closed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, mixedOutput)); !os.IsNotExist(statErr) {
		t.Fatalf("mixed whole-paragraph failure left an output: %v", statErr)
	}
}

func executeDOCXTextReplacement(t *testing.T, hub *ToolHub, root, input, output, find, replacement string) map[string]any {
	t.Helper()
	result, err := hub.Execute(context.Background(), "office.replace_text", map[string]any{
		"path": input, "source_document_sha256": docxSourceSHA256ForTest(t, root, input),
		"output_path": output, "expected_replacements": 1,
		"replacements": []any{map[string]any{"find": find, "replace": replacement}},
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	written := result.Output.(map[string]any)["output_path"].(string)
	return readDOCXDocument(t, root, written)
}

func docxParagraphForTest(t *testing.T, document map[string]any, index int) map[string]any {
	t.Helper()
	paragraphs := testAnySlice(document["paragraphs"])
	if index <= 0 || index > len(paragraphs) {
		t.Fatalf("DOCX paragraph %d is unavailable: %#v", index, paragraphs)
	}
	return paragraphs[index-1].(map[string]any)
}

func writeDOCXRunPreservationFixture(t *testing.T, root string) {
	t.Helper()
	pythonScript := `
import base64
from pathlib import Path
from docx import Document
from docx.opc.constants import RELATIONSHIP_TYPE as RT
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.shared import Inches, Pt, RGBColor
from docx.text.run import Run

root = Path(__import__("sys").argv[1])
doc = Document()

p = doc.add_paragraph()
r = p.add_run("Alpha ")
r.bold = True
r.font.name = "Arial"
r.font.size = Pt(13)
r.font.color.rgb = RGBColor(0x11, 0x22, 0x33)
r = p.add_run("solo-target")
r.bold = True
r.font.name = "Arial"
r.font.size = Pt(13)
r.font.color.rgb = RGBColor(0x11, 0x22, 0x33)
r = p.add_run(" omega")
r.italic = True

p = doc.add_paragraph()
for text in ("Cross ", "boundary"):
    r = p.add_run(text)
    r.bold = True
r = p.add_run(" tail")
r.italic = True

p = doc.add_paragraph()
r = p.add_run("Mixed ")
r.bold = True
r = p.add_run("format")
r.bold = False

p = doc.add_paragraph()
rid = p.part.relate_to("https://example.com/report", RT.HYPERLINK, is_external=True)
hyperlink = OxmlElement("w:hyperlink")
hyperlink.set(qn("r:id"), rid)
run_element = OxmlElement("w:r")
hyperlink.append(run_element)
p._p.append(hyperlink)
linked_run = Run(run_element, p)
linked_run.text = "Linked target"
linked_run.underline = True
field_run = p.add_run()
begin = OxmlElement("w:fldChar")
begin.set(qn("w:fldCharType"), "begin")
field_run._r.append(begin)
instruction = OxmlElement("w:instrText")
instruction.set(qn("xml:space"), "preserve")
instruction.text = " PAGE "
field_run._r.append(instruction)
end = OxmlElement("w:fldChar")
end.set(qn("w:fldCharType"), "end")
field_run._r.append(end)
png = root / "pixel.png"
png.write_bytes(base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Zl1sAAAAASUVORK5CYII="))
p.add_run().add_picture(str(png), width=Inches(0.1))

p = doc.add_paragraph()
p.style = "Heading 2"
p.paragraph_format.left_indent = Inches(0.25)
p.paragraph_format.space_after = Pt(8)
for text in ("Whole ", "paragraph"):
    r = p.add_run(text)
    r.bold = True

p = doc.add_paragraph()
r = p.add_run("Mixed ")
r.bold = True
r = p.add_run("paragraph")
r.italic = True

doc.save(root / "runs.docx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create DOCX run-preservation fixture: %v\n%s", err, output)
	}
}

func readDOCXDocument(t *testing.T, root, path string) map[string]any {
	t.Helper()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())
	result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": path}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	return result.Output.(map[string]any)["document"].(map[string]any)
}
