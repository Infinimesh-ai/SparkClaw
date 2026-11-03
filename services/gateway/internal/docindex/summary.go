package docindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const summariesDirName = "summaries"

// SummaryUnit is one part of a whole-document summary: a top-level outline
// section, the text before the first heading, or a fixed window of chunks
// when the document has no usable outline.
type SummaryUnit struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Page       int    `json:"page,omitempty"`
	PageEnd    int    `json:"page_end,omitempty"`
	ChunkStart int    `json:"chunk_start"`
	ChunkEnd   int    `json:"chunk_end"`
}

// SummaryUnits splits the document into at most limit parts in document
// order. Top-level outline sections are used when there are at least two;
// more sections than limit are merged into consecutive groups.
func (index *Index) SummaryUnits(limit int) []SummaryUnit {
	limit = max(1, limit)
	top := index.topLevelNodes()
	if len(top) < 2 {
		return index.windowUnits(limit)
	}
	units := []SummaryUnit{}
	if first := top[0]; first.ChunkStart > 0 {
		units = append(units, SummaryUnit{ID: "front", Title: "Front matter", ChunkStart: 0, ChunkEnd: first.ChunkStart, Page: 1, PageEnd: max(0, first.Page-1)})
	}
	for _, node := range top {
		units = append(units, SummaryUnit{ID: node.ID, Title: node.Title, Page: node.Page, PageEnd: node.PageEnd, ChunkStart: node.ChunkStart, ChunkEnd: node.ChunkEnd})
	}
	if len(units) <= limit {
		return units
	}
	grouped := make([]SummaryUnit, 0, limit)
	for group := 0; group < limit; group++ {
		first, last := group*len(units)/limit, (group+1)*len(units)/limit-1
		merged := units[first]
		merged.ID = units[first].ID + ".." + units[last].ID
		if first != last {
			merged.Title = units[first].Title + " … " + units[last].Title
		}
		merged.ChunkEnd, merged.PageEnd = units[last].ChunkEnd, units[last].PageEnd
		grouped = append(grouped, merged)
	}
	return grouped
}

func (index *Index) topLevelNodes() []OutlineNode {
	level := 0
	for _, node := range index.Outline {
		if level == 0 || node.Level < level {
			level = node.Level
		}
	}
	nodes := []OutlineNode{}
	for _, node := range index.Outline {
		if node.Level == level {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func (index *Index) windowUnits(limit int) []SummaryUnit {
	count := len(index.Chunks)
	if count == 0 {
		return nil
	}
	parts := min(limit, max(1, (count+39)/40))
	units := make([]SummaryUnit, 0, parts)
	for part := 0; part < parts; part++ {
		start, end := part*count/parts, (part+1)*count/parts
		unit := SummaryUnit{ID: fmt.Sprintf("part%02d", part+1), Title: fmt.Sprintf("Part %d", part+1), ChunkStart: start, ChunkEnd: end,
			Page: index.Chunks[start].Anchor.Page, PageEnd: chunkLastPage(index.Chunks[end-1])}
		units = append(units, unit)
	}
	return units
}

// UnitText joins a unit's chunks without the overlap consecutive chunks
// repeat.
func (index *Index) UnitText(unit SummaryUnit) string {
	var out strings.Builder
	for ordinal := unit.ChunkStart; ordinal < unit.ChunkEnd && ordinal < len(index.Chunks); ordinal++ {
		text := index.Chunks[ordinal].Text
		if ordinal > unit.ChunkStart {
			text = dropRepeatedPrefix(index.Chunks[ordinal-1].Text, text)
			out.WriteString("\n")
		}
		out.WriteString(text)
	}
	return out.String()
}

// UnindexedPagesInUnit lists a unit's pages that have no text yet.
func (index *Index) UnindexedPagesInUnit(unit SummaryUnit) []int {
	if unit.Page <= 0 {
		return nil
	}
	pages := []int{}
	for _, page := range index.Manifest.UnindexedPages {
		if page >= unit.Page && page <= max(unit.PageEnd, unit.Page) {
			pages = append(pages, page)
		}
	}
	return pages
}

// SummaryKey identifies a cached summary of text under one prompt version.
func SummaryKey(version, title, text string) string {
	sum := sha256.Sum256([]byte(version + "\x00" + title + "\x00" + text))
	return hex.EncodeToString(sum[:16])
}

type cachedSummary struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

// CachedSummary returns a summary stored for key in index's directory.
func (s *Service) CachedSummary(target Target, index *Index, key string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(Dir(target.WorkspaceRoot, index.Manifest.SourceSHA256), summariesDirName, key+".json"))
	if err != nil {
		return "", false
	}
	var cached cachedSummary
	if json.Unmarshal(raw, &cached) != nil || cached.Key != key || strings.TrimSpace(cached.Summary) == "" {
		return "", false
	}
	return cached.Summary, true
}

// StoreSummary caches a summary for key next to the index it was made from.
func (s *Service) StoreSummary(target Target, index *Index, key, summary string) error {
	dir := filepath.Join(Dir(target.WorkspaceRoot, index.Manifest.SourceSHA256), summariesDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	raw, err := json.Marshal(cachedSummary{Key: key, Summary: summary})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, key+".json"), raw)
}
