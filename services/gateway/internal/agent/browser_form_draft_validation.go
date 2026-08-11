package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func validateBrowserFormDraftPlan(run app.AgentRun, plan toolPlan) error {
	if run.Workflow == nil || run.Workflow.Plan.ProfileID != app.WorkflowBrowserFormDraft || run.Workflow.Browser == nil {
		return errors.New("browser form action is outside browser.form_draft")
	}
	if len(run.Workflow.Browser.DraftActions) >= app.BrowserFormDraftMaxActions {
		return errors.New("browser form draft action limit reached")
	}
	node, ok := run.Workflow.Nodes[plan.WorkflowNodeID]
	if !ok || node.Status != app.WorkflowNodeActive {
		return errors.New("browser form action has no active workflow node")
	}
	snapshotID := strings.TrimSpace(stringValue(plan.Args["snapshot_id"]))
	pageID := strings.TrimSpace(stringValue(plan.Args["page_id"]))
	elementRef := strings.TrimSpace(stringValue(plan.Args["uid"]))
	var snapshot, element app.ResourceRef
	for _, ref := range node.OutcomeRefs {
		if ref.Kind == "browser_snapshot" && ref.Ref == snapshotID {
			snapshot = ref
		}
		if ref.Kind == "browser_element" && ref.Ref == elementRef && ref.Attributes["snapshot_id"] == snapshotID {
			element = ref
		}
	}
	if snapshot.Ref == "" || element.Ref == "" || snapshot.Attributes["page_id"] != pageID || element.Attributes["page_id"] != pageID ||
		intLikeValue(plan.Args["session_generation"]) != intLikeValue(snapshot.Attributes["session_generation"]) ||
		intLikeValue(plan.Args["page_generation"]) != intLikeValue(snapshot.Attributes["page_generation"]) {
		return errors.New("browser form action is not bound to the current snapshot generation")
	}
	if !toolhub.BrowserDraftControlAllowed(plan.Name, element.Attributes["role"], element.Attributes["name"], element.Attributes["container"]) {
		return fmt.Errorf("browser form control %q is outside the reversible draft boundary", element.Attributes["name"])
	}
	if browserDraftElementAlreadyCompleted(run.Workflow.Browser.DraftActions, element) {
		return fmt.Errorf("browser form control %q already has a completed draft action", element.Attributes["name"])
	}
	valueKey := "text"
	if plan.Name == "browser.select" {
		valueKey = "value"
	}
	if !toolhub.BrowserDraftValueAllowed(run.Workflow.Route.Slots.Query, stringValue(plan.Args[valueKey])) {
		return errors.New("browser form value is not an exact owner-supplied value")
	}
	return nil
}

func materializeBrowserFormDraftSchemas(definitions []app.ToolDefinition, run app.AgentRun, nodeID app.WorkflowNodeID) []app.ToolDefinition {
	if run.Workflow == nil || run.Workflow.Plan.ProfileID != app.WorkflowBrowserFormDraft {
		return definitions
	}
	node, ok := run.Workflow.Nodes[nodeID]
	if !ok || node.Stage != browserStageChooseAndDraft {
		return definitions
	}
	refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
	out := make([]app.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name != "browser.type" && definition.Name != "browser.select" {
			out = append(out, definition)
			continue
		}
		allowed := []any{}
		for _, ref := range refs {
			if ref.Kind == "browser_element" &&
				toolhub.BrowserDraftControlAllowed(definition.Name, ref.Attributes["role"], ref.Attributes["name"], ref.Attributes["container"]) &&
				!browserDraftElementAlreadyCompleted(run.Workflow.Browser.DraftActions, ref) {
				allowed = append(allowed, ref.Ref)
			}
		}
		if len(allowed) == 0 {
			continue
		}
		schema := cloneAnyMap(definition.InputSchema)
		properties, _ := anyMap(schema["properties"])
		properties = cloneAnyMap(properties)
		uid, _ := anyMap(properties["uid"])
		uid = cloneAnyMap(uid)
		uid["enum"] = allowed
		uid["description"] = "Copy one exact current snapshot ref from this enum; aliases and older refs are invalid."
		properties["uid"] = uid
		schema["properties"] = properties
		definition.InputSchema = schema
		out = append(out, definition)
	}
	return out
}

func currentBrowserFormDraftSnapshot(refs []app.ResourceRef) (app.ResourceRef, bool) {
	for _, ref := range currentBrowserSnapshotRefs(refs) {
		if ref.Kind == "browser_snapshot" {
			return ref, true
		}
	}
	return app.ResourceRef{}, false
}

func browserDraftElementAlreadyCompleted(actions []app.BrowserDraftAction, ref app.ResourceRef) bool {
	fingerprint := browserDraftElementFingerprint(ref.Ref)
	for _, action := range actions {
		if !action.Completed || !strings.EqualFold(strings.TrimSpace(action.Role), strings.TrimSpace(ref.Attributes["role"])) ||
			!strings.EqualFold(strings.TrimSpace(action.AccessibleName), strings.TrimSpace(ref.Attributes["name"])) ||
			strings.TrimSpace(action.FormContext) != strings.TrimSpace(ref.Attributes["container"]) {
			continue
		}
		actionFingerprint := browserDraftElementFingerprint(action.ElementRef)
		if fingerprint == "" || actionFingerprint == "" || fingerprint == actionFingerprint {
			return true
		}
	}
	return false
}

func browserDraftElementFingerprint(ref string) string {
	index := strings.LastIndex(strings.TrimSpace(ref), ":")
	if index < 0 || index == len(ref)-1 {
		return ""
	}
	return ref[index+1:]
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
