package gateway

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestWorkbenchV2ToolsRequireExactSignedScopeAndFreshDispatchPolicy(t *testing.T) {
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
	policy := iscpauth.Policy{Version: 2, State: iscpauth.Active, Revision: 1, Scopes: []string{"tools.invoke", "tool.files.read"}}
	session := iscpworkbench.SessionInfo{Profile: iscpworkbench.ProfileV2, GrantRevision: 1, Scopes: slices.Clone(policy.Scopes), QualifiedOperations: []string{iscpworkbench.OperationToolsInvoke}, CheckAuthorization: func(context.Context) (iscpauth.Policy, error) { return policy, nil }}
	a := server.workbenchExecutionAuthorization(session)
	if !slices.Equal(a.AllowedTools, []string{"files.read"}) || a.AllowFiles || a.AuthorizeTool == nil {
		t.Fatal(a)
	}
	if err := a.AuthorizeTool(t.Context(), "files.read"); err != nil {
		t.Fatal(err)
	}
	policy.Scopes = []string{"tools.invoke"}
	if err := a.AuthorizeTool(t.Context(), "files.read"); err == nil {
		t.Fatal("tool scope removal was ignored")
	}
	policy.Scopes = []string{"tools.invoke", "tool.files.read"}
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
}
