package toolhub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	request := map[string]any{"path": path}
	if version := docindex.StructureVersionFromContext(ctx); version >= 2 {
		request["structure_version"] = version
	}
	out, err := run(ctx, request)
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
	info := docindex.SourceInfo{Pages: intArg(out, "pages_total", 0), Limitations: outputStringArray(out["limitations"])}
	if docindex.StructureVersionFromContext(ctx) >= 2 {
		for _, raw := range documentAnySlice(out["outline"]) {
			entry, _ := documentAnyMap(raw)
			info.Outline = append(info.Outline, docindex.OutlineEntry{
				Title: stringArg(entry, "title", ""), Level: intArg(entry, "level", 1), Page: intArg(entry, "page", 0),
			})
		}
	}
	return info, nil
}

// The legacy extractor retains its exact line boundaries and heading behavior
// for persisted workflows. Only v2 uses blank-line blocks and Markdown syntax.
func extractTextIndexUnits(ctx context.Context, path string, emit func(docindex.Unit) error) (docindex.SourceInfo, error) {
	if docindex.StructureVersionFromContext(ctx) >= 2 {
		return extractTextBlockIndexUnits(ctx, path, emit)
	}
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

// textIndexBlockWriter retains source byte spans while separating native
// Markdown headings from the blank-line blocks used by every text format.
type textIndexBlockWriter struct {
	emit                      func(docindex.Unit) error
	block                     strings.Builder
	offset, blockStart, count int
	markdown, endsBlank       bool
	fence                     byte
	fenceLength               int
}

func (writer *textIndexBlockWriter) write(text string, start, level int, title string) error {
	writer.count++
	if writer.count > docindex.MaxSourceUnits {
		return fmt.Errorf("text exceeds structure extraction admission")
	}
	unit := docindex.Unit{Text: text, Kind: "text", Block: fmt.Sprintf("text.bytes[%d:%d]", start, start+len(text)), ByteStart: start, ByteEnd: start + len(text)}
	if level > 0 {
		unit.Kind, unit.HeadingLevel, unit.Title, unit.StructureSource = "heading", level, title, "markdown_heading"
	}
	return writer.emit(unit)
}

func (writer *textIndexBlockWriter) flush() error {
	if writer.block.Len() == 0 {
		return nil
	}
	err := writer.write(writer.block.String(), writer.blockStart, 0, "")
	writer.block.Reset()
	return err
}

func (writer *textIndexBlockWriter) consume(text string) error {
	start := writer.offset
	writer.offset += len(text)
	content := strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	level, title := 0, ""
	if writer.markdown {
		marker, length, rest := markdownIndexFence(content)
		if writer.fence != 0 {
			if marker == writer.fence && length >= writer.fenceLength && strings.TrimSpace(rest) == "" {
				writer.fence, writer.fenceLength = 0, 0
			}
		} else if marker != 0 && (marker != '`' || !strings.Contains(rest, "`")) {
			writer.fence, writer.fenceLength = marker, length
		} else {
			level, title = markdownIndexHeading(content)
		}
	}
	blank := strings.TrimSpace(content) == ""
	if level > 0 || writer.endsBlank && !blank {
		if err := writer.flush(); err != nil {
			return err
		}
	}
	if level > 0 {
		writer.endsBlank = false
		return writer.write(text, start, level, title)
	}
	if writer.block.Len() == 0 {
		writer.blockStart = start
	}
	writer.block.WriteString(text)
	writer.endsBlank = blank
	return nil
}

func extractTextBlockIndexUnits(ctx context.Context, path string, emit func(docindex.Unit) error) (docindex.SourceInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return docindex.SourceInfo{}, err
	}
	defer file.Close()
	writer := textIndexBlockWriter{emit: emit, markdown: isMarkdownIndexPath(path)}
	reader := bufio.NewReaderSize(file, textIndexMaxLineBytes)
	var line []byte
	bytes := 0
	for {
		if err := ctx.Err(); err != nil {
			return docindex.SourceInfo{}, err
		}
		raw, readErr := reader.ReadSlice('\n')
		bytes += len(raw)
		if bytes > docindex.MaxExtractedBytes {
			return docindex.SourceInfo{}, fmt.Errorf("text exceeds structure extraction admission")
		}
		line = append(line, raw...)
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if len(line) > 0 {
			if !utf8.Valid(line) {
				return docindex.SourceInfo{}, fmt.Errorf("text is not valid UTF-8; lossless structural read unavailable")
			}
			if err := writer.consume(string(line)); err != nil {
				return docindex.SourceInfo{}, err
			}
			line = line[:0]
		}
		if readErr == io.EOF {
			if err := writer.flush(); err != nil {
				return docindex.SourceInfo{}, err
			}
			if writer.count == 0 {
				return docindex.SourceInfo{}, writer.write("", 0, 0, "")
			}
			return docindex.SourceInfo{}, nil
		}
		if readErr != nil {
			return docindex.SourceInfo{}, readErr
		}
	}
}

func isMarkdownIndexPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdown", ".mkd", ".mkdn":
		return true
	default:
		return false
	}
}

func markdownIndexFence(line string) (byte, int, string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 || trimmed[0] != '`' && trimmed[0] != '~' {
		return 0, 0, ""
	}
	length := 1
	for length < len(trimmed) && trimmed[length] == trimmed[0] {
		length++
	}
	if length < 3 {
		return 0, 0, ""
	}
	return trimmed[0], length, trimmed[length:]
}

func markdownIndexHeading(line string) (int, string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0, ""
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level < len(trimmed) && trimmed[level] != ' ' && trimmed[level] != '\t' {
		return 0, ""
	}
	title := strings.TrimSpace(trimmed[level:])
	withoutClosing := strings.TrimRight(title, "#")
	if withoutClosing == "" || len(withoutClosing) < len(title) && (strings.HasSuffix(withoutClosing, " ") || strings.HasSuffix(withoutClosing, "\t")) {
		title = strings.TrimSpace(withoutClosing)
	}
	return level, title
}
