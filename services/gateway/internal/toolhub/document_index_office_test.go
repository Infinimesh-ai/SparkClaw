package toolhub

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
)

func TestPPTXIndexUnitsUseOneHeadingPerSlide(t *testing.T) {
	root := t.TempDir()
	script := `
import sys
from pptx import Presentation
from pptx.util import Inches
deck = Presentation()
for number, (title, body) in enumerate([("项目背景", "市场规模持续扩大"), ("技术方案", "采用分层检索架构"), ("里程碑", "第三季度完成验收")], start=1):
    slide = deck.slides.add_slide(deck.slide_layouts[1])
    slide.shapes.title.text = title
    slide.placeholders[1].text = body
    slide.notes_slide.notes_text_frame.text = "讲者备注 %d" % number
deck.save(sys.argv[1])
`
	path := filepath.Join(root, "deck.pptx")
	if out, err := exec.Command(documentPythonBinary(), "-c", script, path).CombinedOutput(); err != nil {
		t.Fatalf("create pptx: %v\n%s", err, out)
	}
	units := []docindex.Unit{}
	info, err := documentIndexExtractor{}.Extract(context.Background(), path, "pptx", func(unit docindex.Unit) error {
		units = append(units, unit)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	headings := []string{}
	for _, unit := range units {
		if unit.HeadingLevel == 1 {
			headings = append(headings, unit.Text)
		}
	}
	if info.Pages != 3 || strings.Join(headings, "|") != "项目背景|技术方案|里程碑" {
		t.Fatalf("slide headings = %v, pages = %d", headings, info.Pages)
	}
	joined := ""
	for _, unit := range units {
		joined += unit.Text + "\n"
	}
	if !strings.Contains(joined, "采用分层检索架构") || !strings.Contains(joined, "讲者备注 3") || units[len(units)-1].Page != 3 {
		t.Fatalf("slide body or notes were not extracted: %s", joined)
	}
}

func TestLargeWorkbookIsIndexedByRowsWithHeaderNames(t *testing.T) {
	root := t.TempDir()
	script := `
const ExcelJS = require("exceljs");
(async () => {
  const workbook = new ExcelJS.Workbook();
  for (const name of ["库存", "订单"]) {
    const sheet = workbook.addWorksheet(name);
    sheet.addRow(["产品编号", "产品名称", "仓库", "数量"]);
    for (let row = 1; row <= 3000; row++) {
      sheet.addRow(["SKU-" + String(100000 + row), name + "测试商品第" + row + "号，用于验证大型表格的检索能力", "华东一号仓", row % 97]);
    }
  }
  await workbook.xlsx.writeFile(process.argv[1]);
})().catch(error => { console.error(error); process.exit(1); });
`
	path := filepath.Join(root, "inventory.xlsx")
	command := exec.Command(documentNodeBinary(), "-e", script, path)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create xlsx: %v\n%s", err, out)
	}
	hub := newRetrievalTestHub(t, root)
	read, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "inventory.xlsx"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	pointer := read.Output.(map[string]any)
	if pointer["retrieval_required"] != true || !strings.Contains(pointer["content"].(string), "订单") {
		t.Fatalf("a large workbook did not return its sheet outline: %.200v", pointer["content"])
	}
	search, err := hub.Execute(context.Background(), "document.search", map[string]any{"path": "inventory.xlsx", "keywords": []any{"SKU-102345"}}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	hits := search.Output.(map[string]any)["hits"].([]any)
	if len(hits) == 0 || !strings.Contains(hits[0].(map[string]any)["snippet"].(string), "SKU-102345") {
		t.Fatalf("row search did not find the product by its header-named cell: %#v", hits)
	}
}
