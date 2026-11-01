package capability

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestCatalogPublishSourceContract(t *testing.T) {
	catalog := MustDefaultCatalog()
	node, _ := catalog.Node(app.CapabilityConversationAnswer)
	for _, source := range []app.MessageSourceKind{app.MessageSourceWeb, app.MessageSourceThirdPartyDevice} {
		if !node.Route.SupportsSource(app.RouteOperationPublish, source) {
			t.Fatalf("publish rejected %s", source)
		}
	}
	for _, source := range []app.MessageSourceKind{"", "unregistered", app.MessageSourceTimer} {
		if node.Route.SupportsSource(app.RouteOperationPublish, source) {
			t.Fatalf("publish admitted %s", source)
		}
	}
	if node.Route.SupportsSource(app.RouteOperationDelete, app.MessageSourceWeb) {
		t.Fatal("unregistered operation admitted")
	}
	if !node.Route.SupportsSource(app.RouteOperationAnswer, app.MessageSourceTimer) {
		t.Fatal("timer answer restricted")
	}
	node.Route.OperationSourceKinds[app.RouteOperationPublish][0] = app.MessageSourceTimer
	again, _ := catalog.Node(app.CapabilityConversationAnswer)
	if again.Route.SupportsSource(app.RouteOperationPublish, app.MessageSourceTimer) {
		t.Fatal("catalog source restriction mutated through returned node")
	}
}

func TestCatalogRejectsInvalidOperationSourceRestrictions(t *testing.T) {
	for _, restrictions := range []map[app.RouteOperation][]app.MessageSourceKind{
		{app.RouteOperationDelete: {app.MessageSourceWeb}},
		{app.RouteOperationPublish: nil},
		{app.RouteOperationPublish: {"unregistered"}},
		{app.RouteOperationPublish: {app.MessageSourceWeb, app.MessageSourceWeb}},
	} {
		workflow := app.WorkflowContractRef{ID: app.WorkflowConversationAnswer, Revision: 1}
		_, err := NewCatalog("test", []Node{
			{ID: RootID, Kind: NodeBranch, Description: "root"},
			{ID: app.CapabilityConversationAnswer, ParentID: RootID, Kind: NodeLeaf, Description: "publish", Workflow: &workflow, Route: &RouteContract{Operations: []app.RouteOperation{app.RouteOperationPublish}, OperationSourceKinds: restrictions}},
		})
		if err == nil {
			t.Fatalf("accepted invalid restriction %#v", restrictions)
		}
	}
}
