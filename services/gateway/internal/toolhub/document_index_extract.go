package toolhub

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
)

// textIndexMaxLineBytes splits a line without newlines, such as minified
// data, into pieces so one line never exceeds the scanner buffer.
const textIndexMaxLineBytes = 64 << 10

// pdfIndexBatchPages keeps one extraction subprocess well inside the
// adapter timeout even for dense pages.
const pdfIndexBatchPages = 40

var markdownHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// documentIndexExtractor feeds docindex with text-only units. It never runs
// OCR or image analysis; scanned PDF pages are reported as unindexed.
type documentIndexExtractor struct{}

func (documentIndexExtractor) Extract(ctx context.Context, path, format string, emit func(docindex.Unit) error) (docindex.SourceInfo, error) {
	switch format {
	case app.DocumentFormatPDF:
		return extractPDFIndexUnits(ctx, path, emit)
	case app.DocumentFormatDOCX:
		return extractDOCXIndexUnits(ctx, path, emit)
	case app.DocumentFormatText:
		return extractTextIndexUnits(ctx, path, emit)
	default:
		return docindex.SourceInfo{}, fmt.Errorf("format %q has no document index extractor", format)
	}
}

func extractPDFIndexUnits(ctx context.Context, path string, emit func(docindex.Unit) error) (docindex.SourceInfo, error) {
	info := docindex.SourceInfo{}
	for start := 0; start == 0 || start < info.Pages; start += pdfIndexBatchPages {
		out, err := runPythonAdapter(ctx, pdfIndexAdapterScript, map[string]any{
			"path": path, "start": start, "count": pdfIndexBatchPages, "outline": start == 0,
		})
		if err != nil {
			return docindex.SourceInfo{}, err
		}
		info.Pages = intArg(out, "pages_total", 0)
		if start == 0 {
			for _, raw := range documentAnySlice(out["outline"]) {
				entry, _ := documentAnyMap(raw)
				info.Outline = append(info.Outline, docindex.OutlineEntry{
					Title: stringArg(entry, "title", ""), Level: intArg(entry, "level", 1), Page: intArg(entry, "page", 0),
				})
			}
		}
		for _, raw := range documentAnySlice(out["pages"]) {
			page, _ := documentAnyMap(raw)
			number := intArg(page, "page", 0)
			if !boolArg(page, "indexed", false) {
				info.UnindexedPages = append(info.UnindexedPages, number)
				continue
			}
			text := joinIdeographLineBreaks(stringArg(page, "text", ""))
			if err := emit(docindex.Unit{Text: text, Page: number, Block: fmt.Sprintf("document.page[%d]", number)}); err != nil {
				return docindex.SourceInfo{}, err
			}
		}
		if info.Pages == 0 {
			break
		}
	}
	return info, nil
}

func extractDOCXIndexUnits(ctx context.Context, path string, emit func(docindex.Unit) error) (docindex.SourceInfo, error) {
	out, err := runPythonAdapter(ctx, docxIndexAdapterScript, map[string]any{"path": path})
	if err != nil {
		return docindex.SourceInfo{}, err
	}
	for _, raw := range documentAnySlice(out["units"]) {
		unit, _ := documentAnyMap(raw)
		if err := emit(docindex.Unit{
			Text: stringArg(unit, "text", ""), HeadingLevel: intArg(unit, "heading_level", 0), Block: stringArg(unit, "block", ""),
		}); err != nil {
			return docindex.SourceInfo{}, err
		}
	}
	return docindex.SourceInfo{}, nil
}

// extractTextIndexUnits streams plain text as blank-line separated
// paragraphs; Markdown `#` lines become headings.
func extractTextIndexUnits(ctx context.Context, path string, emit func(docindex.Unit) error) (docindex.SourceInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return docindex.SourceInfo{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2*textIndexMaxLineBytes)
	scanner.Split(boundedLines)
	paragraph := []string{}
	startLine := 0
	flush := func() error {
		if len(paragraph) == 0 {
			return nil
		}
		text := strings.Join(paragraph, "\n")
		paragraph = paragraph[:0]
		return emit(docindex.Unit{Text: text, Block: fmt.Sprintf("text.line[%d]", startLine)})
	}
	for line := 1; scanner.Scan(); line++ {
		if line%4096 == 0 && ctx.Err() != nil {
			return docindex.SourceInfo{}, ctx.Err()
		}
		text := strings.ToValidUTF8(scanner.Text(), "�")
		if match := markdownHeading.FindStringSubmatch(text); match != nil {
			if err := flush(); err != nil {
				return docindex.SourceInfo{}, err
			}
			if err := emit(docindex.Unit{Text: match[2], HeadingLevel: len(match[1]), Block: fmt.Sprintf("text.line[%d]", line)}); err != nil {
				return docindex.SourceInfo{}, err
			}
			continue
		}
		if strings.TrimSpace(text) == "" {
			if err := flush(); err != nil {
				return docindex.SourceInfo{}, err
			}
			continue
		}
		if len(paragraph) == 0 {
			startLine = line
		}
		paragraph = append(paragraph, text)
	}
	if err := scanner.Err(); err != nil {
		return docindex.SourceInfo{}, err
	}
	return docindex.SourceInfo{}, flush()
}

// joinIdeographLineBreaks removes PDF layout line breaks between two
// ideographs, which would otherwise split Chinese words across terms.
func joinIdeographLineBreaks(text string) string {
	runes := []rune(text)
	out := make([]rune, 0, len(runes))
	for index, r := range runes {
		if r == '\n' && index > 0 && index+1 < len(runes) && unicode.Is(unicode.Han, runes[index-1]) && unicode.Is(unicode.Han, runes[index+1]) {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// boundedLines is bufio.ScanLines with lines longer than
// textIndexMaxLineBytes cut at a rune boundary.
func boundedLines(data []byte, atEOF bool) (int, []byte, error) {
	if newline := bytes.IndexByte(data, '\n'); newline >= 0 && newline < textIndexMaxLineBytes {
		return newline + 1, bytes.TrimSuffix(data[:newline], []byte("\r")), nil
	}
	if len(data) >= textIndexMaxLineBytes {
		cut := textIndexMaxLineBytes
		for cut > 0 && cut < len(data) && !utf8.RuneStart(data[cut]) {
			cut--
		}
		if cut == len(data) {
			// No following byte to test: drop a trailing incomplete rune.
			for back := 1; back < utf8.UTFMax && back <= cut; back++ {
				if utf8.RuneStart(data[cut-back]) {
					if !utf8.FullRune(data[cut-back : cut]) {
						cut -= back
					}
					break
				}
			}
		}
		return cut, data[:cut], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), bytes.TrimSuffix(data, []byte("\r")), nil
	}
	return 0, nil, nil
}
