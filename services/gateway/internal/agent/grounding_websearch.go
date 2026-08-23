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

func renderInfoSearchAnswer(projection websearch.InfoEvidenceProjection) string {
	switch projection.Status {
	case websearch.InfoProjectionFailed:
		return "联网搜索结果不可用：Info 聚合结果未通过完整性校验。"
	case websearch.InfoProjectionNoResults:
		return "没有找到带有可靠来源关联的联网搜索结果。"
	}
	if !websearch.InfoEvidenceProjectionHasEvidence(projection) {
		return "联网搜索结果不完整：投影容量内没有可安全展示的来源支持结论。"
	}

	markers := map[string]int{}
	for index, source := range projection.Sources {
		markers[source.ID] = index + 1
	}
	lines := []string{"联网搜索结果："}
	if projection.Summary != nil {
		if summary := cleanInfoEvidenceForUser(projection.Summary.Text); summary != "" {
			lines = append(lines, summary)
		}
	}
	if len(projection.Facts) > 0 {
		lines = append(lines, "", "事实：")
		for _, fact := range projection.Facts {
			claim := cleanInfoEvidenceForUser(fact.Claim)
			if claim != "" {
				lines = append(lines, "- "+claim+infoCitationMarkers(fact.SourceIDs, markers))
			}
		}
	}
	if len(projection.Conflicts) > 0 {
		lines = append(lines, "", "存在分歧：")
		for _, conflict := range projection.Conflicts {
			lines = append(lines, "- "+cleanInfoEvidenceForUser(conflict.Topic))
			for _, viewpoint := range conflict.Viewpoints {
				claim := cleanInfoEvidenceForUser(viewpoint.Claim)
				if claim != "" {
					lines = append(lines, "  - "+claim+infoCitationMarkers(viewpoint.SourceIDs, markers))
				}
			}
		}
	}
	if shouldRenderInfoFreshness(projection.Freshness) {
		parts := []string{}
		if status := cleanInfoEvidenceForUser(projection.Freshness.Status); status != "" {
			parts = append(parts, "状态 "+status)
		}
		if projection.Freshness.LatestSourceDate != nil {
			parts = append(parts, "最新来源日期 "+cleanInfoEvidenceForUser(*projection.Freshness.LatestSourceDate))
		}
		if risk := cleanInfoEvidenceForUser(projection.Freshness.StalenessRisk); risk != "" {
			parts = append(parts, "过时风险 "+risk)
		}
		if len(parts) > 0 {
			lines = append(lines, "", "时效性："+strings.Join(parts, "；")+"。")
		}
	}
	if len(projection.Uncertainty) > 0 || projection.Status == websearch.InfoProjectionPartial || len(projection.Findings) > 0 {
		lines = append(lines, "", "限制：")
		for _, uncertainty := range projection.Uncertainty {
			if text := cleanInfoEvidenceForUser(uncertainty); text != "" {
				lines = append(lines, "- "+text)
			}
		}
		for _, finding := range projection.Findings {
			lines = append(lines, "- Info 契约值需要审查："+cleanInfoEvidenceForUser(finding.Component)+" ("+cleanInfoEvidenceForUser(finding.Code)+")")
		}
		if projection.Status == websearch.InfoProjectionPartial {
			for _, omission := range projection.Omissions {
				lines = append(lines, fmt.Sprintf("- 结果投影省略 %s（%s，%d 项）。", cleanInfoEvidenceForUser(omission.Component), cleanInfoEvidenceForUser(omission.Reason), omission.Count))
			}
		}
	}
	if len(projection.Sources) > 0 {
		lines = append(lines, "", "来源：")
		for index, source := range projection.Sources {
			label := cleanInfoEvidenceForUser(source.Title)
			if label == "" {
				label = cleanInfoEvidenceForUser(source.ID)
			}
			if label == "" {
				label = fmt.Sprintf("来源 %d", index+1)
			}
			if source.Linkable {
				lines = append(lines, fmt.Sprintf("[%d] %s：%s", index+1, label, source.URL))
			} else {
				lines = append(lines, fmt.Sprintf("[%d] %s（不可链接来源）", index+1, label))
			}
		}
	}
	return strings.Join(lines, "\n")
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

func shouldRenderInfoFreshness(freshness websearch.Freshness) bool {
	return freshness.LatestSourceDate != nil || (strings.TrimSpace(freshness.StalenessRisk) != "" && !strings.EqualFold(strings.TrimSpace(freshness.StalenessRisk), "low"))
}

func cleanInfoEvidenceForUser(value string) string {
	value = html.UnescapeString(value)
	value = infoHTMLTagPattern.ReplaceAllString(value, " ")
	value = infoURLPattern.ReplaceAllString(value, "[外部链接已省略]")
	value = strings.NewReplacer("\\", "", "**", "", "__", "", "`", "", "[", "", "]", "", "<", "", ">", "").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}
