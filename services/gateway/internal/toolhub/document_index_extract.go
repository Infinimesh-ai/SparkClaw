package toolhub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
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
	if err := inspectIndexPackage(ctx, path, format); err != nil {
		return docindex.SourceInfo{}, err
	}
	switch format {
	case app.DocumentFormatPDF:
		return extractPDFIndexUnits(ctx, path, emit)
	case app.DocumentFormatDOCX:
		return extractDOCXIndexUnits(ctx, path, emit)
	case app.DocumentFormatText:
		return extractTextIndexUnits(ctx, path, emit)
	case app.DocumentFormatPPTX:
		return extractScriptIndexUnits(ctx, path, emit, func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runPythonAdapter(ctx, pptxIndexAdapterScript, request)
		})
	case app.DocumentFormatXLSX:
		return extractScriptIndexUnits(ctx, path, emit, func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runNodeAdapter(ctx, xlsxIndexAdapterScript, request)
		})
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
				if err := emit(docindex.Unit{Kind: "page", Page: number, Block: fmt.Sprintf("document.page[%d]", number), Availability: document.NodeUnrecognized, Reason: "no_native_text"}); err != nil {
					return docindex.SourceInfo{}, err
				}
				continue
			}
			text := stringArg(page, "text", "")
			if err := emit(docindex.Unit{Kind: "page", Text: text, Page: number, Block: fmt.Sprintf("document.page[%d]", number)}); err != nil {
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
	return extractScriptIndexUnits(ctx, path, emit, func(ctx context.Context, request map[string]any) (map[string]any, error) {
		return runPythonAdapter(ctx, docxIndexAdapterScript, request)
	})
}

// extractScriptIndexUnits runs one whole-document extractor that returns
// {units, pages_total}; slide numbers arrive as unit pages.
func extractScriptIndexUnits(ctx context.Context, path string, emit func(docindex.Unit) error, run adapterReadFunc) (docindex.SourceInfo, error) {
	out, err := run(ctx, map[string]any{"path": path})
	if err != nil {
		return docindex.SourceInfo{}, err
	}
	for _, raw := range documentAnySlice(out["units"]) {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return docindex.SourceInfo{}, err
		}
		var unit docindex.Unit
		if err := json.Unmarshal(encoded, &unit); err != nil {
			return docindex.SourceInfo{}, err
		}
		if err := emit(unit); err != nil {
			return docindex.SourceInfo{}, err
		}
	}
	return docindex.SourceInfo{Pages: intArg(out, "pages_total", 0), Limitations: outputStringArray(out["limitations"])}, nil
}

// extractTextIndexUnits streams plain text as blank-line separated
// paragraphs; Markdown `#` lines become headings.
func extractTextIndexUnits(ctx context.Context, path string, emit func(docindex.Unit) error) (docindex.SourceInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return docindex.SourceInfo{}, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, textIndexMaxLineBytes)
	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return docindex.SourceInfo{}, err
		}
		raw, readErr := reader.ReadSlice('\n')
		if len(raw) > 0 {
			// Buffer boundaries may split UTF-8. Carry a trailing partial rune into
			// the next segment without changing or replacing original bytes.
			data := append([]byte(nil), raw...)
			if readErr == bufio.ErrBufferFull {
				for !utf8.Valid(data) && len(data) < textIndexMaxLineBytes+utf8.UTFMax {
					next, e := reader.ReadByte()
					if e != nil {
						return docindex.SourceInfo{}, e
					}
					data = append(data, next)
				}
			}
			if !utf8.Valid(data) {
				return docindex.SourceInfo{}, fmt.Errorf("text is not valid UTF-8; lossless structural read unavailable")
			}
			text := string(data)
			unit := docindex.Unit{Text: text, Kind: "text", Block: fmt.Sprintf("text.bytes[%d:%d]", offset, offset+len(data)), ByteStart: offset, ByteEnd: offset + len(data)}
			if match := markdownHeading.FindStringSubmatch(strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")); match != nil {
				unit.HeadingLevel = len(match[1])
				unit.Title = match[2]
				unit.Kind = "heading"
			}
			if err := emit(unit); err != nil {
				return docindex.SourceInfo{}, err
			}
			offset += len(data)
		}
		if readErr == io.EOF {
			if offset == 0 {
				if err := emit(docindex.Unit{Kind: "text", Block: "text.bytes[0:0]"}); err != nil {
					return docindex.SourceInfo{}, err
				}
			}
			return docindex.SourceInfo{}, nil
		}
		if readErr != nil && readErr != bufio.ErrBufferFull {
			return docindex.SourceInfo{}, readErr
		}
	}
}
