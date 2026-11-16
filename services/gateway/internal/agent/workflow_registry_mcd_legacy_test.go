package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

func TestWorkflowRegistryRejectsAllRetiredMCDLeaves(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	catalog := capability.MustDefaultCatalog()
	graph, err := registry.SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"dining.mcd_read", "dining.mcd_cart", "dining.mcd_quote", "dining.mcd_purchase", "dining.mcd_status"} {
		t.Run(retired, func(t *testing.T) {
			if _, ok := catalog.Node(app.CapabilityID(retired)); ok {
				t.Fatal("retired capability remains in the catalog")
			}
			if _, err := registry.Get(app.WorkflowID(retired)); err == nil {
				t.Fatal("retired workflow remains dispatchable")
			}
			if _, err := registry.Get(app.WorkflowID(retired), 1); err == nil {
				t.Fatal("retired persisted workflow remains resumable")
			}
			for contract := range registry.byContract {
				if string(contract.ID) == retired {
					t.Fatalf("retired workflow revision remains registered: %#v", contract)
				}
			}
			for _, candidate := range graph.Candidates() {
				if string(candidate.Capability) == retired || string(candidate.Workflow.ID) == retired || strings.HasPrefix(candidate.ID, retired+"#") {
					t.Fatalf("retired leaf remains in the semantic graph: %#v", candidate)
				}
			}
			decision := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched,
				CatalogRevision: catalog.Revision(), CapabilityPath: []app.CapabilityID{"dining", app.CapabilityID(retired)},
				Slots: app.RouteSlots{Operation: app.RouteOperationDraft, Query: "Order McDonald's"}}
			if _, err := registry.Resolve(catalog, decision, "retired_mcd"); err == nil {
				t.Fatal("retired route silently resolved to a current workflow")
			}
		})
	}
}

func TestWorkflowRegistryMCDOrderingRequiresDraftAndQuery(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	catalog := capability.MustDefaultCatalog()
	node, ok := catalog.Node(app.CapabilityMCDOrder)
	if !ok || node.Route == nil || !node.Route.RequireQuery || !slices.Equal(node.Route.Operations, []app.RouteOperation{app.RouteOperationDraft}) {
		t.Fatalf("ordering must have exactly the draft/query route contract: %#v", node)
	}
	graph, err := registry.SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, candidate := range graph.Candidates() {
		if !strings.HasPrefix(string(candidate.Capability), "dining.mcd_") {
			continue
		}
		count++
		if candidate.ID != mcdOrderCandidateID || candidate.Route.Operation != app.RouteOperationDraft || candidate.Workflow.ID != app.WorkflowMCDOrder {
			t.Fatalf("unexpected McDonald's semantic leaf: %#v", candidate)
		}
	}
	if count != 1 {
		t.Fatalf("McDonald's must have one ordering candidate, got %d", count)
	}
	for _, source := range []app.MessageSourceKind{app.MessageSourceWeb, app.MessageSourceThirdPartyDevice} {
		eligible := graph.EligibleCandidates(source)
		if !slices.ContainsFunc(eligible, func(candidate semanticrouting.Candidate) bool { return candidate.ID == mcdOrderCandidateID }) {
			t.Errorf("ordering entry missing for %s", source)
		}
	}
	for _, test := range []struct {
		name      string
		operation app.RouteOperation
		query     string
		valid     bool
	}{
		{"ordering", app.RouteOperationDraft, "帮我点一份麦辣鸡腿堡套餐", true},
		{"empty query", app.RouteOperationDraft, "", false},
		{"blank query", app.RouteOperationDraft, " \t ", false},
		{"old read operation", app.RouteOperationRead, "只看看麦当劳菜单", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched,
				CatalogRevision: catalog.Revision(), CapabilityPath: []app.CapabilityID{"dining", app.CapabilityMCDOrder},
				Slots: app.RouteSlots{Operation: test.operation, Query: test.query}}
			resolved, err := registry.Resolve(catalog, decision, "mcd_order")
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
			if test.valid && resolved.Profile.ID() != app.WorkflowMCDOrder {
				t.Fatalf("ordering dispatched the wrong profile: %s", resolved.Profile.ID())
			}
		})
	}
}
