package docindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeExtractor struct {
	units []Unit
	info  SourceInfo
	err   error
	calls atomic.Int32
	gate  chan struct{}
}

func (f *fakeExtractor) Extract(ctx context.Context, _, _ string, emit func(Unit) error) (SourceInfo, error) {
	f.calls.Add(1)
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return SourceInfo{}, ctx.Err()
		}
	}
	if f.err != nil {
		return SourceInfo{}, f.err
	}
	for _, unit := range f.units {
		if err := emit(unit); err != nil {
			return SourceInfo{}, err
		}
	}
	return f.info, nil
}

func bookUnits() []Unit {
	units := []Unit{}
	topics := []string{"物理层与传输介质", "数据链路层与以太网", "网络层与路由选择", "传输层与拥塞控制", "应用层与域名系统"}
	for chapter, topic := range topics {
		units = append(units, Unit{Text: fmt.Sprintf("第%d章 %s", chapter+1, topic), HeadingLevel: 1, Page: chapter*20 + 1})
		for paragraph := 0; paragraph < 12; paragraph++ {
			units = append(units, Unit{
				Text: strings.Repeat(fmt.Sprintf("本章讨论%s的基本原理和典型协议。", topic), 6),
				Page: chapter*20 + 1 + paragraph,
			})
		}
	}
	units = append(units, Unit{Text: "TCP 使用慢开始和拥塞避免算法控制发送窗口。", Page: 70})
	return units
}

func writeSourceFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTermsUseHanBigramsAndLowercaseWords(t *testing.T) {
	got := Terms("TCP拥塞控制, IPv6!")
	want := []string{"tcp", "拥塞", "塞控", "控制", "ipv6"}
	if !slices.Equal(got, want) {
		t.Fatalf("Terms = %v, want %v", got, want)
	}
	if got := Terms("网"); !slices.Equal(got, []string{"网"}) {
		t.Fatalf("lone Han character = %v", got)
	}
}

func TestAssemblerSplitsLongTextWithoutChangingSourceBytes(t *testing.T) {
	a := &assembler{}
	a.add(Unit{Text: "标题", HeadingLevel: 1, Page: 1})
	a.add(Unit{Text: strings.Repeat("甲", 500), Page: 1})
	a.add(Unit{Text: strings.Repeat("乙", 950), Page: 2})
	var long strings.Builder
	for index := 0; long.Len() < 3500*3; index++ {
		fmt.Fprintf(&long, "丙%04d", index)
	}
	a.add(Unit{Text: long.String(), Page: 3})
	chunks, outline := a.finish(SourceInfo{})
	if len(chunks) < 6 || len(chunks) > 12 {
		t.Fatalf("unexpected chunk count %d", len(chunks))
	}
	for index, chunk := range chunks {
		if runes := len([]rune(chunk.Text)); runes > chunkTargetRunes+1 {
			t.Fatalf("chunk %d has %d runes", index, runes)
		}
		if index > 0 && chunks[index].Text == chunks[index-1].Text {
			t.Fatalf("chunk %d duplicates its predecessor", index)
		}
	}
	if !strings.HasPrefix(chunks[0].Text, "标题") || len(outline) != 1 || outline[0].ChunkEnd != len(chunks) {
		t.Fatalf("heading did not open the first chunk or own every chunk: %+v", outline)
	}
	if chunks[len(chunks)-1].Anchor.Page != 3 {
		t.Fatalf("last chunk anchor = %+v", chunks[len(chunks)-1].Anchor)
	}
}

func TestPageOutlineStampsHeadingPathsByPage(t *testing.T) {
	a := &assembler{}
	for page := 1; page <= 6; page++ {
		a.add(Unit{Text: strings.Repeat(fmt.Sprintf("第%d页内容。", page), 120), Page: page})
	}
	chunks, outline := a.finish(SourceInfo{Pages: 6, Outline: []OutlineEntry{
		{Title: "第1章 概述", Level: 1, Page: 1}, {Title: "1.1 定义", Level: 2, Page: 2}, {Title: "第2章 物理层", Level: 1, Page: 4},
	}})
	if len(outline) != 3 || outline[1].Level != 2 || outline[0].ChunkEnd != outline[2].ChunkStart {
		t.Fatalf("outline ranges are wrong: %+v", outline)
	}
	last := chunks[len(chunks)-1]
	if !slices.Equal(last.Anchor.HeadingPath, []string{"第2章 物理层"}) {
		t.Fatalf("last chunk heading path = %v", last.Anchor.HeadingPath)
	}
}

func newTestIndex(t *testing.T, units []Unit) *Index {
	t.Helper()
	units, nodes, structure := buildStructure("test-source", "text", "", units, SourceInfo{})
	a := &assembler{}
	for _, unit := range units {
		a.add(unit)
	}
	chunks, outline := a.finish(SourceInfo{})
	return &Index{Manifest: Manifest{SourceSHA256: "test-source", StructureGeneration: structure.StructureGeneration}, Structure: structure, Nodes: nodes, Outline: outline, Chunks: chunks, keywords: buildKeywordIndex(chunks), cursorSecret: "fixture"}
}

func TestSearchRanksTheMatchingSectionFirstAndHonoursSections(t *testing.T) {
	index := newTestIndex(t, bookUnits())
	result, err := index.Search(SearchRequest{Keywords: []string{"拥塞控制"}, TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) == 0 || !strings.Contains(strings.Join(result.Hits[0].Anchor.HeadingPath, ""), "传输层") {
		t.Fatalf("top hit is not in the transport chapter: %+v", result.Hits)
	}
	if !strings.Contains(result.Hits[0].Snippet, "拥塞") {
		t.Fatalf("snippet does not show the match: %q", result.Hits[0].Snippet)
	}
	scoped, err := index.Search(SearchRequest{Keywords: []string{"拥塞控制"}, Section: index.Outline[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Hits) != 0 {
		t.Fatalf("search leaked outside the requested section: %+v", scoped.Hits)
	}
	if _, err := index.Search(SearchRequest{Keywords: []string{"，。"}}); err == nil {
		t.Fatal("punctuation-only keywords were accepted")
	}
	bounded, _ := index.Search(SearchRequest{Keywords: []string{"协议"}, TopK: 20, MaxBytes: 1200})
	if !bounded.Truncated || len(bounded.Hits) == 0 || len(bounded.Hits) >= 20 {
		t.Fatalf("search byte bound was not applied: %d hits truncated=%t", len(bounded.Hits), bounded.Truncated)
	}
}

func TestReadRangeMergesNeighboursAndContinuesAfterTheBudget(t *testing.T) {
	index := newTestIndex(t, bookUnits())
	result, err := index.ReadRange(RangeRequest{ChunkIDs: []string{"c00003"}, Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, span := range result.Spans {
		ids = append(ids, span.ChunkIDs...)
	}
	if !slices.Equal(ids, []string{"c00002", "c00003", "c00004"}) {
		t.Fatalf("context neighbours were not merged into one span: %+v", result.Spans)
	}
	if !strings.HasPrefix(result.Text, "[page ") {
		t.Fatalf("span has no location header: %q", result.Text[:40])
	}
	limited, err := index.ReadRange(RangeRequest{Section: index.Outline[1].ID, MaxBytes: 1800})
	if err != nil {
		t.Fatal(err)
	}
	if !limited.Truncated || limited.NextChunkID == "" || len(limited.Text) > 1800 {
		t.Fatalf("range budget not applied: truncated=%t next=%q bytes=%d", limited.Truncated, limited.NextChunkID, len(limited.Text))
	}
	if _, err := index.ReadRange(RangeRequest{ChunkIDs: []string{"c99999"}}); err == nil {
		t.Fatal("unknown chunk ID was accepted")
	}
}

func TestServiceBuildsOncePersistsAndReloads(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "uploads/book.pdf", "source bytes v1")
	extractor := &fakeExtractor{units: bookUnits(), gate: make(chan struct{})}
	service := NewService(extractor)
	defer service.Close()

	var waited atomic.Int32
	var wg sync.WaitGroup
	results := make([]*Index, 3)
	for index := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			built, err := service.Ensure(context.Background(), Target{WorkspaceRoot: root, RelPath: "uploads/book.pdf", Format: "pdf"}, func() { waited.Add(1) })
			if err != nil {
				t.Error(err)
			}
			results[index] = built
		}(index)
	}
	time.Sleep(50 * time.Millisecond)
	close(extractor.gate)
	wg.Wait()
	if extractor.calls.Load() != 1 || waited.Load() != 3 {
		t.Fatalf("extractor calls=%d waiting callbacks=%d, want 1 and 3", extractor.calls.Load(), waited.Load())
	}
	if results[0] == nil || results[0].Manifest.State != StateReady || results[0].Manifest.Chunks != len(results[0].Chunks) {
		t.Fatalf("built index is not ready: %+v", results[0])
	}

	reloaded := NewService(&fakeExtractor{err: errors.New("must not rebuild")})
	defer reloaded.Close()
	waitedAgain := false
	index, err := reloaded.Ensure(context.Background(), Target{WorkspaceRoot: root, RelPath: "uploads/book.pdf", Format: "pdf"}, func() { waitedAgain = true })
	if err != nil || waitedAgain || len(index.Chunks) != len(results[0].Chunks) {
		t.Fatalf("persisted index was not reused: err=%v waited=%t", err, waitedAgain)
	}
	hits, err := index.Search(SearchRequest{Keywords: []string{"慢开始"}})
	if err != nil || len(hits.Hits) == 0 {
		t.Fatalf("reloaded keyword index does not search: %v %+v", err, hits)
	}
}

func TestServiceRecordsFailureAndRetriesOnDemand(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "doc.txt", "text")
	target := Target{WorkspaceRoot: root, RelPath: "doc.txt", Format: "text"}
	failing := NewService(&fakeExtractor{err: errors.New("parser crashed")})
	if _, err := failing.Ensure(context.Background(), target, nil); err == nil {
		t.Fatal("extraction failure was not reported")
	}
	failing.Close()
	sum, _, _ := fileSHA256(filepath.Join(root, "doc.txt"))
	manifest, ok, _ := readManifest(Dir(root, sum))
	if !ok || manifest.State != StateFailed || manifest.Attempts != 1 {
		t.Fatalf("failed build not recorded: %+v", manifest)
	}
	recovered := NewService(&fakeExtractor{units: bookUnits()})
	defer recovered.Close()
	index, err := recovered.Ensure(context.Background(), target, nil)
	if err != nil || index.Manifest.Attempts != 2 {
		t.Fatalf("on-demand retry failed: %v %+v", err, index)
	}
}

func TestPrebuildRetainsReusableStructureForSmallDocuments(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "small.txt", "small")
	extractor := &fakeExtractor{units: []Unit{{Text: "一段很短的正文。"}}}
	service := NewService(extractor)
	defer service.Close()
	if !service.Enqueue(Prebuild{Target: Target{WorkspaceRoot: root, RelPath: "small.txt", Format: "text"}, SmallSourceBytes: 1 << 20, SmallTextBytes: 1 << 20}) {
		t.Fatal("prebuild was not queued")
	}
	deadline := time.Now().Add(2 * time.Second)
	for extractor.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	sum, _, _ := fileSHA256(filepath.Join(root, "small.txt"))
	if _, err := os.Stat(Dir(root, sum)); err != nil {
		t.Fatalf("small document did not keep its structure: %v", err)
	}
	index, err := service.Ensure(context.Background(), Target{WorkspaceRoot: root, RelPath: "small.txt", Format: "text"}, nil)
	if err != nil || index.Manifest.Chunks != 1 || extractor.calls.Load() != 1 {
		t.Fatalf("an explicit request could not build the small index: %v", err)
	}
}

func TestSweepRemovesIndexesWithoutSources(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "a.txt", "first source")
	writeSourceFile(t, root, "b.txt", "second source!")
	service := NewService(&fakeExtractor{units: bookUnits()})
	for _, rel := range []string{"a.txt", "b.txt"} {
		if _, err := service.Ensure(context.Background(), Target{WorkspaceRoot: root, RelPath: rel, Format: "text"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Each build sweeps in the background after returning; wait for them.
	service.Close()
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := sweep(root, DefaultMaxWorkspaceBytes); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, RelativeRoot))
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("sweep kept %d indexes, want 1", count)
	}
	if err := sweep(root, 1); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(filepath.Join(root, RelativeRoot))
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("size cap kept index %s", entry.Name())
		}
	}
}

func TestTermsFoldCompatibilityIdeographs(t *testing.T) {
	// "⽅⾯" uses Kangxi radicals, as Chrome-generated PDF text layers do.
	if got := Terms("⽅⾯"); !slices.Equal(got, Terms("方面")) {
		t.Fatalf("compatibility ideographs were not folded: %v", got)
	}
}

func TestReadRangeDropsContextBeforeRequestedChunks(t *testing.T) {
	index := newTestIndex(t, bookUnits())
	requested := index.Chunks[5]
	result, err := index.ReadRange(RangeRequest{ChunkIDs: []string{requested.ID}, Context: 1, MaxBytes: len(requested.Text)*2 + 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Spans) != 1 || !slices.Equal(result.Spans[0].ChunkIDs, []string{requested.ID}) {
		t.Fatalf("context displaced the requested chunk: %+v", result.Spans)
	}
}

func scannedBookExtractor() *fakeExtractor {
	unindexed := []int{}
	for page := 1; page <= 30; page++ {
		unindexed = append(unindexed, page)
	}
	return &fakeExtractor{info: SourceInfo{Pages: 30, UnindexedPages: unindexed, Outline: []OutlineEntry{
		{Title: "第1章 概述", Level: 1, Page: 1}, {Title: "第2章 物理层", Level: 1, Page: 11},
		{Title: "第3章 运输层与拥塞控制", Level: 1, Page: 21}, {Title: "3.1 慢开始", Level: 2, Page: 24},
	}}}
}

func TestScannedDocumentIndexLocatesSectionsThenMergesRecognizedPages(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "scan.pdf", "scanned pdf bytes")
	extractor := scannedBookExtractor()
	service := NewService(extractor)
	defer service.Close()
	target := Target{WorkspaceRoot: root, RelPath: "scan.pdf", Format: "pdf"}

	index, err := service.Ensure(context.Background(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if index.Manifest.State != StatePartial || len(index.Chunks) != 0 || len(index.Manifest.UnindexedPages) != 30 {
		t.Fatalf("scanned index = %+v", index.Manifest)
	}
	chapter := index.Outline[2]
	if chapter.Page != 21 || chapter.PageEnd != 30 || index.Outline[0].PageEnd != 10 || index.Outline[3].PageEnd != 30 {
		t.Fatalf("outline page ranges are wrong: %+v", index.Outline)
	}
	result, err := index.Search(SearchRequest{Keywords: []string{"拥塞控制"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 0 || len(result.Sections) != 1 || result.Sections[0].ID != chapter.ID {
		t.Fatalf("title search did not locate the scanned chapter: %+v", result)
	}
	pages, err := index.UnindexedPagesIn(RangeRequest{Section: index.Outline[3].ID})
	if err != nil || !slices.Equal(pages, []int{24, 25, 26, 27, 28, 29, 30}) {
		t.Fatalf("section pages needing OCR = %v, %v", pages, err)
	}

	recognized, err := service.RecognizedPages(context.Background(), target, map[int]string{
		24: "慢开始算法让拥塞窗口按指数增长，直到慢开始门限。", 25: "拥塞避免阶段每个往返时间增加一个报文段。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if extractor.calls.Load() != 1 {
		t.Fatalf("merging recognized pages re-parsed the document %d times", extractor.calls.Load())
	}
	if len(recognized.Chunks) == 0 || !slices.Equal(recognized.Manifest.OCRPages, []int{24, 25}) || len(recognized.Manifest.UnindexedPages) != 28 {
		t.Fatalf("recognized pages were not merged: %+v", recognized.Manifest)
	}
	read, err := recognized.ReadRange(RangeRequest{Section: recognized.Outline[3].ID})
	if err != nil || !strings.Contains(read.Text, "慢开始门限") || read.Spans[0].Anchor.Page != 24 {
		t.Fatalf("recognized section did not read back: %v %+v", err, read)
	}
	reloaded := NewService(&fakeExtractor{err: errors.New("must not rebuild")})
	defer reloaded.Close()
	again, err := reloaded.Ensure(context.Background(), target, nil)
	if err != nil || !slices.Equal(again.Manifest.OCRPages, []int{24, 25}) {
		t.Fatalf("recognized pages did not persist: %v", err)
	}
}

func TestSummaryUnitsFollowTopLevelSectionsAndGroupBeyondTheLimit(t *testing.T) {
	units := []Unit{{Text: "前言部分介绍本书结构。"}}
	units = append(units, bookUnits()...)
	index := newTestIndex(t, units)
	parts := index.SummaryUnits(16)
	if len(parts) != 6 || parts[0].ID != "front" || !strings.HasPrefix(parts[1].Title, "第1章") || parts[5].ChunkEnd != len(index.Chunks) {
		t.Fatalf("summary units = %+v", parts)
	}
	if !strings.Contains(index.UnitText(parts[4]), "传输层") && !strings.Contains(index.UnitText(parts[4]), "运输层") {
		t.Fatalf("unit text does not belong to its chapter: %.80s", index.UnitText(parts[4]))
	}
	grouped := index.SummaryUnits(3)
	if len(grouped) != 3 || grouped[0].ChunkStart != 0 || grouped[2].ChunkEnd != len(index.Chunks) || !strings.Contains(grouped[2].Title, "…") {
		t.Fatalf("grouped units = %+v", grouped)
	}
	flat := newTestIndex(t, []Unit{{Text: strings.Repeat("没有目录的长文本。", 8000)}})
	windows := flat.SummaryUnits(16)
	if len(windows) < 2 || windows[0].ChunkStart != 0 || windows[len(windows)-1].ChunkEnd != len(flat.Chunks) {
		t.Fatalf("outline-less document was not split into windows: %+v", windows)
	}
}
