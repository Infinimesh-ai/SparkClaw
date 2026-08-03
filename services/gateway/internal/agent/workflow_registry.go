package agent

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

const workflowExecutionModelLane = "deep"
const documentWorkflowModelLane = "fast"

func workflowModelLaneForProfile(profileID app.WorkflowID) string {
	switch profileID {
	case app.WorkflowDocumentRead, app.WorkflowDocumentEdit:
		return documentWorkflowModelLane
	default:
		return workflowExecutionModelLane
	}
}

type workflowProfile interface {
	ID() app.WorkflowID
	Revision() int
	Capability() app.CapabilityID
	RoutingSemantics() workflowRoutingSemantics
	Finalization() workflowFinalizationMode
	Resolve(app.RouteDecision, string) (app.IntentEnvelope, app.WorkflowPlan, error)
	Prepare(*app.WorkflowState) (workflowPreparation, error)
	Assess(*app.WorkflowState, app.ToolOutcome) app.NodeAssessment
	StageContext(*app.WorkflowState) workflowStageContext
	TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string
}

type workflowPreparation struct {
	TransitionID app.TransitionID
	CompleteNode bool
	OutcomeRefs  []app.ResourceRef
}

type workflowDecisionSemantics interface {
	DecisionRules(app.WorkflowNode) []string
	DecisionResolvedInstruction(app.ToolDirectoryEntry) string
}

type workflowDirectStageProfile interface {
	DirectStage(*app.WorkflowState) bool
	DirectStageArguments(*app.WorkflowState) map[string]any
}

type workflowFinalizationMode string

const (
	workflowFinalizationGrounded workflowFinalizationMode = "grounded"
	workflowFinalizationModel    workflowFinalizationMode = "model"
)

type workflowRoutingSemantics = semanticrouting.WorkflowSemantics
type workflowRoutingVariant = semanticrouting.IntentVariant
type workflowRouteTemplate = semanticrouting.RouteTemplate

type workflowStageContext struct {
	TaskType             string                        `json:"task_type"`
	EvidenceNeed         string                        `json:"evidence_need"`
	DataScope            string                        `json:"data_scope,omitempty"`
	ToolMode             string                        `json:"tool_mode"`
	BrowserMode          string                        `json:"browser_mode,omitempty"`
	RequiresToolEvidence bool                          `json:"requires_tool_evidence,omitempty"`
	EstimatedRisk        app.RiskLevel                 `json:"estimated_risk"`
	ModelLaneHint        string                        `json:"model_lane_hint"`
	Reason               string                        `json:"reason"`
	WorkflowID           app.WorkflowID                `json:"-"`
	WorkflowNodeID       app.WorkflowNodeID            `json:"-"`
	ScopeRevision        int                           `json:"-"`
	Capability           string                        `json:"-"`
	EvidenceRequirements []workflowEvidenceRequirement `json:"-"`
}

type workflowEvidenceSliceMode string

const (
	workflowEvidenceHead       workflowEvidenceSliceMode = "head"
	workflowEvidenceStructured workflowEvidenceSliceMode = "structured"
)

type workflowEvidenceRequirement struct {
	SourceNodeID app.WorkflowNodeID
	ResourceKind string
	Mode         workflowEvidenceSliceMode
	MaxBytes     int
	Optional     bool
}

type workflowProfileRegistry struct {
	byCapability map[app.CapabilityID]workflowProfile
	byID         map[app.WorkflowID]workflowProfile
	byContract   map[workflowProfileKey]workflowProfile
}

type workflowProfileKey struct {
	ID       app.WorkflowID
	Revision int
}

type resolvedWorkflow struct {
	Profile workflowProfile
	Intent  app.IntentEnvelope
	Plan    app.WorkflowPlan
}

func newWorkflowProfileRegistry(profiles ...workflowProfile) workflowProfileRegistry {
	registry := workflowProfileRegistry{
		byCapability: make(map[app.CapabilityID]workflowProfile, len(profiles)),
		byID:         make(map[app.WorkflowID]workflowProfile, len(profiles)),
		byContract:   make(map[workflowProfileKey]workflowProfile, len(profiles)),
	}
	for _, profile := range profiles {
		if profile == nil || profile.ID() == "" || profile.Revision() <= 0 || profile.Capability() == "" || profile.Finalization() == "" {
			panic("workflow profile registration is incomplete")
		}
		key := workflowProfileKey{ID: profile.ID(), Revision: profile.Revision()}
		if _, exists := registry.byContract[key]; exists {
			panic(fmt.Sprintf("duplicate workflow profile registration: %s r%d", profile.ID(), profile.Revision()))
		}
		registry.byContract[key] = profile
		current := registry.byID[profile.ID()]
		if current == nil || profile.Revision() > current.Revision() {
			if byCapability := registry.byCapability[profile.Capability()]; byCapability != nil && byCapability.ID() != profile.ID() {
				panic("duplicate workflow capability registration: " + string(profile.Capability()))
			}
			registry.byID[profile.ID()] = profile
			registry.byCapability[profile.Capability()] = profile
		}
	}
	return registry
}

func defaultWorkflowProfileRegistry() workflowProfileRegistry {
	return newWorkflowProfileRegistry(
		conversationAnswerProfile{},
		browserInternetSearchProfile{},
		browserWeatherProfile{},
		browserAutomationProfile{},
		browserInteractionProfile{},
		documentReadProfile{},
		documentEditProfile{},
		scheduleManageProfile{},
	)
}

func (r workflowProfileRegistry) Get(id app.WorkflowID, revision ...int) (workflowProfile, error) {
	if len(revision) > 1 {
		return nil, errors.New("workflow profile lookup accepts at most one revision")
	}
	if len(revision) == 1 {
		profile, ok := r.byContract[workflowProfileKey{ID: id, Revision: revision[0]}]
		if !ok {
			return nil, fmt.Errorf("persisted workflow profile is not registered: %s r%d", id, revision[0])
		}
		return profile, nil
	}
	profile, ok := r.byID[id]
	if !ok {
		return nil, errors.New("persisted workflow profile is not registered: " + string(id))
	}
	return profile, nil
}

func (r workflowProfileRegistry) SemanticGraph(catalog capability.Catalog) (*semanticrouting.Graph, error) {
	registrations := make([]semanticrouting.Registration, 0, len(r.byID))
	for _, profile := range r.sortedProfiles() {
		registrations = append(registrations, semanticrouting.Registration{
			Capability: profile.Capability(),
			Workflow: app.WorkflowContractRef{
				ID: profile.ID(), Revision: profile.Revision(),
			},
			Semantics: profile.RoutingSemantics(),
		})
	}
	return semanticrouting.Compile(catalog, registrations)
}

func (r workflowProfileRegistry) sortedProfiles() []workflowProfile {
	profiles := make([]workflowProfile, 0, len(r.byID))
	for _, profile := range r.byID {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].ID() < profiles[j].ID()
	})
	return profiles
}

func (r workflowProfileRegistry) Resolve(catalog capability.Catalog, decision app.RouteDecision, sourceTurnID string) (resolvedWorkflow, error) {
	if err := catalog.ValidateDecision(decision); err != nil {
		return resolvedWorkflow{}, err
	}
	leafID, err := routeLeaf(decision)
	if err != nil {
		return resolvedWorkflow{}, err
	}
	leaf, err := catalog.ResolveLeaf(decision.CapabilityPath)
	if err != nil || leaf.Workflow == nil {
		return resolvedWorkflow{}, errors.New("matched capability leaf has no workflow contract")
	}
	profile, ok := r.byCapability[leafID]
	if !ok {
		return resolvedWorkflow{}, fmt.Errorf("capability %q has no registered workflow", leafID)
	}
	if leaf.Workflow.ID != profile.ID() || leaf.Workflow.Revision != profile.Revision() {
		return resolvedWorkflow{}, fmt.Errorf("capability %q workflow contract does not match its registry entry", leafID)
	}
	intent, plan, err := profile.Resolve(decision, sourceTurnID)
	if err != nil {
		return resolvedWorkflow{}, err
	}
	if err := validateWorkflowPlan(intent, profile, plan); err != nil {
		return resolvedWorkflow{}, err
	}
	return resolvedWorkflow{Profile: profile, Intent: intent, Plan: plan}, nil
}
