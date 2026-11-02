package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Route-only acceptance: calls real Fast/embedding models, never Info or business
// tools. Keep repeated failures and held-out paraphrases as assertions, not just
// calibration logs. Every session and file-store record belongs to a temp root.
func TestRealSearchRoutingAcceptance(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_SEARCH_ACCEPTANCE") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_SEARCH_ACCEPTANCE=1 to call configured real models")
	}
	configPath := os.Getenv("SPARKCLAW_INTENT_CALIBRATION_CONFIG")
	if configPath == "" {
		configPath = filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Mock {
		t.Fatal("search acceptance requires real models")
	}
	root := t.TempDir()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = filepath.Join(root, "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	st, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	hub := toolhub.New(cfg, st)
	defer hub.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	runtime, err := NewRuntimeWithContext(ctx, st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("graph=%s catalog=%s capacity_profile=%s mock=false backend=file", runtime.semanticRouter.graph.Revision(), runtime.capabilities.Revision(), cfg.Model.CapacityProfile)
	type scenario struct{ ID, Query, Expected string }
	tests := []scenario{
		{"stable_recursion", "什么是递归？请用一个简单例子解释。", "conversation.answer"},
		{"stable_economics", "解释一下机会成本。", "conversation.answer"},
		{"stable_compare", "比较递归与迭代的优缺点。", "conversation.answer"},
		{"stable_price", "黄金价格为什么会波动？请解释经济学原理。", "conversation.answer"},
		{"create_today", "今天帮我写一首关于春天的小诗。", "conversation.answer"},
		{"translate_news", "把这句话翻译成英文：今天的新闻说黄金价格上涨了。", "conversation.answer"},
		{"provided_data", "仅根据以下数据计算平均价格，不要查询外部资料：周一100元，周二120元，周三110元。", "conversation.answer"},
		{"provided_policy", "仅总结下面这段政策文本，不需要联网：2020年起本机构每周休息两天。", "conversation.answer"},
		{"no_search", "不要联网，请解释什么是机器学习。", "conversation.answer"},
		{"search_word_discussion", "你认为在什么情况下需要使用联网搜索能力？", "conversation.answer"},
		{"algorithm", "请写一个计算斐波那契数列的Python函数，并解释时间复杂度。", "conversation.answer"},
		{"reasoning", "甲比乙高，乙比丙高，三个人里谁最高？请说明理由。", "conversation.answer"},
		{"fresh_price", "现在的金价是多少？", "browser.internet_search"},
		{"fresh_news", "查一下今天的人工智能新闻。", "browser.internet_search"},
		{"fresh_implicit_role", "谁是英国首相？", "browser.internet_search"},
		{"fresh_library", "Go最新稳定版本是多少？", "browser.internet_search"},
		{"purchase", "我有一万元预算，帮我选一台目前值得买的笔记本电脑。", "browser.internet_search"},
		{"official_catalog", "收集苹果官网目前在售Mac的种类和价格。", "browser.internet_search"},
		{"explicit_common", "请联网查一下法国的首都是哪里，并给出可靠来源。", "browser.internet_search"},
		{"explicit_explain", "请联网搜索光合作用的原理，并给出权威出处。", "browser.internet_search"},
		{"explicit_english", "Search the web: what is photosynthesis? Cite authoritative sources.", "browser.internet_search"},
		{"quote_source", "请找到爱因斯坦说过的‘想象力比知识更重要’的原始出处和原文。", "browser.internet_search"},
		{"historical_verify", "帮我核实拿破仑身高只有一米五这个说法是否属实，并给出处。", "browser.internet_search"},
		{"historical_archive", "查找1969年阿波罗11号登月的原始档案，给出来源链接。", "browser.internet_search"},
		{"specific_paper", "帮我查一下2017年Attention Is All You Need论文中训练使用了多少块GPU。", "browser.internet_search"},
		{"medical", "我正在服用华法林，可以同时服用布洛芬吗？", "medicine.drug_info"},
		{"legal", "我在上海，公司没有签劳动合同，我现在可以要求什么赔偿？", "browser.internet_search"},
		{"history_policy", "查一下2015年中国的二孩政策原文，不要查当前政策。", "browser.internet_search"},
		{"history_weather", "查一下1998年长江洪水期间武汉的天气历史记录。", "browser.internet_search"},
		{"history_path", "搜索郑和下西洋的航行路径及史料出处。", "browser.internet_search"},
		{"history_english", "Search for the price of gold in 1980, not current prices.", "browser.internet_search"},
		{"private_order", "查一下我昨天买的手机订单到哪里了。", "not_public_search"},
		{"local_config", "检查当前项目的配置，告诉我搜索功能是否开启。", "not_public_search"},
		{"weather_card", "杭州今天天气怎么样？", "browser.weather"},
		{"weather_compare", "比较杭州和上海今天的天气。", "browser.internet_search"},
		{"repeat_role_1", "谁是英国首相？", "browser.internet_search"},
		{"repeat_role_2", "谁是英国首相？", "browser.internet_search"},
		{"repeat_medical_1", "我正在服用华法林，可以同时服用布洛芬吗？", "medicine.drug_info"},
		{"repeat_medical_2", "我正在服用华法林，可以同时服用布洛芬吗？", "medicine.drug_info"},
		{"repeat_api_1", "Python的requests库支持HTTP/3吗？", "browser.internet_search"},
		{"repeat_legal_1", "我在上海，公司没有签劳动合同，我现在可以要求什么赔偿？", "browser.internet_search"},
		{"held_role_ceo", "OpenAI的首席执行官是谁？", "browser.internet_search"},
		{"held_role_un", "联合国秘书长是谁？", "browser.internet_search"},
		{"held_api_rust", "Rust的tokio支持io_uring吗？", "browser.internet_search"},
		{"held_api_en", "Does Safari support WebGPU?", "browser.internet_search"},
		{"held_med_alcohol", "我在吃二甲双胍，可以喝酒吗？", "medicine.drug_info"},
		{"held_med_pregnancy", "孕期可以吃对乙酰氨基酚吗？", "medicine.drug_info"},
		{"held_tax", "北京个人所得税专项附加扣除有哪些？", "browser.internet_search"},
		{"held_labor", "公司拖欠工资三个月，我该怎么维权？", "browser.internet_search"},
		{"held_neg_tax_concept", "解释一下什么是个人所得税。", "conversation.answer"},
		{"held_neg_http3", "什么是HTTP/3协议？简单介绍一下。", "conversation.answer"},
		{"held_neg_drug_class", "布洛芬属于哪一类药物？", "medicine.drug_info"},
		{"held_neg_poem", "写一首关于首相的打油诗。", "conversation.answer"},
		{"held_neg_code", "帮我把这段代码从Python 2改成Python 3：print 'hi'", "conversation.answer"},
		{"held_neg_nosearch_med", "不要联网，简单说说抗凝药是什么。", "medicine.drug_info"},
		{"repeat_private_order", "查一下我昨天买的手机订单到哪里了。", "not_public_search"},
		{"repeat_drug_class", "布洛芬属于哪一类药物？", "medicine.drug_info"},
		{"held_private_bank", "帮我看看我的银行卡余额还有多少。", "not_public_search"},
		{"held_private_bill", "看一下我上个月的电费账单是多少。", "not_public_search"},
		{"held_neg_legal_term", "什么是劳动仲裁？简单解释一下。", "conversation.answer"},
		{"no_search_api", "不要联网，根据已有知识解释Python requests是否支持HTTP/3，不确定就说明。", "conversation.answer"},
		{"no_search_role", "Do not search the web. Who is the UK prime minister? Say if your knowledge may be outdated.", "conversation.answer"},
		{"no_search_medical", "不要联网，我正在服用华法林，可以同时服用布洛芬吗？不确定请说明。", "medicine.drug_info"},
		{"held_private_coupon", "帮我查查我的淘宝账号里有哪些优惠券。", "not_public_search"},
		{"held_private_parcel", "我昨晚下单的耳机快递送到哪里了？", "not_public_search"},
		{"held_private_card", "帮我看看我这张信用卡还欠多少钱。", "not_public_search"},
	}
	// Explicit expectations: do not derive these with the production freshness parser.
	dated := map[string]bool{"fresh_price": true, "fresh_news": true, "fresh_library": true,
		"purchase": true, "official_catalog": true, "legal": true, "repeat_legal_1": true, "weather_compare": true}
	if selected := strings.TrimSpace(os.Getenv("SPARKCLAW_REAL_SEARCH_CASES")); selected != "" {
		allowed := map[string]bool{}
		for _, id := range strings.Split(selected, ",") {
			allowed[strings.TrimSpace(id)] = true
		}
		filtered := tests[:0]
		for _, test := range tests {
			if allowed[test.ID] {
				filtered = append(filtered, test)
			}
		}
		tests = filtered
		if len(tests) == 0 {
			t.Fatal("search acceptance case filter selected no cases")
		}
	}
	var wg sync.WaitGroup
	concurrency := 3
	if os.Getenv("SPARKCLAW_REAL_SEARCH_CONCURRENCY") == "1" {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	for _, tc := range tests {
		session := storetest.MustCreateSessionWithScope(t, st, "search acceptance "+tc.ID, app.DefaultOwnerID, root, "web", false)
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				t.Errorf("case=%s: %v", tc.ID, ctx.Err())
				return
			}
			defer func() { <-sem }()
			began := time.Now()
			routing, err := runtime.routeIntentWithRequest(ctx, session.ID, "search_"+tc.ID, tc.Query, nil, nil, app.MessageSourceWeb)
			route := routing.Route
			leaf := ""
			if len(route.CapabilityPath) > 0 {
				leaf = string(route.CapabilityPath[len(route.CapabilityPath)-1])
			}
			t.Logf("case=%s expected=%s status=%s actual=%s query=%q elapsed_ms=%d err=%v", tc.ID, tc.Expected, route.Status, leaf, route.Slots.Query, time.Since(began).Milliseconds(), err)
			if err != nil {
				t.Errorf("case=%s: %v", tc.ID, err)
				return
			}
			if routing.Fusion == nil || routing.Fusion.Channels.Embedding.Status != "healthy" || routing.Fusion.Channels.Tree.Status != "healthy" {
				t.Errorf("case=%s: acceptance needs two healthy semantic channels: %+v", tc.ID, routing.Fusion)
			}
			if tc.Expected == "not_public_search" {
				if leaf == string(app.CapabilityBrowserInternetSearch) || leaf == string(app.CapabilityConversationAnswer) {
					t.Errorf("case=%s: private state routed to %s", tc.ID, leaf)
				}
				return
			}
			if route.Status != app.RouteMatched || leaf != tc.Expected {
				t.Errorf("case=%s: expected %s, got %+v fusion=%+v", tc.ID, tc.Expected, route, routing.Fusion)
				return
			}
			if leaf != string(app.CapabilityBrowserInternetSearch) {
				return
			}
			got := strings.TrimSpace(route.Slots.Query)
			if dated[tc.ID] {
				suffix := " " + currentSearchDateForTimezone(began, "")
				if !strings.HasSuffix(got, suffix) {
					// A bounded call may cross the client date boundary.
					suffix = " " + currentSearchDateForTimezone(time.Now(), "")
				}
				if !strings.HasSuffix(got, suffix) {
					t.Errorf("case=%s: missing explicit current-date anchor: %q", tc.ID, got)
					return
				}
				got = strings.TrimSuffix(got, suffix)
			}
			// The request splitter may remove trailing sentence punctuation, but must
			// preserve all requested facts, negation and historical scope verbatim.
			normalize := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), "。.!！?？") }
			if normalize(got) != normalize(tc.Query) {
				t.Errorf("case=%s: query scope changed: got %q want %q", tc.ID, got, tc.Query)
			}
		}()
	}
	wg.Wait()
	fmt.Printf("SEARCH_ACCEPTANCE_CASES=%d\n", len(tests))
}
