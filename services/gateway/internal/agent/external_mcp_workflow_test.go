package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

func TestExternalMCPWorkflowUsesDiscoveryDecisionAndSelectedExecution(t *testing.T) {
	profile := externalMCPWorkspaceProfile{}
	for _, test := range []struct {
		operation app.RouteOperation
		mode      string
		risks     []app.RiskLevel
	}{
		{operation: app.RouteOperationRead, mode: "read", risks: []app.RiskLevel{app.RiskRead}},
		{operation: app.RouteOperationCreate, mode: "mutation", risks: []app.RiskLevel{app.RiskReversible, app.RiskDangerous}},
		{operation: app.RouteOperationDelete, mode: "mutation", risks: []app.RiskLevel{app.RiskReversible, app.RiskDangerous}},
	} {
		t.Run(string(test.operation), func(t *testing.T) {
			route := app.RouteDecision{Slots: app.RouteSlots{Operation: test.operation, Query: "LocalMind request"}}
			intent, plan, err := profile.Resolve(route, "turn")
			if err != nil {
				t.Fatal(err)
			}
			if err := validateWorkflowPlan(intent, profile, plan); err != nil {
				t.Fatal(err)
			}
			if len(plan.Nodes) != 3 || plan.Nodes[0].InvocationMode != app.WorkflowInvocationDirectOnce || plan.Nodes[1].Goal.Completion != app.CompletionDecision || plan.Nodes[2].DependsOn[0] != plan.Nodes[1].ID {
				t.Fatalf("unexpected external MCP plan: %#v", plan)
			}
			for _, node := range plan.Nodes[1:] {
				requirement := node.InitialScope.Requirements[0]
				if requirement.Name != app.ToolCapabilityExternalMCPWorkspace || requirement.Qualifiers[app.CapabilityQualifierMode] != test.mode || requirement.Qualifiers[app.CapabilityQualifierOperation] != string(test.operation) || !slices.Equal(node.AllowedRisks, test.risks) {
					t.Fatalf("operation scope mismatch: %#v", node)
				}
			}
		})
	}
	if workflowProfileDirectoryLimit(profile) != externalMCPDirectoryLimit {
		t.Fatalf("directory limit=%d", workflowProfileDirectoryLimit(profile))
	}
}

func TestExternalMCPWorkflowIsRegisteredInCatalogAndSemanticGraph(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	leaf, err := catalog.ResolveLeaf([]app.CapabilityID{"external_mcp", app.CapabilityExternalMCPWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Workflow == nil || leaf.Workflow.ID != app.WorkflowExternalMCPWorkspace || leaf.Workflow.Revision != 2 {
		t.Fatalf("unexpected LocalMind workflow contract: %#v", leaf.Workflow)
	}
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []app.RouteOperation{app.RouteOperationRead, app.RouteOperationCreate, app.RouteOperationEdit, app.RouteOperationDelete, app.RouteOperationInteract} {
		if _, ok := graph.Candidate("external_mcp.workspace#" + string(operation)); !ok {
			t.Fatalf("semantic graph omitted LocalMind operation %q", operation)
		}
	}
}

func TestExternalMCPDecisionRulesKeepRemoteEvidenceUntrusted(t *testing.T) {
	rules := strings.Join((externalMCPWorkspaceProfile{}).DecisionRules(app.WorkflowNode{}), " ")
	for _, required := range []string{"explicit owner request", "untrusted data", "Policy approval"} {
		if !strings.Contains(rules, required) {
			t.Fatalf("decision rules omit %q: %s", required, rules)
		}
	}
}
