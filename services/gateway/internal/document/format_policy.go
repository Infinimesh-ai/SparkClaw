package document

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type packagePreservationReport struct {
	Status          string
	CheckedFeatures []string
	CoverageNotes   []string
}

type postEditVerifier func(string, EditRequest, []Match) (packagePreservationReport, error)

type preservationPolicy struct {
	VerifyExpected        func(Representation, Representation, EditRequest, []Match) error
	VerifyTargetStructure func(Representation, Representation, EditRequest, []Match) error
	VerifyLayoutChanges   func(Representation, Representation, EditRequest, map[string]any) (map[string]bool, error)
	AllowsBlock           func(EditRequest, Block) bool
	AllowsEvidenceDelta   func([]string, []string) bool
	AllowsAnnotationText  func(EditRequest, map[string]any) bool
	NormalizeEvidence     func(string, string, map[string]any, EditRequest, bool)
	CheckUnchangedContent bool
	ChangesEntityIndexes  bool
}

type documentFormatPolicy struct {
	Format              string
	NormalizationSource string
	FallbackBlocks      func(string, Representation) []Block
	OutputExtension     func(Metadata) string
	AfterRead           func(*ReadResult) error
	AfterEnrich         func(*ReadResult) error
	BeginEdit           func(Metadata, EditRequest) (postEditVerifier, error)
	Operations          map[string]preservationPolicy
}

type documentFormatPolicyRegistry struct {
	formats map[string]documentFormatPolicy
}

func newDocumentFormatPolicyRegistry(policies ...documentFormatPolicy) documentFormatPolicyRegistry {
	registry := documentFormatPolicyRegistry{formats: make(map[string]documentFormatPolicy, len(policies))}
	for _, policy := range policies {
		format := canonicalDocumentPolicyKey(policy.Format)
		if format == "" {
			panic("document: format policy has an empty format")
		}
		if _, exists := registry.formats[format]; exists {
			panic(fmt.Sprintf("document: duplicate format policy %q", format))
		}
		policy.Format = format
		operations := make(map[string]preservationPolicy, len(policy.Operations))
		for operation, operationPolicy := range policy.Operations {
			operation = canonicalDocumentPolicyKey(operation)
			if operation == "" {
				panic(fmt.Sprintf("document: %s format policy has an empty operation", format))
			}
			if _, exists := operations[operation]; exists {
				panic(fmt.Sprintf("document: duplicate preservation policy %s:%s", format, operation))
			}
			operations[operation] = operationPolicy
		}
		policy.Operations = operations
		registry.formats[format] = policy
	}
	return registry
}

func newRegisteredDocumentFormatPolicyRegistry(policies ...documentFormatPolicy) documentFormatPolicyRegistry {
	registry := newDocumentFormatPolicyRegistry(policies...)
	specs := app.DocumentFormatOperationSpecs()
	canonicalFormats := make(map[string]bool, len(specs))
	for _, spec := range specs {
		canonicalFormats[spec.Format] = true
		policy, ok := registry.formats[spec.Format]
		if !ok {
			panic(fmt.Sprintf("document: canonical format policy %q is missing", spec.Format))
		}
		for _, operation := range spec.Operations {
			if _, ok := policy.Operations[operation.Name]; !ok {
				panic(fmt.Sprintf("document: canonical preservation policy %s:%s is missing", spec.Format, operation.Name))
			}
		}
		for operation := range policy.Operations {
			if _, ok := app.DocumentOperationFor(spec.Format, operation); !ok {
				panic(fmt.Sprintf("document: preservation policy %s:%s is absent from the canonical catalog", spec.Format, operation))
			}
		}
	}
	for format := range registry.formats {
		if !canonicalFormats[format] {
			panic(fmt.Sprintf("document: format policy %q is absent from the canonical catalog", format))
		}
	}
	return registry
}

func canonicalDocumentPolicyKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (r documentFormatPolicyRegistry) format(format string) (documentFormatPolicy, bool) {
	policy, ok := r.formats[canonicalDocumentPolicyKey(format)]
	return policy, ok
}

func (r documentFormatPolicyRegistry) operation(format, operation string) (preservationPolicy, bool) {
	formatPolicy, ok := r.format(format)
	if !ok {
		return preservationPolicy{}, false
	}
	policy, ok := formatPolicy.Operations[canonicalDocumentPolicyKey(operation)]
	return policy, ok
}

var registeredDocumentFormatPolicies = newRegisteredDocumentFormatPolicyRegistry(documentFormatPolicies()...)

func documentFormatPolicies() []documentFormatPolicy {
	implementations := map[string]documentFormatPolicy{
		app.DocumentFormatText: textDocumentPolicy(),
		app.DocumentFormatDOCX: docxDocumentPolicy(),
		app.DocumentFormatXLSX: xlsxDocumentPolicy(),
		app.DocumentFormatPPTX: pptxDocumentPolicy(),
		app.DocumentFormatPDF:  pdfDocumentPolicy(),
	}
	policies := make([]documentFormatPolicy, 0, len(implementations))
	for _, spec := range app.DocumentFormatOperationSpecs() {
		policy, ok := implementations[spec.Format]
		if !ok {
			panic(fmt.Sprintf("document: format policy implementation %q is missing", spec.Format))
		}
		policies = append(policies, policy)
		delete(implementations, spec.Format)
	}
	if len(implementations) != 0 {
		panic("document: format policy implementation is absent from the canonical catalog")
	}
	return policies
}

func textDocumentPolicy() documentFormatPolicy {
	return documentFormatPolicy{
		Format: app.DocumentFormatText, NormalizationSource: "plain_text",
		OutputExtension: func(metadata Metadata) string { return strings.ToLower(filepath.Ext(metadata.Path)) },
		Operations: map[string]preservationPolicy{
			app.DocumentOperationReplaceText: replaceTextPreservationPolicy(nil),
		},
	}
}

func docxDocumentPolicy() documentFormatPolicy {
	operations := map[string]preservationPolicy{
		app.DocumentOperationReplaceText: replaceTextPreservationPolicy(verifyDOCXTextReplacementRuns),
	}
	for _, operation := range []string{
		app.DocumentOperationReplaceParagraph,
		app.DocumentOperationInsertParagraph,
		app.DocumentOperationDeleteParagraph,
		app.DocumentOperationSetTextStyle,
	} {
		operation := operation
		operations[operation] = preservationPolicy{
			VerifyExpected: func(before, after Representation, edit EditRequest, matches []Match) error {
				_, err := verifyDOCXExpectedMutation(operation, before, after, edit, matches)
				return err
			},
			CheckUnchangedContent: operation == app.DocumentOperationReplaceParagraph || operation == app.DocumentOperationSetTextStyle,
			ChangesEntityIndexes:  docxOperationChangesEntityIndexes(operation),
		}
	}
	return documentFormatPolicy{Format: app.DocumentFormatDOCX, NormalizationSource: "python_docx", Operations: operations}
}

func xlsxDocumentPolicy() documentFormatPolicy {
	operations := map[string]preservationPolicy{
		app.DocumentOperationReplaceText: replaceTextPreservationPolicy(nil),
	}
	for _, operation := range []string{
		app.DocumentOperationUpdateCell,
		app.DocumentOperationInsertRow,
		app.DocumentOperationDeleteRow,
		app.DocumentOperationUpdateRow,
		app.DocumentOperationAppendRow,
	} {
		operation := operation
		policy := preservationPolicy{
			VerifyExpected: func(before, after Representation, edit EditRequest, _ []Match) error {
				_, err := verifyXLSXExpectedMutation(operation, before, after, edit)
				return err
			},
			CheckUnchangedContent: operation == app.DocumentOperationUpdateCell || operation == app.DocumentOperationUpdateRow,
			ChangesEntityIndexes:  xlsxOperationChangesEntityIndexes(operation),
			NormalizeEvidence: func(_, key string, projection map[string]any, edit EditRequest, after bool) {
				if key == "merged_ranges" && after {
					projection["range"] = xlsxMergedRangeBeforeCoordinates(projection, edit)
				}
			},
		}
		if operation == app.DocumentOperationUpdateRow {
			policy.AllowsBlock = func(edit EditRequest, block Block) bool {
				allowed, _ := xlsxMutationAllowsBlock(operation, edit, block)
				return allowed
			}
		}
		operations[operation] = policy
	}
	return documentFormatPolicy{
		Format: app.DocumentFormatXLSX, NormalizationSource: "exceljs",
		FallbackBlocks: func(documentID string, representation Representation) []Block {
			return blocksFromSheets(documentID, representation.Sheets)
		},
		AfterRead: func(read *ReadResult) error {
			manifest, err := InspectXLSXPackage(read.Metadata.Path)
			if err != nil {
				return err
			}
			if read.Document.ContentScope == nil {
				read.Document.ContentScope = map[string]any{}
			}
			read.Document.ContentScope["package_coverage"] = XLSXPackageReadCoverage(manifest)
			return nil
		},
		BeginEdit: func(metadata Metadata, request EditRequest) (postEditVerifier, error) {
			before, err := ValidateXLSXPackageForOperation(metadata.Path, request.Operation, request.Arguments)
			if err != nil {
				return nil, err
			}
			return func(outputPath string, edit EditRequest, matches []Match) (packagePreservationReport, error) {
				after, err := InspectXLSXPackage(outputPath)
				if err != nil {
					return packagePreservationReport{}, err
				}
				report, err := VerifyXLSXPackagePreservation(before, after, edit, matches)
				if err != nil {
					return packagePreservationReport{}, err
				}
				return packagePreservationReport{
					Status: "verified", CheckedFeatures: report.CheckedFeatureClasses, CoverageNotes: report.CoverageNotes,
				}, nil
			}, nil
		},
		Operations: operations,
	}
}

func pptxDocumentPolicy() documentFormatPolicy {
	operations := map[string]preservationPolicy{
		app.DocumentOperationReplaceText: replaceTextPreservationPolicy(nil),
	}
	for _, operation := range []string{
		app.DocumentOperationAddSlide,
		app.DocumentOperationUpdateSlide,
		app.DocumentOperationUpdateDeck,
		app.DocumentOperationDuplicateSlide,
		app.DocumentOperationDeleteSlide,
	} {
		operation := operation
		policy := preservationPolicy{
			VerifyExpected: func(before, after Representation, edit EditRequest, _ []Match) error {
				_, err := verifyPPTXExpectedMutation(operation, before, after, edit)
				return err
			},
			ChangesEntityIndexes: pptxOperationChangesEntityIndexes(operation),
		}
		if operation == app.DocumentOperationUpdateSlide || operation == app.DocumentOperationUpdateDeck {
			policy.CheckUnchangedContent = true
			policy.AllowsBlock = func(edit EditRequest, block Block) bool {
				allowed, _ := pptxMutationAllowsBlock(operation, edit, block)
				return allowed
			}
			policy.AllowsAnnotationText = pptxMutationAllowsAnnotationText
			policy.VerifyTargetStructure = verifyPPTXRichTextPreservation
			policy.VerifyLayoutChanges = verifyReportedLayoutChanges
		}
		if operation == app.DocumentOperationAddSlide || operation == app.DocumentOperationDuplicateSlide || operation == app.DocumentOperationDeleteSlide {
			policy.AllowsEvidenceDelta = func(before, after []string) bool {
				allowed, _ := pptxOperationAllowsEvidenceDelta(operation, before, after)
				return allowed
			}
		}
		operations[operation] = policy
	}
	replace := operations[app.DocumentOperationReplaceText]
	replace.VerifyTargetStructure = verifyPPTXRichTextPreservation
	replace.AllowsAnnotationText = pptxMutationAllowsAnnotationText
	operations[app.DocumentOperationReplaceText] = replace
	return documentFormatPolicy{
		Format: app.DocumentFormatPPTX, NormalizationSource: "python_pptx",
		FallbackBlocks: func(documentID string, representation Representation) []Block {
			return blocksFromSlides(documentID, representation.Slides)
		},
		Operations: operations,
	}
}

func pdfDocumentPolicy() documentFormatPolicy {
	operations := map[string]preservationPolicy{}
	for _, operation := range []string{
		app.DocumentOperationExtractPages,
		app.DocumentOperationDeletePages,
		app.DocumentOperationRotatePages,
		app.DocumentOperationSplit,
	} {
		operation := operation
		policy := preservationPolicy{
			VerifyExpected: func(before, after Representation, edit EditRequest, _ []Match) error {
				_, err := verifyPDFExpectedMutation(operation, before, after, edit)
				return err
			},
			CheckUnchangedContent: operation == app.DocumentOperationRotatePages,
			ChangesEntityIndexes:  pdfOperationChangesEntityIndexes(operation),
		}
		if operation == app.DocumentOperationExtractPages || operation == app.DocumentOperationDeletePages || operation == app.DocumentOperationSplit {
			policy.AllowsEvidenceDelta = func(before, after []string) bool {
				allowed, _ := pdfOperationAllowsEvidenceDelta(operation, before, after)
				return allowed
			}
		}
		if operation == app.DocumentOperationRotatePages {
			policy.NormalizeEvidence = func(category, _ string, projection map[string]any, _ EditRequest, _ bool) {
				if category == "layout" {
					delete(projection, "rotation")
				}
			}
		}
		operations[operation] = policy
	}
	return documentFormatPolicy{
		Format: app.DocumentFormatPDF, NormalizationSource: "pypdf",
		FallbackBlocks: func(documentID string, representation Representation) []Block {
			return blocksFromPages(documentID, representation.Pages)
		},
		AfterEnrich: func(read *ReadResult) error {
			promotePDFOCRContent(read)
			return nil
		},
		Operations: operations,
	}
}

func replaceTextPreservationPolicy(augment func(Representation, Representation, EditRequest, []Match) error) preservationPolicy {
	return preservationPolicy{
		VerifyExpected: func(before, after Representation, edit EditRequest, matches []Match) error {
			if err := verifyTextReplacement(before, after, edit); err != nil {
				return err
			}
			if augment != nil {
				return augment(before, after, edit, matches)
			}
			return nil
		},
		CheckUnchangedContent: true,
	}
}

func strictEvidenceDelta(before, after []string) bool {
	return slices.Equal(before, after)
}

// HasOperationPolicy reports whether a preservation policy is registered for
// the (format, operation) pair. Exported so downstream registries (toolhub
// operation providers, agent routing policies) can assert parity in tests:
// an operation that is executable but has no policy would fail closed at
// edit time, and that mismatch should be caught at test time instead.
func HasOperationPolicy(format, operation string) bool {
	_, ok := registeredDocumentFormatPolicies.operation(format, operation)
	return ok
}

// HasFormatPolicy reports whether a lifecycle policy is registered for the
// format. Pipeline.Edit fails closed for formats without one.
func HasFormatPolicy(format string) bool {
	_, ok := registeredDocumentFormatPolicies.format(format)
	return ok
}
