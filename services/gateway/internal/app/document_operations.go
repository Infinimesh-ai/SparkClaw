package app

import (
	"fmt"
	"strings"
)

const (
	DocumentSourceSHA256Argument = "source_sha256"

	DocumentOperationReplaceText      = "replace_text"
	DocumentOperationReplaceParagraph = "replace_paragraph"
	DocumentOperationInsertParagraph  = "insert_paragraph"
	DocumentOperationDeleteParagraph  = "delete_paragraph"
	DocumentOperationSetTextStyle     = "set_text_style"
	DocumentOperationUpdateCell       = "update_cell"
	DocumentOperationInsertRow        = "insert_row"
	DocumentOperationDeleteRow        = "delete_row"
	DocumentOperationUpdateRow        = "update_row"
	DocumentOperationAppendRow        = "append_row"
	DocumentOperationAddSlide         = "add_slide"
	DocumentOperationUpdateSlide      = "update_slide"
	DocumentOperationUpdateDeck       = "update_deck"
	DocumentOperationDuplicateSlide   = "duplicate_slide"
	DocumentOperationDeleteSlide      = "delete_slide"
	DocumentOperationExtractPages     = "extract_pages"
	DocumentOperationDeletePages      = "delete_pages"
	DocumentOperationRotatePages      = "rotate_pages"
	DocumentOperationSplit            = "split"
)

type DocumentOperationSpec struct {
	Name                 string
	RequiresSourceSHA256 bool
}

type DocumentFormatOperationSpec struct {
	Format     string
	Operations []DocumentOperationSpec
}

type documentFormatOperationCatalog struct {
	formats  []DocumentFormatOperationSpec
	byFormat map[string]DocumentFormatOperationSpec
}

var registeredDocumentFormatOperations = newDocumentFormatOperationCatalog([]DocumentFormatOperationSpec{
	{
		Format: DocumentFormatText,
		Operations: []DocumentOperationSpec{
			{Name: DocumentOperationReplaceText},
		},
	},
	{
		Format: DocumentFormatDOCX,
		Operations: sourceBoundDocumentOperations(
			DocumentOperationReplaceText,
			DocumentOperationReplaceParagraph,
			DocumentOperationInsertParagraph,
			DocumentOperationDeleteParagraph,
			DocumentOperationSetTextStyle,
		),
	},
	{
		Format: DocumentFormatXLSX,
		Operations: sourceBoundDocumentOperations(
			DocumentOperationReplaceText,
			DocumentOperationUpdateCell,
			DocumentOperationInsertRow,
			DocumentOperationDeleteRow,
			DocumentOperationUpdateRow,
			DocumentOperationAppendRow,
		),
	},
	{
		Format: DocumentFormatPPTX,
		Operations: sourceBoundDocumentOperations(
			DocumentOperationReplaceText,
			DocumentOperationAddSlide,
			DocumentOperationUpdateSlide,
			DocumentOperationUpdateDeck,
			DocumentOperationDuplicateSlide,
			DocumentOperationDeleteSlide,
		),
	},
	{
		Format: DocumentFormatPDF,
		Operations: []DocumentOperationSpec{
			{Name: DocumentOperationExtractPages},
			{Name: DocumentOperationDeletePages},
			{Name: DocumentOperationRotatePages},
			{Name: DocumentOperationSplit},
		},
	},
})

func sourceBoundDocumentOperations(names ...string) []DocumentOperationSpec {
	operations := make([]DocumentOperationSpec, 0, len(names))
	for _, name := range names {
		operations = append(operations, DocumentOperationSpec{Name: name, RequiresSourceSHA256: true})
	}
	return operations
}

func newDocumentFormatOperationCatalog(specs []DocumentFormatOperationSpec) documentFormatOperationCatalog {
	catalog := documentFormatOperationCatalog{
		formats:  make([]DocumentFormatOperationSpec, 0, len(specs)),
		byFormat: make(map[string]DocumentFormatOperationSpec, len(specs)),
	}
	for _, candidate := range specs {
		format := canonicalDocumentOperationKey(candidate.Format)
		if format == "" {
			panic("app: document operation catalog has an empty format")
		}
		if _, exists := catalog.byFormat[format]; exists {
			panic(fmt.Sprintf("app: duplicate document operation format %q", format))
		}
		if len(candidate.Operations) == 0 {
			panic(fmt.Sprintf("app: document operation format %q has no operations", format))
		}
		seen := make(map[string]bool, len(candidate.Operations))
		operations := make([]DocumentOperationSpec, 0, len(candidate.Operations))
		for _, operationCandidate := range candidate.Operations {
			operation := canonicalDocumentOperationKey(operationCandidate.Name)
			if operation == "" {
				panic(fmt.Sprintf("app: document operation format %q has an empty operation", format))
			}
			if seen[operation] {
				panic(fmt.Sprintf("app: duplicate document operation %s:%s", format, operation))
			}
			seen[operation] = true
			operationCandidate.Name = operation
			operations = append(operations, operationCandidate)
		}
		spec := DocumentFormatOperationSpec{Format: format, Operations: operations}
		catalog.formats = append(catalog.formats, spec)
		catalog.byFormat[format] = spec
	}
	return catalog
}

func canonicalDocumentOperationKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func DocumentFormatOperationSpecs() []DocumentFormatOperationSpec {
	return cloneDocumentFormatOperationSpecs(registeredDocumentFormatOperations.formats)
}

func DocumentOperationsForFormat(format string) ([]DocumentOperationSpec, bool) {
	spec, ok := registeredDocumentFormatOperations.byFormat[canonicalDocumentOperationKey(format)]
	if !ok {
		return nil, false
	}
	return append([]DocumentOperationSpec(nil), spec.Operations...), true
}

func DocumentOperationFor(format, operation string) (DocumentOperationSpec, bool) {
	operations, ok := DocumentOperationsForFormat(format)
	if !ok {
		return DocumentOperationSpec{}, false
	}
	operation = canonicalDocumentOperationKey(operation)
	for _, candidate := range operations {
		if candidate.Name == operation {
			return candidate, true
		}
	}
	return DocumentOperationSpec{}, false
}

func cloneDocumentFormatOperationSpecs(specs []DocumentFormatOperationSpec) []DocumentFormatOperationSpec {
	out := make([]DocumentFormatOperationSpec, len(specs))
	for index, spec := range specs {
		out[index] = DocumentFormatOperationSpec{
			Format:     spec.Format,
			Operations: append([]DocumentOperationSpec(nil), spec.Operations...),
		}
	}
	return out
}
