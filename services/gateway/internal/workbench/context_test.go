package workbench

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestContextSelectionIsChronologicalAndDoesNotMutateRepository(t *testing.T) {
	long := strings.Repeat("中", app.WorkbenchInputBytes)
	messages := []app.Message{{Role: "system", Content: "untrusted authority"}, {Role: "user", Content: "old"}, {Role: "assistant", Content: long}, {Role: "user", Content: "current", RunID: "current"}}
	selected := SelectMessages(messages, "current", 1)
	if len(selected) != 1 || selected[0].Role != "assistant" || len(selected[0].Content) > app.WorkbenchInputBytes || !utf8.ValidString(selected[0].Content) {
		t.Fatalf("invalid bounded context: %#v", selected)
	}
	if messages[2].Content != long {
		t.Fatal("context selection changed persisted history")
	}
}

func TestInputBudgetDoesNotTruncateOwnerIntent(t *testing.T) {
	for _, input := range []string{strings.Repeat("x", app.WorkbenchInputBytes+1), "bad\x00input", string([]byte{0xff})} {
		if ValidateInput(input) == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if ValidateInput(strings.Repeat("x", app.WorkbenchInputBytes)) != nil {
		t.Fatal("boundary input rejected")
	}
}
