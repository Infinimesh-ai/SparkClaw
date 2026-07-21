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
		{"browser", "browser.internet_search"},
		{"browser", "browser.weather"},
		{"browser", "browser.automation"},
		{"browser", "browser.interaction"},
		{"document", "document.read"},
		{"document", "document.edit"},
	}
	for _, path := range paths {
		leaf, err := catalog.ResolveLeaf(path)
		if err != nil {
			t.Fatalf("resolve %v: %v", path, err)
		}
		wantRevision := 1
		if leaf.ID == app.CapabilityDocumentEdit {
			wantRevision = 2
		}
		if leaf.Workflow == nil || app.CapabilityID(leaf.Workflow.ID) != leaf.ID || leaf.Workflow.Revision != wantRevision {
			t.Fatalf("leaf %q has invalid workflow contract: %#v", leaf.ID, leaf.Workflow)
		}
	}
	if catalog.Revision() != DefaultCatalogRevision {
		t.Fatalf("revision = %q", catalog.Revision())
	}
}

func TestCatalogValidatesWeatherLocationContract(t *testing.T) {
	catalog := MustDefaultCatalog()
	decision := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserWeather},
		Slots:          app.RouteSlots{Operation: app.RouteOperationRead, FactScope: app.RouteFactScopeWeatherSnapshot, Query: "今天杭州天气", TargetKind: string(app.TargetKindLocation), TargetRef: "杭州", Location: "杭州", Format: "image"},
		Facts:          map[string]string{"location_source": "current_turn"},
	}
	if err := catalog.ValidateDecision(decision); err != nil {
		t.Fatal(err)
	}
	decision.Slots.TargetRef = ""
	if err := catalog.ValidateDecision(decision); err == nil || !strings.Contains(err.Error(), "requires a deterministic target") {
		t.Fatalf("expected missing weather location rejection, got %v", err)
	}
}

func TestCatalogRejectsInvalidPathEdges(t *testing.T) {
	catalog := MustDefaultCatalog()
	_, err := catalog.ResolveLeaf([]app.CapabilityID{"browser", "document.read"})
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
		CapabilityPath:  []app.CapabilityID{"browser", "browser.internet_search"},
		Slots:           app.RouteSlots{Operation: app.RouteOperationSearch, FactScope: app.RouteFactScopeCurrentInternet, Query: "test"},
		Confidence:      0.92,
	}
	if err := catalog.ValidateDecision(decision); err != nil {
		t.Fatal(err)
	}
	decision.CapabilityPath = []app.CapabilityID{"browser", "document.read"}
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

func TestCatalogRejectsOperationOutsideLeafSlotContract(t *testing.T) {
	catalog := MustDefaultCatalog()
	err := catalog.ValidateDecision(app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"browser", "browser.internet_search"}, Slots: app.RouteSlots{Operation: app.RouteOperationDelete, FactScope: app.RouteFactScopeCurrentInternet, Query: "test"},
	})
	if err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("expected typed operation rejection, got %v", err)
	}
}

func TestCatalogRejectsUnmatchedDecisionWithInventedPath(t *testing.T) {
	catalog := MustDefaultCatalog()
	err := catalog.ValidateDecision(app.RouteDecision{
		SchemaVersion:   app.RouteDecisionSchemaVersion,
		Status:          app.RouteUnmatched,
		CatalogRevision: catalog.Revision(),
		CapabilityPath:  []app.CapabilityID{"browser", "browser.internet_search"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot contain") {
		t.Fatalf("expected unmatched path rejection, got %v", err)
	}
}

func TestCatalogSeparatesCurrentInternetFactsFromWeatherCards(t *testing.T) {
	catalog := MustDefaultCatalog()
	search := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserInternetSearch},
		Slots:          app.RouteSlots{Operation: app.RouteOperationSearch, FactScope: app.RouteFactScopeCurrentInternet, Query: "current gold price"},
	}
	if err := catalog.ValidateDecision(search); err != nil {
		t.Fatal(err)
	}
	weather := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserWeather},
		Slots:          app.RouteSlots{Operation: app.RouteOperationRead, FactScope: app.RouteFactScopeWeatherSnapshot, Query: "今天杭州天气", TargetKind: string(app.TargetKindLocation), TargetRef: "杭州", Location: "杭州"},
		Facts:          map[string]string{"location_source": "current_turn"},
	}
	if err := catalog.ValidateDecision(weather); err != nil {
		t.Fatal(err)
	}
	weather.Slots.FactScope = app.RouteFactScopeCurrentInternet
	if err := catalog.ValidateDecision(weather); err == nil || !strings.Contains(err.Error(), "fact scope") {
		t.Fatalf("weather accepted the Internet-search fact scope: %v", err)
	}
	search.Slots.FactScope = ""
	if err := catalog.ValidateDecision(search); err == nil || !strings.Contains(err.Error(), "fact scope") {
		t.Fatalf("search accepted a missing current-Internet fact scope: %v", err)
	}
}

func TestCurrentInternetCatalogDescriptionOwnsLiveFactsWithoutVerticalLeaves(t *testing.T) {
	catalog := MustDefaultCatalog()
	search, ok := catalog.Node(app.CapabilityBrowserInternetSearch)
	if !ok {
		t.Fatal("browser.internet_search is missing")
	}
	for _, example := range []string{"gold prices", "exchange rates", "stock or index quotes", "current sports results"} {
		if !strings.Contains(search.Description, example) {
			t.Fatalf("Internet search description is missing %q: %s", example, search.Description)
		}
	}
	for _, forbiddenLeaf := range []app.CapabilityID{"browser.gold_price", "browser.exchange_rate", "browser.stock_quote", "browser.sports_result"} {
		if _, exists := catalog.Node(forbiddenLeaf); exists {
			t.Fatalf("live fact category became a vertical leaf: %q", forbiddenLeaf)
		}
	}
}

func TestCatalogRejectsDisconnectedCycle(t *testing.T) {
	workflow := app.WorkflowContractRef{ID: "cycle.leaf", Revision: 1}
	route := &RouteContract{Operations: []app.RouteOperation{app.RouteOperationRead}}
	_, err := NewCatalog("test", []Node{
		{ID: RootID, Kind: NodeBranch, Description: "root"},
		{ID: "valid", ParentID: RootID, Kind: NodeLeaf, Description: "valid", Workflow: &workflow, Route: route},
		{ID: "cycle.a", ParentID: "cycle.b", Kind: NodeBranch, Description: "a"},
		{ID: "cycle.b", ParentID: "cycle.a", Kind: NodeBranch, Description: "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestCatalogReturnsDefensiveWorkflowCopies(t *testing.T) {
	catalog := MustDefaultCatalog()
	node, ok := catalog.Node("browser.internet_search")
	if !ok || node.Workflow == nil {
		t.Fatal("browser.internet_search is missing")
	}
	node.Workflow.Revision = 99
	node.Route.Operations[0] = app.RouteOperationDelete
	again, _ := catalog.Node("browser.internet_search")
	if again.Workflow.Revision != 1 {
		t.Fatalf("catalog workflow mutated through returned node: %#v", again.Workflow)
	}
	if again.Route.Operations[0] != app.RouteOperationSearch {
		t.Fatalf("catalog route contract mutated through returned node: %#v", again.Route)
	}
}

func TestCatalogRoutesFutureRegistrationWithoutCoreNameSwitch(t *testing.T) {
	workflow := app.WorkflowContractRef{ID: "future.translate", Revision: 3}
	catalog, err := NewCatalog("future", []Node{
		{ID: RootID, Kind: NodeBranch, Description: "root"},
		{ID: "future", ParentID: RootID, Kind: NodeBranch, Description: "future branch"},
		{ID: "future.translate", ParentID: "future", Kind: NodeLeaf, Description: "translate content", Workflow: &workflow,
			Route: &RouteContract{Operations: []app.RouteOperation{"translate"}, RequireQuery: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"future", "future.translate"}, Slots: app.RouteSlots{Operation: "translate", Query: "hello"}}
	if err := catalog.ValidateDecision(decision); err != nil {
		t.Fatalf("future registration did not route generically: %v", err)
	}
	options := catalog.RouteOptions()
	if len(options) != 1 || pathKey(options[0].Path) != "future/future.translate" || options[0].Description != "translate content" {
		t.Fatalf("future routing directory was not derived from registration: %#v", options)
	}
}
