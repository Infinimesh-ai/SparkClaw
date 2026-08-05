package agent

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestRealIntentFusionAlphaCalibration(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_INTENT_CALIBRATION") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_INTENT_CALIBRATION=1 to call the configured models")
	}
	cfg, err := config.Load(filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = false
	catalog := capability.MustDefaultCatalog()
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	models := modelrouter.New(cfg)
	router := newSemanticIntentRouter(catalog.Revision(), graph)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	if _, err := router.initializeEmbeddingIndex(ctx, models); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{store: store.NewMemoryStore(), models: models, semanticRouter: router}
	eligible := graph.EligibleCandidates(app.MessageSourceWeb)

	tests := []struct {
		query     string
		expected  string
		confusion string
	}{
		{query: "一分钟后跟我说该吃饭了", expected: "schedule.manage#create"},
		{query: "之前让我办的定时事项都有哪些", expected: "schedule.manage#read"},
		{query: "把先前那个吃饭提示往后挪一小时", expected: "schedule.manage#edit"},
		{query: "那个吃饭提示不用再叫我了", expected: "schedule.manage#delete"},
		{query: "法国首都是哪座城市", expected: "conversation.answer#answer"},
		{query: "最近黄金多少钱一克", expected: "browser.internet_search#search"},
		{query: "杭州接下来几小时冷不冷", expected: "browser.weather#read"},
		{query: "访问 https://example.com", expected: "browser.automation#open"},
		{query: "点一下当前页面里的下一步", expected: "browser.interaction#interact"},
		{query: "说说 report.pdf 里讲了什么", expected: "document.read#read"},
		{query: "识别 scanned.pdf 里的扫描文字", expected: "document.read#read", confusion: "document.edit#transform"},
		{query: "提取 report.pdf 第 3 页的文字", expected: "document.read#read", confusion: "document.edit#transform"},
		{query: "What does page 3 of report.pdf say?", expected: "document.read#read", confusion: "document.edit#transform"},
		{query: "不要导出第 3 页，只告诉我 report.pdf 这一页写了什么", expected: "document.read#read", confusion: "document.edit#transform"},
		{query: "把 note.docx 的标题换成季度总结", expected: "document.edit#edit"},
		{query: "把 report.pdf 第二页旋转一下", expected: "document.edit#transform"},
		{query: "把 report.pdf 第 3 页导出为新 PDF", expected: "document.edit#transform", confusion: "document.read#read"},
		{query: "删除 report.pdf 的第 3 页", expected: "document.edit#transform", confusion: "document.read#read"},
		{query: "Rotate pages 2 and 4 of report.pdf clockwise", expected: "document.edit#transform", confusion: "document.read#read"},
		{query: "把 report.pdf 按页拆开", expected: "document.edit#transform", confusion: "document.read#read"},
		{query: "比较杭州和上海今天的天气", expected: "browser.internet_search#search", confusion: "browser.weather#read"},
		{query: "打开QQ邮箱的草稿箱", expected: "browser.interaction#interact", confusion: "browser.automation#open"},
		{query: "明天下午三点我参加项目复盘", expected: "conversation.answer#answer", confusion: "schedule.manage#create"},
	}

	type channelScores struct {
		embedding embeddingChannelResult
		tree      treeChannelResult
	}
	results := make([]channelScores, len(tests))
	semaphore := make(chan struct{}, 3)
	var wait sync.WaitGroup
	for index, test := range tests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			embeddingCh := make(chan embeddingChannelResult, 1)
			treeCh := make(chan treeChannelResult, 1)
			go func() {
				embeddingCh <- runtime.scoreEmbeddingChannel(ctx, "", fmt.Sprintf("calibration_%d", index), test.query, eligible)
			}()
			go func() {
				treeCh <- runtime.scoreTreeChannel(ctx, "", fmt.Sprintf("calibration_%d", index), test.query, "", app.MessageSourceWeb, eligible)
			}()
			results[index] = channelScores{embedding: <-embeddingCh, tree: <-treeCh}
		}()
	}
	wait.Wait()

	bestAlpha, bestCorrect := 0.0, -1
	for step := 0; step <= 20; step++ {
		alpha := float64(step) / 20
		correct := 0
		for index, test := range tests {
			result := results[index]
			if result.embedding.state.Status != semanticrouting.ChannelHealthy || result.tree.state.Status != semanticrouting.ChannelHealthy {
				t.Fatalf("semantic channel failed for %q: embedding=%#v tree=%#v", test.query, result.embedding.state, result.tree.state)
			}
			calibration := semanticrouting.DefaultCalibration()
			calibration.Alpha = alpha
			ranked, err := semanticrouting.RankFusion(eligible, result.embedding.evidence, result.tree.evidence, map[string]semanticrouting.ChannelState{
				"embedding": result.embedding.state,
				"tree":      result.tree.state,
			}, calibration)
			if err != nil {
				t.Fatal(err)
			}
			if len(ranked) > 0 && ranked[0].Candidate.ID == test.expected {
				correct++
			}
		}
		t.Logf("alpha=%.2f exact_top1=%d/%d", alpha, correct, len(tests))
		if correct > bestCorrect || (correct == bestCorrect && math.Abs(alpha-0.5) < math.Abs(bestAlpha-0.5)) {
			bestAlpha, bestCorrect = alpha, correct
		}
	}

	t.Logf("best alpha=%.2f exact_top1=%d/%d", bestAlpha, bestCorrect, len(tests))
	calibration := semanticrouting.DefaultCalibration()
	calibration.Alpha = bestAlpha
	for index, test := range tests {
		result := results[index]
		ranked, err := semanticrouting.RankFusion(eligible, result.embedding.evidence, result.tree.evidence, map[string]semanticrouting.ChannelState{
			"embedding": result.embedding.state,
			"tree":      result.tree.state,
		}, calibration)
		if err != nil || len(ranked) == 0 {
			t.Fatalf("rank %q: %v", test.query, err)
		}
		expectedEmbedding := result.embedding.evidence[test.expected].Score
		expectedTree := result.tree.evidence[test.expected].Score
		confusionEmbedding, confusionTree := 0.0, 0.0
		if test.confusion != "" {
			confusionEmbedding = result.embedding.evidence[test.confusion].Score
			confusionTree = result.tree.evidence[test.confusion].Score
		}
		t.Logf("query=%q expected=%s top1=%s embedding=%.4f tree=%.4f confusion=%s confusion_embedding=%.4f confusion_tree=%.4f",
			test.query, test.expected, ranked[0].Candidate.ID, expectedEmbedding, expectedTree,
			test.confusion, confusionEmbedding, confusionTree)
	}
}
