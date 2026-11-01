package semanticrouting

import (
	"math"
	"strings"
	"testing"
)

func TestCompileRequiresBilingualClarificationLabels(t *testing.T) {
	for _, label := range []ClarifyLabel{{}, {ZH: "读取文件"}, {EN: "Read file"}, {ZH: "读取\n文件", EN: "Read file"}, {ZH: "读取文件", EN: strings.Repeat("x", 161)}, {ZH: "\xff", EN: "Read file"}} {
		registrations := testRegistrations()
		registrations[0].Semantics.Variants[0].ClarifyLabel = label
		if _, err := Compile(testCatalog(t), registrations); err == nil {
			t.Fatalf("compiled invalid label %#v", label)
		}
	}
}

func TestGraphRevisionIncludesClarificationLabels(t *testing.T) {
	registrations := testRegistrations()
	before, err := Compile(testCatalog(t), registrations)
	if err != nil {
		t.Fatal(err)
	}
	registrations[0].Semantics.Variants[0].ClarifyLabel.EN = "Explain the answer"
	after, err := Compile(testCatalog(t), registrations)
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision() == after.Revision() {
		t.Fatal("label change retained an old graph revision")
	}
}

func TestCalibrationRejectsUnsafeClarificationFloor(t *testing.T) {
	for _, floor := range []float64{0, -0.01, 1.01, math.NaN(), math.Inf(1), DefaultCalibration().AmbiguousMinimum + 0.01} {
		calibration := DefaultCalibration()
		calibration.ClarifyFloor = floor
		if err := calibration.Validate(); err == nil {
			t.Fatalf("accepted clarification floor %v", floor)
		}
	}
	calibration := DefaultCalibration()
	calibration.ClarifyFloor = calibration.AmbiguousMinimum
	if err := calibration.Validate(); err != nil {
		t.Fatal(err)
	}
}
