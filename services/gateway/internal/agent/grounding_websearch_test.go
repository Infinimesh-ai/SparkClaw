package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

func TestInfoRendererSeparatesAnswerFromSupportingDetails(t *testing.T) {
	projection := websearch.InfoEvidenceProjection{
		Status:      websearch.InfoProjectionPartial,
		Summary:     &websearch.InfoEvidenceText{Text: "最终发布日期尚未确定。"},
		Facts:       []websearch.InfoEvidenceFact{{Claim: "发布方暂未确认日期。", SourceIDs: []string{"a"}}},
		Conflicts:   []websearch.InfoEvidenceConflict{{Topic: "发布日期", Viewpoints: []websearch.InfoEvidenceViewpoint{{Claim: "报道预计周一。", SourceIDs: []string{"b"}}}}},
		Freshness:   websearch.Freshness{Status: "current", StalenessRisk: "high"},
		Uncertainty: []string{"需等待正式公告。"},
		Omissions:   []websearch.InfoOmission{{Component: "facts", Reason: "capacity", Count: 1}},
		Sources: []websearch.InfoEvidenceSource{
			{ID: "b", Title: "报道", URL: "https://example.com/report", Linkable: true},
			{ID: "a", Title: "发布方", URL: "https://example.com/official", Linkable: true},
		},
	}
	answer, details, ok := strings.Cut(renderInfoSearchAnswer(projection), "\n\n<details>\n<summary>查看详情</summary>\n\n")
	if !ok || answer != "最终发布日期尚未确定。" {
		t.Fatalf("default answer should contain only the supplied summary: %q", answer)
	}
	for _, want := range []string{
		"发布方暂未确认日期。 [2]", "报道预计周一。 [1]", "资料可能已有变化", "需等待正式公告。",
		"当前可用信息不完整", "[1] [报道](https://example.com/report)", "[2] [发布方](https://example.com/official)", "\n</details>",
	} {
		if !strings.Contains(details, want) {
			t.Errorf("details lost evidence %q: %s", want, details)
		}
	}
}

func TestInfoRendererWithoutSummaryStillShowsAnAnswer(t *testing.T) {
	projection := websearch.InfoEvidenceProjection{
		Status: websearch.InfoProjectionComplete,
		Facts: []websearch.InfoEvidenceFact{
			{Claim: "第一条事实。", SourceIDs: []string{"a"}},
			{Claim: "第二条事实。", SourceIDs: []string{"a"}},
		},
		Sources: []websearch.InfoEvidenceSource{{ID: "a", Title: "报告"}},
	}
	for _, summary := range []*websearch.InfoEvidenceText{nil, {Text: "  <b></b>  "}} {
		projection.Summary = summary
		answer, _, _ := strings.Cut(renderInfoSearchAnswer(projection), "\n\n<details>")
		if answer != "第一条事实。 [1]\n第二条事实。 [1]" {
			t.Fatalf("missing summary hid the result: %q", answer)
		}
	}
	projection.Summary = nil
	projection.Facts = nil
	projection.Conflicts = []websearch.InfoEvidenceConflict{{Topic: "日期", Viewpoints: []websearch.InfoEvidenceViewpoint{{Claim: "周一", SourceIDs: []string{"a"}}}}}
	answer, _, _ := strings.Cut(renderInfoSearchAnswer(projection), "\n\n<details>")
	if answer != "来源说法尚不一致，暂时无法给出确定结论。" {
		t.Fatalf("conflict-only result should remain explicit: %q", answer)
	}
}

func TestInfoRendererKeepsGoldDetailsSelectiveAndReadable(t *testing.T) {
	date := "2026-07-20"
	projection := websearch.InfoEvidenceProjection{
		Status:    websearch.InfoProjectionPartial,
		Summary:   &websearch.InfoEvidenceText{Text: "黄金参考价为877元/克。"},
		Freshness: websearch.Freshness{Status: "current", LatestSourceDate: &date, StalenessRisk: "medium"},
		Facts: []websearch.InfoEvidenceFact{
			{Claim: "黄金参考价为877元/克。", SourceIDs: []string{"quote"}},
			{Claim: "该报价对应国内黄金交易价格。", SourceIDs: []string{"quote"}},
			{Claim: "门店售价还包含品牌和加工费用。", SourceIDs: []string{"retail"}},
			{Claim: "不在简洁展示范围内的背景资料。", SourceIDs: []string{"background"}},
		},
		Uncertainty: []string{"报价会随市场波动。", "各门店的实际售价不同。", "更长的第三条补充说明。"},
		Omissions:   []websearch.InfoOmission{{Component: "aggregate.facts", Reason: "capacity", Count: 5}},
		Findings:    []websearch.InfoFinding{{Component: "aggregate.freshness.status", Code: "unknown_value"}},
		Sources: []websearch.InfoEvidenceSource{
			{ID: "background", Title: "背景材料", URL: "https://example.com/background", Linkable: true},
			{ID: "quote", Title: "交易报价", URL: "https://example.com/gold", Linkable: true},
			{ID: "retail", Title: "门店说明", URL: "https://example.com/retail", Linkable: true},
		},
	}
	answer := renderInfoSearchAnswer(projection)
	if strings.Count(answer, "黄金参考价为877元/克。") != 1 {
		t.Fatalf("summary should not be repeated in details: %s", answer)
	}
	for _, want := range []string{"国内黄金交易价格。 [1]", "品牌和加工费用。 [2]", "资料日期：2026-07-20",
		"[1] [交易报价](https://example.com/gold)", "[2] [门店说明](https://example.com/retail)", "报价会随市场波动。"} {
		if !strings.Contains(answer, want) {
			t.Errorf("missing useful context %q: %s", want, answer)
		}
	}
	for _, unwanted := range []string{"背景资料", "背景材料", "第三条补充", "事实：", "限制：", "current", "medium", "aggregate.", "unknown_value", "capacity", "投影"} {
		if strings.Contains(answer, unwanted) {
			t.Errorf("detail view still dumps %q: %s", unwanted, answer)
		}
	}
	if len(projection.Facts) != 4 || len(projection.Sources) != 3 || projection.Sources[0].ID != "background" {
		t.Fatal("display selection changed the evidence projection")
	}
}
