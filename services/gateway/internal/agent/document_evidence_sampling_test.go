package agent

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func sampledChapters(count int) string {
	chapters := make([]string, 0, count)
	for index := 1; index <= count; index++ {
		chapters = append(chapters, fmt.Sprintf("第%d章 开始\n%s\n第%d章 结束", index, strings.Repeat("网络协议分层与端到端可靠传输。", 60), index))
	}
	return strings.Join(chapters, "\n\n")
}

func TestSampledDocumentEvidenceKeepsFittingTextWhole(t *testing.T) {
	text := sampledChapters(2)
	if got := sampledDocumentEvidence(text, len(text)); got != text {
		t.Fatal("text within budget was changed")
	}
}

func TestSampledDocumentEvidenceCoversTheWholeDocument(t *testing.T) {
	text := sampledChapters(40)
	budget := len(text) / 5
	got := sampledDocumentEvidence(text, budget)
	if len(got) > budget || !utf8.ValidString(got) {
		t.Fatalf("sample is %d bytes (budget %d), valid UTF-8=%t", len(got), budget, utf8.ValidString(got))
	}
	seen := map[string]bool{}
	for _, match := range regexp.MustCompile(`第(\d+)章`).FindAllStringSubmatch(got, -1) {
		seen[match[1]] = true
	}
	// A head-only prefix of this budget ends inside chapter 9.
	if !seen["1"] || !seen["40"] || len(seen) < 10 {
		t.Fatalf("sample is not spread across the document: chapters %v", seen)
	}
	if !strings.Contains(got, "bytes omitted") {
		t.Fatal("omitted spans are not marked")
	}
}

func TestSampledDocumentEvidenceFallsBackToPrefixForTinyBudgets(t *testing.T) {
	text := sampledChapters(4)
	if got := sampledDocumentEvidence(text, 1000); got != boundedUTF8Prefix([]byte(text), 1000) {
		t.Fatal("tiny budget did not fall back to a bounded prefix")
	}
}
