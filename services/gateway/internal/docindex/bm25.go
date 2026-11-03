package docindex

import (
	"math"
	"sort"
	"strings"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// headingTermWeight counts a heading-path term this many times, so a
	// section titled with the keyword outranks a passing mention.
	headingTermWeight = 2
	// MaxSearchResults bounds one search regardless of the requested top_k.
	MaxSearchResults = 20
)

// keywordIndex is a BM25 inverted index over chunk ordinals.
type keywordIndex struct {
	Postings map[string][]posting `json:"postings"`
	Lengths  []int                `json:"lengths"`
	AvgLen   float64              `json:"avg_len"`
}

type posting struct {
	Chunk int `json:"c"`
	TF    int `json:"f"`
}

func buildKeywordIndex(chunks []Chunk) keywordIndex {
	index := keywordIndex{Postings: map[string][]posting{}, Lengths: make([]int, len(chunks))}
	total := 0
	for ordinal, chunk := range chunks {
		counts := map[string]int{}
		for _, term := range Terms(chunk.Text) {
			counts[term]++
		}
		for _, term := range Terms(strings.Join(chunk.Anchor.HeadingPath, " ")) {
			counts[term] += headingTermWeight
		}
		length := 0
		for term, count := range counts {
			index.Postings[term] = append(index.Postings[term], posting{Chunk: ordinal, TF: count})
			length += count
		}
		index.Lengths[ordinal] = length
		total += length
	}
	if len(chunks) > 0 {
		index.AvgLen = float64(total) / float64(len(chunks))
	}
	return index
}

// Hit is one ranked search result.
type Hit struct {
	Chunk int
	Score float64
	// Matched lists the query terms the chunk contains.
	Matched []string
}

// search ranks chunks in [start, end) for the query terms. A chunk must match
// at least one term; ties keep document order.
func (index keywordIndex) search(terms []string, start, end, limit int) []Hit {
	documents := len(index.Lengths)
	if documents == 0 || len(terms) == 0 || limit <= 0 {
		return nil
	}
	scores := map[int]float64{}
	matched := map[int][]string{}
	for _, term := range terms {
		postings := index.Postings[term]
		if len(postings) == 0 {
			continue
		}
		idf := math.Log(1 + (float64(documents)-float64(len(postings))+0.5)/(float64(len(postings))+0.5))
		for _, entry := range postings {
			if entry.Chunk < start || entry.Chunk >= end {
				continue
			}
			tf := float64(entry.TF)
			norm := 1 - bm25B + bm25B*float64(index.Lengths[entry.Chunk])/math.Max(index.AvgLen, 1)
			scores[entry.Chunk] += idf * tf * (bm25K1 + 1) / (tf + bm25K1*norm)
			matched[entry.Chunk] = append(matched[entry.Chunk], term)
		}
	}
	hits := make([]Hit, 0, len(scores))
	for chunk, score := range scores {
		hits = append(hits, Hit{Chunk: chunk, Score: score, Matched: matched[chunk]})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Chunk < hits[j].Chunk
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
