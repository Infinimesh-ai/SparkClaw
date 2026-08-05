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
