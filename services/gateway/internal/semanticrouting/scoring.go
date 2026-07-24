package semanticrouting

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

type CorpusEntry struct {
	CandidateID string
	Negative    bool
	Text        string
}

type EmbeddingIndex struct {
	GraphRevision string
	Model         string
	entries       []indexedEmbedding
}

type indexedEmbedding struct {
	CandidateID string
	Negative    bool
	Vector      []float32
}

type EmbeddingEvidence struct {
	Score            float64
	NegativeConflict float64
}

func (g *Graph) EmbeddingCorpus() []CorpusEntry {
	entries := make([]CorpusEntry, 0)
	for _, candidate := range g.candidates {
		for _, text := range candidate.EmbedTexts {
			entries = append(entries, CorpusEntry{CandidateID: candidate.ID, Text: text})
		}
		for _, text := range candidate.HardNegatives {
			entries = append(entries, CorpusEntry{CandidateID: candidate.ID, Negative: true, Text: text})
		}
	}
	return entries
}

func BuildEmbeddingIndex(graph *Graph, model string, vectors [][]float32) (*EmbeddingIndex, error) {
	if graph == nil {
		return nil, errors.New("embedding index graph is required")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, errors.New("embedding index model identity is required")
	}
	corpus := graph.EmbeddingCorpus()
	if len(vectors) != len(corpus) {
		return nil, fmt.Errorf("embedding index received %d vectors for %d corpus entries", len(vectors), len(corpus))
	}
	entries := make([]indexedEmbedding, 0, len(corpus))
	dimension := 0
	for index, vector := range vectors {
		if len(vector) == 0 {
			return nil, fmt.Errorf("embedding corpus vector %d is empty", index)
		}
		if dimension == 0 {
			dimension = len(vector)
		} else if len(vector) != dimension {
			return nil, errors.New("embedding corpus vectors have inconsistent dimensions")
		}
		entries = append(entries, indexedEmbedding{
			CandidateID: corpus[index].CandidateID, Negative: corpus[index].Negative, Vector: normalizeVector(vector),
		})
	}
	return &EmbeddingIndex{GraphRevision: graph.Revision(), Model: model, entries: entries}, nil
}

func (i *EmbeddingIndex) Score(query []float32, eligible map[string]bool, calibration Calibration) (map[string]EmbeddingEvidence, error) {
	if i == nil || len(i.entries) == 0 || len(query) == 0 {
		return nil, errors.New("embedding index and query vector are required")
	}
	query = normalizeVector(query)
	positives := make(map[string][]float64)
	negativeMax := make(map[string]float64)
	for _, entry := range i.entries {
		if !eligible[entry.CandidateID] {
			continue
		}
		if len(entry.Vector) != len(query) {
			return nil, errors.New("embedding query dimension does not match the graph index")
		}
		similarity := cosine(query, entry.Vector)
		unitScore := clamp01((similarity + 1) / 2)
		if entry.Negative {
			if unitScore > negativeMax[entry.CandidateID] {
				negativeMax[entry.CandidateID] = unitScore
			}
			continue
		}
		positives[entry.CandidateID] = append(positives[entry.CandidateID], unitScore)
	}
	evidence := make(map[string]EmbeddingEvidence, len(eligible))
	for candidateID := range eligible {
		scores := positives[candidateID]
		slices.SortFunc(scores, func(left, right float64) int {
			if left > right {
				return -1
			}
			if left < right {
				return 1
			}
			return 0
		})
		count := min(calibration.EmbeddingTopM, len(scores))
		mean := 0.0
		for _, score := range scores[:count] {
			mean += score
		}
		if count > 0 {
			mean /= float64(count)
		}
		conflict := negativeMax[candidateID]
		evidence[candidateID] = EmbeddingEvidence{
			Score:            clamp01(mean - calibration.EmbeddingNegativeCost*max(0, conflict-mean)),
			NegativeConflict: conflict,
		}
	}
	return evidence, nil
}

func normalizeVector(vector []float32) []float32 {
	out := append([]float32(nil), vector...)
	norm := 0.0
	for _, value := range out {
		norm += float64(value * value)
	}
	if norm == 0 {
		return out
	}
	norm = math.Sqrt(norm)
	for index := range out {
		out[index] = float32(float64(out[index]) / norm)
	}
	return out
}

func cosine(left, right []float32) float64 {
	dot := 0.0
	for index := range left {
		dot += float64(left[index] * right[index])
	}
	return max(-1, min(1, dot))
}

func clamp01(value float64) float64 { return max(0, min(1, value)) }
