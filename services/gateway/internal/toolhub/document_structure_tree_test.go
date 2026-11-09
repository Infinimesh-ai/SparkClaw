package toolhub

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func TestDocumentTreeSummaryLocationsUseCanonicalSectionBounds(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sections.md")
	if err := os.WriteFile(path, []byte("# First\n\nFirst body.\n\n# Second\n\nSecond body.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hub := newRetrievalTestHub(t, root)
	index, _, err := hub.ensureDocumentIndex(docindex.WithStructureVersion(t.Context(), 2), path)
	if err != nil {
		t.Fatal(err)
	}
	units := index.SummaryUnits(16)
	for i, unit := range units {
		locations := summarySourceLocations(index, &unit)
		if len(locations) != 1 || locations[0]["node_id"] != unit.ID || locations[0]["title"] != []string{"First", "Second"}[i] {
			t.Fatalf("summary source locations crossed section boundary: %+v", locations)
		}
	}
	flat := filepath.Join(root, "flat.txt")
	if err := os.WriteFile(flat, []byte(strings.Repeat("x\n\n", 10000)), 0600); err != nil {
		t.Fatal(err)
	}
	index, _, err = hub.ensureDocumentIndex(docindex.WithStructureVersion(t.Context(), 2), flat)
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range index.SummaryUnits(16) {
		locations := summarySourceLocations(index, &unit)
		if len(locations) != 0 {
			t.Fatal("physical navigation metadata displaced summary body")
		}
		parts, missing := scopedSummaryParts(index, unit, 4000)
		if len(parts) == 0 || len(missing) > 0 {
			t.Fatal("small flat summary lost source", missing)
		}
	}
}

func TestDocumentTreeNativePDFBookmarksAndUnrecognizedPages(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "book.pdf")
	script := `import sys
from pypdf import PdfWriter
w=PdfWriter()
for _ in range(5): w.add_blank_page(width=100,height=100)
p=w.add_outline_item("Part",1)
w.add_outline_item("Nested",2,parent=p)
w.add_outline_item("Last",4)
with open(sys.argv[1],"wb") as f: w.write(f)
`
	command := exec.CommandContext(t.Context(), documentPythonBinary(), "-c", script, path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("PDF fixture %v %s", err, output)
	}
	hub := newRetrievalTestHub(t, root)
	index, _, err := hub.ensureDocumentIndex(docindex.WithStructureVersion(t.Context(), 2), path)
	if err != nil {
		t.Fatal(err)
	}
	bookmarks := []document.StructureNode{}
	for _, node := range index.Nodes {
		if node.Kind == "bookmark" {
			bookmarks = append(bookmarks, node)
		}
	}
	if len(bookmarks) != 3 || bookmarks[0].Span.PageStart != 2 || bookmarks[0].Span.PageEnd != 4 || bookmarks[1].ParentID != bookmarks[0].ID || index.Structure.Coverage.Missing != 5 {
		t.Fatalf("native PDF ladder lost source boundaries: %+v %+v", bookmarks, index.Structure.Coverage)
	}
}

func TestDocumentTreeOutlineExpandAndReadNativeSubtree(t *testing.T) {
	root := t.TempDir()
	const source = "# Selected\n\nSELECTED_BODY.\n\n## Child\n\nCHILD_LAST_DETAIL.\n\n# Other\n\nUNSELECTED_BODY.\n"
	if err := os.WriteFile(filepath.Join(root, "tree.md"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	hub := newRetrievalTestHub(t, root)
	outline := treeToolOutput(t, hub, "document.outline", map[string]any{"path": "tree.md", "structure_version": 2, "depth": 1})
	page := treeOutlinePage(t, outline)
	if page.Node.Kind != "document" || len(page.Path) != 0 || len(page.Nodes) != 2 || page.NextCursor != "" {
		t.Fatalf("root did not expose the complete top level: %+v", page)
	}
	selected := page.Nodes[0]
	if selected.Title != "Selected" || selected.Kind != "section" || selected.StructureSource != "markdown_heading" || selected.Span == nil || selected.Stats == nil {
		t.Fatalf("native section metadata missing: %+v", selected)
	}
	assertTreeDiscoveryOnly(t, page)
	expanded := treeToolOutput(t, hub, "document.outline", map[string]any{
		"path": "tree.md", "node_id": selected.ID, "depth": 2,
		"source_sha256": page.SourceSHA256, "structure_generation": page.StructureGeneration,
	})
	childPage := treeOutlinePage(t, expanded)
	if childPage.Node.ID != selected.ID || len(childPage.Path) != 1 || childPage.Path[0].ID != page.Node.ID || childPage.StructureGeneration != page.StructureGeneration {
		t.Fatalf("expansion lost its source-bound path: %+v", childPage)
	}
	assertTreeDiscoveryOnly(t, childPage)
	preview := false
	for _, node := range childPage.Nodes {
		if node.Preview != nil {
			preview = true
			if !node.Preview.IsPreview {
				t.Fatalf("leaf text was not marked as a preview: %+v", node)
			}
		}
	}
	if !preview {
		t.Fatal("lowest expanded level omitted every native leaf preview")
	}
	var selections []map[string]any
	selections = append(selections, map[string]any{"node_ids": []string{selected.ID}, "include_descendants": true})
	selections = append(selections, map[string]any{"section": selected.ID})
	for _, args := range selections {
		args["path"], args["structure_version"] = "tree.md", 2
		args["source_sha256"], args["structure_generation"] = page.SourceSHA256, page.StructureGeneration
		read := treeToolOutput(t, hub, "document.read_range", args)
		content, _ := read["content"].(string)
		if !strings.Contains(content, "SELECTED_BODY") || !strings.Contains(content, "CHILD_LAST_DETAIL") || strings.Contains(content, "UNSELECTED_BODY") {
			t.Fatalf("selection did not read exactly the native subtree: %s", content)
		}
		var coverage document.StructureCoverage
		decodeTreeValue(t, read["coverage"], &coverage)
		if read["read_complete"] != true || read["next_cursor"] != "" || coverage.Scope == "structure_discovery" || coverage.Required != selected.Stats.Leaves || coverage.Covered != coverage.Required || coverage.Missing != 0 {
			t.Fatalf("subtree read did not establish complete content coverage: %+v", read)
		}
	}
}

func TestDocumentTreeOutlineUsesConfiguredFastEvidenceShare(t *testing.T) {
	for _, share := range []int{25, 40} {
		t.Run(strconv.Itoa(share), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "budget.md"), []byte("# One\n\nFirst body.\n\n# Last\n\nLast body.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			hub := newRetrievalTestHub(t, root)
			hub.cfg.Runtime.StageEvidenceMaxBytes = 16000
			hub.cfg.Runtime.DocumentStructureSharePercent = share
			for _, requested := range []int{0, 3000, 100000} {
				args := map[string]any{"path": "budget.md", "structure_version": 2}
				if requested > 0 {
					args["max_bytes"] = requested
				}
				output := treeToolOutput(t, hub, "document.outline", args)
				page := treeOutlinePage(t, output)
				want := hub.cfg.StageEvidenceBytes(hub.cfg.Model.Fast) * share / 100
				if requested > 0 {
					want = min(want, requested)
				}
				if page.Budget.MaxBytes != want || page.Budget.SharePercent != share {
					t.Fatalf("structure share did not follow Fast evidence budget: got=%+v want=%d", page.Budget, want)
				}
				content := output["content"].(string)
				if len(content) > want || !json.Valid([]byte(content)) {
					t.Fatalf("bounded projection was not complete JSON: bytes=%d budget=%d content=%s", len(content), want, content)
				}
				var projected []docindex.OutlineViewNode
				decodeTreeValue(t, output["outline"], &projected)
				if !reflect.DeepEqual(projected, page.Nodes) {
					t.Fatal("JSON content and structured outline describe different trees")
				}
			}
		})
	}
}

func TestDocumentTreeDoesNotRebaseLegacyOutlineGeneration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "legacy.md"), []byte("# Section\n\nOriginal legacy body.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hub := newRetrievalTestHub(t, root)
	legacy := treeToolOutput(t, hub, "document.outline", map[string]any{"path": "legacy.md"})
	tree := treeToolOutput(t, hub, "document.outline", map[string]any{"path": "legacy.md", "structure_version": 2})
	for _, output := range []map[string]any{legacy, tree} {
		if output["source_sha256"] == "" || output["structure_generation"] == "" {
			t.Fatal("source version is missing")
		}
	}
	if legacy["source_sha256"] != tree["source_sha256"] || legacy["structure_generation"] == tree["structure_generation"] {
		t.Fatal("legacy and tree generations were not separated for the same source")
	}
	args := map[string]any{"path": "legacy.md", "source_sha256": legacy["source_sha256"], "structure_generation": legacy["structure_generation"]}
	resumed := treeToolOutput(t, hub, "document.outline", args)
	var structure document.StructureManifest
	decodeTreeValue(t, resumed["structure"], &structure)
	if structure.SchemaVersion != document.LegacyStructureSchemaVersion || resumed["structure_generation"] != legacy["structure_generation"] || resumed["structure_budget"] != nil || resumed["expanded_node_id"] != nil {
		t.Fatalf("legacy caller was upgraded to the tree: %+v", resumed)
	}
	args["all"] = true
	read := treeToolOutput(t, hub, "document.read_range", args)
	if read["generation_rebased"] == true || read["structure_generation"] != legacy["structure_generation"] || read["read_complete"] != true {
		t.Fatalf("legacy content scope was rebased: %+v", read)
	}
	args["structure_generation"] = tree["structure_generation"]
	if _, err := hub.Execute(t.Context(), "document.read_range", args, "session", "run"); err == nil {
		t.Fatal("legacy read silently accepted the tree generation")
	}
}

func treeToolOutput(t *testing.T, hub *ToolHub, tool string, args map[string]any) map[string]any {
	t.Helper()
	result, err := hub.Execute(t.Context(), tool, args, "session", "run")
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("%s returned %T", tool, result.Output)
	}
	return output
}

func treeOutlinePage(t *testing.T, output map[string]any) docindex.TreeOutlineResult {
	t.Helper()
	var page docindex.TreeOutlineResult
	content, ok := output["content"].(string)
	if !ok || json.Unmarshal([]byte(content), &page) != nil {
		t.Fatalf("outline content is not complete JSON: %+v", output)
	}
	return page
}

func decodeTreeValue(t *testing.T, value, target any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}

func assertTreeDiscoveryOnly(t *testing.T, page docindex.TreeOutlineResult) {
	t.Helper()
	if page.Coverage.Scope != "structure_discovery" || page.Coverage.TaskCoverage != "not_assessed" {
		t.Fatalf("navigation metadata became content coverage: %+v", page.Coverage)
	}
	for _, node := range page.Nodes {
		if node.Lead != nil && !node.Lead.IsPreview {
			t.Fatalf("lead was not marked as a preview: %+v", node)
		}
	}
}
