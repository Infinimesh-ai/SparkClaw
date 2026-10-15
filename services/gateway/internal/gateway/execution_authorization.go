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
	// An authenticated ISCP session is trusted like a normal connection: it gets
	// files and every registered tool, including approval-governed ones, with no
	// grant scopes. Only operations the negotiated transport carries are usable,
	// and each dispatch still rechecks that the grant is active and current.
	a := executionAuthorization{AllowedTools: []string{}}
	if session.CheckAuthorization == nil {
		return a
	}
	a.AllowFiles = slices.Contains(session.QualifiedOperations, iscpworkbench.OperationExecutionInputPut)
	if !slices.Contains(session.QualifiedOperations, iscpworkbench.OperationToolsInvoke) {
		return a
	}
	for _, definition := range s.tools.Definitions() {
		if definition.RequiresApproval && !slices.Contains(session.QualifiedOperations, iscpworkbench.OperationApprovalsDecide) {
			continue
		}
		a.AllowedTools = append(a.AllowedTools, definition.Name)
	}
	a.AuthorizeTool = func(ctx context.Context, name string) error {
		policy, err := session.CheckAuthorization(ctx)
		if err != nil {
			return err
		}
		if policy.Revision != session.GrantRevision || !policy.Usable() {
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
