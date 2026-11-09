package toolhub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func modelViewTreeIndex(t *testing.T, name, source string) (*ToolHub, *docindex.Index) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	hub := newRetrievalTestHub(t, root)
	index, _, err := hub.ensureDocumentIndex(docindex.WithStructureVersion(t.Context(), 2), filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return hub, index
}

func TestSummaryContract5CarriesContextOnceWithoutNodeIDs(t *testing.T) {
	var source strings.Builder
	source.WriteString("# Checks\n\n## Final review\n\n")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&source, "Item %02d must be verified before release.\n\n", i)
	}
	_, index := modelViewTreeIndex(t, "checks.md", source.String())
	units := index.SummaryUnits(documentSummaryMaxUnits)
	if len(units) == 0 {
		t.Fatal("no summary units")
	}
	legacy, missing := scopedSummaryParts(index, units[0], 1<<20)
	view, viewMissing := scopedSummaryViewParts(index, units[0], 1<<20)
	if len(missing) != 0 || len(viewMissing) != 0 || len(view) != 1 {
		t.Fatalf("parts=%d missing=%v/%v", len(view), missing, viewMissing)
	}
	body := view[0]
	if strings.Count(body, "## Final review") != 1 || !strings.Contains(body, "Item 01") || !strings.Contains(body, "Item 40") {
		t.Fatalf("contract 5 lost source or repeated context:\n%s", body)
	}
	for _, node := range index.Nodes {
		if strings.Contains(body, node.ID) {
			t.Fatalf("node ID %s reached the section model", node.ID)
		}
	}
	if len(body)*3 > len(strings.Join(legacy, "")) {
		t.Fatalf("contract 5 (%d bytes) is not materially smaller than contract 4 (%d bytes)", len(body), len(strings.Join(legacy, "")))
	}
}

func TestSummaryContract5KeepsTypedValuesAtomicAndSplitsLongText(t *testing.T) {
	index := &docindex.Index{Nodes: []document.StructureNode{
		{ID: "header", Text: "Amount in EUR", Kind: "heading_label", Title: "Amount in EUR", Locator: document.StructureLocator{Block: "sheet[1]"}},
		{ID: "cached", Text: "42", Kind: "cell", Order: 1, Locator: document.StructureLocator{Block: "sheet[1].cell[B2]"}, Values: []map[string]any{{"formula": "SUM(A1:A3)", "cached_result": 42, "formula_result_recalculated": false}}, Dependencies: []string{"header"}},
		{ID: "empty", Text: "", Kind: "cell", Order: 2, Locator: document.StructureLocator{Block: "sheet[1].cell[B3]"}, Values: []map[string]any{{"formula": "A1+A2", "cached_result_available": false, "formula_result_recalculated": false}}, Dependencies: []string{"header"}},
	}, Chunks: []docindex.Chunk{{NodeID: "header", Text: "Amount in EUR"}, {NodeID: "cached", Text: "42"}}}
	unit := docindex.SummaryUnit{ID: "sheet", ChunkStart: 0, ChunkEnd: 2}
	parts, missing := scopedSummaryViewParts(index, unit, 2000)
	body := strings.Join(parts, "\n")
	for _, marker := range []string{"A1+A2", `"cached_result_available":false`, `"formula_result_recalculated":false`, "Amount in EUR [sheet[1]]"} {
		if len(missing) != 0 || !strings.Contains(body, marker) {
			t.Fatalf("typed source metadata lost %q: %s missing=%v", marker, body, missing)
		}
	}
	if _, missing = scopedSummaryViewParts(index, unit, 40); len(missing) != 2 {
		t.Fatalf("oversized typed bundles were not reported: %v", missing)
	}

	long := strings.Repeat("长句子说明限制。", 60)
	index = &docindex.Index{Nodes: []document.StructureNode{
		{ID: "h", Kind: "heading_label", Title: "限制", Locator: document.StructureLocator{Block: "document.p[1]"}},
		{ID: "p", Kind: "paragraph", Order: 1, Text: long, Dependencies: []string{"h"}, Locator: document.StructureLocator{Block: "document.p[2]"}},
	}, Chunks: []docindex.Chunk{{NodeID: "h"}, {NodeID: "p"}}}
	parts, missing = scopedSummaryViewParts(index, docindex.SummaryUnit{ID: "u", ChunkEnd: 2}, 400)
	if len(missing) != 0 || len(parts) < 2 {
		t.Fatalf("long text was not split: parts=%d missing=%v", len(parts), missing)
	}
	joined := ""
	for _, part := range parts {
		if len(part) > 400 || !strings.Contains(part, "# 限制 [document.p[1]]") || !strings.Contains(part, "partial text bytes") {
			t.Fatalf("split part lost bound, context or partial marker: %s", part)
		}
		line := part[strings.LastIndex(part, "] ")+2:]
		joined += line
	}
	if joined != long {
		t.Fatal("split parts do not reassemble the source text")
	}
}

func TestOutlineModelViewKeepsHandlesAndDeliveredEstimate(t *testing.T) {
	const source = "# Selected\n\nSELECTED_BODY.\n\n## Child\n\nCHILD_LAST_DETAIL.\n\n# Other\n\nUNSELECTED_BODY.\n"
	hub, index := modelViewTreeIndex(t, "tree.md", source)
	output := treeToolOutput(t, hub, "document.outline", map[string]any{"path": "tree.md", "structure_version": 2, "depth": 2, "model_view": true})
	content, _ := output["content"].(string)
	var root, selected document.StructureNode
	for _, node := range index.Nodes {
		if node.Kind == "document" {
			root = node
		}
		if node.Kind == "section" && node.Title == "Selected" {
			selected = node
		}
	}
	rootBytes, _ := index.ModelViewBytes(root)
	selectedBytes, _ := index.ModelViewBytes(selected)
	for _, want := range []string{
		"Expanded id=" + root.ID,
		fmt.Sprintf("read_tokens~%d", document.ModelViewTokens(rootBytes)),
		"- id=" + selected.ID + ` section "Selected"`,
		fmt.Sprintf("read_tokens~%d", document.ModelViewTokens(selectedBytes)),
		"  - id=",
		"Discovery coverage: complete",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("outline model view missing %q:\n%s", want, content)
		}
	}
	for _, leaked := range []string{"parent_id", "ordinal_start", "structure_source", `"unrecognized":0`, `"stats"`} {
		if strings.Contains(content, leaked) {
			t.Fatalf("outline model view leaked %q:\n%s", leaked, content)
		}
	}
	// The structured output remains the complete program record.
	if output["outline"] == nil || output["structure_budget"] == nil {
		t.Fatal("structured outline record was removed")
	}
	plain := treeToolOutput(t, hub, "document.outline", map[string]any{"path": "tree.md", "structure_version": 2, "depth": 2})
	treeOutlinePage(t, plain)
}

func TestModelViewLocationsDistinguishTopLevelFromFirstHeading(t *testing.T) {
	const source = "# 第21节\n\n旧清单。\n\n# 附录甲\n\nOrbit 仅用于隔离环境。\n\n# 快速复习\n\n## 最后的自我检查\n\n新清单。\n"
	_, index := modelViewTreeIndex(t, "order.md", source)
	ids := []string{}
	var first document.StructureNode
	for _, node := range index.Nodes {
		if node.Kind == "section" && node.Title == "快速复习" {
			ids = append(ids, node.ID)
		}
		if node.Kind == "section" && node.Title == "第21节" {
			first = node
		}
	}
	legacy := summarySourceLocations(index, nil)
	aware := sourceLocations(index, nil, true)
	var legacyParent, awareParent any
	var top bool
	for i, location := range legacy {
		if location["title"] == "快速复习" {
			legacyParent, awareParent = location["parent_block"], aware[i]["parent_block"]
			top, _ = aware[i]["top_level"].(bool)
		}
	}
	if legacyParent != first.Locator.Block {
		t.Fatalf("legacy map changed: parent=%v", legacyParent)
	}
	if awareParent != "" || !top {
		t.Fatalf("model-view map still names the root block as a parent: %v top=%v", awareParent, top)
	}
	rendered := document.ModelViewSourceLocations(aware)
	if !strings.Contains(rendered, "parent_block=none(top-level heading)") {
		t.Fatalf("top-level marker missing:\n%s", rendered)
	}
	relations := strings.Join(sourceLocationRelations(index, ids, true), "\n")
	if strings.Contains(relations, `"Document"`) || !strings.Contains(relations, "is a top-level heading") {
		t.Fatalf("relations still treat the root as a heading:\n%s", relations)
	}
	if !strings.Contains(strings.Join(selectedSourceLocationRelations(index, ids), "\n"), `"Document"`) {
		t.Fatal("legacy relations changed")
	}
}
