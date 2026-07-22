package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

func TestScheduleManageRecognizesCRUDWithoutInternetRouteCollision(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	catalog := capability.MustDefaultCatalog()
	for _, tc := range []struct {
		query     string
		operation app.RouteOperation
	}{
		{query: "提醒我明天九点提交日报", operation: app.RouteOperationCreate},
		{query: "list current schedules", operation: app.RouteOperationRead},
		{query: "把定时任务 schedule-1 推迟到十点", operation: app.RouteOperationEdit},
		{query: "取消提醒 schedule-1", operation: app.RouteOperationDelete},
	} {
		decision, err := registry.Recognize(catalog, workflowRecognitionContext{SourceTurnID: "turn", Content: tc.query})
		if err != nil {
			t.Fatalf("recognize %q: %v", tc.query, err)
		}
		if decision.Status != app.RouteMatched || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityScheduleManage || decision.Slots.Operation != tc.operation {
			t.Fatalf("unexpected schedule route for %q: %#v", tc.query, decision)
		}
	}
}

func TestScheduleManageResolvesOneOperationQualifiedCapability(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	for _, operation := range []app.RouteOperation{app.RouteOperationCreate, app.RouteOperationRead, app.RouteOperationEdit, app.RouteOperationDelete} {
		route := app.RouteDecision{
			SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
			CapabilityPath: []app.CapabilityID{"schedule", app.CapabilityScheduleManage},
			Slots:          app.RouteSlots{Operation: operation, Query: "schedule operation"},
		}
		resolved, err := defaultWorkflowProfileRegistry().Resolve(catalog, route, "turn")
		if err != nil {
			t.Fatalf("resolve %q: %v", operation, err)
		}
		if len(resolved.Plan.Nodes) != 1 || len(resolved.Plan.Nodes[0].InitialScope.Requirements) != 1 {
			t.Fatalf("operation %q did not resolve one capability: %#v", operation, resolved.Plan)
		}
		requirement := resolved.Plan.Nodes[0].InitialScope.Requirements[0]
		if requirement.Name != app.ToolCapabilityScheduleManage || requirement.Qualifiers[app.CapabilityQualifierOperation] != string(operation) {
			t.Fatalf("operation %q escaped its exact capability qualifier: %#v", operation, requirement)
		}
	}
}
