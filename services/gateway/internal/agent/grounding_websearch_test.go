package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

func TestInfoRendererDoesNotPresentSummaryWithUnlistedSourceIDs(t *testing.T) {
	projection := websearch.InfoEvidenceProjection{
		Status:  websearch.InfoProjectionPartial,
		Summary: &websearch.InfoEvidenceText{Text: "已核验的事件；未经核验的事件。"},
		Facts:   []websearch.InfoEvidenceFact{{Claim: "已核验的事件。", SourceIDs: []string{"src_001"}}},
		Sources: []websearch.InfoEvidenceSource{{ID: "src_001", Title: "来源", URL: "https://example.com/source", Linkable: true}},
	}
	answer := renderInfoSearchAnswer(projection)
	if strings.Contains(answer, "未经核验的事件") || strings.Contains(answer, "src_002") {
		t.Fatalf("summary exposed an unlisted source: %s", answer)
	}
	if answer != "已核验的事件。来源：[来源](https://example.com/source)\n\n当前可用信息不完整，以上内容仅供参考。" {
		t.Fatalf("grounded facts or their link disappeared: %s", answer)
	}
}

func TestInfoRendererAttachesSourcesInlineWithoutDisclosure(t *testing.T) {
	projection := websearch.InfoEvidenceProjection{
		Status:      websearch.InfoProjectionPartial,
		Summary:     &websearch.InfoEvidenceText{Text: "最终发布日期尚未确定。"},
		Facts:       []websearch.InfoEvidenceFact{{Claim: "发布方暂未确认日期。", SourceIDs: []string{"a"}}},
		Conflicts:   []websearch.InfoEvidenceConflict{{Topic: "发布日期", Viewpoints: []websearch.InfoEvidenceViewpoint{{Claim: "报道预计周一", SourceIDs: []string{"b"}}}}},
		Freshness:   websearch.Freshness{Status: "current", StalenessRisk: "high"},
		Uncertainty: []string{"需等待正式公告。"},
		Omissions:   []websearch.InfoOmission{{Component: "facts", Reason: "capacity", Count: 1}},
		Sources: []websearch.InfoEvidenceSource{
			{ID: "b", Title: "报道", URL: "https://example.com/report", Linkable: true},
			{ID: "a", Title: "发布方", URL: "https://example.com/official", Linkable: true},
		},
	}
	want := "发布方暂未确认日期。来源：[发布方](https://example.com/official)\n\n" +
		"关于“发布日期”，来源说法不一致：\n- 报道预计周一。来源：[报道](https://example.com/report)\n\n" +
		"资料可能已有变化，请以来源的最新信息为准。需等待正式公告。当前可用信息不完整，以上内容仅供参考。"
	if answer := renderInfoSearchAnswer(projection); answer != want {
		t.Fatalf("answer should carry each source after its own claim:\n%s", answer)
	}
}

func TestInfoRendererWithoutSummaryStillShowsAnAnswer(t *testing.T) {
	projection := websearch.InfoEvidenceProjection{
		Status: websearch.InfoProjectionComplete,
		Facts: []websearch.InfoEvidenceFact{
			{Claim: "第一条事实。", SourceIDs: []string{"a"}},
			{Claim: "第二条事实", SourceIDs: []string{"a"}},
		},
		Sources: []websearch.InfoEvidenceSource{{ID: "a", Title: "报告"}},
	}
	for _, summary := range []*websearch.InfoEvidenceText{nil, {Text: "  <b></b>  "}} {
		projection.Summary = summary
		if answer := renderInfoSearchAnswer(projection); answer != "- 第一条事实。来源：报告\n- 第二条事实。来源：报告" {
			t.Fatalf("missing summary hid the result: %q", answer)
		}
	}
	projection.Summary = nil
	projection.Facts = nil
	projection.Conflicts = []websearch.InfoEvidenceConflict{{Topic: "日期", Viewpoints: []websearch.InfoEvidenceViewpoint{{Claim: "周一", SourceIDs: []string{"a"}}}}}
	if answer := renderInfoSearchAnswer(projection); !strings.HasPrefix(answer, "来源说法尚不一致，暂时无法给出确定结论。\n\n关于“日期”") {
		t.Fatalf("conflict-only result should remain explicit: %q", answer)
	}
}

func TestInfoRendererKeepsGoldAnswerSelectiveAndReadable(t *testing.T) {
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
		t.Fatalf("summary should not be repeated: %s", answer)
	}
	for _, want := range []string{
		"- 黄金参考价为877元/克。来源：[交易报价](https://example.com/gold)\n",
		"- 门店售价还包含品牌和加工费用。来源：[门店说明](https://example.com/retail)\n",
		"资料日期：2026-07-20。", "报价会随市场波动。",
	} {
		if !strings.Contains(answer, want) {
			t.Errorf("missing useful context %q: %s", want, answer)
		}
	}
	for _, unwanted := range []string{"背景资料", "背景材料", "第三条补充", "<details>", "参考来源", "[1]", "事实：", "限制：", "current", "medium", "aggregate.", "unknown_value", "capacity", "投影"} {
		if strings.Contains(answer, unwanted) {
			t.Errorf("answer still dumps %q: %s", unwanted, answer)
		}
	}
	if len(projection.Facts) != 4 || len(projection.Sources) != 3 || projection.Sources[0].ID != "background" {
		t.Fatal("display selection changed the evidence projection")
	}
}

func TestInfoRendererRemovesInternalSourceIDsFromDisplayText(t *testing.T) {
	projection := websearch.InfoEvidenceProjection{
		Status: websearch.InfoProjectionComplete,
		Facts: []websearch.InfoEvidenceFact{
			{Claim: "宇航员计划于10月29日出舱 (src_002)", SourceIDs: []string{"src_001"}},
			{Claim: "龙飞船送达约2.86吨补给【src_002、src_003】，据src_003报道将于11月返回。", SourceIDs: []string{"src_002", "src_003"}},
		},
		Uncertainty: []string{"出舱时间可能调整（src_001）"},
		Sources: []websearch.InfoEvidenceSource{
			{ID: "src_001", Title: "NASA 最新动态", URL: "https://www.nasa.gov/a", Linkable: true},
			{ID: "src_002", Title: "NASA 对接通报", URL: "https://www.nasa.gov/b", Linkable: true},
			{ID: "src_003", URL: "https://www.spacex.com/c", Linkable: true},
		},
	}
	want := "- 宇航员计划于10月29日出舱。来源：[NASA 最新动态](https://www.nasa.gov/a)\n" +
		"- 龙飞船送达约2.86吨补给，据报道将于11月返回。来源：[NASA 对接通报](https://www.nasa.gov/b)、[spacex.com](https://www.spacex.com/c)\n\n" +
		"出舱时间可能调整。"
	if answer := renderInfoSearchAnswer(projection); answer != want {
		t.Fatalf("internal source IDs leaked or attribution changed:\n%s", answer)
	}
}

func TestInfoRendererKeepsDisplayTextAndLinksWellFormed(t *testing.T) {
	empty := " <b></b> "
	projection := websearch.InfoEvidenceProjection{
		Status:    websearch.InfoProjectionComplete,
		Freshness: websearch.Freshness{Status: "current", LatestSourceDate: &empty},
		Facts: []websearch.InfoEvidenceFact{
			{Claim: "# 2024. 销量上升", SourceIDs: []string{" a "}},
			{Claim: "２０２４. 销量上升。", SourceIDs: []string{"a"}},
			{Claim: "- 详见 https://example.com/raw", SourceIDs: []string{"b"}},
		},
		Uncertainty: []string{"详见 （外部链接已省略）。", "仍需确认"},
		Sources: []websearch.InfoEvidenceSource{
			{ID: "a ", Title: "Space", URL: "https://example.com/a b?q=x y", Linkable: true},
			{ID: "b", Title: "Paren", URL: "https://example.com/(x)", Linkable: true},
		},
	}
	want := "- 2024． 销量上升。来源：[Space](https://example.com/a%20b?q=x%20y)\n" +
		"- 详见 （外部链接已省略）。来源：[Paren](https://example.com/%28x%29)\n\n仍需确认。"
	if answer := renderInfoSearchAnswer(projection); answer != want {
		t.Fatalf("display text should not become Markdown blocks or repeat width variants:\n%s", answer)
	}
}
