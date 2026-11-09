package toolhub

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
)

func TestStructureTreeDOCXNativeLadderAndLegacyExtraction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.docx")
	writeStructureFormatFixture(t, documentPythonBinary(), "-c", `from docx import Document
from docx.enum.style import WD_STYLE_TYPE
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
import sys
doc = Document()
doc.add_paragraph('Document title', 'Title')
doc.add_heading('Parent A', 1)
doc.add_heading('Repeated', 2)
doc.add_paragraph('1.1 Bold body').runs[0].bold = True
doc.add_paragraph('一、Numbered body', 'List Number')
p = doc.add_paragraph('Direct outline')
level = OxmlElement('w:outlineLvl'); level.set(qn('w:val'), '2'); p._p.get_or_add_pPr().append(level)
style = doc.styles.add_style('Custom outline', WD_STYLE_TYPE.PARAGRAPH)
level = OxmlElement('w:outlineLvl'); level.set(qn('w:val'), '1'); style.element.get_or_add_pPr().append(level)
doc.add_paragraph('Inherited outline', style)
p = doc.add_paragraph('Explicit body', style)
level = OxmlElement('w:outlineLvl'); level.set(qn('w:val'), '9'); p._p.get_or_add_pPr().append(level)
doc.add_heading('Parent B', 1)
doc.add_heading('Repeated', 2)
doc.add_heading('', 3)
for name in ('first', 'second'):
    table = doc.add_table(rows=2, cols=2)
    table.cell(0,0).text = name
    table.cell(0,1).text = 'Heading 1 is only cell text'
    table.cell(1,0).merge(table.cell(1,1)).text = '  merged\tvalue  '
doc.save(sys.argv[1])`, path)
	legacy, legacyInfo := extractStructureFormat(t, path, "docx", 0)
	one, oneInfo := extractStructureFormat(t, path, "docx", 1)
	if !reflect.DeepEqual(legacy, one) || !reflect.DeepEqual(legacyInfo, oneInfo) {
		t.Fatal("explicit v1 changed the default DOCX extraction")
	}
	units, info := extractStructureFormat(t, path, "docx", 2)
	if len(units) != 15 || !reflect.DeepEqual(info, legacyInfo) {
		t.Fatalf("unexpected extraction: units=%d info=%+v", len(units), info)
	}
	wantLevels := []int{1, 1, 2, 0, 0, 3, 2, 0, 1, 2, 3}
	for i, want := range wantLevels {
		source := ""
		if want > 0 {
			source = "native_style"
			if i == 5 || i == 6 {
				source = "outline_level"
			}
		}
		if units[i].HeadingLevel != want || units[i].StructureSource != source {
			t.Fatalf("paragraph %d: %+v", i+1, units[i])
		}
	}
	if units[2].Text != units[9].Text || units[2].Block == units[9].Block {
		t.Fatal("duplicate native heading identities were collapsed")
	}
	for i := 11; i < len(units); i++ {
		want := "document.table[1]"
		if i >= 13 {
			want = "document.table[2]"
		}
		if units[i].GroupKey != want {
			t.Fatalf("table lost its atomic grouping: %+v", units[i])
		}
	}
	// Removing the added metadata leaves the old units byte-for-byte equivalent,
	// except for the newly supported inherited native outline property.
	stripped := append([]docindex.Unit(nil), units...)
	for i := range stripped {
		stripped[i].StructureSource, stripped[i].GroupKey = "", ""
	}
	stripped[6].HeadingLevel = 0
	if !reflect.DeepEqual(stripped, legacy) {
		t.Fatal("DOCX structure metadata changed native text, tables or dependencies")
	}
	if !reflect.DeepEqual(legacy[:3], []docindex.Unit{
		{Text: "Document title", HeadingLevel: 1, Block: "document.p[1]"},
		{Text: "Parent A", HeadingLevel: 1, Block: "document.p[2]"},
		{Text: "Repeated", HeadingLevel: 2, Block: "document.p[3]"},
	}) || legacy[6].HeadingLevel != 0 || legacy[12].Values[1]["is_merge_alias"] != true || !reflect.DeepEqual(legacy[12].Dependencies, []string{"document.table[1].row[1]"}) {
		t.Fatalf("legacy DOCX contract changed: %+v", legacy)
	}
}

func TestStructureTreePPTXSectionsUseNativeSlideIDs(t *testing.T) {
	root := t.TempDir()
	path, plain := filepath.Join(root, "sections.pptx"), filepath.Join(root, "plain.pptx")
	writeStructureFormatFixture(t, documentPythonBinary(), "-c", `from pptx import Presentation
from lxml import etree
import sys
p = Presentation()
for title in ('One', 'Two', 'Three', 'Four'):
    slide = p.slides.add_slide(p.slide_layouts[1])
    slide.shapes.title.text = title
    slide.placeholders[1].text = 'Body ' + title
for element, value in zip(p._element.sldIdLst, ('700', '1100', '980', '400')):
    element.set('id', value)
p.save(sys.argv[2])
ns = 'http://schemas.microsoft.com/office/powerpoint/2010/main'
main = 'http://schemas.openxmlformats.org/presentationml/2006/main'
extensions = etree.SubElement(p._element, '{%s}extLst' % main)
ext = etree.SubElement(extensions, '{%s}ext' % main, uri='{521415D9-36F7-43E2-AB2F-B90AF26B5E84}')
sections = etree.SubElement(ext, '{%s}sectionLst' % ns)
for title, ids in [('Repeated', ('400', '980')), ('Opening', ('1100', '700')), ('Missing', ('9999',)), ('Empty', ())]:
    section = etree.SubElement(sections, '{%s}section' % ns, name=title)
    members = etree.SubElement(section, '{%s}sldIdLst' % ns)
    for sid in ids:
        etree.SubElement(members, '{%s}sldId' % ns, id=sid)
p.save(sys.argv[1])`, path, plain)
	legacy, legacyInfo := extractStructureFormat(t, path, "pptx", 0)
	one, oneInfo := extractStructureFormat(t, path, "pptx", 1)
	if !reflect.DeepEqual(legacy, one) || !reflect.DeepEqual(legacyInfo, oneInfo) || len(legacyInfo.Outline) != 0 {
		t.Fatal("legacy PPTX extraction acquired section metadata")
	}
	units, info := extractStructureFormat(t, path, "pptx", 2)
	if !reflect.DeepEqual(info.Outline, []docindex.OutlineEntry{{Title: "Opening", Level: 1, Page: 1}, {Title: "Repeated", Level: 1, Page: 3}}) || info.Pages != 4 {
		t.Fatalf("sections did not follow native slide IDs and source order: %+v", info)
	}
	slides := 0
	for i := range units {
		if units[i].Kind == "slide" {
			slides++
			if units[i].HeadingLevel != 1 || units[i].StructureSource != "slide" {
				t.Fatalf("slide native structure changed: %+v", units[i])
			}
		}
		units[i].StructureSource = ""
	}
	if slides != 4 || len(units) != 8 || !reflect.DeepEqual(units, legacy) {
		t.Fatal("section extraction added synthetic content or changed original slides")
	}
	_, plainInfo := extractStructureFormat(t, plain, "pptx", 2)
	if len(plainInfo.Outline) != 0 {
		t.Fatal("a deck without native sections acquired inferred sections")
	}
}

func TestStructureTreeXLSXRowKeysPreserveNativeCells(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.xlsx")
	writeStructureFormatFixture(t, documentNodeBinary(), "-e", `const ExcelJS = require('exceljs');
(async () => {
 const book = new ExcelJS.Workbook();
 for (const name of ['First', 'Second']) {
  const sheet = book.addWorksheet(name);
  sheet.addRow(['Name', 'Amount']);
  sheet.addRow(['alpha', {formula:'1+2', result:3}]);
  sheet.mergeCells('A5:B5');
  sheet.getCell('A5').value = 'merged';
 }
 await book.xlsx.writeFile(process.argv[1]);
})().catch(error => {console.error(error);process.exit(1)});`, path)
	legacy, legacyInfo := extractStructureFormat(t, path, "xlsx", 0)
	one, oneInfo := extractStructureFormat(t, path, "xlsx", 1)
	if !reflect.DeepEqual(legacy, one) || !reflect.DeepEqual(legacyInfo, oneInfo) {
		t.Fatal("explicit v1 changed the default workbook extraction")
	}
	units, info := extractStructureFormat(t, path, "xlsx", 2)
	sheet := ""
	for i := range units {
		unit := units[i]
		if unit.Kind == "sheet" {
			sheet = unit.Block
			if unit.StructureSource != "sheet" || unit.GroupKey != "" {
				t.Fatalf("bad native sheet metadata: %+v", unit)
			}
		} else {
			address, _ := unit.Values[0]["address"].(string)
			want := sheet + ".row[" + address[1:] + "]"
			if unit.GroupKey != want {
				t.Fatalf("cell lost its native row identity: %+v want=%s", unit, want)
			}
		}
		units[i].StructureSource, units[i].GroupKey = "", ""
	}
	if len(units) != 14 || !reflect.DeepEqual(units, legacy) || !reflect.DeepEqual(info, legacyInfo) {
		t.Fatal("row grouping changed native cell values, merged aliases or dependencies")
	}
	formula := legacy[4]
	if formula.Values[0]["formula"] != "1+2" || formula.Values[0]["cached_result"] != float64(3) || !reflect.DeepEqual(formula.Dependencies, []string{"workbook.sheet[First].cell[B1]"}) {
		t.Fatalf("legacy formula evidence changed: %+v", formula)
	}
}

func TestStructureTreeTextBlocksAreLosslessAndMarkdownSpecific(t *testing.T) {
	cases := []struct {
		name, text string
		blocks     []string
		titles     []string
	}{
		{name: "plain.txt", text: "# is plain\r\n一、native text\r\n1.1 still body\r\n \t\r\n\r\nlast\r\n", blocks: []string{"# is plain\r\n一、native text\r\n1.1 still body\r\n \t\r\n\r\n", "last\r\n"}},
		{name: "leading.txt", text: "\n\nfirst\n\nsecond\n", blocks: []string{"\n\n", "first\n\n", "second\n"}},
		{name: "empty.txt", blocks: []string{""}},
		{name: "unicode.txt", text: strings.Repeat("🙂", 20000) + "\r\n\r\nlast", blocks: []string{strings.Repeat("🙂", 20000) + "\r\n\r\n", "last"}},
		{name: "syntax.MD", text: "intro\r\n\r\n# Main\r\nbody\r\nline\r\n\r\n```md\r\n# hidden\r\n``\r\n## hidden too\r\n```\r\n## Tail ###\r\n~~~\r\n# hidden tilde\r\n~~~\r\n    # indented\r\n# Literal###\r\n#\r\n", titles: []string{"Main", "Tail", "Literal###", ""}},
		{name: "unclosed.markdown", text: "# Heading\n````\n# hidden\n```\n## hidden again\n", titles: []string{"Heading"}},
		{name: "data.json", text: "# literal\n\n## literal again", blocks: []string{"# literal\n\n", "## literal again"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name)
			if err := os.WriteFile(path, []byte(test.text), 0600); err != nil {
				t.Fatal(err)
			}
			units, _ := extractStructureFormat(t, path, "text", 2)
			var text strings.Builder
			var blocks, titles []string
			for _, unit := range units {
				if unit.ByteStart != text.Len() || unit.ByteEnd != text.Len()+len(unit.Text) || test.text[unit.ByteStart:unit.ByteEnd] != unit.Text {
					t.Fatalf("text locator lost source bytes: %+v", unit)
				}
				text.WriteString(unit.Text)
				blocks = append(blocks, unit.Text)
				if unit.HeadingLevel > 0 {
					if unit.StructureSource != "markdown_heading" {
						t.Fatalf("missing Markdown provenance: %+v", unit)
					}
					titles = append(titles, unit.Title)
				}
			}
			if text.String() != test.text || !reflect.DeepEqual(titles, test.titles) || test.blocks != nil && !reflect.DeepEqual(blocks, test.blocks) {
				t.Fatalf("lossless format ladder failed: blocks=%q titles=%q", blocks, titles)
			}
		})
	}
}

func TestStructureTreeLegacyTextBoundariesRemainFrozen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.txt")
	if err := os.WriteFile(path, []byte("# Legacy\r\n\r\nbody\n"), 0600); err != nil {
		t.Fatal(err)
	}
	want := []docindex.Unit{
		{Text: "# Legacy\r\n", Kind: "heading", Title: "Legacy", HeadingLevel: 1, Block: "text.bytes[0:10]", ByteEnd: 10},
		{Text: "\r\n", Kind: "text", Block: "text.bytes[10:12]", ByteStart: 10, ByteEnd: 12},
		{Text: "body\n", Kind: "text", Block: "text.bytes[12:17]", ByteStart: 12, ByteEnd: 17},
	}
	for _, version := range []int{0, 1} {
		units, _ := extractStructureFormat(t, path, "text", version)
		if !reflect.DeepEqual(units, want) {
			t.Fatalf("v%d changed persisted line or heading semantics: %+v", version, units)
		}
	}
}

func TestStructureTreeTextAdmissionAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "limits.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x\n\n", docindex.MaxSourceUnits+1)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := docindex.WithStructureVersion(t.Context(), 2)
	emitted := 0
	_, err := extractTextIndexUnits(ctx, path, func(docindex.Unit) error { emitted++; return nil })
	if err == nil || !strings.Contains(err.Error(), "admission") || emitted != docindex.MaxSourceUnits {
		t.Fatalf("unit admission bypassed: emitted=%d err=%v", emitted, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = extractTextIndexUnits(canceled, path, func(docindex.Unit) error { t.Fatal("emitted after cancellation"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if err := os.WriteFile(path, []byte{0xff, '\n'}, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = extractTextIndexUnits(ctx, path, func(docindex.Unit) error { t.Fatal("invalid UTF-8 emitted"); return nil })
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8 accepted: %v", err)
	}
}

func extractStructureFormat(t *testing.T, path, format string, version int) ([]docindex.Unit, docindex.SourceInfo) {
	t.Helper()
	ctx := t.Context()
	if version != 0 {
		ctx = docindex.WithStructureVersion(ctx, version)
	}
	var units []docindex.Unit
	info, err := (documentIndexExtractor{}).Extract(ctx, path, format, func(unit docindex.Unit) error { units = append(units, unit); return nil })
	if err != nil {
		t.Fatal(err)
	}
	return units, info
}

func writeStructureFormatFixture(t *testing.T, binary, flag, script string, paths ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append([]string{flag, script}, paths...)...)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("format fixture: %v\n%s", err, raw)
	}
}
