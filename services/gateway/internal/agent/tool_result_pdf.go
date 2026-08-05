package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type documentReadCoverage struct {
	Applies            bool
	ReadComplete       bool
	CoverageStatus     string
	TotalPages         int
	MissingPageIndexes []int
	PageStatusCounts   map[string]int
}

func projectPDFReadCoverage(call app.ToolCall, output map[string]any) documentReadCoverage {
	document, _ := anyMap(output["document"])
	format := strings.ToLower(strings.TrimSpace(firstNonEmptyString(document["format"], output["kind"])))
	if call.Tool != "pdf.extract_text" && format != app.DocumentFormatPDF {
		return documentReadCoverage{}
	}
	stats, _ := anyMap(document["stats"])
	readComplete := false
	if value, ok := output["read_complete"]; ok {
		readComplete = boolLikeValue(value)
	} else if value, ok := stats["read_complete"]; ok {
		readComplete = boolLikeValue(value)
	} else {
		readComplete = fileReadComplete(output)
	}
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(output["coverage_status"], stats["coverage_status"])))
	if status == "" {
		status = "partial"
		if readComplete {
			status = "complete"
		} else if strings.TrimSpace(firstNonEmptyString(output["content"])) == "" {
			status = "unavailable"
		}
	}
	missing := pdfIntegerList(firstNonNil(output["missing_page_indexes"], stats["missing_page_indexes"]))
	counts := map[string]int{}
	if values, ok := anyMap(firstNonNil(output["page_status_counts"], stats["page_status_counts"])); ok {
		for key, value := range values {
			counts[key] = intLikeValue(value)
		}
	}
	totalPages := intLikeValue(stats["pages"])
	if totalPages == 0 {
		totalPages = len(anySlice(document["pages"]))
	}
	return documentReadCoverage{
		Applies: true, ReadComplete: readComplete, CoverageStatus: status, TotalPages: totalPages,
		MissingPageIndexes: missing, PageStatusCounts: counts,
	}
}

func (coverage documentReadCoverage) attributes() map[string]string {
	if !coverage.Applies {
		return nil
	}
	attributes := map[string]string{
		"read_complete":   strconv.FormatBool(coverage.ReadComplete),
		"coverage_status": coverage.CoverageStatus,
		"total_pages":     strconv.Itoa(coverage.TotalPages),
	}
	if raw, err := json.Marshal(coverage.MissingPageIndexes); err == nil {
		attributes["missing_page_indexes"] = string(raw)
	}
	if raw, err := json.Marshal(coverage.PageStatusCounts); err == nil {
		attributes["page_status_counts"] = string(raw)
	}
	return attributes
}

func (coverage documentReadCoverage) manifest() string {
	if !coverage.Applies {
		return ""
	}
	parts := []string{
		"read_complete=" + strconv.FormatBool(coverage.ReadComplete),
		"coverage_status=" + coverage.CoverageStatus,
		"total_pages=" + strconv.Itoa(coverage.TotalPages),
		fmt.Sprintf("missing_page_indexes=%v", coverage.MissingPageIndexes),
	}
	keys := make([]string, 0, len(coverage.PageStatusCounts))
	for key := range coverage.PageStatusCounts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	counts := make([]string, 0, len(keys))
	for _, key := range keys {
		counts = append(counts, fmt.Sprintf("%s:%d", key, coverage.PageStatusCounts[key]))
	}
	parts = append(parts, "page_status_counts={"+strings.Join(counts, ",")+"}")
	return strings.Join(parts, " ")
}

func pdfIntegerList(value any) []int {
	values := anySlice(value)
	out := make([]int, 0, len(values))
	for _, item := range values {
		if index := intLikeValue(item); index > 0 {
			out = append(out, index)
		}
	}
	sort.Ints(out)
	return out
}

func documentPageEvidence(document map[string]any) string {
	pages, ok := document["pages"].([]any)
	if !ok || len(pages) == 0 {
		return ""
	}
	lines := []string{}
	for i, item := range pages {
		if i >= 5 {
			break
		}
		page, ok := anyMap(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(stringValue(page["text"]))
		if text == "" {
			continue
		}
		pageNumber := firstNonEmptyString(page["index"], page["page"])
		if pageNumber == "" || pageNumber == "<nil>" {
			pageNumber = fmt.Sprintf("%d", i+1)
		}
		status := firstNonEmptyString(page["text_status"])
		source := firstNonEmptyString(page["text_source"])
		prefix := fmt.Sprintf("page %s", pageNumber)
		if status != "" {
			prefix += " status=" + status
		}
		if source != "" {
			prefix += " source=" + source
		}
		lines = append(lines, prefix+": "+text)
	}
	return strings.Join(lines, "\n")
}
