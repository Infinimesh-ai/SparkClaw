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
	if len(seen) != 22 {
		t.Fatalf("registered operations = %d, want 22", len(seen))
	}
	if !seen[OperationEmailAnalysis] || !seen[OperationPPTXVisualAssessment] || !seen[OperationPPTXVisualRepairPlan] {
		t.Fatal("PPTX visual operations are not registered")
	}
	if spec, err := Spec(OperationMedicineRecognize); err != nil || spec.OutputClass != OutputVisionStructured || len(spec.AllowedLanes) != 1 || spec.AllowedLanes[0] != LaneFast {
		t.Fatalf("medicine recognition must be a fast-lane vision operation: %#v %v", spec, err)
	}
	if spec, err := Spec(OperationMedicineParse); err != nil || spec.OutputClass != OutputCompactStructured || len(spec.AllowedLanes) != 1 || spec.AllowedLanes[0] != LaneFast {
		t.Fatalf("medicine text reading must be a fast-lane structured operation: %#v %v", spec, err)
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
