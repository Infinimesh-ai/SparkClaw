// Grounded summary builders for the web-search strategy, including the
// Info evidence projection rendering.
package agent

import (
	"html"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

var infoSummarySourceID = regexp.MustCompile(`(?i)\bsrc[_-][a-z0-9_-]+`)

func groundedWebSearchSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := webSearchAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	if latestCompletedWebSearchIsInfo(calls) {
		return answer, true
	}
	if cleaned := cleanUserFinalAnswer(fallback); cleaned != "" && shouldPreferModelFinal(goal, cleaned) {
		return cleaned, true
	}
	return answer, true
}

func webSearchAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "web.search" || !toolCallCompleted(call) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringValue(result["provider"])) == websearch.InfoProviderName {
			projection, _ := infoEvidenceProjection(call, result, websearch.MaxInfoProjectionBytes)
			return renderInfoSearchAnswer(projection), true
		}
		answer := strings.TrimSpace(stringValue(result["answer"]))
		count := intLikeValue(result["count"])
		if answer != "" && answer != "<nil>" {
			return answer, true
		}
		if count == 0 {
			return "没有找到可靠的联网搜索结果。", true
		}
		if citations := stringSliceValue(result["citations"]); len(citations) > 0 {
			return "找到了相关来源：" + strings.Join(citations, ", "), true
		}
	}
	return "", false
}

var (
	infoHTMLTagPattern     = regexp.MustCompile(`(?i)</?[a-z][^>]*>`)
	infoURLPattern         = regexp.MustCompile(`(?i)https?://[^\s)\]}>]+`)
	infoLeadingBlockMarker = regexp.MustCompile(`^(?:[#>*+-]+\s+)+`)
	infoLeadingOrdinal     = regexp.MustCompile(`^(\d+)([.)])(\s)`)
	infoSourceIDGroup      = regexp.MustCompile(`(?i)\s*[(（\[【]\s*(?:来源\s*[:：]?\s*)?src[_-][a-z0-9_-]+(?:\s*[,，、;；]\s*src[_-][a-z0-9_-]+)*\s*[)）\]】]`)
)

// Display limits keep the reading view short; the stored tool result retains
// every fact, conflict and note.
const (
	infoMaxDisplayFacts     = 3
	infoMaxConflictGroups   = 1
	infoMaxUncertaintyNotes = 2
	infoFallbackSourceCount = 3
)

func latestCompletedWebSearchIsInfo(calls []app.ToolCall) bool {
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call.Tool != "web.search" || !toolCallCompleted(call) {
			continue
		}
		result, ok := anyMap(call.Result)
		return ok && strings.TrimSpace(stringValue(result["provider"])) == websearch.InfoProviderName
	}
	return false
}

// This is a concise reading view, not an export of the evidence projection.
// Whole claims retain Info order and carry their own sources inline; raw
// evidence and diagnostics remain available in the stored tool result.
func renderInfoSearchAnswer(projection websearch.InfoEvidenceProjection) string {
	switch projection.Status {
	case websearch.InfoProjectionFailed:
		return "暂时无法获取可靠的搜索结果，请稍后再试。"
	case websearch.InfoProjectionNoResults:
		return "暂时没有找到可靠的搜索结果。"
	}
	if !websearch.InfoEvidenceProjectionHasEvidence(projection) {
		return "当前搜索结果不足以给出可靠结论。"
	}

	// The provider summary has no per-claim source edges and may describe
	// material absent from the validated projection, even without source IDs.
	// Build the visible answer from facts with validated source edges instead.
	facts := infoDisplayFacts(projection.Facts)
	conflicts := projection.Conflicts
	if len(conflicts) > infoMaxConflictGroups {
		conflicts = conflicts[:infoMaxConflictGroups]
	}
	sources := infoDisplaySources(projection.Sources, facts, conflicts)
	labels := map[string]string{}
	for _, source := range sources {
		if id := strings.TrimSpace(source.ID); id != "" {
			labels[id] = infoSourceLabel(source)
		}
	}

	lines := []string{}
	cited := false
	cite := func(claim string, sourceIDs []string) string {
		attribution := infoInlineSources(sourceIDs, labels)
		cited = cited || attribution != ""
		return infoSentence(claim) + attribution
	}
	for _, fact := range facts {
		if len(facts) == 1 {
			lines = append(lines, cite(fact.Claim, fact.SourceIDs))
		} else {
			lines = append(lines, "- "+cite(fact.Claim, fact.SourceIDs))
		}
	}
	if len(facts) == 0 {
		if len(conflicts) > 0 {
			lines = append(lines, "来源说法尚不一致，暂时无法给出确定结论。")
		} else {
			lines = append(lines, "当前搜索结果不足以给出可靠结论。")
		}
	}
	for _, conflict := range conflicts {
		topic := cleanInfoEvidenceForUser(conflict.Topic)
		if topic == "" {
			topic = "这项信息"
		}
		lines = append(lines, "", "关于“"+topic+"”，来源说法不一致：")
		// Keep both sides of the selected conflict; never turn disagreement
		// into a single apparently settled claim to shorten the display.
		for _, viewpoint := range conflict.Viewpoints {
			if claim := cleanInfoEvidenceForUser(viewpoint.Claim); claim != "" {
				lines = append(lines, "- "+cite(claim, viewpoint.SourceIDs))
			}
		}
	}
	if !cited && len(sources) > 0 {
		names := []string{}
		for _, source := range sources {
			names = append(names, infoSourceLabel(source))
		}
		lines = append(lines, "", "来源："+strings.Join(names, "、"))
	}

	notes := []string{}
	if len(projection.Conflicts) > len(conflicts) {
		notes = append(notes, "其他信息也存在不同说法，请结合来源原文判断。")
	}
	if projection.Freshness.LatestSourceDate != nil {
		if date := cleanInfoEvidenceForUser(*projection.Freshness.LatestSourceDate); date != "" {
			notes = append(notes, "资料日期："+date+"。")
		}
	}
	if projection.Freshness.StalenessRisk == "high" ||
		(projection.Freshness.StalenessRisk == "medium" && projection.Freshness.LatestSourceDate == nil) {
		notes = append(notes, "资料可能已有变化，请以来源的最新信息为准。")
	}
	seenNotes := infoDisplaySeen{}
	for _, fact := range facts {
		seenNotes.add(fact.Claim)
	}
	noteCount := 0
	for _, uncertainty := range projection.Uncertainty {
		note := "部分信息缺少可核验来源。"
		if !infoSummaryHasUnlistedSource(uncertainty, sources) {
			note = cleanInfoEvidenceForUser(uncertainty)
		}
		if !seenNotes.add(note) {
			continue
		}
		notes = append(notes, infoSentence(note))
		noteCount++
		if noteCount == infoMaxUncertaintyNotes {
			break
		}
	}
	if projection.Status == websearch.InfoProjectionPartial {
		notes = append(notes, "当前可用信息不完整，以上内容仅供参考。")
	} else if len(projection.Findings) > 0 {
		notes = append(notes, "部分资料信息尚待确认。")
	}
	if len(notes) > 0 {
		lines = append(lines, "", strings.Join(notes, ""))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// infoSourceLabel is the visible attribution: a Markdown link for linkable
// sources, plain text otherwise.
func infoSourceLabel(source websearch.InfoEvidenceSource) string {
	label := cleanInfoEvidenceForUser(source.Title)
	if label == "" {
		if parsed, err := url.Parse(strings.TrimSpace(source.URL)); err == nil && source.Linkable {
			label = strings.TrimPrefix(parsed.Hostname(), "www.")
		}
	}
	if label == "" {
		label = "未命名来源"
	}
	if target := infoMarkdownLinkTarget(source); target != "" {
		return "[" + label + "](" + target + ")"
	}
	return label
}

func infoInlineSources(sourceIDs []string, labels map[string]string) string {
	parts := []string{}
	seen := map[string]bool{}
	for _, sourceID := range sourceIDs {
		label := labels[strings.TrimSpace(sourceID)]
		if label != "" && !seen[label] {
			seen[label] = true
			parts = append(parts, label)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "来源：" + strings.Join(parts, "、")
}

// infoSentence ends display text with sentence punctuation so an inline
// attribution or the next note reads as a separate sentence.
func infoSentence(text string) string {
	text = strings.TrimSpace(text)
	for _, end := range []string{"。", "！", "？", "!", "?", "…", "."} {
		if strings.HasSuffix(text, end) {
			return text
		}
	}
	if text = strings.TrimRight(text, ";；,，:： "); text == "" {
		return ""
	}
	return text + "。"
}

func infoSummaryHasUnlistedSource(summary string, sources []websearch.InfoEvidenceSource) bool {
	listed := make(map[string]bool, len(sources))
	for _, source := range sources {
		listed[strings.ToLower(strings.TrimSpace(source.ID))] = true
	}
	for _, id := range infoSummarySourceID.FindAllString(summary, -1) {
		if !listed[strings.ToLower(id)] {
			return true
		}
	}
	return false
}

// infoDisplaySeen suppresses repeated display text that differs only in
// width, letter case or trailing punctuation.
type infoDisplaySeen map[string]bool

func (seen infoDisplaySeen) add(text string) bool {
	key := strings.ToLower(strings.TrimRight(norm.NFKC.String(text), "。.!！?？;；,，: \t"))
	if key == "" || seen[key] {
		return false
	}
	seen[key] = true
	return true
}

func infoDisplayFacts(facts []websearch.InfoEvidenceFact) []websearch.InfoEvidenceFact {
	out := []websearch.InfoEvidenceFact{}
	seen := infoDisplaySeen{}
	for _, fact := range facts {
		fact.Claim = cleanInfoEvidenceForUser(fact.Claim)
		if !seen.add(fact.Claim) {
			continue
		}
		out = append(out, fact)
		if len(out) == infoMaxDisplayFacts {
			break
		}
	}
	return out
}

// infoMarkdownLinkTarget returns a link target that the WebChat and plain-text
// link patterns accept as one token, or "" when the source is not linkable.
func infoMarkdownLinkTarget(source websearch.InfoEvidenceSource) string {
	if !source.Linkable {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(source.URL))
	if err != nil {
		return ""
	}
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(parsed.String())
}

func infoDisplaySources(sources []websearch.InfoEvidenceSource, facts []websearch.InfoEvidenceFact, conflicts []websearch.InfoEvidenceConflict) []websearch.InfoEvidenceSource {
	referenced := map[string]bool{}
	for _, fact := range facts {
		for _, id := range fact.SourceIDs {
			if id = strings.TrimSpace(id); id != "" {
				referenced[id] = true
			}
		}
	}
	for _, conflict := range conflicts {
		for _, viewpoint := range conflict.Viewpoints {
			for _, id := range viewpoint.SourceIDs {
				if id = strings.TrimSpace(id); id != "" {
					referenced[id] = true
				}
			}
		}
	}
	out := []websearch.InfoEvidenceSource{}
	for _, source := range sources {
		if referenced[strings.TrimSpace(source.ID)] || (len(referenced) == 0 && len(out) < infoFallbackSourceCount) {
			out = append(out, source)
		}
	}
	return out
}

func cleanInfoEvidenceForUser(value string) string {
	value = html.UnescapeString(value)
	value = infoHTMLTagPattern.ReplaceAllString(value, " ")
	// Source IDs are internal evidence keys; attribution is rendered from the
	// validated source edges instead.
	value = infoSourceIDGroup.ReplaceAllString(value, "")
	value = infoSummarySourceID.ReplaceAllString(value, "")
	// A URL already enclosed in parentheses (such as a Markdown link target)
	// keeps them; a bare URL gets its own so the notice reads as an aside.
	value = infoURLPattern.ReplaceAllString(value, "\x00")
	value = strings.NewReplacer("(\x00)", "(外部链接已省略)", "\x00", "（外部链接已省略）").Replace(value)
	value = strings.NewReplacer("\\", "", "**", "", "__", "", "`", "", "[", "", "]", "", "<", "", ">", "").Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	// Display text is placed at line starts; keep it from becoming a heading,
	// quote or list item in the Markdown-like message renderer.
	value = infoLeadingBlockMarker.ReplaceAllString(value, "")
	return infoLeadingOrdinal.ReplaceAllStringFunc(value, func(match string) string {
		parts := infoLeadingOrdinal.FindStringSubmatch(match)
		return parts[1] + map[string]string{".": "．", ")": "）"}[parts[2]] + parts[3]
	})
}
