package agent

import "testing"

func TestCanonicalizeWebSearchQueryKeepsRequestedTimeScope(t *testing.T) {
	const date = "2026-11-02"
	for _, test := range []struct {
		query, want string
	}{
		{"现在的金价是多少？", "现在的金价是多少？ " + date},
		{"查一下今天的人工智能新闻", "查一下今天的人工智能新闻 " + date},
		{"收集苹果官网目前在售Mac的种类和价格", "收集苹果官网目前在售Mac的种类和价格 " + date},
		{"Go最新稳定版本是多少", "Go最新稳定版本是多少 " + date},
		{"What is the current gold price?", "What is the current gold price? " + date},
		{"今天杭州新闻 " + date, "今天杭州新闻 " + date},
		// Topic words alone are not a requested time scope.
		{"谁是英国首相？", "谁是英国首相？"},
		{"搜索郑和下西洋的航行路径及史料出处", "搜索郑和下西洋的航行路径及史料出处"},
		{"苹果官网在售mac的种类和价格", "苹果官网在售mac的种类和价格"},
		// Explicit historical scope wins, and negated present wording is ignored.
		{"查一下2015年中国的二孩政策原文，不要查当前政策", "查一下2015年中国的二孩政策原文，不要查当前政策"},
		{"查一下1998年长江洪水期间武汉的天气历史记录", "查一下1998年长江洪水期间武汉的天气历史记录"},
		{"Search for the price of gold in 1980, not current prices.", "Search for the price of gold in 1980, not current prices."},
		{"现在的金价是历史最高吗", "现在的金价是历史最高吗"},
		{"去年最新的人口数据", "去年最新的人口数据"},
		{"查一下2026年1月最新公布的政策原文", "查一下2026年1月最新公布的政策原文"},
		{"Find the latest policy as of 2026-01-15", "Find the latest policy as of 2026-01-15"},
		{"查一下2027年最新政策的预测", "查一下2027年最新政策的预测"},
		// English matching uses word boundaries; four-digit amounts are not years.
		{"Search for concurrent programming textbooks", "Search for concurrent programming textbooks"},
		{"Is it known now?", "Is it known now? " + date},
		{"现在1999元能买什么手机", "现在1999元能买什么手机 " + date},
		{"这是不是最新版本", "这是不是最新版本 " + date},
		{"2026年最新的个税政策", "2026年最新的个税政策 " + date},
	} {
		if got := canonicalizeWebSearchQuery(test.query, date); got != test.want {
			t.Errorf("canonicalizeWebSearchQuery(%q) = %q, want %q", test.query, got, test.want)
		}
	}
	if got := canonicalizeWebSearchQuery("今天的新闻", ""); got != "今天的新闻" {
		t.Errorf("missing client date changed the query: %q", got)
	}
}
