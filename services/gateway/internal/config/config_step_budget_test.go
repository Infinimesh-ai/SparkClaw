package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeStepBudgetConfig(t *testing.T, runtime map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"runtime": runtime})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRuntimeStepBudgetReadsWorkflowStepKeys(t *testing.T) {
	cfg, err := Load(writeStepBudgetConfig(t, map[string]any{
		"workflow_step_max_duration_seconds": 90,
		"workflow_step_max_tool_calls":       5,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.StepMaxDurationSeconds != 90 || cfg.Runtime.StepMaxToolCalls != 5 {
		t.Fatalf("workflow_step_* keys were not applied: %#v", cfg.Runtime)
	}
	if cfg.Runtime.StepMaxObservationBytes != 48000 || cfg.Runtime.StepMaxNoProgressActions != 3 || cfg.Runtime.StepMaxRepeatedToolCalls != 3 {
		t.Fatalf("unset budgets should keep defaults: %#v", cfg.Runtime)
	}
}

func TestRuntimeStepBudgetFallsBackToLegacyReactKeys(t *testing.T) {
	cfg, err := Load(writeStepBudgetConfig(t, map[string]any{
		"react_max_duration_seconds":   240,
		"react_max_tool_calls":         9,
		"react_max_observation_bytes":  1234,
		"workflow_step_max_tool_calls": 7,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.StepMaxDurationSeconds != 240 || cfg.Runtime.StepMaxObservationBytes != 1234 {
		t.Fatalf("legacy react_max_* fallback was not applied: %#v", cfg.Runtime)
	}
	if cfg.Runtime.StepMaxToolCalls != 7 {
		t.Fatalf("workflow_step_* keys must win over legacy keys: %#v", cfg.Runtime)
	}
}
