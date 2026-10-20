// Grounded summary builders for the web-search strategy, including the
// Info evidence projection rendering.
package agent

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

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
	infoHTMLTagPattern = regexp.MustCompile(`(?i)</?[a-z][^>]*>`)
	infoURLPattern     = regexp.MustCompile(`(?i)https?://[^\s)\]}>]+`)
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
// Whole claims retain Info order and their own source edges; raw evidence and
// diagnostics remain available in the stored tool result.
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

	answer := ""
	if projection.Summary != nil {
		answer = cleanInfoEvidenceForUser(projection.Summary.Text)
	}
	facts := infoDisplayFacts(projection.Facts, answer)
	conflicts := projection.Conflicts
	if len(conflicts) > 1 {
		conflicts = conflicts[:1]
	}
	sources := infoDisplaySources(projection.Sources, facts, conflicts)
	markers := map[string]int{}
	for index, source := range sources {
		markers[source.ID] = index + 1
	}

	lines := []string{}
	claims := []string{}
	for _, fact := range facts {
		claims = append(claims, fact.Claim+infoCitationMarkers(fact.SourceIDs, markers))
	}
	if answer == "" {
		answer = strings.Join(claims, "\n")
	} else {
		for _, claim := range claims {
			lines = append(lines, "- "+claim)
		}
	}
	if answer == "" {
		if len(conflicts) > 0 {
			answer = "来源说法尚不一致，暂时无法给出确定结论。"
		} else {
			answer = "当前搜索结果不足以给出可靠结论。"
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
				lines = append(lines, "- "+claim+infoCitationMarkers(viewpoint.SourceIDs, markers))
			}
		}
	}
	if len(projection.Conflicts) > len(conflicts) {
		lines = append(lines, "", "其他信息也存在不同说法，请结合来源原文判断。")
	}
	if projection.Freshness.LatestSourceDate != nil {
		lines = append(lines, "", "资料日期："+cleanInfoEvidenceForUser(*projection.Freshness.LatestSourceDate))
	}
	if projection.Freshness.StalenessRisk == "high" ||
		(projection.Freshness.StalenessRisk == "medium" && projection.Freshness.LatestSourceDate == nil) {
		lines = append(lines, "", "资料可能已有变化，请以来源的最新信息为准。")
	}
	seenNotes := map[string]bool{answer: true}
	noteCount := 0
	for _, uncertainty := range projection.Uncertainty {
		note := cleanInfoEvidenceForUser(uncertainty)
		if note == "" || seenNotes[note] {
			continue
		}
		seenNotes[note] = true
		lines = append(lines, "", note)
		noteCount++
		if noteCount == 2 {
			break
		}
	}
	if projection.Status == websearch.InfoProjectionPartial {
		lines = append(lines, "", "当前可用信息不完整，以上内容仅供参考。")
	} else if len(projection.Findings) > 0 {
		lines = append(lines, "", "部分资料信息尚待确认。")
	}
	if len(sources) > 0 {
		lines = append(lines, "", "参考来源")
		for index, source := range sources {
			label := cleanInfoEvidenceForUser(source.Title)
			if label == "" {
				label = fmt.Sprintf("来源 %d", index+1)
			}
			if source.Linkable {
				// Prevent source URLs from terminating the Markdown link.
				target := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(source.URL)
				label = "[" + label + "](" + target + ")"
			}
			lines = append(lines, fmt.Sprintf("[%d] %s", index+1, label))
		}
	}
	details := strings.TrimSpace(strings.Join(lines, "\n"))
	if details == "" {
		return answer
	}
	return answer + "\n\n<details>\n<summary>查看详情</summary>\n\n" + details + "\n</details>"
}

func infoDisplayFacts(facts []websearch.InfoEvidenceFact, summary string) []websearch.InfoEvidenceFact {
	out := []websearch.InfoEvidenceFact{}
	seen := map[string]bool{summary: true}
	for _, fact := range facts {
		fact.Claim = cleanInfoEvidenceForUser(fact.Claim)
		if fact.Claim == "" || seen[fact.Claim] {
			continue
		}
		seen[fact.Claim] = true
		out = append(out, fact)
		if len(out) == 2 {
			break
		}
	}
	return out
}

func infoDisplaySources(sources []websearch.InfoEvidenceSource, facts []websearch.InfoEvidenceFact, conflicts []websearch.InfoEvidenceConflict) []websearch.InfoEvidenceSource {
	referenced := map[string]bool{}
	for _, fact := range facts {
		for _, id := range fact.SourceIDs {
			referenced[strings.TrimSpace(id)] = true
		}
	}
	for _, conflict := range conflicts {
		for _, viewpoint := range conflict.Viewpoints {
			for _, id := range viewpoint.SourceIDs {
				referenced[strings.TrimSpace(id)] = true
			}
		}
	}
	out := []websearch.InfoEvidenceSource{}
	for _, source := range sources {
		if referenced[source.ID] || (len(referenced) == 0 && len(out) < 3) {
			out = append(out, source)
		}
	}
	return out
}

func infoCitationMarkers(sourceIDs []string, markers map[string]int) string {
	parts := []string{}
	seen := map[int]bool{}
	for _, sourceID := range sourceIDs {
		marker := markers[strings.TrimSpace(sourceID)]
		if marker > 0 && !seen[marker] {
			seen[marker] = true
			parts = append(parts, fmt.Sprintf("[%d]", marker))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, "")
}

func cleanInfoEvidenceForUser(value string) string {
	value = html.UnescapeString(value)
	value = infoHTMLTagPattern.ReplaceAllString(value, " ")
	value = infoURLPattern.ReplaceAllString(value, "[外部链接已省略]")
	value = strings.NewReplacer("\\", "", "**", "", "__", "", "`", "", "[", "", "]", "", "<", "", ">", "").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}
