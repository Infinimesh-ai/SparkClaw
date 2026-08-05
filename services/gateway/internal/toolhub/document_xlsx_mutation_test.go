package toolhub

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestXLSXSixOperationsPreserveRealWorkbookPackageAndOriginal(t *testing.T) {
	tests := []struct {
		operation   string
		tool        string
		wantChanges int
		arguments   map[string]any
		assert      func(*testing.T, map[string]any, map[string]any)
	}{
		{
			operation: "replace_text", tool: "office.replace_text", wantChanges: 1,
			arguments: map[string]any{
				"replacements":          []any{map[string]any{"find": "Pending", "replace": "Approved"}},
				"expected_replacements": 1,
			},
			assert: func(t *testing.T, _, after map[string]any) {
				assertXLSXTestCell(t, after, "Data", 2, "B2", "Approved", "")
			},
		},
		{
			operation: "update_cell", tool: "xlsx.update_cell", wantChanges: 1,
			arguments: map[string]any{"sheet": "Data", "cell": "C2", "value": 43.5},
			assert: func(t *testing.T, before, after map[string]any) {
				beforeCell := xlsxTestCell(t, before, "Data", 2, "C2")
				afterCell := xlsxTestCell(t, after, "Data", 2, "C2")
				if afterCell["raw_value"] != float64(43.5) || afterCell["number_format"] != "0.00" || afterCell["style_hash"] != beforeCell["style_hash"] {
					t.Fatalf("formatted numeric cell was not updated safely: before=%#v after=%#v", beforeCell, afterCell)
				}
			},
		},
		{
			operation: "update_row", tool: "xlsx.update_row", wantChanges: 2,
			arguments: map[string]any{"sheet": "Data", "row": 2, "values": []any{"Alicia", "Approved"}},
			assert: func(t *testing.T, before, after map[string]any) {
				assertXLSXTestCell(t, after, "Data", 2, "A2", "Alicia", "")
				assertXLSXTestCell(t, after, "Data", 2, "B2", "Approved", "")
				beforeTail := xlsxTestCell(t, before, "Data", 2, "C2")
				afterTail := xlsxTestCell(t, after, "Data", 2, "C2")
				if beforeTail["source_hash"] != afterTail["source_hash"] {
					t.Fatalf("update_row changed an omitted trailing cell: before=%#v after=%#v", beforeTail, afterTail)
				}
			},
		},
		{
			operation: "insert_row", tool: "xlsx.insert_row", wantChanges: 1,
			arguments: map[string]any{"sheet": "Data", "row": 3, "position": "before", "values": []any{"Delta", "Pending", 55}},
			assert: func(t *testing.T, _, after map[string]any) {
				assertXLSXTestCell(t, after, "Data", 3, "A3", "Delta", "")
				assertXLSXTestCell(t, after, "Data", 4, "A4", "Bravo", "")
			},
		},
		{
			operation: "append_row", tool: "xlsx.append_row", wantChanges: 1,
			arguments: map[string]any{"sheet": "Data", "values": []any{"Delta", "Pending", 88}},
			assert: func(t *testing.T, _, after map[string]any) {
				assertXLSXTestCell(t, after, "Data", 5, "A5", "Delta", "")
				assertXLSXTestCell(t, after, "Data", 5, "C5", float64(88), "")
			},
		},
		{
			operation: "delete_row", tool: "xlsx.delete_row", wantChanges: 1,
			arguments: map[string]any{"sheet": "Data", "row": 3},
			assert: func(t *testing.T, _, after map[string]any) {
				assertXLSXTestCell(t, after, "Data", 3, "A3", "Charlie", "")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.operation, func(t *testing.T) {
			root := t.TempDir()
			inputRef := "workbook.xlsx"
			outputRef := filepath.Join("outputs", test.operation+".xlsx")
			inputPath := filepath.Join(root, inputRef)
			writeXLSXAllOperationsFixture(t, inputPath)
			original, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			beforePackage, err := document.InspectXLSXPackage(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			referencePart := beforePackage.SheetParts["Reference"]
			if referencePart == "" {
				t.Fatalf("fixture has no Reference worksheet package part: %#v", beforePackage.SheetParts)
			}

			hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
			before := executeDocumentRead(t, hub, inputRef)
			args := cloneTestMap(test.arguments)
			args["path"] = inputRef
			args["output_path"] = outputRef
			if test.operation == "replace_text" {
				documentMap := before["document"].(map[string]any)
				args["source_sha256"] = documentMap["metadata"].(map[string]any)["sha256"]
			} else {
				for key, value := range xlsxBoundTestArgs(t, before, "Data", test.operation, intArg(args, "row", 0), stringArg(args, "cell", "")) {
					args[key] = value
				}
			}

			result, err := hub.Execute(context.Background(), test.tool, args, "session", "run")
			if err != nil {
				t.Fatal(err)
			}
			output := result.Output.(map[string]any)
			summary := output["change_summary"].(map[string]any)
			if intArg(output, "changes", 0) != test.wantChanges || output["package_preservation"] != "verified" ||
				summary["package_preservation"] != "verified" || summary["original_unchanged"] != true {
				t.Fatalf("%s result omitted verified preservation evidence: %#v", test.operation, output)
			}
			for _, feature := range []string{"formulas", "styles", "merged_ranges"} {
				if !testSliceContainsString(summary["package_checked_features"], feature) {
					t.Fatalf("%s did not report checking %s: %#v", test.operation, feature, summary)
				}
			}

			unchanged, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(original) != sha256.Sum256(unchanged) {
				t.Fatalf("%s modified the input workbook", test.operation)
			}
			after := executeDocumentRead(t, hub, outputRef)
			test.assert(t, before, after)
			assertXLSXTestCell(t, after, "Reference", 2, "B2", float64(4), "2*2")
			assertXLSXTestCell(t, after, "Reference", 4, "A4", "Merged reference", "")

			afterPackage, err := document.InspectXLSXPackage(filepath.Join(root, outputRef))
			if err != nil {
				t.Fatal(err)
			}
			if beforePackage.Parts[referencePart].SHA256 != afterPackage.Parts[referencePart].SHA256 {
				t.Fatalf("%s changed the unrelated Reference worksheet package part", test.operation)
			}
		})
	}
}

func TestXLSXUpdateRowChangesOnlySuppliedPrefix(t *testing.T) {
	root := t.TempDir()
	writeTypedXLSXFixture(t, root, "book.xlsx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	before := executeDocumentRead(t, hub, "book.xlsx")
	args := xlsxBoundTestArgs(t, before, "Data", "update_row", 2, "")
	args["path"] = "book.xlsx"
	args["output_path"] = "outputs/updated.xlsx"
	args["values"] = []any{"Beta", 50}

	result, err := hub.Execute(context.Background(), "xlsx.update_row", args, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	changedCells := testAnySlice(output["changed_cells"])
	if intArg(output, "changes", 0) != 2 || len(changedCells) != 2 ||
		changedCells[0].(map[string]any)["address"] != "A2" || changedCells[1].(map[string]any)["address"] != "B2" {
		t.Fatalf("update_row did not report exact cell deltas: %#v", output)
	}
	changeSummary := output["change_summary"].(map[string]any)
	if output["package_preservation"] != "verified" || changeSummary["package_preservation"] != "verified" ||
		len(testAnySlice(changeSummary["package_checked_features"])) == 0 || len(testAnySlice(changeSummary["target_deltas"])) != 2 {
		t.Fatalf("verified XLSX package evidence is missing from the change result: %#v", output)
	}

	after := executeDocumentRead(t, hub, "outputs/updated.xlsx")
	beforeCells := xlsxTestRowCells(t, before, "Data", 2)
	afterCells := xlsxTestRowCells(t, after, "Data", 2)
	if afterCells[0]["raw_value"] != "Beta" || afterCells[1]["raw_value"] != float64(50) {
		t.Fatalf("updated prefix is incorrect: %#v", afterCells)
	}
	for index := 2; index < len(beforeCells); index++ {
		if beforeCells[index]["source_hash"] != afterCells[index]["source_hash"] {
			t.Fatalf("trailing cell %s changed: before=%#v after=%#v", beforeCells[index]["address"], beforeCells[index], afterCells[index])
		}
	}
	if afterCells[2]["formula"] != "B2*2" || afterCells[3]["display_text"] != "Keep me" || afterCells[4]["raw_value"] != "Docs" {
		t.Fatalf("formula/comment/hyperlink tail evidence was not retained: %#v", afterCells)
	}
}

func TestXLSXEditRejectsStaleSourceAndTargetHashesBeforeWriting(t *testing.T) {
	root := t.TempDir()
	writeTypedXLSXFixture(t, root, "book.xlsx")
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	read := executeDocumentRead(t, hub, "book.xlsx")

	for _, test := range []struct {
		name  string
		field string
	}{
		{name: "source", field: "source_sha256"},
		{name: "row", field: "source_row_hash"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := xlsxBoundTestArgs(t, read, "Data", "update_row", 2, "")
			args["path"] = "book.xlsx"
			args["output_path"] = "outputs/stale-" + test.name + ".xlsx"
			args["values"] = []any{"Blocked"}
			args[test.field] = "sha256:stale"
			_, err := hub.Execute(context.Background(), "xlsx.update_row", args, "session", "run")
			if !document.IsErrorCode(err, document.CodeResourceInvalid) {
				t.Fatalf("stale %s hash was not rejected: %v", test.field, err)
			}
			if _, statErr := os.Stat(filepath.Join(root, args["output_path"].(string))); !os.IsNotExist(statErr) {
				t.Fatalf("stale evidence left an output: %v", statErr)
			}
		})
	}
}

func xlsxBoundTestArgs(t *testing.T, read map[string]any, sheetName, operation string, row int, cell string) map[string]any {
	t.Helper()
	documentMap := read["document"].(map[string]any)
	metadata := documentMap["metadata"].(map[string]any)
	args := map[string]any{"source_sha256": metadata["sha256"]}
	for _, rawSheet := range testAnySlice(documentMap["sheets"]) {
		sheet := rawSheet.(map[string]any)
		if sheet["name"] != sheetName {
			continue
		}
		args["sheet"] = sheet["name"]
		if operation == "append_row" {
			args["source_sheet_hash"] = sheet["source_hash"]
			return args
		}
		for _, rawRow := range testAnySlice(sheet["rows"]) {
			rowMap := rawRow.(map[string]any)
			if operation == "update_cell" {
				for _, rawCell := range testAnySlice(rowMap["cells"]) {
					cellMap := rawCell.(map[string]any)
					if cellMap["address"] == cell {
						args["cell"] = cellMap["address"]
						args["source_cell_hash"] = cellMap["source_hash"]
						return args
					}
				}
				continue
			}
			if intArg(rowMap, "index", 0) != row {
				continue
			}
			args["row"] = rowMap["index"]
			args["source_row_hash"] = rowMap["source_hash"]
			return args
		}
	}
	t.Fatalf("missing XLSX test evidence: sheet=%s operation=%s row=%d cell=%s", sheetName, operation, row, cell)
	return nil
}

func xlsxTestRowCells(t *testing.T, read map[string]any, sheetName string, row int) []map[string]any {
	t.Helper()
	documentMap := read["document"].(map[string]any)
	for _, rawSheet := range testAnySlice(documentMap["sheets"]) {
		sheet := rawSheet.(map[string]any)
		if sheet["name"] != sheetName {
			continue
		}
		for _, rawRow := range testAnySlice(sheet["rows"]) {
			rowMap := rawRow.(map[string]any)
			if intArg(rowMap, "index", 0) != row {
				continue
			}
			cells := []map[string]any{}
			for _, rawCell := range testAnySlice(rowMap["cells"]) {
				cells = append(cells, rawCell.(map[string]any))
			}
			return cells
		}
	}
	t.Fatalf("missing XLSX row %s!%d", sheetName, row)
	return nil
}

func xlsxTestCell(t *testing.T, read map[string]any, sheetName string, row int, address string) map[string]any {
	t.Helper()
	for _, cell := range xlsxTestRowCells(t, read, sheetName, row) {
		if cell["address"] == address {
			return cell
		}
	}
	t.Fatalf("missing XLSX cell %s!%s", sheetName, address)
	return nil
}

func assertXLSXTestCell(t *testing.T, read map[string]any, sheetName string, row int, address string, rawValue any, formula string) {
	t.Helper()
	cell := xlsxTestCell(t, read, sheetName, row, address)
	if cell["raw_value"] != rawValue || stringArg(cell, "formula", "") != formula {
		t.Fatalf("unexpected XLSX cell %s!%s: %#v", sheetName, address, cell)
	}
}

func testSliceContainsString(value any, want string) bool {
	for _, item := range testAnySlice(value) {
		if item == want {
			return true
		}
	}
	return false
}

func writeXLSXAllOperationsFixture(t *testing.T, path string) {
	t.Helper()
	script := `
const ExcelJS = require("exceljs");
(async () => {
  const workbook = new ExcelJS.Workbook();
  const reference = workbook.addWorksheet("Reference");
  reference.addRow(["Label", "Value"]);
  reference.addRow(["Constant formula", {formula: "2*2", result: 4}]);
  reference.mergeCells("A4:B4");
  reference.getCell("A4").value = "Merged reference";

  const data = workbook.addWorksheet("Data");
  data.addRows([
    ["Name", "Status", "Amount"],
    ["Alpha", "Pending", 42],
    ["Bravo", "Review", 60],
    ["Charlie", "Approved", 75]
  ]);
  data.getCell("C2").numFmt = "0.00";
  data.getCell("C2").font = {bold: true, color: {argb: "FF1F4E78"}};
  const outputPath = process.argv[1];
  await workbook.xlsx.writeFile(outputPath);
  const stable = new ExcelJS.Workbook();
  await stable.xlsx.readFile(outputPath);
  await stable.xlsx.writeFile(outputPath);
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});`
	cmd := exec.Command(documentNodeBinary(), "-e", script, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create six-operation XLSX fixture: %v\n%s", err, output)
	}
}
