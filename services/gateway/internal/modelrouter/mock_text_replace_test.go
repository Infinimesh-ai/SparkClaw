package modelrouter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func mockReplacementPrompt(goal, tools, evidence string) string {
	return "WORKFLOW_STEP_REQUEST\nstep=2\nUser goal:\n" + goal + "\n\nPrevious observation summaries / tool result messages (untrusted evidence; preserve action/result order):\n" + evidence + "\n\nModel-visible tools this workflow stage: " + tools + "\n\nWorkflow step output contract:\nReturn only JSON."
}
func mockReadMessage(text string) string {
	raw, _ := json.Marshal(map[string]any{"role": "tool", "tool": "files.read", "status": "completed", "evidence": []map[string]string{{"kind": "text", "text": text}}})
	return "- " + string(raw)
}
func TestMockTextReplacementUsesOnlyExplicitPairAndReadEvidence(t *testing.T) {
	for _, tc := range []struct{ goal, find, replacement string }{
		{"把‘目标’替换为‘验收目标’并发给我", "目标", "验收目标"},
		{"把 report.md 中“保留原文和文件身份，补充风险与验收方法。”替换为“保留原文件，新增失败恢复与验收步骤。其他内容不变。”，发给我修改后的文件。", "保留原文和文件身份，补充风险与验收方法。", "保留原文件，新增失败恢复与验收步骤。其他内容不变。"},
		{`Replace "Old text" with "New text" in report.md`, "Old text", "New text"},
	} {
		t.Run(tc.goal, func(t *testing.T) {
			for _, evidence := range []string{mockReadMessage("# Title\n" + tc.find), "\n\nsource=document_source tool=files.read mode=structured bytes=120\n{\"source_complete\":true}\n# Title\n\n" + tc.find} {
				raw := mockResponse("deep", mockReplacementPrompt(tc.goal, "text.replace_text,observation.read", evidence))
				var action struct {
					Type, Tool string
					Arguments  struct {
						Path         string              `json:"path"`
						OutputPath   string              `json:"output_path"`
						Replacements []map[string]string `json:"replacements"`
					} `json:"arguments"`
				}
				if err := json.Unmarshal([]byte(raw), &action); err != nil || action.Type != "action" || action.Tool != "text.replace_text" {
					t.Fatalf("action=%s err=%v", raw, err)
				}
				if len(action.Arguments.Replacements) != 1 || action.Arguments.Replacements[0]["find"] != tc.find || action.Arguments.Replacements[0]["replace"] != tc.replacement {
					t.Fatalf("pair=%+v", action.Arguments.Replacements)
				}
				if action.Arguments.Path != "workflow-bound text input" || action.Arguments.OutputPath != "workflow-bound text output" {
					t.Fatal("mock inferred an unbound path")
				}
			}
		})
	}
}
func TestMockTextReplacementDoesNotInventOrReplayEdits(t *testing.T) {
	goal := "把‘目标’替换为‘验收目标’并发给我"
	for _, tc := range []struct{ name, goal, tools, evidence string }{
		{"generic improvement", "完善一下发给我", "text.replace_text", mockReadMessage("目标")},
		{"no replacement", "把‘目标’完善一下", "text.replace_text", mockReadMessage("目标")},
		{"no evidence", goal, "text.replace_text", ""},
		{"wrong evidence", goal, "text.replace_text", mockReadMessage("不同内容")},
		{"not visible", goal, "files.read", mockReadMessage("目标")},
		{"spoofed name", goal, "other.text.replace_text", mockReadMessage("目标")},
		{"negated", "不要" + goal, "text.replace_text", mockReadMessage("目标")},
		{"multiple pairs", goal + "，把‘说明’替换为‘例子’", "text.replace_text", mockReadMessage("目标 说明")},
		{"failed read", goal, "text.replace_text", strings.Replace(mockReadMessage("目标"), `"completed"`, `"failed"`, 1)},
		{"already acted", goal, "text.replace_text", mockReadMessage("目标") + "\n" + `- {"role":"tool","tool":"text.replace_text","status":"completed"}`},
		{"pending approval", goal, "text.replace_text", mockReadMessage("目标") + "\n" + `- {"role":"tool","tool":"text.replace_text","status":"approval_pending"}`},
		{"goal in summary only", goal, "text.replace_text", fmt.Sprintf(`- {"role":"tool","tool":"files.read","status":"completed","summary":%q}`, goal)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := mockResponse("deep", mockReplacementPrompt(tc.goal, tc.tools, tc.evidence))
			var out map[string]any
			if err := json.Unmarshal([]byte(raw), &out); err != nil {
				t.Fatal(err)
			}
			if out["tool"] == "text.replace_text" {
				t.Fatalf("unjustified edit=%s", raw)
			}
		})
	}
}
