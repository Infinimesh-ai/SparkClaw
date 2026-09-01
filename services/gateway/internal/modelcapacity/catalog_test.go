package modelcapacity

import "testing"

func TestOperationRegistryIsCompleteAndUnique(t *testing.T) {
	seen := map[Operation]bool{}
	for _, spec := range Operations() {
		if spec.Operation == "" || seen[spec.Operation] {
			t.Fatalf("invalid or duplicate operation %q", spec.Operation)
		}
		seen[spec.Operation] = true
		if len(spec.AllowedLanes) == 0 {
			t.Fatalf("operation %q has no allowed lane", spec.Operation)
		}
		if spec.Generates && !IsKnownClass(string(spec.OutputClass)) {
			t.Fatalf("operation %q has unknown output class %q", spec.Operation, spec.OutputClass)
		}
		if !spec.Generates && spec.OutputClass != "" {
			t.Fatalf("non-generating operation %q has output class %q", spec.Operation, spec.OutputClass)
		}
		for _, lane := range spec.AllowedLanes {
			if !IsKnownLane(string(lane)) {
				t.Fatalf("operation %q has unknown lane %q", spec.Operation, lane)
			}
		}
	}
	if len(seen) != 15 {
		t.Fatalf("registered operations = %d, want 15", len(seen))
	}
	if !seen[OperationPPTXVisualAssessment] || !seen[OperationPPTXVisualRepairPlan] {
		t.Fatal("PPTX visual operations are not registered")
	}
}

func TestRequiredClassesAreDerivedFromOperations(t *testing.T) {
	want := map[Lane][]OutputBudgetClass{
		LaneFast:      {OutputAnswer, OutputCompactStructured, OutputVisionStructured, OutputWorkflowStructured},
		LaneDeep:      {OutputAnswer, OutputWorkflowStructured},
		LaneEmbedding: {},
		LaneGuard:     {OutputGuard},
		LaneOCR:       {OutputOCRDocument},
	}
	for lane, expected := range want {
		actual := RequiredClasses(lane)
		if len(actual) != len(expected) {
			t.Fatalf("RequiredClasses(%q) = %#v, want %#v", lane, actual, expected)
		}
		for index := range expected {
			if actual[index] != expected[index] {
				t.Fatalf("RequiredClasses(%q) = %#v, want %#v", lane, actual, expected)
			}
		}
	}
}
