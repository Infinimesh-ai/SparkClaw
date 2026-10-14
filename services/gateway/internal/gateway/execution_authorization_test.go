package gateway

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestWorkbenchV2ToolsAdmitRegisteredToolsUnderFreshDispatchPolicy(t *testing.T) {
	server, _, cfg, _ := workbenchISCPFixture(t, nil)
	cfg.ApplicationProfiles = []string{iscpworkbench.ProfileV2, iscpworkbench.Profile}
	handler, err := server.NewWorkbenchISCPHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := domainTestRequest(iscpworkbench.OperationSettingsOwnerGet, nil)
	if result := handler(domainTestContext(t), request); result.Status != 403 {
		t.Fatal("principal alone bypassed authenticated v2 session", result)
	}
	policy := iscpauth.Policy{Version: 2, State: iscpauth.Active, Revision: 1, Scopes: []string{"tools.invoke"}}
	session := iscpworkbench.SessionInfo{Profile: iscpworkbench.ProfileV2, GrantRevision: 1, Scopes: slices.Clone(policy.Scopes), QualifiedOperations: []string{iscpworkbench.OperationToolsInvoke}, CheckAuthorization: func(context.Context) (iscpauth.Policy, error) { return policy, nil }}
	a := server.workbenchExecutionAuthorization(session)
	expected := []string{}
	for _, definition := range server.tools.Definitions() {
		if !definition.RequiresApproval {
			expected = append(expected, definition.Name)
		}
	}
	if len(expected) == 0 || !slices.Contains(expected, "files.read") || !slices.Equal(a.AllowedTools, expected) || a.AllowFiles || a.AuthorizeTool == nil {
		t.Fatal("trusted ISCP session must admit every registered tool without per-tool scopes", a.AllowedTools)
	}
	if err := a.AuthorizeTool(t.Context(), "files.read"); err != nil {
		t.Fatal(err)
	}
	policy.Scopes = []string{"settings.read"}
	if err := a.AuthorizeTool(t.Context(), "files.read"); err == nil {
		t.Fatal("tools.invoke removal was ignored")
	}
	policy.Scopes = []string{"tools.invoke"}
	policy.Revision++
	if err := a.AuthorizeTool(t.Context(), "files.read"); err == nil {
		t.Fatal("task crossed authorization generation")
	}
	session.CheckAuthorization = func(context.Context) (iscpauth.Policy, error) {
		return iscpauth.Policy{}, errors.New("issuer unavailable")
	}
	a = server.workbenchExecutionAuthorization(session)
	if err := a.AuthorizeTool(t.Context(), "files.read"); err == nil {
		t.Fatal("issuer unavailability did not fail closed")
	}
	session.QualifiedOperations = nil
	if a := server.workbenchExecutionAuthorization(session); len(a.AllowedTools) != 0 || a.AllowedTools == nil {
		t.Fatal("missing qualification became unrestricted")
	}
	session.QualifiedOperations = []string{iscpworkbench.OperationToolsInvoke}
	session.Scopes = []string{"settings.read"}
	if a := server.workbenchExecutionAuthorization(session); len(a.AllowedTools) != 0 || a.AllowedTools == nil {
		t.Fatal("missing tools.invoke admitted tools")
	}
}

func TestISCPToolDiscoveryRequiresUsableApprovalAuthority(t *testing.T) {
	server, _, _, _ := workbenchISCPFixture(t, nil)
	name := ""
	for _, definition := range server.tools.Definitions() {
		if definition.RequiresApproval {
			name = definition.Name
			break
		}
	}
	if name == "" {
		t.Fatal("fixture has no approval-governed tool")
	}
	session := iscpworkbench.SessionInfo{
		Scopes:              []string{"tools.invoke"},
		QualifiedOperations: []string{iscpworkbench.OperationToolsInvoke, iscpworkbench.OperationApprovalsDecide},
		CheckAuthorization:  func(context.Context) (iscpauth.Policy, error) { return iscpauth.Policy{}, nil },
	}
	if slices.Contains(server.workbenchExecutionAuthorization(session).AllowedTools, name) {
		t.Fatal("cannot advertise a tool whose approval is forbidden")
	}
	session.Scopes = append(session.Scopes, "approvals.decide")
	if !slices.Contains(server.workbenchExecutionAuthorization(session).AllowedTools, name) {
		t.Fatal("qualified authorized tool should be available")
	}
	session.QualifiedOperations = []string{iscpworkbench.OperationToolsInvoke}
	if slices.Contains(server.workbenchExecutionAuthorization(session).AllowedTools, name) {
		t.Fatal("unqualified approval cannot enable tool")
	}
}
