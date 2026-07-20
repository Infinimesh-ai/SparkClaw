package websearch

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	InfoProjectionSchemaVersion = 1
	MaxInfoProjectionBytes      = 8192
	defaultInfoProjectionBytes  = 2400
	minInfoProjectionBytes      = 512
	maxProjectedSummaryBytes    = 560
	maxProjectedFactBytes       = 360
	maxProjectedSnippetBytes    = 320
)

const (
	InfoProjectionComplete = "complete"
	InfoProjectionPartial  = "partial"
	InfoProjectionFailed   = "failed"
)

type InfoEvidenceProjection struct {
	SchemaVersion     int                  `json:"schema_version"`
	Status            string               `json:"status"`
	RequestID         string               `json:"request_id,omitempty"`
	Query             string               `json:"query,omitempty"`
	Summary           *InfoEvidenceText    `json:"summary,omitempty"`
	Facts             []InfoEvidenceFact   `json:"facts,omitempty"`
	Sources           []InfoEvidenceSource `json:"sources,omitempty"`
	Citations         []string             `json:"citations,omitempty"`
	MissingComponents []string             `json:"missing_components,omitempty"`
	FailureCode       string               `json:"failure_code,omitempty"`
	Untrusted         bool                 `json:"untrusted"`
}

type InfoEvidenceText struct {
	Ref       string `json:"ref"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

type InfoEvidenceFact struct {
	Ref        string   `json:"ref"`
	Claim      string   `json:"claim"`
	Confidence string   `json:"confidence,omitempty"`
	SourceIDs  []string `json:"source_ids,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
}

type InfoEvidenceSource struct {
	Index           int                `json:"index"`
	ID              string             `json:"id,omitempty"`
	Title           string             `json:"title,omitempty"`
	URL             string             `json:"url,omitempty"`
	PublishedAt     string             `json:"published_at,omitempty"`
	Snippets        []InfoEvidenceText `json:"snippets"`
	OmittedSnippets int                `json:"omitted_snippets,omitempty"`
}

// ProjectInfoEvidence selects a bounded evidence directory for the frozen
// task query. It never asks Info to reshape its fixed response and never
// synthesizes facts when a fixed response component is absent.
func ProjectInfoEvidence(result Result, frozenQuery string, maxBytes int) InfoEvidenceProjection {
	maxBytes = normalizeProjectionLimit(maxBytes)
	projection, valid := newInfoEvidenceProjection(result, frozenQuery)
	if !valid {
		return projection
	}

	terms := infoProjectionTerms(projection.Query)
	capacityOmitted := false
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		text, truncated := relevantEvidenceExcerpt(summary, terms, maxProjectedSummaryBytes)
		candidate := projection
		candidate.Summary = &InfoEvidenceText{Ref: "summary:0", Text: text, Truncated: truncated}
		if infoProjectionFits(candidate, maxBytes) {
			projection = candidate
		} else {
			capacityOmitted = true
		}
	}

	facts := rankedInfoFacts(result.KeyFacts, terms)
	for _, fact := range facts {
		claim, truncated := relevantEvidenceExcerpt(fact.Claim, terms, maxProjectedFactBytes)
		if claim == "" {
			continue
		}
		ref := strings.TrimSpace(fact.ID)
		if ref == "" {
			ref = "fact:" + itoa(len(projection.Facts))
		}
		candidate := projection
		candidate.Facts = append(append([]InfoEvidenceFact(nil), projection.Facts...), InfoEvidenceFact{
			Ref: ref, Claim: claim, Confidence: strings.TrimSpace(fact.Confidence),
			SourceIDs: append([]string(nil), fact.Sources...), Truncated: truncated,
		})
		if infoProjectionFits(candidate, maxBytes) {
			projection = candidate
		} else {
			capacityOmitted = true
		}
		if len(projection.Facts) >= 4 {
			if len(facts) > len(projection.Facts) {
				capacityOmitted = true
			}
			break
		}
	}

	sources := rankedInfoSources(result.Results, projectedSourceIDs(projection.Facts), result.Citations, terms)
	for _, source := range sources {
		projected := projectInfoSource(source, terms)
		if len(projected.Snippets) == 0 {
			continue
		}
		candidate := projection
		candidate.Sources = append(append([]InfoEvidenceSource(nil), projection.Sources...), projected)
		candidate.Citations = appendProjectionCitation(projection.Citations, source.URL, result.Citations)
		if infoProjectionFits(candidate, maxBytes) {
			projection = candidate
		} else if len(projected.Snippets) > 1 {
			projected.Snippets = projected.Snippets[:1]
			candidate = projection
			candidate.Sources = append(append([]InfoEvidenceSource(nil), projection.Sources...), projected)
			candidate.Citations = appendProjectionCitation(projection.Citations, source.URL, result.Citations)
			if infoProjectionFits(candidate, maxBytes) {
				projection = candidate
			} else {
				capacityOmitted = true
			}
		} else {
			capacityOmitted = true
		}
		if len(projection.Sources) >= 3 {
			if len(sources) > len(projection.Sources) {
				capacityOmitted = true
			}
			break
		}
	}

	projection = finalizeInfoProjection(projection, result, capacityOmitted)
	projection = fitFinalInfoProjection(projection, result, maxBytes)
	if !infoProjectionHasEvidence(projection) {
		projection.Status = InfoProjectionFailed
		if projection.FailureCode == "" {
			projection.FailureCode = "evidence_unavailable"
		}
	}
	return projection
}

func newInfoEvidenceProjection(result Result, frozenQuery string) (InfoEvidenceProjection, bool) {
	frozenQuery = strings.TrimSpace(frozenQuery)
	projection := InfoEvidenceProjection{
		SchemaVersion: InfoProjectionSchemaVersion,
		RequestID:     strings.TrimSpace(result.RequestID),
		Query:         frozenQuery,
		Untrusted:     true,
	}
	switch {
	case frozenQuery == "":
		projection.MissingComponents = []string{"frozen_query"}
		projection.FailureCode = "frozen_query_missing"
	case strings.TrimSpace(result.Query) != frozenQuery:
		projection.MissingComponents = []string{"query_match"}
		projection.FailureCode = "query_mismatch"
	case projection.RequestID == "":
		projection.MissingComponents = []string{"request_id"}
		projection.FailureCode = "request_id_missing"
	case !result.Untrusted:
		projection.MissingComponents = []string{"untrusted_marker"}
		projection.FailureCode = "trust_boundary_missing"
	}
	if projection.FailureCode != "" {
		projection.Status = InfoProjectionFailed
		return projection, false
	}
	return projection, true
}

func normalizeProjectionLimit(maxBytes int) int {
	if maxBytes <= 0 {
		return defaultInfoProjectionBytes
	}
	if maxBytes < minInfoProjectionBytes {
		return minInfoProjectionBytes
	}
	if maxBytes > MaxInfoProjectionBytes {
		return MaxInfoProjectionBytes
	}
	return maxBytes
}

func finalizeInfoProjection(projection InfoEvidenceProjection, result Result, capacityOmitted bool) InfoEvidenceProjection {
	missing := []string{}
	if projection.Summary == nil {
		missing = append(missing, "answer_context.summary")
	}
	if len(projection.Facts) == 0 {
		missing = append(missing, "answer_context.key_facts")
	}
	if len(projection.Sources) == 0 {
		missing = append(missing, "sources.snippets")
	}
	if capacityOmitted {
		missing = append(missing, "projection.capacity")
	}
	projection.MissingComponents = missing
	if len(missing) == 0 {
		projection.Status = InfoProjectionComplete
	} else {
		projection.Status = InfoProjectionPartial
	}
	if strings.TrimSpace(result.Summary) == "" && len(result.KeyFacts) == 0 && !resultHasSourceSnippets(result) {
		projection.Status = InfoProjectionFailed
		projection.FailureCode = "fixed_response_has_no_evidence"
	}
	return projection
}

func infoProjectionFits(projection InfoEvidenceProjection, maxBytes int) bool {
	if projection.Summary == nil {
		projection.MissingComponents = append(projection.MissingComponents, "answer_context.summary")
	}
	if len(projection.Facts) == 0 {
		projection.MissingComponents = append(projection.MissingComponents, "answer_context.key_facts")
	}
	if len(projection.Sources) == 0 {
		projection.MissingComponents = append(projection.MissingComponents, "sources.snippets")
	}
	if len(projection.MissingComponents) == 0 {
		projection.Status = InfoProjectionComplete
	} else {
		projection.Status = InfoProjectionPartial
	}
	raw, err := json.Marshal(projection)
	return err == nil && len(raw) <= maxBytes
}

func fitFinalInfoProjection(projection InfoEvidenceProjection, result Result, maxBytes int) InfoEvidenceProjection {
	for infoProjectionSize(projection) > maxBytes {
		switch {
		case len(projection.Sources) > 0:
			projection.Sources = projection.Sources[:len(projection.Sources)-1]
			projection.Citations = projectionCitations(projection.Sources, result.Citations)
		case len(projection.Facts) > 0:
			projection.Facts = projection.Facts[:len(projection.Facts)-1]
		case projection.Summary != nil:
			projection.Summary = nil
		default:
			projection.Query = ""
			projection.Status = InfoProjectionFailed
			projection.FailureCode = "projection_size_exceeded"
			projection.MissingComponents = []string{"projection.capacity"}
			return projection
		}
		projection = finalizeInfoProjection(projection, result, true)
	}
	return projection
}

func infoProjectionSize(projection InfoEvidenceProjection) int {
	raw, err := json.Marshal(projection)
	if err != nil {
		return MaxInfoProjectionBytes + 1
	}
	return len(raw)
}

func projectionCitations(sources []InfoEvidenceSource, citations []string) []string {
	out := []string{}
	for _, source := range sources {
		out = appendProjectionCitation(out, source.URL, citations)
	}
	return out
}

func infoProjectionHasEvidence(projection InfoEvidenceProjection) bool {
	return projection.Summary != nil || len(projection.Facts) > 0 || len(projection.Sources) > 0
}

func resultHasSourceSnippets(result Result) bool {
	for _, source := range result.Results {
		if len(source.Snippets) > 0 || strings.TrimSpace(source.Snippet) != "" {
			return true
		}
	}
	return false
}

type rankedFact struct {
	fact  KeyFact
	index int
	score int
}

func rankedInfoFacts(facts []KeyFact, terms []string) []KeyFact {
	ranked := make([]rankedFact, 0, len(facts))
	maxScore := 0
	for index, fact := range facts {
		if strings.TrimSpace(fact.Claim) == "" {
			continue
		}
		score := infoEvidenceScore(fact.Claim, terms)
		if score > maxScore {
			maxScore = score
		}
		ranked = append(ranked, rankedFact{fact: fact, index: index, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].index < ranked[j].index
		}
		return ranked[i].score > ranked[j].score
	})
	out := make([]KeyFact, 0, len(ranked))
	for _, item := range ranked {
		if maxScore > 0 && item.score == 0 {
			continue
		}
		out = append(out, item.fact)
	}
	return out
}

type rankedSource struct {
	source Item
	index  int
	score  int
}

func rankedInfoSources(sources []Item, referencedIDs map[string]bool, citations, terms []string) []Item {
	citedURLs := map[string]bool{}
	for _, citation := range citations {
		citedURLs[strings.TrimSpace(citation)] = true
	}
	ranked := make([]rankedSource, 0, len(sources))
	maxScore := 0
	for index, source := range sources {
		text := strings.Join(append([]string{source.Title}, source.Snippets...), " ")
		if strings.TrimSpace(text) == "" {
			text = source.Snippet
		}
		score := infoEvidenceScore(text, terms)
		if referencedIDs[strings.TrimSpace(source.ID)] {
			score += 8
		}
		if citedURLs[strings.TrimSpace(source.URL)] {
			score += 4
		}
		if score > maxScore {
			maxScore = score
		}
		ranked = append(ranked, rankedSource{source: source, index: index, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].index < ranked[j].index
		}
		return ranked[i].score > ranked[j].score
	})
	out := make([]Item, 0, len(ranked))
	for _, item := range ranked {
		if maxScore > 0 && item.score == 0 {
			continue
		}
		out = append(out, item.source)
	}
	return out
}

func projectedSourceIDs(facts []InfoEvidenceFact) map[string]bool {
	out := map[string]bool{}
	for _, fact := range facts {
		for _, sourceID := range fact.SourceIDs {
			if sourceID = strings.TrimSpace(sourceID); sourceID != "" {
				out[sourceID] = true
			}
		}
	}
	return out
}

func projectInfoSource(source Item, terms []string) InfoEvidenceSource {
	snippets := source.Snippets
	if len(snippets) == 0 && strings.TrimSpace(source.Snippet) != "" {
		snippets = []string{source.Snippet}
	}
	type rankedSnippet struct {
		text  string
		index int
		score int
	}
	ranked := make([]rankedSnippet, 0, len(snippets))
	for index, snippet := range snippets {
		if strings.TrimSpace(snippet) != "" {
			ranked = append(ranked, rankedSnippet{text: snippet, index: index, score: infoEvidenceScore(snippet, terms)})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].index < ranked[j].index
		}
		return ranked[i].score > ranked[j].score
	})
	out := InfoEvidenceSource{
		Index: source.EvidenceIndex, ID: strings.TrimSpace(source.ID), Title: strings.TrimSpace(source.Title),
		URL: strings.TrimSpace(source.URL), PublishedAt: strings.TrimSpace(source.PublishedAt),
	}
	for _, snippet := range ranked {
		text, truncated := relevantEvidenceExcerpt(snippet.text, terms, maxProjectedSnippetBytes)
		out.Snippets = append(out.Snippets, InfoEvidenceText{
			Ref:  "source:" + itoa(source.EvidenceIndex) + ":snippet:" + itoa(snippet.index),
			Text: text, Truncated: truncated,
		})
		if len(out.Snippets) >= 2 {
			break
		}
	}
	out.OmittedSnippets = len(ranked) - len(out.Snippets)
	return out
}

func appendProjectionCitation(current []string, sourceURL string, citations []string) []string {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return current
	}
	for _, citation := range citations {
		if strings.TrimSpace(citation) != sourceURL {
			continue
		}
		for _, existing := range current {
			if existing == sourceURL {
				return current
			}
		}
		return append(append([]string(nil), current...), sourceURL)
	}
	return current
}

func infoProjectionTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	terms := []string{}
	seen := map[string]bool{}
	appendTerm := func(term string) {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 || seen[term] || projectionStopTerms[term] {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, field := range strings.FieldsFunc(query, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && !(r >= '\u4e00' && r <= '\u9fff')
	}) {
		appendTerm(field)
		runes := []rune(field)
		if containsCJKRunes(runes) {
			for index := 0; index+1 < len(runes); index++ {
				appendTerm(string(runes[index : index+2]))
			}
		}
	}
	return terms
}

var projectionStopTerms = map[string]bool{
	"search": true, "online": true, "find": true, "current": true, "latest": true, "today": true,
	"查询": true, "搜索": true, "联网": true, "当前": true, "最新": true, "今天": true, "一下": true,
}

func containsCJKRunes(runes []rune) bool {
	for _, r := range runes {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func infoEvidenceScore(text string, terms []string) int {
	lower := strings.ToLower(text)
	score := 0
	for _, term := range terms {
		if strings.Contains(lower, term) {
			score += len([]rune(term))
		}
	}
	return score
}

func relevantEvidenceExcerpt(text string, terms []string, maxBytes int) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) <= maxBytes {
		return text, false
	}
	lower := strings.ToLower(text)
	match := -1
	for _, term := range terms {
		if index := strings.Index(lower, term); index >= 0 && (match < 0 || index < match) {
			match = index
		}
	}
	if match >= 0 {
		start, end := evidenceSentenceBounds(text, match)
		if sentence := strings.TrimSpace(text[start:end]); sentence != "" && len(sentence) <= maxBytes {
			return sentence, true
		}
	}
	start := 0
	if match > maxBytes/4 {
		start = match - maxBytes/4
	}
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	end := start + maxBytes
	if end > len(text) {
		end = len(text)
		start = end - maxBytes
		for start < end && !utf8.RuneStart(text[start]) {
			start++
		}
	}
	for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return strings.TrimSpace(text[start:end]), true
}

func evidenceSentenceBounds(text string, match int) (int, int) {
	start := match
	for start > 0 {
		_, size := utf8.DecodeLastRuneInString(text[:start])
		if size <= 0 {
			break
		}
		previous := start - size
		r, _ := utf8.DecodeRuneInString(text[previous:start])
		if strings.ContainsRune(".!?。！？\n；;", r) {
			break
		}
		start = previous
	}
	end := match
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if size <= 0 {
			break
		}
		end += size
		if strings.ContainsRune(".!?。！？\n；;", r) {
			break
		}
	}
	return start, end
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := [20]byte{}
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
