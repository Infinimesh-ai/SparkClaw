package document

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestInspectXLSXPackageReportsVerifiedFeatureCoverage(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "book.xlsx")
	writeDocumentXLSXPackageFixture(t, packagePath, xlsxPackageFixtureOptions{})

	manifest, err := ValidateXLSXPackageForOperation(packagePath, "update_cell")
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{"formulas", "merged_ranges", "styles"} {
		if !slices.Contains(manifest.FeatureClasses, class) {
			t.Fatalf("XLSX package manifest omitted %s: %#v", class, manifest.FeatureClasses)
		}
	}
	if manifest.ContentTypesHash == "" || manifest.RelationshipGraphHash == "" || manifest.StylesSemanticHash == "" || manifest.SheetParts["Data"] != "xl/worksheets/sheet1.xml" {
		t.Fatalf("XLSX package manifest is incomplete: %#v", manifest)
	}
	coverage := XLSXPackageReadCoverage(manifest)
	if coverage["status"] != "complete" || coverage["mutation_supported"] != true {
		t.Fatalf("supported XLSX package coverage is incorrect: %#v", coverage)
	}
	if _, err := ValidateXLSXPackageForOperation(packagePath, "insert_row", map[string]any{"sheet": "Data", "row": 1}); !IsErrorCode(err, CodeMutationUnsupported) {
		t.Fatalf("formula and merge anchors on the target sheet passed structural mutation preflight: %v", err)
	}
}

func TestXLSXPackageGateRejectsUnsupportedAndMalformedFeatures(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		packagePath := filepath.Join(t.TempDir(), "table.xlsx")
		writeDocumentXLSXPackageFixture(t, packagePath, xlsxPackageFixtureOptions{table: true})
		manifest, inspectErr := InspectXLSXPackage(packagePath)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		coverage := XLSXPackageReadCoverage(manifest)
		if coverage["status"] != "partial" || coverage["mutation_supported"] != false {
			t.Fatalf("read-only table coverage did not remain available and partial: %#v", coverage)
		}
		if _, err := ValidateXLSXPackageForOperation(packagePath, "update_cell"); !IsErrorCode(err, CodeMutationUnsupported) {
			t.Fatalf("unverified table package passed the mutation gate: %v", err)
		}
	})

	t.Run("broken relationship", func(t *testing.T) {
		packagePath := filepath.Join(t.TempDir(), "broken.xlsx")
		writeDocumentXLSXPackageFixture(t, packagePath, xlsxPackageFixtureOptions{brokenWorksheetRelationship: true})
		if _, err := InspectXLSXPackage(packagePath); !IsErrorCode(err, CodeParseFailed) {
			t.Fatalf("broken relationship graph did not fail package inspection: %v", err)
		}
	})
}

func TestVerifyXLSXPackagePreservationAllowsOnlyTargetSheetAndSharedStrings(t *testing.T) {
	root := t.TempDir()
	beforePath := filepath.Join(root, "before.xlsx")
	afterPath := filepath.Join(root, "after.xlsx")
	writeDocumentXLSXPackageFixture(t, beforePath, xlsxPackageFixtureOptions{})
	writeDocumentXLSXPackageFixture(t, afterPath, xlsxPackageFixtureOptions{updatedCell: true})
	before, err := InspectXLSXPackage(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	after, err := InspectXLSXPackage(afterPath)
	if err != nil {
		t.Fatal(err)
	}
	edit := EditRequest{Operation: "update_cell", Arguments: map[string]any{"sheet": "Data", "cell": "A1", "value": "Beta"}}
	report, err := VerifyXLSXPackagePreservation(before, after, edit, nil)
	if err != nil || !slices.Contains(report.CheckedFeatureClasses, "relationships") {
		t.Fatalf("evidence-bound worksheet delta was rejected: report=%#v err=%v", report, err)
	}

	tamperedPath := filepath.Join(root, "tampered.xlsx")
	writeDocumentXLSXPackageFixture(t, tamperedPath, xlsxPackageFixtureOptions{updatedCell: true, tamperedStyles: true})
	tampered, err := InspectXLSXPackage(tamperedPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyXLSXPackagePreservation(before, tampered, edit, nil); !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("unreported styles delta did not fail preservation: %v", err)
	}
}

func TestPipelineRemovesXLSXOutputWithUnreportedPackageDelta(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "book.xlsx")
	outputPath := filepath.Join(root, "book-edited.xlsx")
	writeDocumentXLSXPackageFixture(t, inputPath, xlsxPackageFixtureOptions{})
	parser := ParserFunc(func(_ context.Context, metadata Metadata, _ int) (AdapterReadResult, error) {
		value := "Alpha"
		if metadata.Path == outputPath {
			value = "Beta"
		}
		return AdapterReadResult{Content: value, Document: map[string]any{
			"sheets": []any{map[string]any{
				"name": "Data", "index": 1,
				"rows": []any{map[string]any{
					"index": 1,
					"cells": []any{
						map[string]any{"address": "A1", "column": 1, "value_kind": "string", "raw_value": value, "display_text": value},
						map[string]any{"address": "B1", "column": 2, "value_kind": "formula", "raw_value": float64(1), "display_text": "1", "formula": "A1"},
					},
				}},
			}},
		}}, nil
	})
	strategy := NewSmallFileStrategy(map[string]Parser{app.DocumentFormatXLSX: parser}, map[string]Editor{
		EditorKey(app.DocumentFormatXLSX, "update_cell"): EditorFunc(func(_ context.Context, request ApplyRequest) (ApplyResult, error) {
			writeDocumentXLSXPackageFixture(t, request.Edit.OutputPath, xlsxPackageFixtureOptions{updatedCell: true, tamperedStyles: true})
			changedCells := []any{map[string]any{
				"address": "A1", "before": map[string]any{"raw_value": "Alpha"}, "after": map[string]any{"raw_value": "Beta"},
			}}
			return ApplyResult{
				OutputPath: request.Edit.OutputPath, Changed: 1,
				Details: map[string]any{"changed_cells": changedCells},
			}, nil
		}),
	})
	pipeline := NewPipeline(InspectorFunc(InspectFile), strategy)
	metadata, err := InspectFile(context.Background(), root, inputPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pipeline.Edit(context.Background(), EditRequest{
		Root: root, Path: inputPath, OutputPath: outputPath, Operation: app.DocumentOperationUpdateCell, SourceSHA256: metadata.SHA256,
		Target:    LocatorRequest{Kind: LocatorCell, Sheet: "Data", Cell: "A1"},
		Arguments: map[string]any{"sheet": "Data", "cell": "A1", "value": "Beta"},
	})
	if !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("unreported XLSX package delta did not fail the Pipeline: %v", err)
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid XLSX output was not removed: %v", statErr)
	}
	if _, statErr := os.Stat(inputPath); statErr != nil {
		t.Fatalf("XLSX input was not preserved: %v", statErr)
	}
}

type xlsxPackageFixtureOptions struct {
	updatedCell                 bool
	tamperedStyles              bool
	table                       bool
	brokenWorksheetRelationship bool
}

func writeDocumentXLSXPackageFixture(t *testing.T, packagePath string, options xlsxPackageFixtureOptions) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(packagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	worksheetTarget := "worksheets/sheet1.xml"
	if options.brokenWorksheetRelationship {
		worksheetTarget = "worksheets/missing.xml"
	}
	sharedString := "Alpha"
	if options.updatedCell {
		sharedString = "Beta"
	}
	stylesSuffix := ""
	if options.tamperedStyles {
		stylesSuffix = `<cellStyles count="1"><cellStyle name="Tampered" xfId="0" builtinId="0"/></cellStyles>`
	}
	entries := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
  <Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets>
</workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="` + worksheetTarget + `"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>
</Relationships>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <dimension ref="A1:B2"/><sheetData><row r="1"><c r="A1" t="s" s="1"><v>0</v></c><c r="B1"><f>A1</f><v>1</v></c></row></sheetData>
  <mergeCells count="1"><mergeCell ref="A2:B2"/></mergeCells>
</worksheet>`,
		"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="1" uniqueCount="1"><si><t>` + sharedString + `</t></si></sst>`,
		"xl/styles.xml": `<?xml version="1.0" encoding="UTF-8"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="1"><font/></fonts><fills count="1"><fill/></fills><borders count="1"><border/></borders><cellXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellXfs>` + stylesSuffix + `</styleSheet>`,
	}
	if options.updatedCell {
		entries["xl/worksheets/sheet1.xml"] = stringsReplaceOnce(entries["xl/worksheets/sheet1.xml"], `<v>0</v>`, `<v>0</v><extLst><ext uri="updated"/></extLst>`)
	}
	if options.table {
		entries["xl/tables/table1.xml"] = `<?xml version="1.0" encoding="UTF-8"?><table xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" id="1" name="Table1" displayName="Table1" ref="A1:B2"/>`
	}
	file, err := os.Create(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		writer, createErr := archive.Create(name)
		if createErr == nil {
			_, createErr = writer.Write([]byte(content))
		}
		if createErr != nil {
			_ = archive.Close()
			_ = file.Close()
			t.Fatal(createErr)
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func stringsReplaceOnce(value, old, replacement string) string {
	index := strings.Index(value, old)
	if index < 0 {
		return value
	}
	return value[:index] + replacement + value[index+len(old):]
}
