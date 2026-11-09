package modelrouter

import (
	"encoding/json"
	"testing"
)

func TestMockBrowserUsesProjectedControlAndRuntimeBoundArguments(t *testing.T) {
	prompt := "PROVISIONED_EVIDENCE\n" + `{"controls":[{"ref":"f1e4","role":"textbox","accessible_name":"Name"},{"ref":"e5","role":"paragraph"}]}`
	for _, stage := range []string{"assess_goal_initial", "assess_goal_after_action", "choose_and_draft"} {
		var action struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(mockBrowserInteractionAction(prompt, "填写 Alice。", stage)), &action); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"snapshot_id", "page_id", "session_generation", "page_generation", "reason"} {
			if _, exists := action.Arguments[field]; exists {
				t.Fatal("mock emitted non-visible argument", field)
			}
		}
		if stage == "choose_and_draft" {
			if action.Arguments["uid"] != "f1e4" || action.Arguments["text"] != "Alice" {
				t.Fatal(action)
			}
		} else if action.Arguments["evidence_refs"].([]any)[0] != "f1e4" {
			t.Fatal(action)
		}
	}
}
