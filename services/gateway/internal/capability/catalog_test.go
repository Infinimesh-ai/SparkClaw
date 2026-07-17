package capability

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDefaultCatalogResolvesEveryDocumentedLeaf(t *testing.T) {
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	paths := [][]app.CapabilityID{
		{"conversation", "conversation.answer"},
		{"browser", "browser.search"},
		{"browser", "browser.automation"},
		{"file", "file.discover"},
		{"file", "file.read"},
		{"file", "file.create"},
		{"file", "file.edit"},
		{"file", "file.transform"},
		{"file", "file.delete"},
		{"message", "message.send"},
		{"message", "message.schedule"},
	}
	for _, path := range paths {
		leaf, err := catalog.ResolveLeaf(path)
		if err != nil {
			t.Fatalf("resolve %v: %v", path, err)
		}
		if leaf.Workflow == nil || app.CapabilityID(leaf.Workflow.ID) != leaf.ID || leaf.Workflow.Revision != 1 {
			t.Fatalf("leaf %q has invalid workflow contract: %#v", leaf.ID, leaf.Workflow)
		}
	}
	if catalog.Revision() != DefaultCatalogRevision {
		t.Fatalf("revision = %q", catalog.Revision())
	}
}

func TestCatalogRejectsInvalidPathEdges(t *testing.T) {
	catalog := MustDefaultCatalog()
	_, err := catalog.ResolveLeaf([]app.CapabilityID{"browser", "file.read"})
	if err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("expected invalid edge error, got %v", err)
	}
	_, err = catalog.ResolveLeaf([]app.CapabilityID{"browser"})
	if err == nil || !strings.Contains(err.Error(), "ends at branch") {
		t.Fatalf("expected branch termination error, got %v", err)
	}
}

func TestCatalogValidatesFastRouteDecisionAgainstRevisionAndEdges(t *testing.T) {
	catalog := MustDefaultCatalog()
	decision := app.RouteDecision{
		SchemaVersion:   app.RouteDecisionSchemaVersion,
		Status:          app.RouteMatched,
		CatalogRevision: catalog.Revision(),
		CapabilityPath:  []app.CapabilityID{"browser", "browser.search"},
		Confidence:      0.92,
	}
	if err := catalog.ValidateDecision(decision); err != nil {
		t.Fatal(err)
	}
	decision.CapabilityPath = []app.CapabilityID{"browser", "file.read"}
	if err := catalog.ValidateDecision(decision); err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("expected edge validation error, got %v", err)
	}
	decision.CapabilityPath = nil
	decision.Status = app.RouteUnmatched
	decision.CatalogRevision = "stale"
	if err := catalog.ValidateDecision(decision); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected revision validation error, got %v", err)
	}
}

func TestCatalogRejectsUnmatchedDecisionWithInventedPath(t *testing.T) {
	catalog := MustDefaultCatalog()
	err := catalog.ValidateDecision(app.RouteDecision{
		SchemaVersion:   app.RouteDecisionSchemaVersion,
		Status:          app.RouteUnmatched,
		CatalogRevision: catalog.Revision(),
		CapabilityPath:  []app.CapabilityID{"browser", "browser.search"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot contain") {
		t.Fatalf("expected unmatched path rejection, got %v", err)
	}
}

func TestCatalogRejectsDisconnectedCycle(t *testing.T) {
	workflow := app.WorkflowContractRef{ID: "cycle.leaf", Revision: 1}
	_, err := NewCatalog("test", []Node{
		{ID: RootID, Kind: NodeBranch, Description: "root"},
		{ID: "valid", ParentID: RootID, Kind: NodeLeaf, Description: "valid", Workflow: &workflow},
		{ID: "cycle.a", ParentID: "cycle.b", Kind: NodeBranch, Description: "a"},
		{ID: "cycle.b", ParentID: "cycle.a", Kind: NodeBranch, Description: "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestCatalogReturnsDefensiveWorkflowCopies(t *testing.T) {
	catalog := MustDefaultCatalog()
	node, ok := catalog.Node("browser.search")
	if !ok || node.Workflow == nil {
		t.Fatal("browser.search is missing")
	}
	node.Workflow.Revision = 99
	again, _ := catalog.Node("browser.search")
	if again.Workflow.Revision != 1 {
		t.Fatalf("catalog workflow mutated through returned node: %#v", again.Workflow)
	}
}
