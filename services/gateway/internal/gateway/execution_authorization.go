package gateway

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// executionAuthorization is trusted local state derived only from an
// authenticated ISCP session. It is never decoded from an execution envelope.
type executionAuthorization struct {
	AllowedTools      []string
	AuthorizeTool     func(context.Context, string) error
	AllowFiles        bool
	AllowMCDSelection bool
	AllowMCDPrepare   bool
	AllowMCDPurchase  bool
	AllowMCDStatus    bool
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
	a.AllowMCDStatus = s.cfg.MCDSelection.Enabled && s.mcdSelection != nil
	for _, op := range []string{iscpworkbench.OperationMCDOperationStatus, iscpworkbench.OperationMCDOrdersQuery} {
		a.AllowMCDStatus = a.AllowMCDStatus && slices.Contains(session.NegotiatedOperations, op) && slices.Contains(session.QualifiedOperations, op)
	}
	a.AllowMCDSelection = s.cfg.MCDSelection.Enabled && s.mcdSelection != nil && slices.Contains(session.NegotiatedOperations, iscpworkbench.OperationMCDSessionProvision) && slices.Contains(session.QualifiedOperations, iscpworkbench.OperationMCDSessionProvision)
	a.AllowMCDPrepare = a.AllowMCDSelection && s.cfg.MCDSelection.CartEnabled
	for _, op := range []string{iscpworkbench.OperationMCDCheckoutPrepare, iscpworkbench.OperationMCDCheckoutAbort, iscpworkbench.OperationMCDOperationStatus} {
		a.AllowMCDPrepare = a.AllowMCDPrepare && slices.Contains(session.NegotiatedOperations, op) && slices.Contains(session.QualifiedOperations, op)
	}
	a.AllowMCDPurchase = a.AllowMCDPrepare && s.cfg.MCDSelection.PurchaseEnabled
	for _, op := range []string{iscpworkbench.OperationMCDCheckoutCommit, iscpworkbench.OperationMCDOrdersQuery, iscpworkbench.OperationApprovalsDecide} {
		a.AllowMCDPurchase = a.AllowMCDPurchase && slices.Contains(session.NegotiatedOperations, op) && slices.Contains(session.QualifiedOperations, op)
	}
	a.AllowFiles = slices.Contains(session.QualifiedOperations, iscpworkbench.OperationExecutionInputPut)
	if !slices.Contains(session.QualifiedOperations, iscpworkbench.OperationToolsInvoke) {
		return a
	}
	for _, definition := range s.tools.Definitions() {
		if definition.Name == "mcd.purchase.status" {
			if a.AllowMCDStatus {
				a.AllowedTools = append(a.AllowedTools, definition.Name)
			}
			continue
		}
		if strings.HasPrefix(definition.Name, "mcd.purchase.") && !a.AllowMCDPurchase {
			continue
		}
		if definition.Name == "mcd.quote.prepare" && !a.AllowMCDPrepare {
			continue
		}
		if strings.HasPrefix(definition.Name, "mcd.") && !a.AllowMCDSelection {
			continue
		}
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
