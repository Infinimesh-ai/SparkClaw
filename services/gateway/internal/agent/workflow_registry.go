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

type workflowDirectoryLimitProfile interface {
	DirectoryLimit() int
}

type workflowDecisionReasonProfile interface {
	DecisionReasonCodes() workflowDecisionReasonCodes
}

type workflowDecisionReasonCodes struct {
	NoMatch  string
	Invalid  string
	Selected string
}

type workflowDirectStageProfile interface {
	DirectStage(*app.WorkflowState) bool
	DirectStageArguments(*app.WorkflowState) map[string]any
}

type workflowAlwaysDirectProfile interface {
	workflowDirectStageProfile
	alwaysDirectWorkflowProfile()
}

type legacyWorkflowProfile struct {
	workflowProfile
	revision int
}

func (profile legacyWorkflowProfile) Revision() int { return profile.revision }

func (profile legacyWorkflowProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent, plan, err := profile.workflowProfile.Resolve(route, sourceTurnID)
	if err != nil {
		return intent, plan, err
	}
	plan.ProfileRevision = profile.revision
	stripWorkflowSupportRequirements(&plan)
	return intent, plan, nil
}

func (profile legacyWorkflowProfile) DirectStage(state *app.WorkflowState) bool {
	direct, ok := profile.workflowProfile.(workflowDirectStageProfile)
	return ok && direct.DirectStage(state)
}

func (profile legacyWorkflowProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	direct, ok := profile.workflowProfile.(workflowDirectStageProfile)
	if !ok {
		return nil
	}
	return direct.DirectStageArguments(state)
}

func (profile legacyWorkflowProfile) DirectoryLimit() int {
	bounded, ok := profile.workflowProfile.(workflowDirectoryLimitProfile)
	if !ok {
		return 0
	}
	return bounded.DirectoryLimit()
}

func (profile legacyWorkflowProfile) DecisionReasonCodes() workflowDecisionReasonCodes {
	semantic, ok := profile.workflowProfile.(workflowDecisionReasonProfile)
	if !ok {
		return workflowDecisionReasonCodes{}
	}
	return semantic.DecisionReasonCodes()
}

func (profile legacyWorkflowProfile) DecisionRules(node app.WorkflowNode) []string {
	semantic, ok := profile.workflowProfile.(workflowDecisionSemantics)
	if !ok {
		return nil
	}
	return semantic.DecisionRules(node)
}

func (profile legacyWorkflowProfile) DecisionResolvedInstruction(entry app.ToolDirectoryEntry) string {
	semantic, ok := profile.workflowProfile.(workflowDecisionSemantics)
	if !ok {
		return ""
	}
	return semantic.DecisionResolvedInstruction(entry)
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
	SemanticVariables    []string                      `json:"semantic_variables"`
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
		conversationAnswerProfileV1{},
		conversationAnswerProfileV2{},
		conversationAnswerProfile{},
		legacyWorkflowProfile{workflowProfile: browserInternetSearchProfile{}, revision: 1},
		browserInternetSearchProfile{},
		legacyWorkflowProfile{workflowProfile: browserWeatherProfile{}, revision: 1},
		legacyWorkflowProfile{workflowProfile: browserWeatherProfile{}, revision: 2},
		browserWeatherProfile{},
		legacyWorkflowProfile{workflowProfile: browserAutomationProfile{}, revision: 2},
		browserAutomationProfile{},
		browserPageReadProfile{},
		legacyWorkflowProfile{workflowProfile: browserInteractionProfile{}, revision: 2},
		browserInteractionProfile{},
		legacyWorkflowProfile{workflowProfile: browserFormDraftProfile{}, revision: 1},
		browserFormDraftProfile{},
		documentReadProfile{},
		legacyWorkflowProfile{workflowProfile: documentEditProfile{}, revision: 6},
		documentEditProfile{},
		localMindReadProfile{},
		localMindWriteProfile{},
		localMindQueryProfile{},
		localMindCancelProfile{},
		legacyWorkflowProfile{workflowProfile: scheduleManageProfile{}, revision: 2},
		scheduleManageProfile{},
		legacyWorkflowProfile{workflowProfile: codingAgentManageProfile{}, revision: 1},
		codingAgentManageProfile{},
	)
}

func workflowProfileDirectoryLimit(profile workflowProfile) int {
	if bounded, ok := profile.(workflowDirectoryLimitProfile); ok {
		if limit := bounded.DirectoryLimit(); limit > 0 && limit <= 32 {
			return limit
		}
	}
	return 32
}

func workflowProfileDecisionReasonCodes(profile workflowProfile) workflowDecisionReasonCodes {
	if semantic, ok := profile.(workflowDecisionReasonProfile); ok {
		codes := semantic.DecisionReasonCodes()
		if codes.NoMatch != "" && codes.Invalid != "" && codes.Selected != "" {
			return codes
		}
	}
	return workflowDecisionReasonCodes{
		NoMatch: "no_registered_editor_matches", Invalid: "edit_operation_selection_invalid", Selected: "edit_operation_selected",
	}
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
	freezeWorkflowSupportRequirements(profile, &plan)
	if err := validateWorkflowPlan(intent, profile, plan); err != nil {
		return resolvedWorkflow{}, err
	}
	return resolvedWorkflow{Profile: profile, Intent: intent, Plan: plan}, nil
}

// workflowProfileAlwaysDirect reports the always-direct marker, unwrapping
// legacyWorkflowProfile: the wrapper embeds the workflowProfile interface, so
// marker methods outside that interface are not promoted and a bare type
// assertion on the wrapper would silently diverge from the wrapped profile.
func workflowProfileAlwaysDirect(profile workflowProfile) bool {
	if legacy, ok := profile.(legacyWorkflowProfile); ok {
		return workflowProfileAlwaysDirect(legacy.workflowProfile)
	}
	_, alwaysDirect := profile.(workflowAlwaysDirectProfile)
	return alwaysDirect
}

func freezeWorkflowSupportRequirements(profile workflowProfile, plan *app.WorkflowPlan) {
	if plan == nil {
		return
	}
	if workflowProfileAlwaysDirect(profile) {
		return
	}
	support := []app.CapabilityRequirement{{Name: app.ToolCapabilityObservationRead}}
	for nodeIndex := range plan.Nodes {
		node := &plan.Nodes[nodeIndex]
		if node.Goal.Completion != app.CompletionEvidence || node.InvocationMode == app.WorkflowInvocationDirectOnce {
			continue
		}
		node.InitialScope.SupportRequirements = append([]app.CapabilityRequirement(nil), support...)
		for transitionIndex := range node.Transitions {
			if node.Transitions[transitionIndex].Replace != nil {
				node.Transitions[transitionIndex].Replace.SupportRequirements = append([]app.CapabilityRequirement(nil), support...)
			}
		}
	}
}

func stripWorkflowSupportRequirements(plan *app.WorkflowPlan) {
	if plan == nil {
		return
	}
	for nodeIndex := range plan.Nodes {
		plan.Nodes[nodeIndex].InitialScope.SupportRequirements = nil
		for transitionIndex := range plan.Nodes[nodeIndex].Transitions {
			if plan.Nodes[nodeIndex].Transitions[transitionIndex].Replace != nil {
				plan.Nodes[nodeIndex].Transitions[transitionIndex].Replace.SupportRequirements = nil
			}
		}
	}
}
