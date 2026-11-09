package config

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"

// Content budgets size the channels that carry persisted tool content into a
// model prompt (workflow finalization and stage evidence provisioning). They
// are shares of the consuming model's input window, so one deployment never
// starves a long-context model nor overflows a short one. Observation
// envelopes and run accumulation budgets are history indexes and stay fixed.
const (
	// ContentBytesPerToken converts a token share to bytes. Chinese text costs
	// three UTF-8 bytes per rune and roughly one token per rune, so three bytes
	// per token is a near-lower bound for CJK documents and conservative for
	// Latin text. Prompts outside ContextBuilder admission still confirm the
	// rendered request with the model tokenizer before dispatch.
	ContentBytesPerToken = 3
	// FinalEvidenceSharePercent is the input share for completed evidence in
	// a final answer; the remainder covers instructions, the goal and margin.
	FinalEvidenceSharePercent = 60
	// StageEvidenceSharePercent is the input share for evidence provisioned
	// to one workflow step or decision, which also carries tools, history and
	// the newest observations.
	StageEvidenceSharePercent = 30
	// MaxDocumentExtractionBytes bounds one complete document extraction
	// regardless of model capacity; larger text needs a retrieval strategy.
	MaxDocumentExtractionBytes = 2 << 20

	minContentBudgetBytes = 1024
)

// InputTokens is the context window left after reserving the output budget of
// class. It is zero when the profile cannot serve that class.
func (p ModelProfile) InputTokens(class modelcapacity.OutputBudgetClass) int {
	output := p.OutputBudgets[class]
	if p.ContextTokens <= 0 || output <= 0 || output >= p.ContextTokens {
		return 0
	}
	return p.ContextTokens - output
}

// ContentBudgetBytes converts sharePercent of the class input window to bytes.
func (p ModelProfile) ContentBudgetBytes(class modelcapacity.OutputBudgetClass, sharePercent int) int {
	tokens := p.InputTokens(class) * sharePercent / 100
	return max(minContentBudgetBytes, tokens*ContentBytesPerToken)
}

// FinalEvidenceBytes is the completed-evidence budget of a final answer
// rendered on profile.
func (c Config) FinalEvidenceBytes(profile ModelProfile) int {
	return c.Runtime.capContentBudget(profile.ContentBudgetBytes(modelcapacity.OutputAnswer, FinalEvidenceSharePercent))
}

// StageEvidenceBytes is the provisioned-evidence budget of one workflow step
// or decision rendered on profile.
func (c Config) StageEvidenceBytes(profile ModelProfile) int {
	return c.Runtime.capContentBudget(profile.ContentBudgetBytes(modelcapacity.OutputWorkflowStructured, StageEvidenceSharePercent))
}

// DocumentExtractionBytes is the largest complete extraction a document read
// accepts: whatever any finalizing lane can carry, never below floor (the
// smallest extraction the pipeline guarantees) nor above
// MaxDocumentExtractionBytes. The operator evidence ceiling does not apply:
// extraction is not model-visible and stays available to later windows.
func (c Config) DocumentExtractionBytes(floor int) int {
	visible := max(
		c.Model.Fast.ContentBudgetBytes(modelcapacity.OutputAnswer, FinalEvidenceSharePercent),
		c.Model.Deep.ContentBudgetBytes(modelcapacity.OutputAnswer, FinalEvidenceSharePercent),
	)
	return min(MaxDocumentExtractionBytes, max(floor, visible))
}

// capContentBudget applies workflow_stage_evidence_max_bytes, an optional
// operator ceiling; zero derives the budget from model capacity alone.
func (rt RuntimeConfig) capContentBudget(derived int) int {
	if rt.StageEvidenceMaxBytes > 0 && rt.StageEvidenceMaxBytes < derived {
		return rt.StageEvidenceMaxBytes
	}
	return derived
}

// DocumentStructureBytes reserves a configurable share of the Fast evidence
// channel for navigation. It never raises a small model's evidence budget.
func (c Config) DocumentStructureBytes() int {
	share := c.Runtime.DocumentStructureSharePercent
	if share <= 0 || share > 100 {
		share = 25
	}
	return c.StageEvidenceBytes(c.Model.Fast) * share / 100
}
