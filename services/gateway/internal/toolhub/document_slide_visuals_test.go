package toolhub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestDocumentSlideVisualEvidenceAndFailuresRemainSeparateFromNativeCoverage(t *testing.T) {
	for _, mode := range []string{"image", "invalid_model", "unavailable", "invalid_pdf", "disabled", "invalid_endpoint", "bounded"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			writeSingleSlidePptxFixture(t, root, "source.pptx")
			pdfPath := filepath.Join(root, "render.pdf")
			writePPTXVisualQAPDFFixture(t, pdfPath, 960, 720)
			if mode == "bounded" {
				script := `from pptx import Presentation
from pypdf import PdfWriter
import sys
p=Presentation(sys.argv[1]); s=p.slides.add_slide(p.slide_layouts[1]); s.shapes.title.text='Unprocessed second page'; p.save(sys.argv[1])
w=PdfWriter(); w.add_blank_page(960,720); w.add_blank_page(960,720)
with open(sys.argv[2],'wb') as f: w.write(f)
`
				if raw, err := exec.Command(documentPythonBinary(), "-c", script, filepath.Join(root, "source.pptx"), pdfPath).CombinedOutput(); err != nil {
					t.Fatal(err, string(raw))
				}
			}
			pdf, _ := os.ReadFile(pdfPath)
			renderer := newPPTXVisualTestRenderer(t, func(w http.ResponseWriter, _ *http.Request) {
				if mode == "unavailable" {
					w.WriteHeader(503)
					return
				}
				if mode == "invalid_pdf" {
					_, _ = io.WriteString(w, "not PDF")
					return
				}
				_, _ = w.Write(pdf)
			})
			images := 0
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), "data:image/png;base64,") || !strings.Contains(string(raw), "document_slide_read_v1") {
					t.Error("actual image/strict schema missing")
				}
				images++
				content := `{"description":"BETA points to ALPHA; 37 units","limitations":"small label unreadable"}`
				if mode == "invalid_model" {
					content = `{"description":"37"}`
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
			}))
			defer model.Close()
			cfg := configtest.MustLoadDefault()
			cfg.Model.Mock, cfg.Model.Fast.BaseURL = false, model.URL
			cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
			cfg.Adapters.PPTXVisualQA = testPPTXVisualQAConfig(renderer.URL)
			cfg.Adapters.PPTXVisualQA.AllowedHosts = []string{"127.0.0.1"}
			if mode == "invalid_endpoint" {
				cfg.Adapters.PPTXVisualQA.AllowedHosts = nil
			}
			// Read capability is independent of mutation-QA rollout phase.
			cfg.Adapters.PPTXVisualQA.Phase = "disabled"
			if mode == "bounded" {
				cfg.Adapters.PPTXVisualQA.MaxChangedPages = 1
			}
			if mode == "disabled" {
				cfg.Adapters.PPTXVisualQA.BaseURL = ""
			}
			hub := New(cfg, store.NewMemoryStore())
			defer hub.Close()
			args := map[string]any{"path": "source.pptx", "page_start": 1, "page_end": 1, "include_visuals": true}
			if mode == "bounded" {
				delete(args, "page_start")
				delete(args, "page_end")
				args["all"] = true
			}
			result, err := hub.Execute(t.Context(), "document.read_range", args, "session", "run")
			if mode == "invalid_pdf" {
				if err == nil {
					t.Fatal("render integrity failure became partial success")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			output := result.Output.(map[string]any)
			coverage := output["visual_coverage"].(map[string]any)
			if coverage["object_parse_complete"] != false || coverage["semantic_correctness"] != "not_assessed" {
				t.Fatal("image observation certified semantics", coverage)
			}
			if (coverage["status"] == "processed") != (mode == "image") {
				t.Fatal("incorrect visual status", coverage)
			}
			if mode == "image" {
				encoded, _ := json.Marshal(output["visual_evidence"])
				for _, required := range []string{"37 units", "small label unreadable", "png_sha256", "source_sha256", "model_call_id", "presentation.slide[1]"} {
					if !strings.Contains(string(encoded), required) {
						t.Fatal("lost bound evidence", required)
					}
				}
			}
			if mode == "bounded" {
				missing := coverage["missing_pages"].([]int)
				if images != 1 || len(missing) != 1 || missing[0] != 2 || coverage["page_limit"] != 1 {
					t.Fatal("visual budget hid an unprocessed page", coverage, images)
				}
			}
			if mode == "disabled" || mode == "unavailable" || mode == "invalid_endpoint" {
				if images != 0 {
					t.Fatal("image call without rendered source")
				}
			}
			// Legacy calls and continuation results must never re-render.
			before := images
			delete(args, "include_visuals")
			legacy, err := hub.Execute(t.Context(), "document.read_range", args, "session", "legacy")
			if err != nil || legacy.Output.(map[string]any)["visual_coverage"] != nil || images != before {
				t.Fatal("legacy read changed", err)
			}
		})
	}
}

func TestSummaryLocationsBypassModelProseAndRetainDuplicateHeadingParents(t *testing.T) {
	root := t.TempDir()
	source := "# Chapter 21\n\n## Checklist\n\nOld item\n\n# Appendix\n\nRestriction\n\n# Final review\n\n## Checklist\n\nNew item\n"
	if err := os.WriteFile(filepath.Join(root, "source.md"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "not answering the whole-document task") || !strings.Contains(string(raw), "parent_block") {
			t.Error("summary missing local scope or native locations")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Unreliable generated location: before appendix"}}}})
	}))
	defer model.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock, cfg.Model.Fast.BaseURL = false, model.URL
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
	hub := New(cfg, store.NewMemoryStore())
	defer hub.Close()
	result, err := hub.Execute(t.Context(), "document.summarize", map[string]any{"path": "source.md", "contract_version": 4}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	locations := result.Output.(map[string]any)["source_locations"].([]map[string]any)
	checklists := []map[string]any{}
	for _, location := range locations {
		if location["title"] == "Checklist" {
			checklists = append(checklists, location)
		}
	}
	if len(checklists) != 2 || checklists[0]["block"] == checklists[1]["block"] || checklists[0]["parent_block"] == checklists[1]["parent_block"] {
		t.Fatal("native duplicate heading identities lost", locations)
	}
	// A targeted read also needs global navigation order: requesting the final
	// body must not make the earlier appendix or duplicate title disappear.
	index, _, err := hub.ensureDocumentIndex(t.Context(), filepath.Join(root, "source.md"))
	if err != nil {
		t.Fatal(err)
	}
	last := ""
	for _, node := range index.Nodes {
		if node.Title == "Checklist" {
			last = node.ID
		}
	}
	read, err := hub.Execute(t.Context(), "document.read_range", map[string]any{"path": "source.md", "node_ids": []string{last}, "include_descendants": true, "include_source_locations": true}, "session", "read")
	if err != nil {
		t.Fatal(err)
	}
	readOutput := read.Output.(map[string]any)
	if len(readOutput["source_locations"].([]map[string]any)) != len(locations) || strings.Contains(readOutput["content"].(string), "Old item") {
		t.Fatal("navigation map changed selected content scope", readOutput)
	}
	parent := ""
	for _, node := range index.Nodes {
		if node.Title == "Final review" {
			parent = node.ID
		}
	}
	relations := strings.Join(selectedSourceLocationRelations(index, []string{parent}), "\n")
	if !strings.Contains(relations, `comes AFTER "Appendix"`) || strings.Contains(relations, `comes BEFORE "Appendix"`) {
		t.Fatal("runtime sibling order incorrect", relations)
	}
}
