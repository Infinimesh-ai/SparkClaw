package app

import (
	"slices"
	"testing"
)

func TestDocumentFormatOperationCatalogIsOrderedAndDefensive(t *testing.T) {
	specs := DocumentFormatOperationSpecs()
	formats := make([]string, 0, len(specs))
	for _, spec := range specs {
		formats = append(formats, spec.Format)
	}
	if want := []string{DocumentFormatText, DocumentFormatDOCX, DocumentFormatXLSX, DocumentFormatPPTX, DocumentFormatPDF}; !slices.Equal(formats, want) {
		t.Fatalf("document formats = %#v, want %#v", formats, want)
	}

	docx, ok := DocumentOperationsForFormat(DocumentFormatDOCX)
	if !ok {
		t.Fatal("DOCX operation catalog is missing")
	}
	docxNames := make([]string, 0, len(docx))
	for _, operation := range docx {
		docxNames = append(docxNames, operation.Name)
		if !operation.RequiresSourceSHA256 {
			t.Fatalf("DOCX operation %q does not require source SHA-256", operation.Name)
		}
	}
	if want := []string{
		DocumentOperationReplaceText,
		DocumentOperationReplaceParagraph,
		DocumentOperationInsertParagraph,
		DocumentOperationDeleteParagraph,
		DocumentOperationSetTextStyle,
	}; !slices.Equal(docxNames, want) {
		t.Fatalf("DOCX operations = %#v, want %#v", docxNames, want)
	}

	specs[0].Format = "mutated"
	specs[1].Operations[0].Name = "mutated"
	fresh := DocumentFormatOperationSpecs()
	if fresh[0].Format != DocumentFormatText || fresh[1].Operations[0].Name != DocumentOperationReplaceText {
		t.Fatalf("document operation authority was mutable: %#v", fresh)
	}
}

func TestDocumentOperationLookupNormalizesAndRejectsCrossFormatPairs(t *testing.T) {
	operation, ok := DocumentOperationFor(" DOCX ", " REPLACE_TEXT ")
	if !ok || operation.Name != DocumentOperationReplaceText || !operation.RequiresSourceSHA256 {
		t.Fatalf("normalized DOCX lookup failed: %#v ok=%v", operation, ok)
	}
	if _, ok := DocumentOperationFor(DocumentFormatXLSX, DocumentOperationUpdateSlide); ok {
		t.Fatal("cross-format document operation unexpectedly resolved")
	}
	if _, ok := DocumentOperationsForFormat(DocumentFormatImage); ok {
		t.Fatal("image unexpectedly appears in the executable document operation catalog")
	}
}

func TestDocumentFormatOperationCatalogRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name  string
		specs []DocumentFormatOperationSpec
	}{
		{name: "empty format", specs: []DocumentFormatOperationSpec{{Operations: []DocumentOperationSpec{{Name: "edit"}}}}},
		{name: "duplicate format", specs: []DocumentFormatOperationSpec{
			{Format: "synthetic", Operations: []DocumentOperationSpec{{Name: "edit"}}},
			{Format: " SYNTHETIC ", Operations: []DocumentOperationSpec{{Name: "edit"}}},
		}},
		{name: "empty operations", specs: []DocumentFormatOperationSpec{{Format: "synthetic"}}},
		{name: "empty operation", specs: []DocumentFormatOperationSpec{{Format: "synthetic", Operations: []DocumentOperationSpec{{}}}}},
		{name: "duplicate operation", specs: []DocumentFormatOperationSpec{{
			Format: "synthetic", Operations: []DocumentOperationSpec{{Name: "edit"}, {Name: " EDIT "}},
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid document operation catalog did not panic")
				}
			}()
			_ = newDocumentFormatOperationCatalog(test.specs)
		})
	}
}
