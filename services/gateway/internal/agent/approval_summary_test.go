package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Every tool registered with RequiresApproval must have a human-readable
// approval summary: the bare "Approve <name>" fallback shows the raw tool ID
// with no path or operation context. This turns the historical omission
// pattern (pdf.transform, the xlsx family and text.replace_text fell through
// to the fallback while their docx siblings had full sentences) into a test
// failure for future tools.
func TestEveryApprovalRequiringToolHasAReadableSummary(t *testing.T) {
	cfg := configtest.MustLoadDefault()
	hub := toolhub.New(cfg, store.NewMemoryStore())
	defer hub.Close()
	args := map[string]any{
		"path": "/workspace/doc.bin", "command": "ls", "operation": "split",
	}
	for _, definition := range hub.Definitions() {
		if !definition.RequiresApproval {
			continue
		}
		summary := approvalSummary(definition.Name, args)
		if summary == "Approve "+definition.Name {
			t.Errorf("tool %s requires approval but has no dedicated summary", definition.Name)
		}
		if strings.TrimSpace(summary) == "" {
			t.Errorf("tool %s produced an empty approval summary", definition.Name)
		}
	}
}
