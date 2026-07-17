package agent

import (
	"errors"
	"fmt"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type workflowProfile interface {
	ID() app.WorkflowID
	Revision() int
	Recognize(sourceTurnID, content string) (app.IntentEnvelope, bool)
	Match(app.IntentEnvelope) bool
	Resolve(app.IntentEnvelope) (app.WorkflowPlan, error)
	Assess(*app.WorkflowState, app.ToolOutcome) app.NodeAssessment
	Hint(*app.WorkflowState) workflowExecutionHint
	TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string
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

func (r workflowProfileRegistry) Route(intent app.IntentEnvelope) (workflowProfile, bool, error) {
	var matched workflowProfile
	for _, profile := range r.ordered {
		if !profile.Match(intent) {
			continue
		}
		if matched != nil {
			return nil, true, fmt.Errorf("stable intent matches ambiguous workflow profiles %q and %q", matched.ID(), profile.ID())
		}
		matched = profile
	}
	return matched, matched != nil, nil
}

type workflowProfileRegistry struct {
	ordered []workflowProfile
	byID    map[app.WorkflowID]workflowProfile
}

type recognizedWorkflow struct {
	Profile workflowProfile
	Intent  app.IntentEnvelope
}

func newWorkflowProfileRegistry(profiles ...workflowProfile) workflowProfileRegistry {
	registry := workflowProfileRegistry{
		ordered: append([]workflowProfile(nil), profiles...),
		byID:    make(map[app.WorkflowID]workflowProfile, len(profiles)),
	}
	for _, profile := range profiles {
		if profile == nil || profile.ID() == "" || profile.Revision() <= 0 {
			panic("workflow profile registration is incomplete")
		}
		if _, exists := registry.byID[profile.ID()]; exists {
			panic("duplicate workflow profile registration: " + string(profile.ID()))
		}
		registry.byID[profile.ID()] = profile
	}
	return registry
}

func defaultWorkflowProfileRegistry() workflowProfileRegistry {
	return newWorkflowProfileRegistry(
		webPublicResearchProfile{},
		webExplicitURLProfile{},
		workspaceFileSearchProfile{},
		workspaceFileReadProfile{},
	)
}

func (r workflowProfileRegistry) Recognize(sourceTurnID, content string) (recognizedWorkflow, bool, error) {
	var matched recognizedWorkflow
	for _, profile := range r.ordered {
		intent, ok := profile.Recognize(sourceTurnID, content)
		if !ok {
			continue
		}
		if matched.Profile != nil {
			return recognizedWorkflow{}, true, fmt.Errorf("ambiguous workflow profiles %q and %q", matched.Profile.ID(), profile.ID())
		}
		matched = recognizedWorkflow{Profile: profile, Intent: intent}
	}
	return matched, matched.Profile != nil, nil
}

func (r workflowProfileRegistry) Get(id app.WorkflowID) (workflowProfile, error) {
	profile, ok := r.byID[id]
	if !ok {
		return nil, errors.New("persisted workflow profile is not registered: " + string(id))
	}
	return profile, nil
}

func (r workflowProfileRegistry) Resolve(recognized recognizedWorkflow) (workflowProfile, app.WorkflowPlan, error) {
	if recognized.Profile == nil {
		return nil, app.WorkflowPlan{}, errors.New("recognized workflow has no profile")
	}
	profile, err := r.Get(recognized.Profile.ID())
	if err != nil {
		return nil, app.WorkflowPlan{}, err
	}
	plan, err := profile.Resolve(recognized.Intent)
	if err != nil {
		return nil, app.WorkflowPlan{}, err
	}
	if err := validateWorkflowPlan(recognized.Intent, profile, plan); err != nil {
		return nil, app.WorkflowPlan{}, err
	}
	return profile, plan, nil
}
