package agent

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

var (
	errExposureWorkflowMismatch = errors.New("tool exposure request does not match the active workflow")
	errExposureViewStale        = errors.New("tool directory view is stale or unknown")
	errExposureEntryInvalid     = errors.New("tool directory entry was not returned by the latest view")
)

type ToolExposure interface {
	Search(context.Context, app.ExposureRequest) (app.DirectoryView, error)
	Materialize(context.Context, app.MaterializeRequest) (app.ExposureView, error)
}

type exposureViewState struct {
	view      app.DirectoryView
	toolNames map[app.ToolDirectoryEntryID]string
}

type toolExposureEngine struct {
	store  store.Store
	tools  *toolhub.ToolHub
	policy policy.Engine
	secret []byte

	mu     sync.Mutex
	latest map[string]exposureViewState
}

func newToolExposureEngine(st store.Store, tools *toolhub.ToolHub, policyEngine policy.Engine) *toolExposureEngine {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("tool exposure: failed to initialize view signer")
	}
	return &toolExposureEngine{
		store:  st,
		tools:  tools,
		policy: policyEngine,
		secret: secret,
		latest: map[string]exposureViewState{},
	}
}

func (e *toolExposureEngine) Search(_ context.Context, request app.ExposureRequest) (app.DirectoryView, error) {
	run, node, state, err := e.activeNode(request.RunID, request.WorkflowID, request.NodeID, request.ScopeRevision)
	if err != nil {
		return app.DirectoryView{}, err
	}
	eligible := e.eligibleDefinitions(request.ActorRef, run, node, state.CurrentScope)
	entries := make([]app.ToolDirectoryEntry, 0, len(eligible))
	toolNames := make(map[app.ToolDirectoryEntryID]string, len(eligible))
	goalText := strings.ToLower(node.Goal.Summary)
	for _, item := range eligible {
		entryID := directoryEntryID(item.definition, item.capability)
		entries = append(entries, app.ToolDirectoryEntry{
			ID:            entryID,
			Capability:    item.capability,
			Summary:       item.definition.Directory.Summary,
			WhenToUse:     item.definition.Directory.WhenToUse,
			WhenNotToUse:  item.definition.Directory.WhenNotToUse,
			Effects:       append([]app.ToolEffect(nil), item.definition.Directory.Effects...),
			Risk:          item.definition.Risk,
			RelevanceRank: directoryRelevance(goalText, item.definition.Directory),
		})
		toolNames[entryID] = item.definition.Name
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].RelevanceRank == entries[j].RelevanceRank {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].RelevanceRank > entries[j].RelevanceRank
	})
	limit := request.Limit
	if limit <= 0 || limit > 32 {
		limit = 16
	}
	if len(entries) > limit {
		entries = entries[:limit]
		selectedNames := make(map[app.ToolDirectoryEntryID]string, len(entries))
		for _, entry := range entries {
			selectedNames[entry.ID] = toolNames[entry.ID]
		}
		toolNames = selectedNames
	}
	directoryRevision := e.directoryRevision(eligible)
	view := app.DirectoryView{
		RunID:             request.RunID,
		WorkflowID:        request.WorkflowID,
		NodeID:            request.NodeID,
		ActorRef:          request.ActorRef,
		ScopeRevision:     request.ScopeRevision,
		DirectoryRevision: directoryRevision,
		Entries:           entries,
	}
	view.ViewID = e.signView(view)

	e.mu.Lock()
	e.latest[exposureViewKey(request.RunID, request.NodeID)] = exposureViewState{view: view, toolNames: toolNames}
	e.mu.Unlock()

	state.LastDirectory = &app.DirectoryViewRef{
		ViewID:            view.ViewID,
		DirectoryRevision: view.DirectoryRevision,
		EntryIDs:          directoryEntryIDs(entries),
	}
	run.Workflow.Nodes[request.NodeID] = state
	e.store.SaveRun(run)
	return view, nil
}

func (e *toolExposureEngine) Materialize(_ context.Context, request app.MaterializeRequest) (app.ExposureView, error) {
	run, node, state, err := e.activeNode(request.RunID, request.WorkflowID, request.NodeID, request.ScopeRevision)
	if err != nil {
		return app.ExposureView{}, err
	}
	e.mu.Lock()
	latest, ok := e.latest[exposureViewKey(request.RunID, request.NodeID)]
	e.mu.Unlock()
	if !ok || latest.view.ViewID != request.ViewID || latest.view.ActorRef != request.ActorRef ||
		latest.view.ScopeRevision != request.ScopeRevision || !hmac.Equal([]byte(latest.view.ViewID), []byte(e.signView(latest.view))) {
		return app.ExposureView{}, errExposureViewStale
	}
	if len(request.EntryIDs) == 0 {
		return app.ExposureView{}, errExposureEntryInvalid
	}
	eligible := e.eligibleDefinitions(request.ActorRef, run, node, state.CurrentScope)
	eligibleByName := make(map[string]app.ToolDefinition, len(eligible))
	for _, item := range eligible {
		eligibleByName[item.definition.Name] = item.definition
	}
	definitions := make([]app.ToolDefinition, 0, len(request.EntryIDs))
	seen := map[string]bool{}
	for _, entryID := range request.EntryIDs {
		name, returned := latest.toolNames[entryID]
		if !returned {
			return app.ExposureView{}, errExposureEntryInvalid
		}
		definition, stillEligible := eligibleByName[name]
		if !stillEligible {
			return app.ExposureView{}, errExposureViewStale
		}
		if !seen[name] {
			definitions = append(definitions, definition)
			seen[name] = true
		}
	}
	state.SelectedEntries = append([]app.ToolDirectoryEntryID(nil), request.EntryIDs...)
	run.Workflow.Nodes[request.NodeID] = state
	e.store.SaveRun(run)
	return app.ExposureView{
		ViewID:            latest.view.ViewID,
		RunID:             request.RunID,
		WorkflowID:        request.WorkflowID,
		NodeID:            request.NodeID,
		ActorRef:          request.ActorRef,
		ScopeRevision:     request.ScopeRevision,
		DirectoryRevision: latest.view.DirectoryRevision,
		Definitions:       definitions,
	}, nil
}

type eligibleDefinition struct {
	definition app.ToolDefinition
	capability app.CapabilityDescriptor
}

func (e *toolExposureEngine) eligibleDefinitions(actorRef string, run app.AgentRun, node app.WorkflowNode, scope app.CapabilityScope) []eligibleDefinition {
	allowedRisks := make(map[app.RiskLevel]bool, len(node.AllowedRisks))
	for _, risk := range node.AllowedRisks {
		allowedRisks[risk] = true
	}
	deniedEffects := make(map[app.ToolEffect]bool, len(scope.DeniedEffects))
	for _, effect := range scope.DeniedEffects {
		deniedEffects[effect] = true
	}
	out := []eligibleDefinition{}
	for _, definition := range e.tools.Definitions() {
		if len(definition.Capabilities) == 0 || !allowedRisks[definition.Risk] || hasDeniedEffect(definition.Directory.Effects, deniedEffects) {
			continue
		}
		if !e.policy.MayExpose(definition).Allowed {
			continue
		}
		for _, capability := range definition.Capabilities {
			if matchesAnyRequirement(capability, scope.Requirements) {
				out = append(out, eligibleDefinition{definition: definition, capability: capability})
			}
		}
	}
	return out
}

func (e *toolExposureEngine) activeNode(runID string, workflowID app.WorkflowID, nodeID app.WorkflowNodeID, scopeRevision int) (app.AgentRun, app.WorkflowNode, app.WorkflowNodeState, error) {
	run, ok := e.store.GetRun(runID)
	if !ok || run.Workflow == nil || run.Workflow.Plan.ProfileID != workflowID || run.Workflow.PlanDigest == "" {
		return app.AgentRun{}, app.WorkflowNode{}, app.WorkflowNodeState{}, errExposureWorkflowMismatch
	}
	state, ok := run.Workflow.Nodes[nodeID]
	if !ok || state.Status != app.WorkflowNodeActive || state.ScopeRevision != scopeRevision || !containsWorkflowNodeID(run.Workflow.ActiveNodeIDs, nodeID) {
		return app.AgentRun{}, app.WorkflowNode{}, app.WorkflowNodeState{}, errExposureWorkflowMismatch
	}
	for _, node := range run.Workflow.Plan.Nodes {
		if node.ID == nodeID {
			return run, node, state, nil
		}
	}
	return app.AgentRun{}, app.WorkflowNode{}, app.WorkflowNodeState{}, errExposureWorkflowMismatch
}

func matchesAnyRequirement(capability app.CapabilityDescriptor, requirements []app.CapabilityRequirement) bool {
	for _, requirement := range requirements {
		if capability.Name != requirement.Name {
			continue
		}
		matched := true
		for key, value := range requirement.Qualifiers {
			if capability.Qualifiers[key] != value {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func hasDeniedEffect(effects []app.ToolEffect, denied map[app.ToolEffect]bool) bool {
	for _, effect := range effects {
		if denied[effect] {
			return true
		}
	}
	return false
}

func directoryRelevance(goal string, metadata app.ToolDirectoryMetadata) int {
	corpus := strings.ToLower(metadata.Summary + " " + metadata.WhenToUse + " " + metadata.WhenNotToUse)
	score := 0
	for _, token := range strings.Fields(goal) {
		token = strings.Trim(token, " ,.;:!?()[]{}\"'")
		if len(token) > 2 && strings.Contains(corpus, token) {
			score++
		}
	}
	return score
}

func directoryEntryID(definition app.ToolDefinition, capability app.CapabilityDescriptor) app.ToolDirectoryEntryID {
	payload, _ := json.Marshal(struct {
		Name       string
		Capability app.CapabilityDescriptor
	}{definition.Name, capability})
	sum := sha256.Sum256(payload)
	return app.ToolDirectoryEntryID("entry_" + hex.EncodeToString(sum[:12]))
}

func (e *toolExposureEngine) directoryRevision(eligible []eligibleDefinition) string {
	type revisionEntry struct {
		Definition app.ToolDefinition
		Capability app.CapabilityDescriptor
	}
	entries := make([]revisionEntry, 0, len(eligible))
	for _, item := range eligible {
		entries = append(entries, revisionEntry{Definition: item.definition, Capability: item.capability})
	}
	sort.Slice(entries, func(i, j int) bool {
		return directoryEntryID(entries[i].Definition, entries[i].Capability) < directoryEntryID(entries[j].Definition, entries[j].Capability)
	})
	payload, _ := json.Marshal(entries)
	sum := sha256.Sum256(payload)
	return "dir_" + hex.EncodeToString(sum[:12])
}

func (e *toolExposureEngine) signView(view app.DirectoryView) string {
	copyView := view
	copyView.ViewID = ""
	payload, _ := json.Marshal(copyView)
	mac := hmac.New(sha256.New, e.secret)
	_, _ = mac.Write(payload)
	return "view_" + hex.EncodeToString(mac.Sum(nil))
}

func exposureViewKey(runID string, nodeID app.WorkflowNodeID) string {
	return runID + "\x00" + string(nodeID)
}

func directoryEntryIDs(entries []app.ToolDirectoryEntry) []app.ToolDirectoryEntryID {
	out := make([]app.ToolDirectoryEntryID, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}

func containsWorkflowNodeID(values []app.WorkflowNodeID, want app.WorkflowNodeID) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
