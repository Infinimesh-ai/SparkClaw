package browsercontrol

import "testing"

func TestScriptOperationAdmissionIncludesRead(t *testing.T) {
	service := New(nil, nil, "default")
	for _, operation := range []string{"probe", "send", "read", "discover", "capture", "enumerate_thread", "mark_read", "collect_page", "delete"} {
		_, err := service.RunScript(t.Context(), RunScriptRequest{
			TaskID: "test-email", Provider: "gmail", Operation: operation, ScriptID: "gmail." + operation,
			Revision: 1, CredentialGeneration: 1, Input: map[string]any{"schema_version": 1},
		})
		want := CodeVaultUnavailable
		if operation == "delete" {
			want = CodeInvalidRequest
		}
		if ErrorCode(err) != want {
			t.Fatalf("operation=%s code=%s want=%s", operation, ErrorCode(err), want)
		}
	}
}
