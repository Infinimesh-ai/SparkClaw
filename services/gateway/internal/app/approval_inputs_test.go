package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func inputFormFixture() *ApprovalInputForm {
	min, max := 0.0, 100000.0
	return &ApprovalInputForm{SchemaVersion: 1, Entries: []ApprovalInputEntry{
		{ID: "entry_1", Label: "First", Summary: "Known values", Fields: []ApprovalInputField{{Name: "quantity", Label: "Quantity", Type: ApprovalInputNumber, Required: true, Min: &min, Max: &max}, {Name: "unit", Label: "Unit", Type: ApprovalInputSelect, Required: true, Options: []ApprovalInputOption{{Value: "capsule", Label: "Capsules"}}}}},
		{ID: "entry_2", Label: "Second", Fields: []ApprovalInputField{{Name: "expiry", Label: "Expiry", Type: ApprovalInputDateOrNone, Required: true}}},
	}}
}
func TestApprovalInputsStrictFieldAndTargetValidation(t *testing.T) {
	good := ApprovalInputValues{"entry_1": {"quantity": 24.0, "unit": "capsule"}, "entry_2": {"expiry": "2027-05-31"}}
	if err := ValidateApprovalInputs(inputFormFixture(), good); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(ApprovalInputValues)
	}{
		{"missing entry", func(v ApprovalInputValues) { delete(v, "entry_2") }},
		{"wrong entry", func(v ApprovalInputValues) { v["entry_3"] = map[string]any{"expiry": "2027-05-31"} }},
		{"unlisted field", func(v ApprovalInputValues) { v["entry_1"]["target_id"] = "med_forged" }},
		{"unlisted option", func(v ApprovalInputValues) { v["entry_1"]["unit"] = "invented" }},
		{"negative", func(v ApprovalInputValues) { v["entry_1"]["quantity"] = -1 }},
		{"string number", func(v ApprovalInputValues) { v["entry_1"]["quantity"] = "24" }},
		{"object number", func(v ApprovalInputValues) { v["entry_1"]["quantity"] = map[string]any{"value": 24} }},
		{"false date", func(v ApprovalInputValues) { v["entry_2"]["expiry"] = "2027-02-31" }},
		{"unset date", func(v ApprovalInputValues) { v["entry_2"]["expiry"] = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(good)
			var values ApprovalInputValues
			_ = json.Unmarshal(raw, &values)
			test.mutate(values)
			if err := ValidateApprovalInputs(inputFormFixture(), values); err == nil {
				t.Fatal("invalid completion accepted")
			}
		})
	}
	good["entry_2"]["expiry"] = "no_expiry"
	if err := ValidateApprovalInputs(inputFormFixture(), good); err != nil {
		t.Fatal(err)
	}
	form := inputFormFixture()
	form.SchemaVersion = 2
	if ValidateApprovalInputs(form, good) == nil {
		t.Fatal("unknown schema accepted")
	}
}
func TestApprovalInputDigestsBindDraftAndExactValues(t *testing.T) {
	approval := Approval{ID: "a", RunID: "r", SessionID: "s", ToolCallID: "c", Tool: "notify.ask_approval", Arguments: map[string]any{"draft": "first"}, InputForm: inputFormFixture()}
	a, _ := ApprovalFormDigest(approval)
	approval.Arguments["draft"] = "changed"
	b, _ := ApprovalFormDigest(approval)
	if a == b {
		t.Fatal("draft change not bound")
	}
	left, _ := ApprovalInputsDigest(ApprovalInputValues{"e": {"notes": "<>&\u2028\u2029", "quantity": 24.0}})
	right, _ := ApprovalInputsDigest(ApprovalInputValues{"e": {"quantity": 24, "notes": "<>&\u2028\u2029"}})
	if left != right || len(left) != 64 {
		t.Fatal(left, right)
	}
	form := inputFormFixture()
	form.Entries[0].Fields[0].Name = strings.Repeat("x", 81)
	if ValidateApprovalInputForm(form) == nil {
		t.Fatal("unbounded key accepted")
	}
}

func TestApprovalInputFormSubmitLabelValidation(t *testing.T) {
	for _, test := range []struct {
		name  string
		label string
		valid bool
	}{
		{"omitted medicine label", "", true},
		{"English order label", "Confirm order details", true},
		{"Chinese order label", "确认点餐信息", true},
		{"Unicode length boundary", strings.Repeat("确", 80), true},
		{"too long", strings.Repeat("确", 81), false},
		{"blank", " \u3000 ", false},
		{"newline", "Confirm\norder", false},
		{"tab", "Confirm\torder", false},
		{"control", "Confirm\x7forder", false},
		{"Unicode control", "Confirm\u0085order", false},
		{"line separator", "Confirm\u2028order", false},
		{"paragraph separator", "Confirm\u2029order", false},
		{"invalid UTF-8", "Confirm\xfforder", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			form := inputFormFixture()
			form.SubmitLabel = test.label
			if err := ValidateApprovalInputForm(form); (err == nil) != test.valid {
				t.Fatalf("valid = %v, error = %v", test.valid, err)
			}
		})
	}
}

func TestApprovalInputFormDigestBindsSubmitLabel(t *testing.T) {
	approval := Approval{ID: "a", RunID: "r", SessionID: "s", ToolCallID: "c", Tool: "notify.ask_approval", InputForm: inputFormFixture()}
	legacy, err := json.Marshal(approval.InputForm)
	if err != nil || strings.Contains(string(legacy), "submit_label") {
		t.Fatalf("legacy form wire representation changed: %s, %v", legacy, err)
	}
	original, err := ApprovalFormDigest(approval)
	if err != nil {
		t.Fatal(err)
	}
	approval.InputForm.SubmitLabel = "确认点餐信息"
	labeled, err := ApprovalFormDigest(approval)
	if err != nil || labeled == original {
		t.Fatalf("adding a submit label must change the form digest: %v", err)
	}
	raw, err := json.Marshal(approval.InputForm)
	if err != nil {
		t.Fatal(err)
	}
	var restored ApprovalInputForm
	if err := json.Unmarshal(raw, &restored); err != nil || restored.SubmitLabel != approval.InputForm.SubmitLabel {
		t.Fatalf("submit label was not preserved: %#v, %v", restored, err)
	}
	approval.InputForm = &restored
	roundTrip, err := ApprovalFormDigest(approval)
	if err != nil || roundTrip != labeled {
		t.Fatalf("form digest changed on JSON round trip: %v", err)
	}
	approval.InputForm.SubmitLabel = "Confirm order details"
	tampered, err := ApprovalFormDigest(approval)
	if err != nil || tampered == labeled {
		t.Fatalf("changing only the submit label must invalidate the bound digest: %v", err)
	}
	approval.InputForm.SubmitLabel = ""
	removed, err := ApprovalFormDigest(approval)
	if err != nil || removed != original || removed == labeled {
		t.Fatalf("removing the submit label must restore only the legacy digest: %v", err)
	}
}
