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

type workflowProfile interface {
	ID() app.WorkflowID
	Revision() int
	Capability() app.CapabilityID
	RoutingSemantics() workflowRoutingSemantics
	Finalization() workflowFinalizationMode
	Resolve(app.RouteDecision, string) (app.IntentEnvelope, app.WorkflowPlan, error)
	Prepare(*app.WorkflowState) (workflowPreparation, error)
	Assess(*app.WorkflowState, app.ToolOutcome) app.NodeAssessment
	Hint(*app.WorkflowState) workflowExecutionHint
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

type workflowFinalizationMode string

const (
	workflowFinalizationGrounded workflowFinalizationMode = "grounded"
	workflowFinalizationModel    workflowFinalizationMode = "model"
)

type workflowRoutingSemantics = semanticrouting.WorkflowSemantics
type workflowRoutingVariant = semanticrouting.IntentVariant
type workflowRouteTemplate = semanticrouting.RouteTemplate

type workflowExecutionHint struct {
	TaskType             string
	EvidenceNeed         string
	DataScope            string
	ToolMode             string
	BrowserMode          string
	RequiresToolEvidence bool
	EstimatedRisk        app.RiskLevel
	ModelLaneHint        string
	Reason               string
	WorkflowID           app.WorkflowID
	WorkflowNodeID       app.WorkflowNodeID
	ScopeRevision        int
	Capability           string
}

func (hint workflowExecutionHint) taskHint() TaskHint {
	return TaskHint{
		TaskType:             hint.TaskType,
		EvidenceNeed:         hint.EvidenceNeed,
		DataScope:            hint.DataScope,
		ToolMode:             hint.ToolMode,
		BrowserMode:          hint.BrowserMode,
		RequiresToolEvidence: hint.RequiresToolEvidence,
		EstimatedRisk:        string(hint.EstimatedRisk),
		ModelLaneHint:        hint.ModelLaneHint,
		Reason:               hint.Reason,
		WorkflowID:           hint.WorkflowID,
		WorkflowNodeID:       hint.WorkflowNodeID,
		ScopeRevision:        hint.ScopeRevision,
		Capability:           hint.Capability,
	}
}

type workflowProfileRegistry struct {
	byCapability map[app.CapabilityID]workflowProfile
	byID         map[app.WorkflowID]workflowProfile
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
	}
	for _, profile := range profiles {
		if profile == nil || profile.ID() == "" || profile.Revision() <= 0 || profile.Capability() == "" || profile.Finalization() == "" {
			panic("workflow profile registration is incomplete")
		}
		if _, exists := registry.byID[profile.ID()]; exists {
			panic("duplicate workflow profile registration: " + string(profile.ID()))
		}
		if _, exists := registry.byCapability[profile.Capability()]; exists {
			panic("duplicate workflow capability registration: " + string(profile.Capability()))
		}
		registry.byID[profile.ID()] = profile
		registry.byCapability[profile.Capability()] = profile
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

func (r workflowProfileRegistry) Get(id app.WorkflowID) (workflowProfile, error) {
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
