package agent

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

const workflowExecutionModelLane = "deep"

type workflowProfile interface {
	ID() app.WorkflowID
	Revision() int
	Capability() app.CapabilityID
	Recognize(workflowRecognitionContext) (workflowRecognition, bool)
	Resolve(app.RouteDecision, string) (app.IntentEnvelope, app.WorkflowPlan, error)
	Prepare(*app.WorkflowState) (app.TransitionID, bool, error)
	Assess(*app.WorkflowState, app.ToolOutcome) app.NodeAssessment
	Hint(*app.WorkflowState) workflowExecutionHint
	TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string
}

type workflowRecognitionContext struct {
	SourceTurnID  string
	Content       string
	Resources     []app.MessagePart
	Snapshot      agentContextSnapshot
	WorkspaceRoot string
}

type workflowRecognition struct {
	Status     app.RouteStatus
	Slots      app.RouteSlots
	Facts      map[string]string
	Confidence float64
	Reason     string
}

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
		if profile == nil || profile.ID() == "" || profile.Revision() <= 0 || profile.Capability() == "" {
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
		browserInternetSearchProfile{},
		browserWeatherProfile{},
		browserAutomationProfile{},
		browserInteractionProfile{},
		documentReadProfile{},
		documentEditProfile{},
	)
}

func (r workflowProfileRegistry) Get(id app.WorkflowID) (workflowProfile, error) {
	profile, ok := r.byID[id]
	if !ok {
		return nil, errors.New("persisted workflow profile is not registered: " + string(id))
	}
	return profile, nil
}

func (r workflowProfileRegistry) Recognize(catalog capability.Catalog, input workflowRecognitionContext) (app.RouteDecision, error) {
	matches := make([]app.RouteDecision, 0, 1)
	profileIDs := make([]string, 0, len(r.byID))
	for id := range r.byID {
		profileIDs = append(profileIDs, string(id))
	}
	sort.Strings(profileIDs)
	for _, profileID := range profileIDs {
		profile := r.byID[app.WorkflowID(profileID)]
		recognition, ok := profile.Recognize(input)
		if !ok {
			continue
		}
		path, err := catalog.PathTo(profile.Capability())
		if err != nil {
			return app.RouteDecision{}, fmt.Errorf("registered workflow %q has no catalog leaf: %w", profile.ID(), err)
		}
		status := recognition.Status
		if status == "" {
			status = app.RouteMatched
		}
		decision := app.RouteDecision{
			SchemaVersion: app.RouteDecisionSchemaVersion, Status: status, CatalogRevision: catalog.Revision(),
			CapabilityPath: path, Slots: recognition.Slots, Facts: cloneStringMap(recognition.Facts),
			Confidence: recognition.Confidence, Reason: recognition.Reason,
		}
		if err := catalog.ValidateDecision(decision); err != nil {
			return app.RouteDecision{}, fmt.Errorf("workflow %q recognition is invalid: %w", profile.ID(), err)
		}
		matches = append(matches, decision)
	}
	if len(matches) == 0 {
		return app.RouteDecision{
			SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteUnmatched, CatalogRevision: catalog.Revision(),
			Confidence: 0.8, Reason: "No registered capability profile matched the request.",
		}, nil
	}
	if len(matches) > 1 {
		return app.RouteDecision{
			SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteClarify, CatalogRevision: catalog.Revision(),
			Confidence: 0.5, Reason: "More than one registered capability profile matched the request.",
		}, nil
	}
	return matches[0], nil
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
