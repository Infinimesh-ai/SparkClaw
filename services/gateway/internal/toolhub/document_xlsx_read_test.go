package toolhub

import (
	"os/exec"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestXLSXReadPreservesTypedFormulaFormatAndMergeEvidence(t *testing.T) {
	root := t.TempDir()
	writeTypedXLSXFixture(t, root, "typed.xlsx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	read := executeDocumentRead(t, hub, "typed.xlsx")
	document := read["document"].(map[string]any)
	sheets := testAnySlice(document["sheets"])
	data := sheets[0].(map[string]any)
	rows := testAnySlice(data["rows"])
	cells := testAnySlice(rows[1].(map[string]any)["cells"])
	number := cells[1].(map[string]any)
	formula := cells[2].(map[string]any)
	mergedRows := testAnySlice(data["rows"])
	mergedCell := testAnySlice(mergedRows[2].(map[string]any)["cells"])[0].(map[string]any)
	if number["value_kind"] != "number" || number["raw_value"] != float64(42) || number["display_text"] != "42" || number["number_format"] != "0.00" {
		t.Fatalf("typed formatted number is missing: %#v", number)
	}
	if formula["value_kind"] != "formula" || formula["formula"] != "B2*2" || formula["raw_value"] != float64(84) {
		t.Fatalf("formula identity/result is missing: %#v", formula)
	}
	if mergedCell["merge_anchor"] != "A3" {
		t.Fatalf("merged-cell anchor is missing: %#v", mergedCell)
	}
	if rows[1].(map[string]any)["source_hash"] == "" || data["source_hash"] == "" {
		t.Fatalf("row/sheet evidence hashes are missing: row=%#v sheet=%#v", rows[1], data)
	}
}

func writeTypedXLSXFixture(t *testing.T, root, name string) {
	t.Helper()
	nodeScript := `
const ExcelJS = require("exceljs");
(async () => {
  const root = process.argv[1];
  const name = process.argv[2];
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet("Data");
  sheet.addRow(["Name", "Amount", "Double"]);
  sheet.addRow(["Alpha", 42, { formula: "B2*2", result: 84 }]);
  sheet.getCell("B2").numFmt = "0.00";
  sheet.getCell("B2").font = { bold: true };
  sheet.mergeCells("A3:B3");
  sheet.getCell("A3").value = "Merged";
  sheet.getRow(4).hidden = true;
  sheet.getCell("A4").value = "Hidden";
  await workbook.xlsx.writeFile(root + "/" + name);
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});
`
	cmd := exec.Command(documentNodeBinary(), "-e", nodeScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create typed xlsx fixture: %v\n%s", err, out)
	}
}
