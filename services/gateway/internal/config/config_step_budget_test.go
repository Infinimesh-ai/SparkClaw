package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeRuntimeBudgetConfig(t *testing.T, runtime map[string]any) string {
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

func TestRuntimeBudgetReadsStageAndRunKeys(t *testing.T) {
	cfg, err := Load(writeRuntimeBudgetConfig(t, map[string]any{
		"workflow_stage_max_duration_seconds":    90,
		"workflow_stage_max_no_progress_actions": 5,
		"workflow_run_max_duration_seconds":      600,
		"workflow_run_max_tool_calls":            12,
		"workflow_run_max_observation_bytes":     32000,
		"workflow_run_max_repeated_tool_calls":   4,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.StageMaxDurationSeconds != 90 || cfg.Runtime.StageMaxNoProgressActions != 5 {
		t.Fatalf("workflow_stage_* keys were not applied: %#v", cfg.Runtime)
	}
	if cfg.Runtime.RunMaxDurationSeconds != 600 || cfg.Runtime.RunMaxToolCalls != 12 ||
		cfg.Runtime.RunMaxObservationBytes != 32000 || cfg.Runtime.RunMaxRepeatedToolCalls != 4 {
		t.Fatalf("workflow_run_* keys were not applied: %#v", cfg.Runtime)
	}
}

func TestRuntimeBudgetKeepsDefaultsForUnsetKeys(t *testing.T) {
	cfg, err := Load(writeRuntimeBudgetConfig(t, map[string]any{
		"workflow_stage_max_duration_seconds": 90,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defaults := Default().Runtime
	if cfg.Runtime.StageMaxNoProgressActions != defaults.StageMaxNoProgressActions ||
		cfg.Runtime.RunMaxDurationSeconds != defaults.RunMaxDurationSeconds ||
		cfg.Runtime.RunMaxToolCalls != defaults.RunMaxToolCalls ||
		cfg.Runtime.RunMaxObservationBytes != defaults.RunMaxObservationBytes ||
		cfg.Runtime.RunMaxRepeatedToolCalls != defaults.RunMaxRepeatedToolCalls {
		t.Fatalf("unset budgets should keep defaults: %#v", cfg.Runtime)
	}
}

func TestRuntimeBudgetFallsBackToDeprecatedWorkflowStepKeys(t *testing.T) {
	cfg, err := Load(writeRuntimeBudgetConfig(t, map[string]any{
		"workflow_step_max_duration_seconds":    90,
		"workflow_step_max_tool_calls":          5,
		"workflow_step_max_observation_bytes":   24000,
		"workflow_step_max_no_progress_actions": 4,
		"workflow_step_max_repeated_tool_calls": 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.StageMaxDurationSeconds != 90 || cfg.Runtime.StageMaxNoProgressActions != 4 {
		t.Fatalf("workflow_step_* stage fallbacks were not applied: %#v", cfg.Runtime)
	}
	if cfg.Runtime.RunMaxToolCalls != 5 || cfg.Runtime.RunMaxObservationBytes != 24000 || cfg.Runtime.RunMaxRepeatedToolCalls != 2 {
		t.Fatalf("workflow_step_* run fallbacks were not applied: %#v", cfg.Runtime)
	}
	if cfg.Runtime.RunMaxDurationSeconds != Default().Runtime.RunMaxDurationSeconds {
		t.Fatalf("the deprecated stage duration must not shrink the run duration: %#v", cfg.Runtime)
	}
}

func TestRuntimeBudgetFallsBackToLegacyReactKeys(t *testing.T) {
	cfg, err := Load(writeRuntimeBudgetConfig(t, map[string]any{
		"react_max_duration_seconds":   240,
		"react_max_tool_calls":         9,
		"react_max_observation_bytes":  1234,
		"workflow_step_max_tool_calls": 7,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.StageMaxDurationSeconds != 240 || cfg.Runtime.RunMaxObservationBytes != 1234 {
		t.Fatalf("legacy react_max_* fallback was not applied: %#v", cfg.Runtime)
	}
	if cfg.Runtime.RunMaxToolCalls != 7 {
		t.Fatalf("workflow_step_* keys must win over react_* keys: %#v", cfg.Runtime)
	}
}

func TestRuntimeBudgetNewKeysWinOverDeprecatedKeys(t *testing.T) {
	cfg, err := Load(writeRuntimeBudgetConfig(t, map[string]any{
		"workflow_stage_max_duration_seconds": 75,
		"workflow_step_max_duration_seconds":  90,
		"react_max_duration_seconds":          240,
		"workflow_run_max_tool_calls":         20,
		"workflow_step_max_tool_calls":        5,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.StageMaxDurationSeconds != 75 {
		t.Fatalf("workflow_stage_* keys must win over deprecated keys: %#v", cfg.Runtime)
	}
	if cfg.Runtime.RunMaxToolCalls != 20 {
		t.Fatalf("workflow_run_* keys must win over deprecated keys: %#v", cfg.Runtime)
	}
}

func TestRuntimeObservationBytesEnvOverrideChain(t *testing.T) {
	t.Setenv("SPARKCLAW_REACT_MAX_OBSERVATION_BYTES", "1000")
	cfg, err := Load(writeRuntimeBudgetConfig(t, map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.RunMaxObservationBytes != 1000 {
		t.Fatalf("legacy react env override was not applied: %#v", cfg.Runtime)
	}

	t.Setenv("SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES", "2000")
	cfg, err = Load(writeRuntimeBudgetConfig(t, map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.RunMaxObservationBytes != 2000 {
		t.Fatalf("deprecated step env override must win over react: %#v", cfg.Runtime)
	}

	t.Setenv("SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES", "3000")
	cfg, err = Load(writeRuntimeBudgetConfig(t, map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.RunMaxObservationBytes != 3000 {
		t.Fatalf("new run env override must win: %#v", cfg.Runtime)
	}
}
