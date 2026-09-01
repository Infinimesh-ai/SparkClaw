package modelcapacity

import (
	"fmt"
	"slices"
	"strings"
)

type Lane string

const (
	LaneFast      Lane = "fast"
	LaneDeep      Lane = "deep"
	LaneEmbedding Lane = "embedding"
	LaneGuard     Lane = "guard"
	LaneOCR       Lane = "ocr"
)

type OutputBudgetClass string

const (
	OutputGuard              OutputBudgetClass = "guard"
	OutputCompactStructured  OutputBudgetClass = "compact_structured"
	OutputWorkflowStructured OutputBudgetClass = "workflow_structured"
	OutputAnswer             OutputBudgetClass = "answer"
	OutputVisionStructured   OutputBudgetClass = "vision_structured"
	OutputOCRDocument        OutputBudgetClass = "ocr_document"
)

type Operation string

const (
	OperationGuardModeration        Operation = "guard.moderation"
	OperationIntentCatalogEmbedding Operation = "intent.catalog_embedding"
	OperationIntentQueryEmbedding   Operation = "intent.query_embedding"
	OperationIntentTreeScore        Operation = "intent.tree_score"
	OperationIntentTreeRepair       Operation = "intent.tree_repair"
	OperationWorkflowStep           Operation = "workflow.step"
	OperationWorkflowDecision       Operation = "workflow.decision"
	OperationConversationAnswer     Operation = "conversation.answer"
	OperationWorkflowFinalAnswer    Operation = "workflow.final_answer"
	OperationDirectChat             Operation = "gateway.direct_chat"
	OperationImageInspect           Operation = "image.inspect"
	OperationDocumentImageEnrich    Operation = "document.image_enrichment"
	OperationPPTXVisualAssessment   Operation = "pptx.visual_assessment"
	OperationPPTXVisualRepairPlan   Operation = "pptx.visual_repair_plan"
	OperationDocumentOCR            Operation = "document.ocr"
)

type OperationSpec struct {
	Operation    Operation
	OutputClass  OutputBudgetClass
	AllowedLanes []Lane
	Generates    bool
}

var operationSpecs = []OperationSpec{
	{Operation: OperationGuardModeration, OutputClass: OutputGuard, AllowedLanes: []Lane{LaneGuard}, Generates: true},
	{Operation: OperationIntentCatalogEmbedding, AllowedLanes: []Lane{LaneEmbedding}},
	{Operation: OperationIntentQueryEmbedding, AllowedLanes: []Lane{LaneEmbedding}},
	{Operation: OperationIntentTreeScore, OutputClass: OutputCompactStructured, AllowedLanes: []Lane{LaneFast}, Generates: true},
	{Operation: OperationIntentTreeRepair, OutputClass: OutputCompactStructured, AllowedLanes: []Lane{LaneFast}, Generates: true},
	{Operation: OperationWorkflowStep, OutputClass: OutputWorkflowStructured, AllowedLanes: []Lane{LaneFast, LaneDeep}, Generates: true},
	{Operation: OperationWorkflowDecision, OutputClass: OutputWorkflowStructured, AllowedLanes: []Lane{LaneFast, LaneDeep}, Generates: true},
	{Operation: OperationConversationAnswer, OutputClass: OutputAnswer, AllowedLanes: []Lane{LaneFast, LaneDeep}, Generates: true},
	{Operation: OperationWorkflowFinalAnswer, OutputClass: OutputAnswer, AllowedLanes: []Lane{LaneFast, LaneDeep}, Generates: true},
	{Operation: OperationDirectChat, OutputClass: OutputAnswer, AllowedLanes: []Lane{LaneFast, LaneDeep}, Generates: true},
	{Operation: OperationImageInspect, OutputClass: OutputVisionStructured, AllowedLanes: []Lane{LaneFast}, Generates: true},
	{Operation: OperationDocumentImageEnrich, OutputClass: OutputVisionStructured, AllowedLanes: []Lane{LaneFast}, Generates: true},
	{Operation: OperationPPTXVisualAssessment, OutputClass: OutputVisionStructured, AllowedLanes: []Lane{LaneFast}, Generates: true},
	{Operation: OperationPPTXVisualRepairPlan, OutputClass: OutputVisionStructured, AllowedLanes: []Lane{LaneFast}, Generates: true},
	{Operation: OperationDocumentOCR, OutputClass: OutputOCRDocument, AllowedLanes: []Lane{LaneOCR}, Generates: true},
}

func Spec(operation Operation) (OperationSpec, error) {
	for _, spec := range operationSpecs {
		if spec.Operation == operation {
			spec.AllowedLanes = slices.Clone(spec.AllowedLanes)
			return spec, nil
		}
	}
	return OperationSpec{}, fmt.Errorf("unknown model operation %q", operation)
}

func Operations() []OperationSpec {
	out := make([]OperationSpec, len(operationSpecs))
	for index, spec := range operationSpecs {
		spec.AllowedLanes = slices.Clone(spec.AllowedLanes)
		out[index] = spec
	}
	return out
}

func Lanes() []Lane {
	return []Lane{LaneFast, LaneDeep, LaneEmbedding, LaneGuard, LaneOCR}
}

func Classes() []OutputBudgetClass {
	return []OutputBudgetClass{
		OutputGuard,
		OutputCompactStructured,
		OutputWorkflowStructured,
		OutputAnswer,
		OutputVisionStructured,
		OutputOCRDocument,
	}
}

func RequiredClasses(lane Lane) []OutputBudgetClass {
	seen := map[OutputBudgetClass]bool{}
	out := []OutputBudgetClass{}
	for _, spec := range operationSpecs {
		if !spec.Generates || !slices.Contains(spec.AllowedLanes, lane) || seen[spec.OutputClass] {
			continue
		}
		seen[spec.OutputClass] = true
		out = append(out, spec.OutputClass)
	}
	slices.Sort(out)
	return out
}

func IsKnownLane(value string) bool {
	value = strings.TrimSpace(value)
	return slices.Contains(Lanes(), Lane(value))
}

func IsKnownClass(value string) bool {
	value = strings.TrimSpace(value)
	return slices.Contains(Classes(), OutputBudgetClass(value))
}

func Allows(spec OperationSpec, lane Lane) bool {
	return slices.Contains(spec.AllowedLanes, lane)
}
