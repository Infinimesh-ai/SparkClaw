package agent

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	pptxScopeFact        = "pptx_scope"
	pptxSlideIndexesFact = "pptx_slide_indexes"
	pptxScopeSingleSlide = "single_slide"
	pptxScopeWholeDeck   = "whole_deck"
	pptxScopeStructural  = "structural"
	pptxScopeExactText   = "exact_text"
	pptxScopeUnspecified = "unspecified"
	pptxScopeUnsupported = "unsupported_target"
)

type pptxEditScopeGrounding struct {
	Scope        string
	SlideIndexes []int
	Reason       string
}

var englishOrdinalSlidePattern = regexp.MustCompile(`(?i)(?:slide|page)\s*(?:number\s*)?#?\s*([0-9]+)`)

func documentPPTXReadRoutingExamples() []string {
	return []string{"Explain the attached presentation"}
}

func documentPPTXEditRoutingExamples() []string {
	// Keep one embedding anchor so adding PPTX scope coverage does not silently
	// reweight the shared document.edit centroid. Scope variants are exercised
	// by deterministic grounding and route evals below the semantic candidate.
	return []string{"优化附件演示文稿的第 3 页"}
}

func groundPPTXEditScope(text string) pptxEditScopeGrounding {
	query := strings.ToLower(strings.TrimSpace(text))
	for _, marker := range []string{
		"smartart", "animation", "chart data", "chart dataset", "slide master", "presentation master", "macro",
		"智能图形", "动画", "图表数据", "母版", "宏",
	} {
		if strings.Contains(query, marker) {
			return pptxEditScopeGrounding{Scope: pptxScopeUnsupported, Reason: "pptx_edit_target_unsupported"}
		}
	}
	indexes := explicitSlideIndexes(text)
	for _, marker := range []string{
		"whole deck", "entire deck", "entire presentation", "whole presentation", "all slides",
		"整份演示文稿", "整个演示文稿", "整套演示文稿", "全部幻灯片", "所有幻灯片", "每一页",
	} {
		if strings.Contains(query, marker) {
			return pptxEditScopeGrounding{Scope: pptxScopeWholeDeck}
		}
	}
	for _, marker := range []string{
		"add slide", "add a slide", "append slide", "append a slide", "insert slide", "insert a slide",
		"duplicate slide", "duplicate the slide", "delete slide", "delete the slide",
		"新增一页", "添加一页", "插入一页", "追加一页", "复制幻灯片", "删除幻灯片",
	} {
		if strings.Contains(query, marker) {
			return pptxEditScopeGrounding{Scope: pptxScopeStructural, SlideIndexes: indexes}
		}
	}
	if len(indexes) > 0 {
		return pptxEditScopeGrounding{Scope: pptxScopeSingleSlide, SlideIndexes: indexes}
	}
	for _, marker := range []string{"replace ", "replace\n", "替换", "改成", "更正"} {
		if strings.Contains(query, marker) {
			return pptxEditScopeGrounding{Scope: pptxScopeExactText}
		}
	}
	return pptxEditScopeGrounding{Scope: pptxScopeUnspecified, Reason: "pptx_edit_scope_unspecified"}
}

func scopePPTXDirectoryEntries(route app.RouteDecision, entries []app.ToolDirectoryEntry) []app.ToolDirectoryEntry {
	if !strings.EqualFold(strings.TrimSpace(route.Facts["document_format"]), app.DocumentFormatPPTX) {
		return entries
	}
	allowed := map[string]bool{}
	switch strings.TrimSpace(route.Facts[pptxScopeFact]) {
	case pptxScopeSingleSlide:
		allowed["update_slide"] = true
	case pptxScopeWholeDeck:
		allowed["update_deck"] = true
	case pptxScopeExactText:
		allowed["replace_text"] = true
	case pptxScopeStructural:
		for _, operation := range []string{"add_slide", "duplicate_slide", "delete_slide"} {
			allowed[operation] = true
		}
	default:
		return nil
	}
	filtered := make([]app.ToolDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		operation := strings.TrimSpace(entry.Capability.Qualifiers[app.CapabilityQualifierOperation])
		if allowed[operation] {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func explicitSlideIndexes(text string) []int {
	values := []int{}
	for _, pattern := range []*regexp.Regexp{arabicSlideOrdinalPattern, englishOrdinalSlidePattern} {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			value, err := strconv.Atoi(match[1])
			if err == nil && value > 0 && !slices.Contains(values, value) {
				values = append(values, value)
			}
		}
	}
	for _, match := range chineseSlideOrdinalPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		if value, ok := chineseOrdinalValue(match[1]); ok && !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	return values
}

func encodePPTXSlideIndexes(indexes []int) string {
	values := make([]string, 0, len(indexes))
	for _, index := range indexes {
		if index > 0 {
			values = append(values, strconv.Itoa(index))
		}
	}
	return strings.Join(values, ",")
}

func decodePPTXSlideIndexes(value string) []int {
	indexes := []int{}
	for _, item := range strings.Split(value, ",") {
		index, err := strconv.Atoi(strings.TrimSpace(item))
		if err == nil && index > 0 && !slices.Contains(indexes, index) {
			indexes = append(indexes, index)
		}
	}
	return indexes
}
