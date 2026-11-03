package docindex

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	snippetRunes = 240
	// MaxRangeChunks bounds the chunk IDs one range read accepts.
	MaxRangeChunks = 64
)

// Index is a loaded, queryable document index.
type Index struct {
	Manifest Manifest
	Outline  []OutlineNode
	Chunks   []Chunk
	keywords keywordIndex
}

// SearchRequest searches the whole document, or one outline node and its
// descendants when Section names a node ID.
type SearchRequest struct {
	Keywords []string
	TopK     int
	Section  string
	MaxBytes int
}

type SearchHit struct {
	ChunkID string   `json:"chunk_id"`
	Score   float64  `json:"score"`
	Anchor  Anchor   `json:"anchor"`
	Matched []string `json:"matched_terms"`
	Snippet string   `json:"snippet"`
}

// SectionHit is an outline node whose title matches the query, so a
// section can be located before its pages have text, as in a scanned PDF.
type SectionHit struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Page    int      `json:"page,omitempty"`
	PageEnd int      `json:"page_end,omitempty"`
	Matched []string `json:"matched_terms"`
}

type SearchResult struct {
	Terms     []string     `json:"terms"`
	Hits      []SearchHit  `json:"hits"`
	Sections  []SectionHit `json:"sections"`
	Truncated bool         `json:"truncated"`
}

const maxSectionHits = 5

// Search ranks chunks with BM25 and returns snippets centred on the first
// matched term, stopping before the results exceed MaxBytes.
func (index *Index) Search(request SearchRequest) (SearchResult, error) {
	start, end, err := index.sectionRange(request.Section)
	if err != nil {
		return SearchResult{}, err
	}
	terms := queryTerms(request.Keywords)
	if len(terms) == 0 {
		return SearchResult{}, errors.New("search keywords contain no searchable terms")
	}
	limit := request.TopK
	if limit <= 0 || limit > MaxSearchResults {
		limit = min(MaxSearchResults, max(limit, 8))
	}
	result := SearchResult{Terms: terms, Hits: []SearchHit{}, Sections: index.matchingSections(terms, start, end)}
	used := 0
	for _, hit := range index.keywords.search(terms, start, end, limit) {
		chunk := index.Chunks[hit.Chunk]
		item := SearchHit{ChunkID: chunk.ID, Score: roundScore(hit.Score), Anchor: chunk.Anchor, Matched: hit.Matched, Snippet: snippet(chunk.Text, hit.Matched)}
		cost := len(item.Snippet) + len(strings.Join(chunk.Anchor.HeadingPath, " ")) + 96
		if request.MaxBytes > 0 && used+cost > request.MaxBytes {
			result.Truncated = true
			break
		}
		used += cost
		result.Hits = append(result.Hits, item)
	}
	return result, nil
}

// matchingSections ranks outline nodes inside [start, end) by how many query
// terms their titles contain, keeping document order among equals.
func (index *Index) matchingSections(terms []string, start, end int) []SectionHit {
	type scored struct {
		hit   SectionHit
		count int
	}
	candidates := []scored{}
	for _, node := range index.Outline {
		if node.ChunkStart < start || (end > start && node.ChunkStart > end) {
			continue
		}
		titleTerms := map[string]bool{}
		for _, term := range Terms(node.Title) {
			titleTerms[term] = true
		}
		matched := []string{}
		for _, term := range terms {
			if titleTerms[term] {
				matched = append(matched, term)
			}
		}
		if len(matched) > 0 {
			candidates = append(candidates, scored{hit: SectionHit{ID: node.ID, Title: node.Title, Page: node.Page, PageEnd: node.PageEnd, Matched: matched}, count: len(matched)})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].count > candidates[j].count })
	out := []SectionHit{}
	for _, candidate := range candidates[:min(len(candidates), maxSectionHits)] {
		out = append(out, candidate.hit)
	}
	return out
}

// UnindexedPagesIn lists the requested pages that have no text yet, in page
// order: those of a page range, or of a section's page range.
func (index *Index) UnindexedPagesIn(request RangeRequest) ([]int, error) {
	first, last := request.PageStart, max(request.PageEnd, request.PageStart)
	if strings.TrimSpace(request.Section) != "" {
		node, ok := index.outlineNode(request.Section)
		if !ok {
			return nil, fmt.Errorf("section %q is not in this document outline", request.Section)
		}
		first, last = node.Page, max(node.PageEnd, node.Page)
	}
	if first <= 0 {
		return nil, nil
	}
	pages := []int{}
	for _, page := range index.Manifest.UnindexedPages {
		if page >= first && page <= last {
			pages = append(pages, page)
		}
	}
	return pages, nil
}

func (index *Index) outlineNode(id string) (OutlineNode, bool) {
	for _, node := range index.Outline {
		if node.ID == strings.TrimSpace(id) {
			return node, true
		}
	}
	return OutlineNode{}, false
}

// RangeRequest selects chunks by ID, by page range, or by outline node.
// Context adds that many neighbouring chunks on each side of listed IDs.
type RangeRequest struct {
	ChunkIDs  []string
	PageStart int
	PageEnd   int
	Section   string
	Context   int
	MaxBytes  int
}

type RangeSpan struct {
	ChunkIDs []string `json:"chunk_ids"`
	Anchor   Anchor   `json:"anchor"`
}

type RangeResult struct {
	Text        string      `json:"text"`
	Spans       []RangeSpan `json:"spans"`
	Truncated   bool        `json:"truncated"`
	NextChunkID string      `json:"next_chunk_id,omitempty"`
}

// ReadRange renders the selected chunks in document order. Contiguous chunks
// form one span with a location header, and the overlap a chunk repeats from
// its predecessor is dropped. Output stops at MaxBytes; NextChunkID names
// where a continuation read starts.
func (index *Index) ReadRange(request RangeRequest) (RangeResult, error) {
	ordinals, err := index.rangeOrdinals(request)
	if err != nil {
		return RangeResult{}, err
	}
	if request.Context > 0 && len(request.ChunkIDs) > 0 && request.MaxBytes > 0 && index.textBytes(ordinals) > request.MaxBytes {
		// Neighbouring context is dropped before any requested chunk.
		request.Context = 0
		if ordinals, err = index.rangeOrdinals(request); err != nil {
			return RangeResult{}, err
		}
	}
	result := RangeResult{Spans: []RangeSpan{}}
	var out strings.Builder
	previous := -2
	for position, ordinal := range ordinals {
		chunk := index.Chunks[ordinal]
		piece, header := chunk.Text, ""
		if ordinal == previous+1 {
			piece = dropRepeatedPrefix(index.Chunks[previous].Text, piece)
		} else {
			header = spanHeader(index.Manifest.Format, chunk)
		}
		if request.MaxBytes > 0 && out.Len()+len(header)+len(piece)+2 > request.MaxBytes {
			result.Truncated = true
			if out.Len() > 0 {
				result.NextChunkID = chunk.ID
				break
			}
			// A single chunk larger than the budget is cut, and reading
			// continues after it.
			piece = boundedPrefix(piece, max(0, request.MaxBytes-len(header)-2))
			if position+1 < len(ordinals) {
				result.NextChunkID = index.Chunks[ordinals[position+1]].ID
			}
		}
		if header != "" {
			if out.Len() > 0 {
				out.WriteString("\n\n")
			}
			out.WriteString(header + "\n")
			result.Spans = append(result.Spans, RangeSpan{Anchor: chunk.Anchor})
		} else {
			out.WriteString("\n")
		}
		out.WriteString(piece)
		span := &result.Spans[len(result.Spans)-1]
		span.ChunkIDs = append(span.ChunkIDs, chunk.ID)
		if last := chunkLastPage(chunk); last > max(span.Anchor.Page, span.Anchor.PageEnd) {
			span.Anchor.PageEnd = last
		}
		previous = ordinal
		if result.Truncated {
			break
		}
	}
	result.Text = out.String()
	return result, nil
}

func (index *Index) textBytes(ordinals []int) int {
	total := 0
	for _, ordinal := range ordinals {
		total += len(index.Chunks[ordinal].Text) + 2
	}
	return total
}

func (index *Index) rangeOrdinals(request RangeRequest) ([]int, error) {
	selected := map[int]bool{}
	switch {
	case len(request.ChunkIDs) > 0:
		if len(request.ChunkIDs) > MaxRangeChunks {
			return nil, fmt.Errorf("at most %d chunk_ids can be read at once", MaxRangeChunks)
		}
		context := max(0, min(request.Context, 2))
		for _, id := range request.ChunkIDs {
			ordinal, ok := chunkOrdinal(strings.TrimSpace(id), len(index.Chunks))
			if !ok {
				return nil, fmt.Errorf("chunk_id %q is not in this document index", id)
			}
			for neighbour := ordinal - context; neighbour <= ordinal+context; neighbour++ {
				if neighbour >= 0 && neighbour < len(index.Chunks) {
					selected[neighbour] = true
				}
			}
		}
	case request.PageStart > 0:
		last := max(request.PageEnd, request.PageStart)
		for ordinal, chunk := range index.Chunks {
			if chunkLastPage(chunk) >= request.PageStart && max(chunk.Anchor.Page, 1) <= last {
				selected[ordinal] = true
			}
		}
	case strings.TrimSpace(request.Section) != "":
		start, end, err := index.sectionRange(request.Section)
		if err != nil {
			return nil, err
		}
		for ordinal := start; ordinal < end; ordinal++ {
			selected[ordinal] = true
		}
	default:
		return nil, errors.New("read_range requires chunk_ids, a page range, or a section")
	}
	if len(selected) == 0 {
		return nil, errors.New("the requested range contains no indexed text")
	}
	ordinals := make([]int, 0, len(selected))
	for ordinal := range selected {
		ordinals = append(ordinals, ordinal)
	}
	sort.Ints(ordinals)
	return ordinals, nil
}

func (index *Index) sectionRange(section string) (int, int, error) {
	section = strings.TrimSpace(section)
	if section == "" {
		return 0, len(index.Chunks), nil
	}
	for _, node := range index.Outline {
		if node.ID == section {
			return node.ChunkStart, node.ChunkEnd, nil
		}
	}
	return 0, 0, fmt.Errorf("section %q is not in this document outline", section)
}

// chunkOrdinal parses the stable "c00012" chunk ID format.
func chunkOrdinal(id string, count int) (int, bool) {
	if !strings.HasPrefix(id, "c") {
		return 0, false
	}
	number, err := strconv.Atoi(id[1:])
	if err != nil || number < 1 || number > count {
		return 0, false
	}
	return number - 1, true
}

// spanHeader names a span's location. PDF pages are labelled as PDF page
// indexes because recognized text often carries the book's own printed page
// numbers, which differ.
func spanHeader(format string, chunk Chunk) string {
	parts := []string{}
	if chunk.Anchor.Page > 0 {
		label := "page "
		switch format {
		case "pdf":
			label = "PDF page "
		case "pptx":
			label = "slide "
		}
		parts = append(parts, label+strconv.Itoa(chunk.Anchor.Page))
	}
	if len(chunk.Anchor.HeadingPath) > 0 {
		parts = append(parts, strings.Join(chunk.Anchor.HeadingPath, " > "))
	}
	parts = append(parts, chunk.ID)
	return "[" + strings.Join(parts, " | ") + "]"
}

// dropRepeatedPrefix removes the overlap the next chunk carries from the end
// of the previous one.
func dropRepeatedPrefix(previous, next string) string {
	limit := min(len(previous), len(next))
	for size := min(limit, chunkOverlapRunes*utf8.UTFMax); size > 0; size-- {
		if strings.HasSuffix(previous, next[:size]) && utf8.ValidString(next[:size]) {
			return strings.TrimPrefix(next[size:], "\n")
		}
	}
	return next
}

func snippet(text string, matched []string) string {
	runes := []rune(text)
	if len(runes) <= snippetRunes {
		return text
	}
	center := 0
	lower := strings.ToLower(text)
	for _, term := range matched {
		if at := strings.Index(lower, term); at >= 0 {
			center = utf8.RuneCountInString(lower[:at])
			break
		}
	}
	start := max(0, min(center-snippetRunes/3, len(runes)-snippetRunes))
	out := string(runes[start : start+snippetRunes])
	if start > 0 {
		out = "…" + out
	}
	if start+snippetRunes < len(runes) {
		out += "…"
	}
	return out
}

func boundedPrefix(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

func roundScore(score float64) float64 {
	return float64(int(score*1000+0.5)) / 1000
}
