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
	valueKey := "text"
	if plan.Name == "browser.select" {
		valueKey = "value"
	}
	if !toolhub.BrowserDraftValueAllowed(run.Workflow.Route.Slots.Query, stringValue(plan.Args[valueKey])) {
		return errors.New("browser form value is not an exact owner-supplied value")
	}
	return nil
}
