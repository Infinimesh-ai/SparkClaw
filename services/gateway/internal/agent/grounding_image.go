// Grounded summary builders for the image-inspect strategy.
package agent

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func groundedImageInspectSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	if !imageInspectCanFinalize(goal) {
		return "", false
	}
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "images.inspect" || !toolCallCompleted(call) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		summary := strings.TrimSpace(stringValue(result["summary"]))
		if summary == "" || summary == "<nil>" {
			continue
		}
		return strings.TrimSpace(summary), true
	}
	return "", false
}

func imageInspectCanFinalize(goal string) bool {
	lower := strings.ToLower(goal)
	if isActionOrModificationGoal(goal) {
		return false
	}
	if containsAny(lower,
		"查证", "验证", "核实", "真假", "是否真实", "是真的吗", "真的假的", "可靠", "来源", "出处", "官方",
		"最新", "今天", "昨天", "昨日", "联网", "上网", "搜索", "查一下", "网页", "新闻",
		"compare", "comparison", "versus", " vs ", "比较", "对比",
	) {
		return false
	}
	return true
}
