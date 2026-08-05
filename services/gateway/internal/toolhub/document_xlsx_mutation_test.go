package toolhub

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

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
