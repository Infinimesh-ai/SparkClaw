package toolhub

import (
	"context"
	"os/exec"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

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
