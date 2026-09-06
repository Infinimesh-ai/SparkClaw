package app

import (
	"slices"
	"testing"
)

func TestPPTXVisualIssueClassTableIsConsistentAndDefensive(t *testing.T) {
	specs := PPTXVisualIssueClassSpecs()
	if len(specs) != 13 {
		t.Fatalf("PPTX visual issue class table has %d rows, want 13", len(specs))
	}
	operations := PPTXVisualRepairOperationNames()
	seen := map[string]bool{}
	for _, spec := range specs {
		if spec.Class == "" || seen[spec.Class] {
			t.Fatalf("PPTX visual issue class %q is empty or duplicated", spec.Class)
		}
		seen[spec.Class] = true
		if len(spec.AllowedOperations) == 0 {
			t.Fatalf("PPTX visual issue class %q allows no repair operation", spec.Class)
		}
		for _, operation := range spec.AllowedOperations {
			if !slices.Contains(operations, string(operation)) {
				t.Fatalf("PPTX visual issue class %q allows unknown operation %q", spec.Class, operation)
			}
		}
		switch spec.EvidenceSource {
		case PPTXVisualEvidenceObjective:
			if spec.DiagnosticKind == "" || spec.HarmfulEffect == "" || spec.BenignEffect == "" || spec.HarmfulEffect == spec.BenignEffect || spec.Subjective() {
				t.Fatalf("objective PPTX visual class %q lacks its diagnostic binding: %#v", spec.Class, spec)
			}
			if PPTXVisualClassForFactReview(spec.DiagnosticKind, spec.HarmfulEffect) != spec.Class || PPTXVisualClassForDiagnosticKind(spec.DiagnosticKind) != spec.Class {
				t.Fatalf("objective PPTX visual class %q does not round-trip through its diagnostic", spec.Class)
			}
		case PPTXVisualEvidenceSubjective:
			if spec.DiagnosticKind != "" || spec.HarmfulEffect != "" || spec.BenignEffect != "" || !spec.Subjective() {
				t.Fatalf("subjective PPTX visual class %q carries a diagnostic binding: %#v", spec.Class, spec)
			}
		default:
			t.Fatalf("PPTX visual class %q has unknown evidence source %q", spec.Class, spec.EvidenceSource)
		}
		if spec.OutcomeOnly && (spec.BlockingEligible || !spec.Subjective()) {
			t.Fatalf("outcome-only PPTX visual class %q must be subjective and never block: %#v", spec.Class, spec)
		}
	}
	if got := PPTXVisualBlockingEligibleClasses(); !slices.Equal(got, []string{PPTXVisualClassTextClipped, PPTXVisualClassContentObscured, PPTXVisualClassElementOffCanvas, PPTXVisualClassMissingGlyph}) {
		t.Fatalf("PPTX visual blocking ceiling drifted: %#v", got)
	}
	if got := PPTXVisualSubjectiveIssueTypes(); len(got) != 10 || slices.Contains(got, PPTXVisualClassTextClipped) {
		t.Fatalf("PPTX visual subjective issue types drifted: %#v", got)
	}
	if PPTXVisualClassForFactReview(PPTXVisualDiagnosticTextClipping, "decorative_or_empty") != "" || PPTXVisualClassForDiagnosticKind("unknown") != "" {
		t.Fatal("benign or unknown diagnostics mapped to a PPTX visual class")
	}
	if got := PPTXVisualDiagnosticKinds(); !slices.Equal(got, []PPTXVisualDiagnosticKind{PPTXVisualDiagnosticTextClipping, PPTXVisualDiagnosticGeometryOverlap, PPTXVisualDiagnosticOffCanvas}) {
		t.Fatalf("PPTX visual diagnostic kinds drifted: %#v", got)
	}
	if got := PPTXVisualSemanticEffectsForDiagnostic(PPTXVisualDiagnosticOffCanvas); !slices.Equal(got, []string{"harmful_overflow", "intentional_bleed", PPTXVisualSemanticEffectUnclear}) {
		t.Fatalf("PPTX visual semantic effects for off_canvas drifted: %#v", got)
	}
	if PPTXVisualSemanticEffectsForDiagnostic("unknown") != nil || len(PPTXVisualSemanticEffects()) != 7 {
		t.Fatalf("PPTX visual semantic effect vocabulary drifted: %#v", PPTXVisualSemanticEffects())
	}
	if _, ok := PPTXVisualIssueClass("arbitrary_ooxml"); ok {
		t.Fatal("unknown PPTX visual class was found")
	}

	specs[0].AllowedOperations[0] = "mutated"
	if spec, _ := PPTXVisualIssueClass(PPTXVisualClassTextClipped); spec.AllowedOperations[0] == "mutated" {
		t.Fatal("PPTX visual issue class table exposed internal slices")
	}
	names := PPTXVisualRepairOperationNames()
	names[0] = "mutated"
	if PPTXVisualRepairOperationNames()[0] == "mutated" {
		t.Fatal("PPTX visual repair operation names exposed the internal slice")
	}
}
