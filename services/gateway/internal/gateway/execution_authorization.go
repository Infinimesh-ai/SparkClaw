package gateway

import (
	"context"
	"errors"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// executionAuthorization is trusted local state derived only from an
// authenticated ISCP session. It is never decoded from an execution envelope.
type executionAuthorization struct {
	AllowedTools  []string
	AuthorizeTool func(context.Context, string) error
	AllowFiles    bool
}

func (s *Server) workbenchExecutionAuthorization(session iscpworkbench.SessionInfo) executionAuthorization {
	a := executionAuthorization{AllowedTools: []string{}}
	a.AllowFiles = slices.Contains(session.Scopes, "objects.write") && slices.Contains(session.QualifiedOperations, iscpworkbench.OperationExecutionInputPut)
	if session.CheckAuthorization == nil || !slices.Contains(session.Scopes, "tools.invoke") || !slices.Contains(session.QualifiedOperations, iscpworkbench.OperationToolsInvoke) {
		return a
	}
	for _, definition := range s.tools.Definitions() {
		if !slices.Contains(session.Scopes, "tool."+definition.Name) {
			continue
		}
		if definition.RequiresApproval && (!slices.Contains(session.Scopes, "approvals.decide") || !slices.Contains(session.QualifiedOperations, iscpworkbench.OperationApprovalsDecide)) {
			continue
		}
		a.AllowedTools = append(a.AllowedTools, definition.Name)
	}
	a.AuthorizeTool = func(ctx context.Context, name string) error {
		policy, err := session.CheckAuthorization(ctx)
		if err != nil {
			return err
		}
		if policy.Revision != session.GrantRevision || !policy.Allows("tools.invoke") || !policy.Allows("tool."+name) {
			return errors.New("current ISCP authorization does not permit this tool")
		}
		return nil
	}
	return a
}

type executionAuthorizationKey struct{}

func withExecutionAuthorization(ctx context.Context, authorization executionAuthorization) context.Context {
	authorization.AllowedTools = slices.Clone(authorization.AllowedTools)
	return context.WithValue(ctx, executionAuthorizationKey{}, authorization)
}

func executionResourcesForContext(ctx context.Context, resources toolhub.ExecutionResources) toolhub.ExecutionResources {
	if authorization, ok := ctx.Value(executionAuthorizationKey{}).(executionAuthorization); ok {
		resources.AllowedTools = slices.Clone(authorization.AllowedTools)
		resources.AuthorizeTool = authorization.AuthorizeTool
	}
	return resources
}
