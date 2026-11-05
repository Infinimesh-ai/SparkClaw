package docindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func canonicalFixture(t *testing.T, units []Unit, info SourceInfo) (*Service, Target, *Index) {
	t.Helper()
	root := t.TempDir()
	writeSourceFile(t, root, "source.txt", "bound source")
	service := NewService(&fakeExtractor{units: units, info: info})
	t.Cleanup(service.Close)
	target := Target{WorkspaceRoot: root, RelPath: "source.txt", Format: "text"}
	index, err := service.Ensure(t.Context(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service, target, index
}

func TestCanonicalTraversalPreservesOriginalBytesAndRejectsForeignCursors(t *testing.T) {
	text := "  ＡＢＣ ⽅\t\r\n" + strings.Repeat("原文🙂\t", 4000) + "  END\r\n"
	_, target, index := canonicalFixture(t, []Unit{{Text: text, Block: "text.bytes[0:60000]"}, {Text: "\n\n", Block: "text.tail"}}, SourceInfo{})
	if index.Nodes[0].Text != text {
		t.Fatal("native text was normalized")
	}
	search, err := index.Search(SearchRequest{Keywords: []string{"abc"}})
	if err != nil || len(search.Hits) == 0 {
		t.Fatalf("search did not use normalization: %+v %v", search, err)
	}
	request := RangeRequest{All: true, MaxBytes: 1800, SourceSHA256: index.Manifest.SourceSHA256, StructureGeneration: index.Manifest.StructureGeneration}
	var got strings.Builder
	firstCursor := ""
	for count := 0; ; count++ {
		if count > 3000 {
			t.Fatal("traversal made no progress")
		}
		result, err := index.ReadRange(request)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range result.Nodes {
			got.WriteString(node.Text)
		}
		if result.NextCursor == "" {
			if result.Coverage.Status != "complete" || result.Coverage.Covered != 2 {
				t.Fatalf("final coverage %+v", result.Coverage)
			}
			break
		}
		if firstCursor == "" {
			firstCursor = result.NextCursor
		}
		if result.NextCursor == request.Cursor {
			t.Fatal("cursor did not advance")
		}
		request.Cursor = result.NextCursor
	}
	if got.String() != text+"\n\n" {
		t.Fatal("range continuation lost, normalized or duplicated original bytes")
	}
	changed := request
	changed.All = false
	changed.NodeIDs = []string{index.Nodes[0].ID}
	changed.Cursor = firstCursor
	if _, err := index.ReadRange(changed); err == nil {
		t.Fatal("cursor changed selection")
	}
	copy := *index
	copy.AccessScope = "another-workspace"
	request.Cursor = firstCursor
	if _, err := copy.ReadRange(request); err == nil {
		t.Fatal("cross-workspace cursor accepted")
	}
	foreign := NewService(&fakeExtractor{units: []Unit{{Text: "changed"}}})
	defer foreign.Close()
	writeSourceFile(t, target.WorkspaceRoot, "source.txt", "other source")
	other, err := foreign.Ensure(t.Context(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ReadRange(request); err == nil {
		t.Fatal("stale source cursor accepted")
	}
}

func TestCanonicalOutlinePaginatedAndTypedDependenciesRetained(t *testing.T) {
	units := []Unit{{Text: "Revenue\t(USD)", Block: "header", Kind: "table_row"}, {Text: "12.00", Block: "B2", Kind: "cell", Dependencies: []string{"header"}, Values: []map[string]any{{"value": 12.0, "formula": "SUM(B3:B4)", "cached_result": 12.0, "display_text": "12.00", "row_hidden": true}}}}
	for i := 0; i < 30; i++ {
		units = append(units, Unit{Text: "paragraph", Block: strings.Repeat("x", i+1)})
	}
	_, _, index := canonicalFixture(t, units, SourceInfo{})
	page, err := index.OutlinePage(OutlineRequest{Limit: 2, MaxBytes: 4096})
	if err != nil || len(page.Nodes) != 2 || page.NextCursor == "" {
		t.Fatalf("outline paging %+v %v", page, err)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err = index.OutlinePage(OutlineRequest{Cursor: cursor, Limit: 2, MaxBytes: 4096})
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range page.Nodes {
			if seen[node.ID] {
				t.Fatal("duplicate outline node")
			}
			seen[node.ID] = true
			if node.Text != "" || node.Values != nil {
				t.Fatal("outline exposed body")
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(index.Nodes) {
		t.Fatal("outline omitted nodes")
	}
	result, err := index.ReadRange(RangeRequest{NodeIDs: []string{index.Nodes[1].ID}, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Dependencies) != 1 || result.Dependencies[0].Text != "Revenue\t(USD)" || result.Nodes[0].Values[0]["formula"] != "SUM(B3:B4)" || result.Coverage.Covered != 1 {
		t.Fatalf("typed value or header missing %+v", result)
	}
}

type mutatingExtractor struct{}

func (mutatingExtractor) Extract(_ context.Context, path, _ string, emit func(Unit) error) (SourceInfo, error) {
	if err := emit(Unit{Text: "old version"}); err != nil {
		return SourceInfo{}, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return SourceInfo{}, err
	}
	return SourceInfo{}, os.WriteFile(path, []byte("new version"), 0600)
}
func TestStructureRejectsSourceMutationBeforePublishing(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "source.txt", "old version")
	old, _, _ := fileSHA256(filepath.Join(root, "source.txt"))
	service := NewService(mutatingExtractor{})
	defer service.Close()
	if _, err := service.Ensure(t.Context(), Target{WorkspaceRoot: root, RelPath: "source.txt", Format: "text"}, nil); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("mixed source accepted %v", err)
	}
	original, readErr := os.ReadFile(filepath.Join(root, "source.txt"))
	if readErr != nil || string(original) != "old version" {
		t.Fatal("parser modified governed original", readErr)
	}
	manifest, ok, _ := readManifest(Dir(root, old))
	if ok && manifest.State.Usable() {
		t.Fatal("mixed source index published")
	}
}

func TestOCRGenerationKeepsNativeNodesAndRejectsOldContinuation(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "scan.pdf", "PDF source")
	service := NewService(&fakeExtractor{units: []Unit{{Text: strings.Repeat("native ", 2000), Page: 1, Kind: "page", Block: "document.page[1]"}}, info: SourceInfo{Pages: 2, UnindexedPages: []int{2}}})
	defer service.Close()
	target := Target{WorkspaceRoot: root, RelPath: "scan.pdf", Format: "pdf", EnrichmentVersion: "provider-model-prompt-v1"}
	index, err := service.Ensure(t.Context(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Nodes) != 2 || index.Nodes[1].Availability != document.NodeUnrecognized {
		t.Fatal("missing physical page")
	}
	first, err := index.ReadRange(RangeRequest{All: true, MaxBytes: 1800})
	if err != nil || first.NextCursor == "" {
		t.Fatal(err)
	}
	target.SourceSHA256 = index.Manifest.SourceSHA256
	updated, err := service.RecognizedPages(t.Context(), target, map[int]string{2: "OCR page"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Nodes[0].ID != index.Nodes[0].ID || updated.Nodes[1].ID != index.Nodes[1].ID || updated.Manifest.StructureGeneration == index.Manifest.StructureGeneration {
		t.Fatal("enrichment renumbered native nodes or did not change generation")
	}
	if _, err := updated.ReadRange(RangeRequest{All: true, Cursor: first.NextCursor, MaxBytes: 1800}); err == nil {
		t.Fatal("old generation cursor accepted")
	}
	target.EnrichmentVersion = "provider-model-prompt-v2"
	fresh, err := service.Ensure(t.Context(), target, nil)
	if err != nil || fresh.Nodes[1].Availability != document.NodeUnrecognized {
		t.Fatalf("old OCR provider cache reused: %v", err)
	}
	writeSourceFile(t, root, "scan.pdf", "replaced source")
	if err := service.CacheRecognizedPages(target, map[int]string{2: "old OCR"}); err == nil {
		t.Fatal("OCR rebound to a replaced source")
	}
}

func TestStructureCacheReopensAndNeverSharesOwnershipByHash(t *testing.T) {
	service, target, index := canonicalFixture(t, []Unit{{Text: "private source", Block: "p1"}}, SourceInfo{})
	service.Close()
	reopened := NewService(&fakeExtractor{err: errors.New("must use existing structure")})
	defer reopened.Close()
	loaded, err := reopened.Ensure(t.Context(), target, nil)
	if err != nil || loaded.Nodes[0].ID != index.Nodes[0].ID {
		t.Fatal("cache cannot reopen", err)
	}
	otherRoot := t.TempDir()
	writeSourceFile(t, otherRoot, "source.txt", "bound source")
	if _, err := reopened.Ensure(t.Context(), Target{WorkspaceRoot: otherRoot, RelPath: "source.txt", Format: "text"}, nil); err == nil {
		t.Fatal("other workspace borrowed private index")
	}
}

func TestChunkReadsStartAtHitAndResolveTransitiveDependencies(t *testing.T) {
	_, _, index := canonicalFixture(t, []Unit{{Text: strings.Repeat("x", 7000) + "late needle", Block: "long-page"}}, SourceInfo{})
	chunk := index.Chunks[len(index.Chunks)-1]
	read, err := index.ReadRange(RangeRequest{ChunkIDs: []string{chunk.ID}, MaxBytes: 1800})
	if err != nil {
		t.Fatal(err)
	}
	if read.Nodes[0].ByteStart != chunk.ByteStart || read.Nodes[0].Text != "late needle" || read.Nodes[0].WholeNode {
		t.Fatalf("target chunk displaced by prefix: %+v", read)
	}
	if jsonBytes(read) > 1800 {
		t.Fatal("read JSON exceeds budget")
	}
	if _, err := index.ReadRange(RangeRequest{ChunkIDs: []string{index.Chunks[0].ID, chunk.ID}, MaxBytes: 8192}); err == nil {
		t.Fatal("noncontiguous source ranges silently expanded")
	}
	_, _, table := canonicalFixture(t, []Unit{{Text: "Revenue (USD)", Block: "A1"}, {Text: "", Block: "B1", Dependencies: []string{"A1"}}, {Text: "12", Block: "B2", Dependencies: []string{"B1"}}}, SourceInfo{})
	value, err := table.ReadRange(RangeRequest{NodeIDs: []string{table.Nodes[2].ID}, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Dependencies) != 2 || value.Dependencies[0].Text != "Revenue (USD)" {
		t.Fatalf("merged header dependency missing %+v", value.Dependencies)
	}
}

func TestCursorOnlySurvivesCacheEvictionAndBudgetChanges(t *testing.T) {
	units := []Unit{{Text: strings.Repeat("original byte sequence ", 1000), Block: "long"}}
	service, target, index := canonicalFixture(t, units, SourceInfo{})
	first, err := index.ReadRange(RangeRequest{All: true, NodeIDs: []string{}, ChunkIDs: []string{}, MaxBytes: 4096})
	if err != nil || first.NextCursor == "" {
		t.Fatal(err)
	}
	service.Close()
	if err := os.RemoveAll(Dir(target.WorkspaceRoot, index.Manifest.SourceSHA256)); err != nil {
		t.Fatal(err)
	}
	nextService := NewService(&fakeExtractor{units: units})
	defer nextService.Close()
	next, err := nextService.Ensure(t.Context(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Manifest.StructureGeneration != index.Manifest.StructureGeneration {
		t.Fatal("unchanged source rebuild changed generation")
	}
	continued, err := next.ReadRange(RangeRequest{Cursor: first.NextCursor, MaxBytes: 8192})
	if err != nil {
		t.Fatal(err)
	}
	if continued.Nodes[0].ByteStart != first.Nodes[len(first.Nodes)-1].ByteEnd {
		t.Fatal("continuation skipped source bytes")
	}
	if jsonBytes(continued) > 8192 {
		t.Fatal("read JSON exceeds new budget")
	}
}
