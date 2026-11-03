package config

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
)

func contextBudgetProfile(contextTokens, answer, structured int) ModelProfile {
	return ModelProfile{ContextTokens: contextTokens, OutputBudgets: map[modelcapacity.OutputBudgetClass]int{
		modelcapacity.OutputAnswer: answer, modelcapacity.OutputWorkflowStructured: structured,
	}}
}

func TestContentBudgetsFollowModelInputWindow(t *testing.T) {
	cfg := Default()
	for _, test := range []struct {
		name         string
		profile      ModelProfile
		final, stage int
	}{
		{name: "hosted 256K fast", profile: contextBudgetProfile(262144, 8192, 8192), final: 457113, stage: 228555},
		{name: "local 32K fast", profile: contextBudgetProfile(32768, 4096, 4096), final: 51609, stage: 25803},
		{name: "local 64K deep", profile: contextBudgetProfile(65536, 8192, 8192), final: 103218, stage: 51609},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := cfg.FinalEvidenceBytes(test.profile); got != test.final {
				t.Fatalf("final evidence = %d, want %d", got, test.final)
			}
			if got := cfg.StageEvidenceBytes(test.profile); got != test.stage {
				t.Fatalf("stage evidence = %d, want %d", got, test.stage)
			}
		})
	}
}

func TestContentBudgetOperatorCeilingOnlyLowers(t *testing.T) {
	cfg := Default()
	profile := contextBudgetProfile(262144, 8192, 8192)
	cfg.Runtime.StageEvidenceMaxBytes = 200000
	if got := cfg.FinalEvidenceBytes(profile); got != 200000 {
		t.Fatalf("operator ceiling was not applied to final evidence: %d", got)
	}
	small := contextBudgetProfile(32768, 4096, 4096)
	if got := cfg.FinalEvidenceBytes(small); got != 51609 {
		t.Fatalf("operator ceiling raised a short-context budget above its window: %d", got)
	}
}

func TestContentBudgetWithoutServableClassKeepsMinimum(t *testing.T) {
	profile := ModelProfile{ContextTokens: 4096}
	if got := profile.ContentBudgetBytes(modelcapacity.OutputAnswer, FinalEvidenceSharePercent); got != minContentBudgetBytes {
		t.Fatalf("unservable class budget = %d", got)
	}
}

func TestDocumentExtractionBytesUsesLargestFinalizerWithinBounds(t *testing.T) {
	cfg := Default()
	cfg.Runtime.StageEvidenceMaxBytes = 8000
	cfg.Model.Fast = contextBudgetProfile(32768, 4096, 4096)
	cfg.Model.Deep = contextBudgetProfile(65536, 8192, 8192)
	if got := cfg.DocumentExtractionBytes(200000); got != 200000 {
		t.Fatalf("short-context extraction fell below the small-document floor: %d", got)
	}
	cfg.Model.Fast = contextBudgetProfile(262144, 8192, 8192)
	if got := cfg.DocumentExtractionBytes(200000); got != 457113 {
		t.Fatalf("extraction did not follow the long-context finalizer: %d", got)
	}
	cfg.Model.Fast = contextBudgetProfile(2097152, 8192, 8192)
	if got := cfg.DocumentExtractionBytes(200000); got != MaxDocumentExtractionBytes {
		t.Fatalf("extraction exceeded the hard ceiling: %d", got)
	}
}
