package toolhub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestPptxSlideToolsWriteNewVersions(t *testing.T) {
	root := t.TempDir()
	writePptxFixture(t, root, "deck.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := []struct {
		tool       string
		args       map[string]any
		wantSlides int
		wantText   string
		wantTitles []string
	}{
		{
			tool: "pptx.add_slide",
			args: map[string]any{
				"path":        "deck.pptx",
				"layout_ref":  "layout:/ppt/slideLayouts/slideLayout2.xml",
				"title":       "Added slide",
				"body":        "Added body",
				"output_path": "outputs/added.pptx",
			},
			wantSlides: 3,
			wantText:   "Added slide",
		},
		{
			tool: "pptx.update_slide",
			args: map[string]any{
				"path":        "deck.pptx",
				"slide_index": 2,
				"updates": []any{map[string]any{
					"shape_index": 2,
					"old_text":    "Second body",
					"text":        "Expanded second body",
				}},
				"output_path": "outputs/updated.pptx",
			},
			wantSlides: 2,
			wantText:   "Expanded second body",
		},
		{
			tool: "pptx.duplicate_slide",
			args: map[string]any{
				"path":        "deck.pptx",
				"slide_index": 1,
				"output_path": "outputs/duplicated.pptx",
			},
			wantSlides: 3,
			wantText:   "First slide",
			wantTitles: []string{"First slide", "First slide", "Second slide"},
		},
		{
			tool: "pptx.delete_slide",
			args: map[string]any{
				"path":        "deck.pptx",
				"slide_index": 2,
				"output_path": "outputs/deleted.pptx",
			},
			wantSlides: 1,
			wantText:   "First slide",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			args := cloneTestMap(tc.args)
			args["source_sha256"] = docxSourceSHA256ForTest(t, root, "deck.pptx")
			result, err := hub.Execute(context.Background(), tc.tool, args, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			outputPath := out["output_path"].(string)
			if outputPath == filepath.Join(root, "deck.pptx") {
				t.Fatalf("tool overwrote input: %#v", out)
			}
			if _, err := os.Stat(outputPath); err != nil {
				t.Fatalf("expected output file: %v", err)
			}
			read, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": outputPath}, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			readOut := read.Output.(map[string]any)
			content := readOut["content"].(string)
			document := readOut["document"].(map[string]any)
			slides := document["slides"].([]any)
			if len(slides) != tc.wantSlides {
				t.Fatalf("expected %d slides, got %#v", tc.wantSlides, document)
			}
			if !strings.Contains(content, tc.wantText) {
				t.Fatalf("edited pptx missing %q: %q", tc.wantText, content)
			}
			if len(tc.wantTitles) > 0 {
				gotTitles := pptxSlideTitles(document)
				if !slicesEqual(gotTitles, tc.wantTitles) {
					t.Fatalf("unexpected slide order, got %#v want %#v", gotTitles, tc.wantTitles)
				}
			}
		})
	}
}

func TestPptxUpdateSlideRejectsStaleShapeEvidence(t *testing.T) {
	root := t.TempDir()
	writePptxFixture(t, root, "deck.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "deck.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "deck.pptx"), "slide_index": 2, "output_path": "outputs/stale.pptx",
		"updates": []any{map[string]any{"shape_index": 2, "old_text": "Invented body", "text": "Updated body"}},
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "old_text does not match slide shape 2") {
		t.Fatalf("expected stale PPTX shape evidence error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "outputs", "stale.pptx")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed PPTX update left an output copy: %v", statErr)
	}
}

func TestPptxUpdateSlideExpandsLongTextWithoutShrinkingFont(t *testing.T) {
	root := t.TempDir()
	writePptxSingleLineFixture(t, root, "single-line.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "single-line.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "single-line.pptx"), "slide_index": 1, "output_path": "outputs/fitted.pptx",
		"updates": []any{map[string]any{
			"shape_index": 1,
			"old_text":    "应用层协议",
			"text":        "HTTP、DNS、SMTP、FTP；处理用户可见的数据格式与交互逻辑。",
		}},
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if intArg(out, "fitted_shapes", -1) != 0 || intArg(out, "layout_adjusted_shapes", 0) != 1 || stringArg(out, "layout_policy", "") != "coordinated" {
		t.Fatalf("long single-line text was not safely expanded: %#v", out)
	}
	outputPath := stringArg(out, "output_path", "")
	pythonScript := `
from pptx import Presentation
prs = Presentation(__import__("sys").argv[1])
shape = prs.slides[0].shapes[0]
tf = shape.text_frame
print(tf.paragraphs[0].runs[0].font.size.pt, tf.word_wrap, shape.width)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, outputPath)
	inspection, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect fitted PPTX: %v\n%s", err, inspection)
	}
	var size float64
	var wordWrap string
	var width int
	if _, err := fmt.Sscan(string(inspection), &size, &wordWrap, &width); err != nil {
		t.Fatalf("parse fitted PPTX inspection %q: %v", inspection, err)
	}
	if size != 18 || wordWrap != "False" || width <= 6*914400 {
		t.Fatalf("unexpected expanded text properties: size=%v word_wrap=%s width=%d", size, wordWrap, width)
	}
}

func TestPptxUpdateSlideCoordinatesPeerBandsAndReportsLayoutChecks(t *testing.T) {
	root := t.TempDir()
	writePptxBandFixture(t, root, "bands.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "bands.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "bands.pptx"), "slide_index": 1, "layout_policy": "coordinated", "output_path": "outputs/bands.pptx",
		"updates": []any{
			map[string]any{"shape_index": 3, "old_text": "读取内容", "text": "完整读取演示文稿内容并保留结构证据"},
			map[string]any{"shape_index": 6, "old_text": "定位内容", "text": "使用稳定的页面与形状索引定位修改目标"},
			map[string]any{"shape_index": 9, "old_text": "修改内容", "text": "生成新版本并校验原始文件保持不变"},
		},
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if intArg(out, "layout_adjusted_shapes", 0) != 9 || intArg(out, "companion_groups_used", 0) != 3 {
		t.Fatalf("peer band layout was not coordinated: %#v", out)
	}
	checks := out["layout_checks"].(map[string]any)
	if checks["updated_text_fits"] != true || checks["canvas_bounds"] != true || checks["companion_non_overlap"] != true || checks["peer_font_uniform"] != true {
		t.Fatalf("layout checks are incomplete: %#v", checks)
	}
	summary := out["change_summary"].(map[string]any)
	if summary["layout_policy"] != "coordinated" || intArg(summary, "layout_adjusted_shapes", 0) != 9 || summary["original_unchanged"] != true {
		t.Fatalf("change summary omitted coordinated layout evidence: %#v", summary)
	}
	if len(documentAnySlice(summary["preservation_warnings"])) == 0 {
		t.Fatalf("page marker warning was not surfaced in change_summary: %#v", summary)
	}
	outputPath := stringArg(out, "output_path", "")
	pythonScript := `
from pptx import Presentation
prs = Presentation(__import__("sys").argv[1])
slide = prs.slides[0]
for background_index, body_index in ((0, 2), (3, 5), (6, 8)):
    background = slide.shapes[background_index]
    body = slide.shapes[body_index]
    size = body.text_frame.paragraphs[0].runs[0].font.size.pt
    print(background.left + background.width, body.left, body.width, size, body.text_frame.word_wrap)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, outputPath)
	inspection, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect coordinated PPTX: %v\n%s", err, inspection)
	}
	lines := strings.Split(strings.TrimSpace(string(inspection)), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected coordinated inspection: %q", inspection)
	}
	width := 0
	for _, line := range lines {
		var backgroundRight, bodyLeft, bodyWidth int
		var size float64
		var wordWrap string
		if _, err := fmt.Sscan(line, &backgroundRight, &bodyLeft, &bodyWidth, &size, &wordWrap); err != nil {
			t.Fatalf("parse coordinated inspection %q: %v", line, err)
		}
		if backgroundRight > bodyLeft || size != 16.5 || wordWrap != "False" || (width != 0 && width != bodyWidth) {
			t.Fatalf("peer band geometry or typography is inconsistent: %q", line)
		}
		width = bodyWidth
	}
}

func TestPptxUpdateSlideCoordinatesFullySelectedMixedFontPeerBands(t *testing.T) {
	root := t.TempDir()
	writePptxBandFixtureWithBodyFonts(t, root, "mixed-font-bands.pptx", []float64{13.5, 16.5, 14.5})
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "mixed-font-bands.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "mixed-font-bands.pptx"), "slide_index": 1, "layout_policy": "coordinated", "output_path": "outputs/mixed-font-bands.pptx",
		"updates": []any{
			map[string]any{"shape_index": 3, "old_text": "读取内容", "text": "读取内容"},
			map[string]any{"shape_index": 6, "old_text": "定位内容", "text": "定位内容"},
			map[string]any{"shape_index": 9, "old_text": "修改内容", "text": "修改内容"},
		},
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	checks := out["layout_checks"].(map[string]any)
	if intArg(out, "companion_groups_used", 0) != 3 || checks["peer_font_uniform"] != false || checks["peer_font_preserved"] != true {
		t.Fatalf("mixed-font peer bands were not coordinated: %#v", out)
	}
	outputPath := stringArg(out, "output_path", "")
	pythonScript := `
from pptx import Presentation
prs = Presentation(__import__("sys").argv[1])
slide = prs.slides[0]
print(*(slide.shapes[index].text_frame.paragraphs[0].runs[0].font.size.pt for index in (2, 5, 8)))
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, outputPath)
	inspection, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect coordinated mixed-font PPTX: %v\n%s", err, inspection)
	}
	if strings.TrimSpace(string(inspection)) != "13.5 16.5 14.5" {
		t.Fatalf("peer body fonts did not retain their original sizes: %q", inspection)
	}
}

func TestPptxUpdateSlideCoordinatesPartiallySelectedMixedFontPeerBands(t *testing.T) {
	root := t.TempDir()
	writePptxBandFixtureWithBodyFonts(t, root, "mixed-font-bands.pptx", []float64{13.5, 16.5, 14.5})
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "mixed-font-bands.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "mixed-font-bands.pptx"), "slide_index": 1, "layout_policy": "coordinated", "output_path": "outputs/mixed-font-bands.pptx",
		"updates": []any{
			map[string]any{"shape_index": 3, "old_text": "读取内容", "text": "更新后的读取内容"},
		},
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	checks := out["layout_checks"].(map[string]any)
	if intArg(out, "companion_groups_used", 0) != 3 || checks["peer_font_uniform"] != false || checks["peer_font_preserved"] != true {
		t.Fatalf("partially selected mixed-font peer bands were not coordinated: %#v", out)
	}
}

func TestPptxUpdateSlideWrapsPeerCardsAndCompanions(t *testing.T) {
	root := t.TempDir()
	writePptxCardFixture(t, root, "cards.pptx")
	original, err := os.ReadFile(filepath.Join(root, "cards.pptx"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	replacements := []string{
		"统一接收请求并校验当前上下文\n保留明确的换行结构",
		"根据页面证据定位需要修改的文本框并保持引用稳定",
		"生成输出副本，同时复核布局边界与关联组件",
	}
	result, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "cards.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "cards.pptx"), "slide_index": 1, "layout_policy": "coordinated", "output_path": "outputs/cards.pptx",
		"updates": []any{
			map[string]any{"shape_index": 4, "old_text": "接收请求", "text": replacements[0]},
			map[string]any{"shape_index": 8, "old_text": "定位目标", "text": replacements[1]},
			map[string]any{"shape_index": 12, "old_text": "生成副本", "text": replacements[2]},
		},
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if intArg(out, "wrapped_shapes", 0) != 3 || intArg(out, "companion_groups_used", 0) != 3 {
		t.Fatalf("card wrapping or companion detection was incomplete: %#v", out)
	}
	wrappedIndexes, ok := out["wrapped_shape_indexes"].([]int)
	if !ok || !slicesEqualInts(wrappedIndexes, []int{4, 8, 12}) {
		t.Fatalf("wrapped shape indexes were not projected as exact integers: %#v", out["wrapped_shape_indexes"])
	}
	adjustedIndexes, ok := out["layout_adjusted_shape_indexes"].([]int)
	if !ok || len(adjustedIndexes) < 9 {
		t.Fatalf("coordinated layout indexes were not projected as exact integers: %#v", out["layout_adjusted_shape_indexes"])
	}
	checks := out["layout_checks"].(map[string]any)
	for _, key := range []string{"updated_text_fits", "wrapped_text_fits", "canvas_bounds", "companion_non_overlap", "peer_font_uniform", "peer_geometry_uniform"} {
		if checks[key] != true {
			t.Fatalf("layout check %q was not satisfied: %#v", key, checks)
		}
	}
	summary := out["change_summary"].(map[string]any)
	if summary["original_unchanged"] != true {
		t.Fatalf("card update did not verify original preservation: %#v", summary)
	}
	unchanged, err := os.ReadFile(filepath.Join(root, "cards.pptx"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, unchanged) {
		t.Fatal("card update modified the original presentation")
	}

	pythonScript := `
import base64
from pptx import Presentation
prs = Presentation(__import__("sys").argv[1])
slide = prs.slides[0]
for background_index, accent_index, body_index in ((0, 1, 3), (4, 5, 7), (8, 9, 11)):
    background = slide.shapes[background_index]
    accent = slide.shapes[accent_index]
    body = slide.shapes[body_index]
    text = base64.b64encode(body.text_frame.text.encode("utf-8")).decode("ascii")
    size = body.text_frame.paragraphs[0].runs[0].font.size.pt
    contained = (
        body.left >= background.left
        and body.top >= background.top
        and body.left + body.width <= background.left + background.width
        and body.top + body.height <= background.top + background.height
    )
    print(body.height, background.height, accent.height, size, body.text_frame.word_wrap, contained, background.top + background.height, text)
print("footer", slide.shapes[12].top, prs.slide_height)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, stringArg(out, "output_path", ""))
	inspection, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect coordinated card PPTX: %v\n%s", err, inspection)
	}
	lines := strings.Split(strings.TrimSpace(string(inspection)), "\n")
	if len(lines) != 4 {
		t.Fatalf("unexpected coordinated card inspection: %q", inspection)
	}
	bodyHeight, backgroundHeight, accentHeight := 0, 0, 0
	backgroundBottom := 0
	for index, line := range lines[:3] {
		var currentBodyHeight, currentBackgroundHeight, currentAccentHeight, currentBottom int
		var size float64
		var wordWrap, contained, encoded string
		if _, err := fmt.Sscan(line, &currentBodyHeight, &currentBackgroundHeight, &currentAccentHeight, &size, &wordWrap, &contained, &currentBottom, &encoded); err != nil {
			t.Fatalf("parse coordinated card inspection %q: %v", line, err)
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode card body text: %v", err)
		}
		if string(decoded) != replacements[index] && !(index == 0 && string(decoded) == strings.ReplaceAll(replacements[index], "\n", "\v")) {
			t.Fatalf("card body replacement did not persist: got %q want %q", decoded, replacements[index])
		}
		if size != 13.5 || wordWrap != "True" || contained != "True" || currentAccentHeight != currentBackgroundHeight {
			t.Fatalf("card geometry or typography is inconsistent: %q", line)
		}
		if bodyHeight != 0 && (currentBodyHeight != bodyHeight || currentBackgroundHeight != backgroundHeight) {
			t.Fatalf("peer card heights differ: %q", inspection)
		}
		bodyHeight, backgroundHeight, accentHeight = currentBodyHeight, currentBackgroundHeight, currentAccentHeight
		backgroundBottom = currentBottom
	}
	var footerLabel string
	var footerTop, slideHeight int
	if _, err := fmt.Sscan(lines[3], &footerLabel, &footerTop, &slideHeight); err != nil {
		t.Fatalf("parse card footer inspection %q: %v", lines[3], err)
	}
	if footerLabel != "footer" || backgroundBottom >= footerTop || backgroundBottom >= slideHeight || accentHeight != backgroundHeight {
		t.Fatalf("card layout crossed the footer or canvas: %q", inspection)
	}
	if !strings.Contains(string(mustDecodeBase64Field(t, lines[0])), "\v") {
		t.Fatalf("explicit newline was not persisted as a PowerPoint soft break: %q", inspection)
	}
}

func TestPptxUpdateSlideRejectsUnreadablyLongText(t *testing.T) {
	root := t.TempDir()
	writePptxSingleLineFixture(t, root, "single-line.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "single-line.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "single-line.pptx"), "slide_index": 1, "output_path": "outputs/unreadable.pptx",
		"updates": []any{map[string]any{
			"shape_index": 1,
			"old_text":    "应用层协议",
			"text":        strings.Repeat("过长内容", 50),
		}},
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "updated text is too long for its slide shape") ||
		app.ToolErrorCodeFrom(err) != app.ToolErrorPPTXLayoutFitConflict {
		t.Fatalf("expected unreadable text rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "outputs", "unreadable.pptx")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unreadable PPTX update left an output copy: %v", statErr)
	}
}

func pptxSlideTitles(document map[string]any) []string {
	slides, _ := document["slides"].([]any)
	out := []string{}
	for _, slideValue := range slides {
		slide, _ := slideValue.(map[string]any)
		items, _ := slide["items"].([]any)
		title := ""
		for _, itemValue := range items {
			item, _ := itemValue.(map[string]any)
			if item["type"] == "text" {
				title = fmt.Sprint(item["text"])
				break
			}
		}
		out = append(out, title)
	}
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func slicesEqualInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func mustDecodeBase64Field(t *testing.T, line string) []byte {
	t.Helper()
	fields := strings.Fields(line)
	if len(fields) == 0 {
		t.Fatalf("missing encoded text in inspection line %q", line)
	}
	decoded, err := base64.StdEncoding.DecodeString(fields[len(fields)-1])
	if err != nil {
		t.Fatalf("decode inspection text: %v", err)
	}
	return decoded
}

func TestPptxDeleteSlideRejectsOnlySlide(t *testing.T) {
	root := t.TempDir()
	writeSingleSlidePptxFixture(t, root, "single.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "pptx.delete_slide", map[string]any{
		"path":          "single.pptx",
		"source_sha256": docxSourceSHA256ForTest(t, root, "single.pptx"),
		"slide_index":   1,
		"output_path":   "outputs/deleted.pptx",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "cannot delete the only slide") {
		t.Fatalf("expected only-slide error, got %v", err)
	}
}

func writePptxFixture(t *testing.T, root, name string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
slide1 = prs.slides.add_slide(prs.slide_layouts[1])
slide1.shapes.title.text = "First slide"
slide1.placeholders[1].text = "First body"
slide2 = prs.slides.add_slide(prs.slide_layouts[1])
slide2.shapes.title.text = "Second slide"
slide2.placeholders[1].text = "Second body"
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create pptx fixture: %v\n%s", err, out)
	}
}

func writePptxSingleLineFixture(t *testing.T, root, name string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
from pptx.util import Inches, Pt
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[6])
shape = slide.shapes.add_textbox(Inches(1), Inches(1), Inches(6), Inches(0.35))
run = shape.text_frame.paragraphs[0].add_run()
run.text = "应用层协议"
run.font.size = Pt(18)
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create single-line pptx fixture: %v\n%s", err, out)
	}
}

func writePptxBandFixture(t *testing.T, root, name string) {
	t.Helper()
	writePptxBandFixtureWithBodyFonts(t, root, name, []float64{16.5, 16.5, 16.5})
}

func writePptxBandFixtureWithBodyFonts(t *testing.T, root, name string, bodyFontSizes []float64) {
	t.Helper()
	if len(bodyFontSizes) != 3 {
		t.Fatalf("expected three band body font sizes, got %#v", bodyFontSizes)
	}
	encodedFontSizes, err := json.Marshal(bodyFontSizes)
	if err != nil {
		t.Fatal(err)
	}
	pythonScript := `
import json
from pathlib import Path
from pptx import Presentation
from pptx.dml.color import RGBColor
from pptx.enum.shapes import MSO_AUTO_SHAPE_TYPE
from pptx.util import Inches, Pt
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
body_font_sizes = json.loads(__import__("sys").argv[3])
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[6])
rows = ((2.0, "读取", "读取内容", (22, 101, 52)), (3.0, "定位", "定位内容", (3, 105, 161)), (4.0, "修改", "修改内容", (180, 83, 9)))
for (top, label_text, body_text, color), body_font_size in zip(rows, body_font_sizes):
    band = slide.shapes.add_shape(MSO_AUTO_SHAPE_TYPE.RECTANGLE, Inches(1.5), Inches(top), Inches(4.5), Inches(.6))
    band.fill.solid()
    band.fill.fore_color.rgb = RGBColor(*color)
    band.line.fill.background()
    label = slide.shapes.add_textbox(Inches(1.7), Inches(top + .08), Inches(1.2), Inches(.35))
    label_run = label.text_frame.paragraphs[0].add_run()
    label_run.text = label_text
    label_run.font.size = Pt(16.5)
    body = slide.shapes.add_textbox(Inches(3.2), Inches(top + .08), Inches(5), Inches(.35))
    body_run = body.text_frame.paragraphs[0].add_run()
    body_run.text = body_text
    body_run.font.size = Pt(body_font_size)
marker = slide.shapes.add_textbox(Inches(8.5), Inches(6.8), Inches(1), Inches(.3))
marker.text_frame.paragraphs[0].add_run().text = "课程 · 2/4"
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name, string(encodedFontSizes))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create band pptx fixture: %v\n%s", err, out)
	}
}

func writePptxCardFixture(t *testing.T, root, name string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
from pptx.dml.color import RGBColor
from pptx.enum.shapes import MSO_AUTO_SHAPE_TYPE
from pptx.util import Inches, Pt
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[6])
cards = (
    (.6, "接收", "接收请求", (25, 89, 140)),
    (3.65, "定位", "定位目标", (38, 116, 77)),
    (6.7, "输出", "生成副本", (181, 91, 16)),
)
for left, title_text, body_text, color in cards:
    background = slide.shapes.add_shape(MSO_AUTO_SHAPE_TYPE.ROUNDED_RECTANGLE, Inches(left), Inches(1.4), Inches(2.7), Inches(2.1))
    background.fill.solid()
    background.fill.fore_color.rgb = RGBColor(245, 247, 249)
    background.line.color.rgb = RGBColor(210, 216, 222)
    accent = slide.shapes.add_shape(MSO_AUTO_SHAPE_TYPE.RECTANGLE, Inches(left), Inches(1.4), Inches(.08), Inches(2.1))
    accent.fill.solid()
    accent.fill.fore_color.rgb = RGBColor(*color)
    accent.line.fill.background()
    title = slide.shapes.add_textbox(Inches(left + .3), Inches(1.65), Inches(2.1), Inches(.35))
    title_run = title.text_frame.paragraphs[0].add_run()
    title_run.text = title_text
    title_run.font.size = Pt(17)
    body = slide.shapes.add_textbox(Inches(left + .3), Inches(2.15), Inches(2.1), Inches(.45))
    body_run = body.text_frame.paragraphs[0].add_run()
    body_run.text = body_text
    body_run.font.size = Pt(13.5)
footer = slide.shapes.add_textbox(Inches(.6), Inches(6.8), Inches(8.8), Inches(.3))
footer.text_frame.paragraphs[0].add_run().text = "Footer"
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create card pptx fixture: %v\n%s", err, out)
	}
}

func writeSingleSlidePptxFixture(t *testing.T, root, name string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[1])
slide.shapes.title.text = "Only slide"
slide.placeholders[1].text = "Only body"
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create single pptx fixture: %v\n%s", err, out)
	}
}
