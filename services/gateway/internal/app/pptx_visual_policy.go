package app

import "slices"

// PPTXVisualEvidenceSource states which pipeline stage produced a Runtime
// issue: a deterministic diagnostic fact confirmed on the rendered page, or a
// Fast model's subjective reading of the page pixels.
type PPTXVisualEvidenceSource string

const (
	PPTXVisualEvidenceObjective  PPTXVisualEvidenceSource = "objective"
	PPTXVisualEvidenceSubjective PPTXVisualEvidenceSource = "subjective"
)

// PPTXVisualDiagnosticKind names a deterministic diagnostic the render
// analysis script emits for a page.
type PPTXVisualDiagnosticKind string

const (
	PPTXVisualDiagnosticTextClipping    PPTXVisualDiagnosticKind = "text_clipping"
	PPTXVisualDiagnosticGeometryOverlap PPTXVisualDiagnosticKind = "geometry_overlap"
	PPTXVisualDiagnosticOffCanvas       PPTXVisualDiagnosticKind = "off_canvas"
)

// PPTXVisualSemanticEffectUnclear is the reviewer verdict that neither
// confirms nor dismisses a diagnostic fact; it never promotes a class.
const PPTXVisualSemanticEffectUnclear = "unclear"

// PPTXVisualRepairOperation is one bounded mutation the repair adapter can
// apply to a current-candidate shape. The Python repair script dispatches on
// these exact names and the QA script advertises them as edit capabilities.
type PPTXVisualRepairOperation string

const (
	PPTXVisualRepairRewriteText          PPTXVisualRepairOperation = "rewrite_text"
	PPTXVisualRepairSetGeometry          PPTXVisualRepairOperation = "set_geometry"
	PPTXVisualRepairSetTextStyle         PPTXVisualRepairOperation = "set_text_style"
	PPTXVisualRepairSetShapeStyle        PPTXVisualRepairOperation = "set_shape_style"
	PPTXVisualRepairPlaceAbove           PPTXVisualRepairOperation = "place_above"
	PPTXVisualRepairPlaceBelow           PPTXVisualRepairOperation = "place_below"
	PPTXVisualRepairDeleteGeneratedShape PPTXVisualRepairOperation = "delete_generated_shape"
)

// Runtime issue classes. Objective classes are derived from a confirmed
// diagnostic fact plus the model's harmful semantic effect; subjective classes
// are reported directly by the Fast reviewer as an issue type.
const (
	PPTXVisualClassTextClipped       = "text_clipped"
	PPTXVisualClassContentObscured   = "content_obscured"
	PPTXVisualClassElementOffCanvas  = "element_off_canvas"
	PPTXVisualClassWeakHierarchy     = "weak_hierarchy"
	PPTXVisualClassPoorWhitespace    = "poor_whitespace"
	PPTXVisualClassUnclearFocus      = "unclear_focus"
	PPTXVisualClassBrokenLayout      = "broken_layout"
	PPTXVisualClassOvercrowded       = "overcrowded"
	PPTXVisualClassMisaligned        = "misaligned"
	PPTXVisualClassLowContrast       = "low_contrast"
	PPTXVisualClassTextTooSmall      = "text_too_small"
	PPTXVisualClassMissingGlyph      = "missing_glyph"
	PPTXVisualClassInconsistentStyle = "inconsistent_style"
)

// PPTXVisualIssueClassSpec is one row of the Runtime issue-class table.
//
// EvidenceSource decides whether the class is bound to a diagnostic fact
// (DiagnosticKind, whose HarmfulEffect review promotes it to the class and
// whose BenignEffect review dismisses it) or to a subjective issue type equal
// to the class name. AllowedOperations is the repair ceiling for the class; operator
// qualification may only narrow it. BlockingEligible is the blocking ceiling:
// only these classes may ever be qualified to block publication. OutcomeOnly
// classes are repaired only when the user's request granted outcome authority.
type PPTXVisualIssueClassSpec struct {
	Class             string
	EvidenceSource    PPTXVisualEvidenceSource
	DiagnosticKind    PPTXVisualDiagnosticKind
	HarmfulEffect     string
	BenignEffect      string
	AllowedOperations []PPTXVisualRepairOperation
	BlockingEligible  bool
	OutcomeOnly       bool
}

// Subjective reports whether the class is diagnosed by the Fast reviewer from
// page pixels rather than derived from a deterministic diagnostic fact.
func (spec PPTXVisualIssueClassSpec) Subjective() bool {
	return spec.EvidenceSource == PPTXVisualEvidenceSubjective
}

// AllowsOperation reports whether the class ceiling admits the operation.
func (spec PPTXVisualIssueClassSpec) AllowsOperation(operation PPTXVisualRepairOperation) bool {
	return slices.Contains(spec.AllowedOperations, operation)
}

var pptxLayoutRepairOperations = []PPTXVisualRepairOperation{
	PPTXVisualRepairSetGeometry, PPTXVisualRepairPlaceAbove, PPTXVisualRepairPlaceBelow, PPTXVisualRepairDeleteGeneratedShape,
}

var pptxStyleRepairOperations = []PPTXVisualRepairOperation{PPTXVisualRepairSetTextStyle, PPTXVisualRepairSetShapeStyle}

var pptxOutcomeRepairOperations = []PPTXVisualRepairOperation{
	PPTXVisualRepairSetGeometry, PPTXVisualRepairSetTextStyle, PPTXVisualRepairSetShapeStyle,
	PPTXVisualRepairPlaceAbove, PPTXVisualRepairPlaceBelow, PPTXVisualRepairDeleteGeneratedShape,
}

// pptxVisualIssueClassTable is the single definition of the Runtime issue
// classes. Config validation, report projection, repair-plan validation, the
// reviewer schema, and the qualification corpus test all derive from it.
// Subjective rows keep the order offered to the reviewer.
var pptxVisualIssueClassTable = []PPTXVisualIssueClassSpec{
	{
		Class: PPTXVisualClassTextClipped, EvidenceSource: PPTXVisualEvidenceObjective,
		DiagnosticKind: PPTXVisualDiagnosticTextClipping, HarmfulEffect: "required_content_lost", BenignEffect: "decorative_or_empty",
		AllowedOperations: []PPTXVisualRepairOperation{PPTXVisualRepairSetGeometry, PPTXVisualRepairSetTextStyle, PPTXVisualRepairRewriteText},
		BlockingEligible:  true,
	},
	{
		Class: PPTXVisualClassContentObscured, EvidenceSource: PPTXVisualEvidenceObjective,
		DiagnosticKind: PPTXVisualDiagnosticGeometryOverlap, HarmfulEffect: "harmful_obstruction", BenignEffect: "intentional_layering",
		AllowedOperations: pptxLayoutRepairOperations, BlockingEligible: true,
	},
	{
		Class: PPTXVisualClassElementOffCanvas, EvidenceSource: PPTXVisualEvidenceObjective,
		DiagnosticKind: PPTXVisualDiagnosticOffCanvas, HarmfulEffect: "harmful_overflow", BenignEffect: "intentional_bleed",
		AllowedOperations: pptxLayoutRepairOperations, BlockingEligible: true,
	},
	{Class: PPTXVisualClassWeakHierarchy, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: pptxOutcomeRepairOperations, OutcomeOnly: true},
	{Class: PPTXVisualClassPoorWhitespace, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: pptxOutcomeRepairOperations, OutcomeOnly: true},
	{Class: PPTXVisualClassUnclearFocus, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: pptxOutcomeRepairOperations, OutcomeOnly: true},
	{Class: PPTXVisualClassBrokenLayout, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: pptxLayoutRepairOperations},
	{Class: PPTXVisualClassOvercrowded, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: pptxLayoutRepairOperations},
	{Class: PPTXVisualClassMisaligned, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: pptxLayoutRepairOperations},
	{Class: PPTXVisualClassLowContrast, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: pptxStyleRepairOperations},
	{Class: PPTXVisualClassTextTooSmall, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: []PPTXVisualRepairOperation{PPTXVisualRepairSetTextStyle}},
	{Class: PPTXVisualClassMissingGlyph, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: []PPTXVisualRepairOperation{PPTXVisualRepairSetTextStyle}, BlockingEligible: true},
	{Class: PPTXVisualClassInconsistentStyle, EvidenceSource: PPTXVisualEvidenceSubjective, AllowedOperations: pptxStyleRepairOperations},
}

// pptxVisualRepairOperationTable is the complete repair operation vocabulary,
// in the order operators see it. Every class ceiling above must draw from it.
var pptxVisualRepairOperationTable = []PPTXVisualRepairOperation{
	PPTXVisualRepairRewriteText, PPTXVisualRepairSetGeometry, PPTXVisualRepairSetTextStyle, PPTXVisualRepairSetShapeStyle,
	PPTXVisualRepairPlaceAbove, PPTXVisualRepairPlaceBelow, PPTXVisualRepairDeleteGeneratedShape,
}

// PPTXVisualIssueClassSpecs returns a defensive copy of the class table.
func PPTXVisualIssueClassSpecs() []PPTXVisualIssueClassSpec {
	out := make([]PPTXVisualIssueClassSpec, 0, len(pptxVisualIssueClassTable))
	for _, spec := range pptxVisualIssueClassTable {
		spec.AllowedOperations = slices.Clone(spec.AllowedOperations)
		out = append(out, spec)
	}
	return out
}

// PPTXVisualIssueClassSpec looks up one class; ok is false for unknown names.
func PPTXVisualIssueClass(class string) (PPTXVisualIssueClassSpec, bool) {
	for _, spec := range pptxVisualIssueClassTable {
		if spec.Class == class {
			spec.AllowedOperations = slices.Clone(spec.AllowedOperations)
			return spec, true
		}
	}
	return PPTXVisualIssueClassSpec{}, false
}

// PPTXVisualIssueClasses lists every Runtime issue class in table order.
func PPTXVisualIssueClasses() []string {
	out := make([]string, 0, len(pptxVisualIssueClassTable))
	for _, spec := range pptxVisualIssueClassTable {
		out = append(out, spec.Class)
	}
	return out
}

// PPTXVisualBlockingEligibleClasses lists the classes that may ever be
// qualified to block publication.
func PPTXVisualBlockingEligibleClasses() []string {
	out := []string{}
	for _, spec := range pptxVisualIssueClassTable {
		if spec.BlockingEligible {
			out = append(out, spec.Class)
		}
	}
	return out
}

// PPTXVisualSubjectiveIssueTypes lists the issue types the Fast reviewer may
// report, in the order they are offered to the model.
func PPTXVisualSubjectiveIssueTypes() []string {
	out := []string{}
	for _, spec := range pptxVisualIssueClassTable {
		if spec.Subjective() {
			out = append(out, spec.Class)
		}
	}
	return out
}

// PPTXVisualRepairOperations lists the complete repair operation vocabulary.
func PPTXVisualRepairOperations() []PPTXVisualRepairOperation {
	return slices.Clone(pptxVisualRepairOperationTable)
}

// PPTXVisualRepairOperationNames is PPTXVisualRepairOperations as strings, for
// config validation and JSON schema enums.
func PPTXVisualRepairOperationNames() []string {
	out := make([]string, 0, len(pptxVisualRepairOperationTable))
	for _, operation := range pptxVisualRepairOperationTable {
		out = append(out, string(operation))
	}
	return out
}

// PPTXVisualClassForFactReview maps a confirmed diagnostic fact plus the
// reviewer's semantic effect to a Runtime class. Benign effects map to "".
func PPTXVisualClassForFactReview(kind PPTXVisualDiagnosticKind, semanticEffect string) string {
	for _, spec := range pptxVisualIssueClassTable {
		if spec.EvidenceSource == PPTXVisualEvidenceObjective && spec.DiagnosticKind == kind && spec.HarmfulEffect == semanticEffect {
			return spec.Class
		}
	}
	return ""
}

// PPTXVisualClassForDiagnosticKind maps a diagnostic kind to the Runtime
// class it can prove, independent of the reviewer's verdict.
func PPTXVisualClassForDiagnosticKind(kind PPTXVisualDiagnosticKind) string {
	for _, spec := range pptxVisualIssueClassTable {
		if spec.EvidenceSource == PPTXVisualEvidenceObjective && spec.DiagnosticKind == kind {
			return spec.Class
		}
	}
	return ""
}

// PPTXVisualDiagnosticKinds lists the deterministic diagnostic kinds the
// render analysis may report, in table order.
func PPTXVisualDiagnosticKinds() []PPTXVisualDiagnosticKind {
	out := []PPTXVisualDiagnosticKind{}
	for _, spec := range pptxVisualIssueClassTable {
		if spec.EvidenceSource == PPTXVisualEvidenceObjective {
			out = append(out, spec.DiagnosticKind)
		}
	}
	return out
}

// PPTXVisualSemanticEffectsForDiagnostic lists the reviewer verdicts accepted
// for one diagnostic kind: its harmful effect, its benign effect, then
// "unclear". Unknown kinds accept nothing.
func PPTXVisualSemanticEffectsForDiagnostic(kind PPTXVisualDiagnosticKind) []string {
	for _, spec := range pptxVisualIssueClassTable {
		if spec.EvidenceSource == PPTXVisualEvidenceObjective && spec.DiagnosticKind == kind {
			return []string{spec.HarmfulEffect, spec.BenignEffect, PPTXVisualSemanticEffectUnclear}
		}
	}
	return nil
}

// PPTXVisualSemanticEffects lists every reviewer verdict across all
// diagnostic kinds, in the order offered to the model.
func PPTXVisualSemanticEffects() []string {
	out := []string{}
	for _, spec := range pptxVisualIssueClassTable {
		if spec.EvidenceSource == PPTXVisualEvidenceObjective {
			out = append(out, spec.HarmfulEffect, spec.BenignEffect)
		}
	}
	return append(out, PPTXVisualSemanticEffectUnclear)
}
