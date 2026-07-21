package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func validateWorkflowPlan(intent app.IntentEnvelope, profile workflowProfile, plan app.WorkflowPlan) error {
	if profile == nil || plan.ProfileID != profile.ID() || plan.ProfileRevision != profile.Revision() {
		return errors.New("workflow plan identity does not match its registered profile")
	}
	if intent.Version <= 0 || intent.SourceTurnID == "" || intent.Resolution.Status != app.IntentResolved || len(intent.Objectives) == 0 {
		return errors.New("workflow plan requires a resolved stable intent")
	}
	if plan.SchemaVersion <= 0 || plan.Completion == "" || len(plan.Nodes) == 0 || len(plan.InitialNodeIDs) == 0 {
		return errors.New("workflow plan contract is incomplete")
	}
	if plan.ResultProjection != "" && plan.ResultProjection != app.WorkflowResultTextAndOutputs && plan.ResultProjection != app.WorkflowResultOutputsOnly {
		return errors.New("workflow plan has an unsupported result projection")
	}

	objectives := make(map[string]bool, len(intent.Objectives))
	for _, objective := range intent.Objectives {
		if objective.ID == "" || objectives[objective.ID] {
			return errors.New("stable intent objective IDs must be non-empty and unique")
		}
		objectives[objective.ID] = true
	}
	nodes := make(map[app.WorkflowNodeID]app.WorkflowNode, len(plan.Nodes))
	for _, node := range plan.Nodes {
		if node.ID == "" || strings.TrimSpace(node.InitialStage) == "" || node.MaxAttempts <= 0 || len(node.Goal.ObjectiveIDs) == 0 || node.Goal.Completion == "" || len(node.AllowedRisks) == 0 {
			return fmt.Errorf("workflow node %q contract is incomplete", node.ID)
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("workflow node ID %q is duplicated", node.ID)
		}
		allowEmptyInitialScope := false
		for _, transition := range node.Transitions {
			allowEmptyInitialScope = allowEmptyInitialScope || transition.Deterministic
		}
		if err := validateCapabilityScope(node.InitialScope, allowEmptyInitialScope); err != nil {
			return fmt.Errorf("workflow node %q initial scope: %w", node.ID, err)
		}
		for _, objectiveID := range node.Goal.ObjectiveIDs {
			if !objectives[objectiveID] {
				return fmt.Errorf("workflow node %q references unknown objective %q", node.ID, objectiveID)
			}
		}
		if err := validateNodeTransitions(node); err != nil {
			return err
		}
		if err := validateNodeBindings(node); err != nil {
			return err
		}
		if err := validateStageCapabilityRules(node); err != nil {
			return err
		}
		nodes[node.ID] = node
	}

	initial := make(map[app.WorkflowNodeID]bool, len(plan.InitialNodeIDs))
	for _, nodeID := range plan.InitialNodeIDs {
		node, exists := nodes[nodeID]
		if !exists || initial[nodeID] {
			return fmt.Errorf("initial workflow node %q is unknown or duplicated", nodeID)
		}
		if len(node.DependsOn) != 0 {
			return fmt.Errorf("initial workflow node %q cannot have dependencies", nodeID)
		}
		initial[nodeID] = true
	}
	for nodeID, node := range nodes {
		if !initial[nodeID] && len(node.DependsOn) == 0 {
			return fmt.Errorf("non-initial workflow node %q requires dependencies", nodeID)
		}
		seenDependencies := map[app.WorkflowNodeID]bool{}
		for _, dependency := range node.DependsOn {
			if _, exists := nodes[dependency]; !exists || dependency == nodeID || seenDependencies[dependency] {
				return fmt.Errorf("workflow node %q has an invalid dependency %q", nodeID, dependency)
			}
			seenDependencies[dependency] = true
		}
	}
	if workflowPlanHasCycle(nodes) {
		return errors.New("workflow node dependency graph contains a cycle")
	}
	return nil
}

func validateStageCapabilityRules(node app.WorkflowNode) error {
	if len(node.StageCapabilities) == 0 {
		return nil
	}
	knownStages := map[string]bool{node.InitialStage: true}
	for _, transition := range node.Transitions {
		knownStages[transition.NextStage] = true
	}
	seen := map[string]bool{}
	for _, rule := range node.StageCapabilities {
		stage := strings.TrimSpace(rule.Stage)
		if stage == "" || seen[stage] || !knownStages[stage] || len(rule.Capabilities) == 0 {
			return fmt.Errorf("workflow node %q has an invalid stage capability rule for %q", node.ID, rule.Stage)
		}
		seen[stage] = true
		for _, capability := range rule.Capabilities {
			if strings.TrimSpace(capability) == "" || !nodeCanRequireCapability(node, capability) {
				return fmt.Errorf("workflow node %q stage %q allows capability %q outside its frozen scopes", node.ID, stage, capability)
			}
		}
	}
	for stage := range knownStages {
		if !seen[stage] {
			return fmt.Errorf("workflow node %q has no capability rule for stage %q", node.ID, stage)
		}
	}
	return nil
}

func workflowStageAllowsCapability(node app.WorkflowNode, stage, capability string) bool {
	if len(node.StageCapabilities) == 0 {
		return true
	}
	for _, rule := range node.StageCapabilities {
		if rule.Stage != stage {
			continue
		}
		return containsString(rule.Capabilities, capability)
	}
	return false
}

func validateCapabilityScope(scope app.CapabilityScope, allowEmpty bool) error {
	if len(scope.Requirements) == 0 && !allowEmpty {
		return errors.New("at least one capability requirement is required")
	}
	for _, requirement := range scope.Requirements {
		if strings.TrimSpace(requirement.Name) == "" {
			return errors.New("capability requirement name is empty")
		}
	}
	return nil
}

func validateNodeTransitions(node app.WorkflowNode) error {
	seen := map[app.TransitionID]bool{}
	for _, transition := range node.Transitions {
		if transition.ID == "" || strings.TrimSpace(transition.NextStage) == "" || seen[transition.ID] || transition.MaxActivations <= 0 {
			return fmt.Errorf("workflow node %q has an invalid transition %q", node.ID, transition.ID)
		}
		seen[transition.ID] = true
		if !transition.Deterministic && len(transition.On.OutcomeSignals) == 0 && len(transition.On.Assessments) == 0 {
			return fmt.Errorf("workflow node %q transition %q has no predicate", node.ID, transition.ID)
		}
		if transition.Deterministic && (len(transition.On.OutcomeSignals) != 0 || len(transition.On.Assessments) != 0) {
			return fmt.Errorf("workflow node %q deterministic transition %q cannot have an outcome predicate", node.ID, transition.ID)
		}
		if transition.Replace != nil && len(transition.Add) != 0 {
			return fmt.Errorf("workflow node %q transition %q cannot add and replace scope together", node.ID, transition.ID)
		}
		if transition.Replace != nil {
			if err := validateCapabilityScope(*transition.Replace, false); err != nil {
				return fmt.Errorf("workflow node %q transition %q replacement scope: %w", node.ID, transition.ID, err)
			}
		} else if err := validateCapabilityScope(app.CapabilityScope{Requirements: transition.Add}, false); err != nil {
			return fmt.Errorf("workflow node %q transition %q additive scope: %w", node.ID, transition.ID, err)
		}
	}
	return nil
}

func validateNodeBindings(node app.WorkflowNode) error {
	for _, binding := range node.ArgumentBindings {
		if binding.Capability == "" || binding.Argument == "" || binding.ResourceKind == "" ||
			(binding.Source != app.ArgumentBindingIntentTarget && binding.Source != app.ArgumentBindingOutcomeRef &&
				binding.Source != app.ArgumentBindingRouteSlot && binding.Source != app.ArgumentBindingRouteFact) {
			return fmt.Errorf("workflow node %q has an incomplete argument binding", node.ID)
		}
		if binding.Source == app.ArgumentBindingIntentTarget && len(binding.TargetKinds) == 0 {
			return fmt.Errorf("workflow node %q intent-target binding requires target kinds", node.ID)
		}
		if (binding.Source == app.ArgumentBindingRouteSlot || binding.Source == app.ArgumentBindingRouteFact) && strings.TrimSpace(binding.SourceKey) == "" {
			return fmt.Errorf("workflow node %q route binding requires a source key", node.ID)
		}
		if !nodeCanRequireCapability(node, binding.Capability) {
			return fmt.Errorf("workflow node %q binds capability %q outside its frozen scopes", node.ID, binding.Capability)
		}
	}
	return nil
}

func nodeCanRequireCapability(node app.WorkflowNode, capability string) bool {
	if scopeRequiresCapability(node.InitialScope, capability) {
		return true
	}
	for _, transition := range node.Transitions {
		if transition.Replace != nil && scopeRequiresCapability(*transition.Replace, capability) {
			return true
		}
		for _, requirement := range transition.Add {
			if requirement.Name == capability {
				return true
			}
		}
	}
	return false
}

func scopeRequiresCapability(scope app.CapabilityScope, capability string) bool {
	for _, requirement := range scope.Requirements {
		if requirement.Name == capability {
			return true
		}
	}
	return false
}

func workflowPlanHasCycle(nodes map[app.WorkflowNodeID]app.WorkflowNode) bool {
	const (
		visiting = 1
		visited  = 2
	)
	marks := map[app.WorkflowNodeID]int{}
	var visit func(app.WorkflowNodeID) bool
	visit = func(nodeID app.WorkflowNodeID) bool {
		if marks[nodeID] == visiting {
			return true
		}
		if marks[nodeID] == visited {
			return false
		}
		marks[nodeID] = visiting
		for _, dependency := range nodes[nodeID].DependsOn {
			if visit(dependency) {
				return true
			}
		}
		marks[nodeID] = visited
		return false
	}
	for nodeID := range nodes {
		if visit(nodeID) {
			return true
		}
	}
	return false
}
