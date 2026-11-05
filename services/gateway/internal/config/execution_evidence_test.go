package config

import "testing"

func TestExecutionEvidenceDefaultsAndBounds(t *testing.T) {
	cfg := Default().Storage.ExecutionEvidence
	if cfg.Enabled {
		t.Fatal("evaluation must be opt-in")
	}
	if err := normalizeExecutionEvidenceConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ExecutionEvidenceConfig){
		func(c *ExecutionEvidenceConfig) { c.MaxPayloadBytes = -1 },
		func(c *ExecutionEvidenceConfig) { c.MaxRunBytes = c.MaxPayloadBytes - 1 },
		func(c *ExecutionEvidenceConfig) { c.MaxTotalBytes = c.MaxRunBytes - 1 },
		func(c *ExecutionEvidenceConfig) { c.MaxEvents = -1 },
		func(c *ExecutionEvidenceConfig) { c.OperationTimeoutSeconds = 301 },
		func(c *ExecutionEvidenceConfig) { c.ExportTimeoutSeconds = 3601 },
	} {
		invalid := cfg
		change(&invalid)
		if err := normalizeExecutionEvidenceConfig(&invalid); err == nil {
			t.Fatal("unsafe evidence bounds accepted")
		}
	}
	var zero ExecutionEvidenceConfig
	if err := normalizeExecutionEvidenceConfig(&zero); err != nil || zero != cfg {
		t.Fatalf("zero default normalization: %+v %v", zero, err)
	}
}
