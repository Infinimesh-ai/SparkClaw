package docindex

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func treeFixture(t *testing.T, format string, units []Unit, info SourceInfo) (*Service, Target, *Index) {
	t.Helper()
	root := t.TempDir()
	writeSourceFile(t, root, "source."+format, "source bound fixture")
	s := NewService(&fakeExtractor{units: units, info: info})
	t.Cleanup(s.Close)
	target := Target{WorkspaceRoot: root, RelPath: "source." + format, Format: format, StructureVersion: 2}
	index, err := s.Ensure(t.Context(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, target, index
}
func treeLeaves(index *Index) []document.StructureNode {
	out := []document.StructureNode{}
	for _, node := range index.Nodes {
		if !node.IsContainer() {
			out = append(out, node)
		}
	}
	return out
}

func TestStructureTreeNativeHierarchyAndCanonicalConsumers(t *testing.T) {
	units := []Unit{
		{Block: "document.p[1]", Text: "Preface."},
		{Block: "document.p[2]", Text: "Same title", HeadingLevel: 1, StructureSource: "native_style"},
		{Block: "document.p[3]", Text: "First explanation. Further details."},
		{Block: "document.p[4]", Text: "Same title", HeadingLevel: 2, StructureSource: "outline_level"},
		{Block: "document.p[5]", Text: "Nested body"},
		{Block: "document.p[6]", Text: "Same title", HeadingLevel: 1, StructureSource: "native_style"},
		{Block: "document.p[7]", Text: "Last explanation"},
	}
	_, _, index := treeFixture(t, "docx", units, SourceInfo{})
	if index.Structure.SchemaVersion != 2 || index.Structure.ParserVersion != "structure_tree_v1" || index.Structure.Coverage.Required != len(units) {
		t.Fatalf("wrong contract %+v", index.Structure)
	}
	leaves := treeLeaves(index)
	for i, leaf := range leaves {
		if leaf.Text != units[i].Text || leaf.Locator.Block != units[i].Block {
			t.Fatal("source order or bytes changed")
		}
	}
	sections := []document.StructureNode{}
	byID := map[string]document.StructureNode{}
	for _, node := range index.Nodes {
		byID[node.ID] = node
		if node.Kind == "section" {
			sections = append(sections, node)
		}
	}
	if len(sections) != 3 || sections[0].ID == sections[2].ID || sections[1].ParentID != sections[0].ID || sections[1].StructureSource != "outline_level" {
		t.Fatalf("wrong native hierarchy %+v", sections)
	}
	if sections[0].Lead == nil || sections[0].Lead.Text != "First explanation." || !sections[0].Lead.IsPreview {
		t.Fatal("lead is not the bounded first body sentence")
	}
	top := index.SummaryUnits(16)
	if len(top) != 3 || byID[top[0].ID].Kind != "front_matter" || top[1].ID != sections[0].ID || top[2].ID != sections[2].ID {
		t.Fatalf("summary used a second sectioning %+v", top)
	}
	search, err := index.Search(SearchRequest{Keywords: []string{"Same title"}, TopK: 3})
	if err != nil || len(search.Sections) != 3 {
		t.Fatal(search, err)
	}
	for _, hit := range search.Sections {
		if !byID[hit.ID].IsContainer() {
			t.Fatal("search returned non-tree section ID")
		}
	}
	read, err := index.ReadRange(RangeRequest{NodeIDs: []string{sections[0].ID}, IncludeDescendants: true, MaxBytes: 64000})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Nodes) != 4 || read.Coverage.Required != 4 || read.Coverage.Covered != 4 || strings.Contains(read.Text, "Last explanation") {
		t.Fatal("container selection did not acquire exactly its subtree", read)
	}
	legacySelector, err := index.ReadRange(RangeRequest{Section: sections[0].ID, MaxBytes: 64000})
	if err != nil || legacySelector.Text != read.Text {
		t.Fatal("section projection differs from subtree", err)
	}
	if _, err := index.ReadRange(RangeRequest{NodeIDs: []string{sections[0].ID}}); err == nil {
		t.Fatal("container counted as content without descendants")
	}
}

func TestTreeBookmarksOwnPagesWithoutDuplicateCoverage(t *testing.T) {
	units := []Unit{}
	for i := 1; i <= 8; i++ {
		units = append(units, Unit{Page: i, Kind: "page", Block: fmt.Sprintf("document.page[%d]", i), Text: fmt.Sprintf("page %d", i)})
	}
	_, _, index := treeFixture(t, "pdf", units, SourceInfo{Pages: 8, Outline: []OutlineEntry{{Title: "Part", Level: 1, Page: 2}, {Title: "Chapter", Level: 2, Page: 3}, {Title: "Repeated", Level: 1, Page: 6}, {Title: "Repeated", Level: 1, Page: 6}}})
	if len(treeLeaves(index)) != 8 || index.Structure.Coverage.Required != 8 {
		t.Fatal("bookmark page overlap changed coverage")
	}
	bookmarks := []document.StructureNode{}
	for _, node := range index.Nodes {
		if node.Kind == "bookmark" {
			bookmarks = append(bookmarks, node)
		}
	}
	if len(bookmarks) != 4 || bookmarks[0].Span.PageStart != 2 || bookmarks[0].Span.PageEnd != 5 || bookmarks[1].ParentID != bookmarks[0].ID || bookmarks[2].Stats.Leaves != 0 || bookmarks[2].ID == bookmarks[3].ID {
		t.Fatalf("bad bookmarks %+v", bookmarks)
	}
	read, err := index.ReadRange(RangeRequest{NodeIDs: []string{bookmarks[0].ID}, IncludeDescendants: true})
	if err != nil || read.Coverage.Required != 4 || strings.Contains(read.Text, "page 6") {
		t.Fatal(read, err)
	}
}

func TestTreeWholeExtentAndEveryExpansionWithinBudget(t *testing.T) {
	for _, format := range []string{"docx", "pptx", "pdf"} {
		t.Run(format, func(t *testing.T) {
			units := []Unit{}
			count := 300
			if format == "pdf" {
				count = 500
			}
			for i := 1; i <= count; i++ {
				unit := Unit{Block: fmt.Sprintf("document.p[%d]", i), Text: fmt.Sprintf("第%d段开头。%s", i, strings.Repeat("content ", 40))}
				if format == "pdf" {
					unit.Page = i
					unit.Kind = "page"
				}
				if format == "pptx" {
					unit.Kind = "slide"
					unit.HeadingLevel = 1
					unit.Title = fmt.Sprintf("Slide %d", i)
					unit.Page = i
				}
				units = append(units, unit)
			}
			_, _, index := treeFixture(t, format, units, SourceInfo{Pages: func() int {
				if format == "docx" {
					return 0
				}
				return count
			}()})
			budget := 6000
			root, err := index.OutlineTree(OutlineRequest{MaxBytes: budget, Depth: 1, SharePercent: 25})
			if err != nil {
				t.Fatal(err)
			}
			if jsonBytes(root) > budget || root.NextCursor != "" || root.Coverage.Scope != "structure_discovery" || root.Coverage.TaskCoverage != "not_assessed" {
				t.Fatal("invalid root budget or coverage")
			}
			end := 0
			for _, node := range root.Nodes {
				if node.Span == nil || node.Span.OrdinalStart != end {
					t.Fatal("first call omits some extent", node)
				}
				end = node.Span.OrdinalEnd
			}
			if end != count {
				t.Fatalf("root only shows %d/%d", end, count)
			}
			seen := map[string]bool{}
			visits := map[string]bool{}
			var walk func(string)
			walk = func(id string) {
				if visits[id] {
					t.Fatalf("expansion made no progress: %s", id)
				}
				visits[id] = true
				cursor := ""
				for {
					page, err := index.OutlineTree(OutlineRequest{NodeID: id, Depth: 1, Cursor: cursor, MaxBytes: budget, Limit: 5})
					if err != nil {
						t.Fatal(err)
					}
					if jsonBytes(page) > budget {
						t.Fatal("unbounded expansion")
					}
					for _, node := range page.Nodes {
						if node.Span != nil {
							walk(node.ID)
						} else {
							if seen[node.ID] {
								t.Fatal("duplicate leaf")
							}
							seen[node.ID] = true
							if node.Preview != nil && !node.Preview.IsPreview {
								t.Fatal("preview claimed evidence")
							}
						}
					}
					if page.NextCursor == "" {
						break
					}
					if page.NextCursor == cursor {
						t.Fatal("cursor stalled")
					}
					cursor = page.NextCursor
				}
			}
			walk(root.Node.ID)
			if len(seen) != count {
				t.Fatalf("expanded %d/%d", len(seen), count)
			}
			first := root.Nodes[0]
			read, err := index.ReadRange(RangeRequest{NodeIDs: []string{first.ID}, IncludeDescendants: true, MaxBytes: 4096})
			if err != nil || read.Coverage.Required != first.Stats.Leaves {
				t.Fatal("navigation window could not select source span", err)
			}
			for read.NextCursor != "" {
				read, err = index.ReadRange(RangeRequest{Cursor: read.NextCursor, MaxBytes: 6000})
				if err != nil {
					t.Fatal(err)
				}
			}
			if read.Coverage.Status != "complete" {
				t.Fatal("container cursor walk incomplete")
			}
		})
	}
}

func TestTreeUniformLeadsAndLeafPreviews(t *testing.T) {
	units := []Unit{}
	for i := 0; i < 8; i++ {
		units = append(units, Unit{Block: fmt.Sprintf("h%d", i), Text: "Chapter", HeadingLevel: 1}, Unit{Block: fmt.Sprintf("p%d", i), Text: strings.Repeat("句子开头内容", 40)})
	}
	_, _, index := treeFixture(t, "docx", units, SourceInfo{})
	with, err := index.OutlineTree(OutlineRequest{Depth: 1, MaxBytes: 64000})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range with.Nodes {
		if node.Lead == nil || len([]rune(node.Lead.Text)) > leadRunes {
			t.Fatal("missing or unbounded level lead")
		}
	}
	without := with
	without.Nodes = append([]OutlineViewNode(nil), with.Nodes...)
	for i := range without.Nodes {
		without.Nodes[i].Lead = nil
	}
	narrow, err := index.OutlineTree(OutlineRequest{Depth: 1, MaxBytes: jsonBytes(without) + 30})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range narrow.Nodes {
		if node.Lead != nil {
			t.Fatal("leads included for only some of a level")
		}
	}
	if !narrow.Budget.LeadsOmitted {
		t.Fatal("lead degradation not recorded")
	}
	leaf, err := index.OutlineTree(OutlineRequest{NodeID: with.Nodes[0].ID, Depth: 1, MaxBytes: 64000, Limit: 1})
	if err != nil || leaf.NextCursor == "" {
		t.Fatal("leaf cursor missing", err)
	}
	if _, err := index.OutlineTree(OutlineRequest{NodeID: with.Nodes[1].ID, Cursor: leaf.NextCursor, MaxBytes: 64000}); err == nil {
		t.Fatal("cursor crossed containers")
	}
}

func TestTreeAtomicPhysicalGroupsAndNoHeadingInference(t *testing.T) {
	units := []Unit{{Text: "一、Heading-looking body", Block: "p1"}}
	for i := 1; i <= 80; i++ {
		units = append(units, Unit{Text: strings.Repeat("row ", 200), Kind: "table_row", Block: fmt.Sprintf("document.table[1].row[%d]", i), GroupKey: "table1", Dependencies: func() []string {
			if i > 1 {
				return []string{"document.table[1].row[1]"}
			}
			return nil
		}()})
	}
	_, _, index := treeFixture(t, "docx", units, SourceInfo{})
	parents := map[string]bool{}
	for _, node := range index.Nodes {
		if node.Kind == "section" {
			t.Fatal("body text promoted to heading")
		}
		if node.Kind == "table_row" {
			parents[node.ParentID] = true
		}
	}
	if len(parents) != 1 {
		t.Fatal("physical grouping split a table")
	}
	// An oversize atomic table can still paginate previews, without losing rows.
	for id := range parents {
		page, err := index.OutlineTree(OutlineRequest{NodeID: id, MaxBytes: 4000})
		if err != nil || page.NextCursor == "" || !page.Budget.PreviewsTrimmed {
			t.Fatal(page, err)
		}
	}
}

func TestTreeAndLegacyCachesCoexistAcrossColdResume(t *testing.T) {
	s, target, tree := treeFixture(t, "text", []Unit{{Text: "Heading", Block: "h", HeadingLevel: 1}, {Text: strings.Repeat("body ", 2000), Block: "p"}}, SourceInfo{})
	legacyTarget := target
	legacyTarget.StructureVersion = 1
	legacy, err := s.Ensure(t.Context(), legacyTarget, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := legacy.ReadRange(RangeRequest{All: true, MaxBytes: 2000})
	if err != nil || before.NextCursor == "" {
		t.Fatal(err)
	}
	if legacy.Manifest.StructureGeneration == tree.Manifest.StructureGeneration {
		t.Fatal("legacy generation rebased")
	}
	s.Close()
	reopened := NewService(&fakeExtractor{err: fmt.Errorf("must reopen both caches")})
	defer reopened.Close()
	for _, target := range []Target{target, legacyTarget} {
		loaded, err := reopened.Ensure(t.Context(), target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if target.StructureVersion == 1 {
			next, err := loaded.ReadRange(RangeRequest{Cursor: before.NextCursor, MaxBytes: 4000})
			previous := before.Nodes[len(before.Nodes)-1]
			wantStart := 0
			if next.Nodes[0].ID == previous.ID {
				wantStart = previous.ByteEnd
			}
			if err != nil || next.Nodes[0].ByteStart != wantStart {
				t.Fatal("legacy cursor lost", err)
			}
		} else if _, err := loaded.ReadRange(RangeRequest{Cursor: before.NextCursor}); err == nil {
			t.Fatal("legacy cursor accepted by tree")
		}
	}
}

func TestTreeUniformPreviewsDuringAutoDeepening(t *testing.T) {
	units := []Unit{{HeadingLevel: 1, Block: "h", Text: "Heading"}}
	for i := 0; i < 5; i++ {
		units = append(units, Unit{Block: fmt.Sprintf("p%d", i), Text: strings.Repeat("long preview text ", 20)})
	}
	_, _, index := treeFixture(t, "docx", units, SourceInfo{})
	whole, err := index.OutlineTree(OutlineRequest{MaxBytes: 64000})
	if err != nil {
		t.Fatal(err)
	}
	minimal := whole
	minimal.Nodes = append([]OutlineViewNode(nil), whole.Nodes...)
	for i := range minimal.Nodes {
		minimal.Nodes[i].Preview = nil
	}
	budget := jsonBytes(minimal) + 10
	result, err := index.OutlineTree(OutlineRequest{MaxBytes: budget})
	if err != nil || len(result.Nodes) != len(whole.Nodes) || !result.Budget.PreviewsTrimmed || jsonBytes(result) > budget {
		t.Fatalf("whole leaf level lost instead of trimming uniformly: %+v %v", result, err)
	}
}

func TestTreePDFContainersStableAcrossOCRSizeGrowth(t *testing.T) {
	units := []Unit{}
	pending := []int{}
	for i := 1; i <= 60; i++ {
		units = append(units, Unit{Kind: "page", Block: fmt.Sprintf("document.page[%d]", i), Page: i, Availability: document.NodeUnrecognized})
		pending = append(pending, i)
	}
	s, target, index := treeFixture(t, "pdf", units, SourceInfo{Pages: 60, UnindexedPages: pending, Outline: []OutlineEntry{{Title: "First", Level: 1, Page: 1}, {Title: "Second", Level: 1, Page: 11}, {Title: "Third", Level: 1, Page: 40}}})
	before := map[string]document.StructureSpan{}
	for _, node := range index.Nodes {
		if node.IsContainer() {
			before[node.ID] = *node.Span
		}
	}
	ocr := map[int]string{}
	for _, page := range pending {
		ocr[page] = strings.Repeat("recognized text ", 3000)
	}
	after, err := s.RecognizedPages(t.Context(), target, ocr)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, node := range after.Nodes {
		if node.IsContainer() {
			span, exists := before[node.ID]
			if !exists || span != *node.Span {
				t.Fatal("OCR changed a selected container span", node)
			}
			count++
		}
	}
	if count != len(before) {
		t.Fatal("OCR inserted navigation containers")
	}
}

func TestTreeRejectsMalformedSpansAndEmptySources(t *testing.T) {
	if _, _, _, err := buildStructureTree("source", "docx", "", nil, SourceInfo{}); err == nil {
		t.Fatal("empty parser result published a usable tree")
	}
	units, nodes, _, err := buildStructureTree("source", "docx", "", []Unit{{Text: "title", Block: "h", HeadingLevel: 1}, {Text: "body", Block: "p"}}, SourceInfo{})
	if err != nil {
		t.Fatal(err)
	}
	span := *nodes[0].Span
	span.OrdinalEnd++
	nodes[0].Span = &span
	if err := validateStructureTree(nodes, units); err == nil {
		t.Fatal("bad union span accepted")
	}
}

func TestTreeRowWindowsReadExternalHeaderDependencyClosure(t *testing.T) {
	units := []Unit{{Kind: "sheet", HeadingLevel: 1, Text: "Data", Title: "Data", Block: "sheet"}}
	for row := 1; row <= 80; row++ {
		for col := 1; col <= 2; col++ {
			u := Unit{Kind: "cell", Text: fmt.Sprintf("row %d col %d", row, col), Block: fmt.Sprintf("cell[%d,%d]", row, col), GroupKey: fmt.Sprint(row)}
			if row > 1 {
				u.Dependencies = []string{fmt.Sprintf("cell[1,%d]", col)}
			}
			units = append(units, u)
		}
	}
	_, _, index := treeFixture(t, "xlsx", units, SourceInfo{})
	var last document.StructureNode
	for _, node := range index.Nodes {
		if node.Kind == "group" {
			last = node
		}
	}
	read, err := index.ReadRange(RangeRequest{NodeIDs: []string{last.ID}, IncludeDescendants: true, MaxBytes: 64000})
	if err != nil {
		t.Fatal(err)
	}
	deps := map[string]bool{}
	for _, node := range read.Dependencies {
		deps[node.Locator.Block] = true
	}
	if !deps["sheet"] || !deps["cell[1,1]"] || !deps["cell[1,2]"] || read.Coverage.Required != last.Stats.Leaves {
		t.Fatal("row windows lost external context or counted it as duplicate ownership", read)
	}
	parents := map[string]string{}
	for _, node := range treeLeaves(index) {
		if strings.HasPrefix(node.Locator.Block, "cell[") {
			var row, col int
			fmt.Sscanf(node.Locator.Block, "cell[%d,%d]", &row, &col)
			key := fmt.Sprint(row)
			if p := parents[key]; p != "" && p != node.ParentID {
				t.Fatal("row split across groups")
			}
			parents[key] = node.ParentID
		}
	}
}
