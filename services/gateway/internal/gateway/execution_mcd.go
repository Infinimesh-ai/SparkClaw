package gateway

import (
	"context"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcddining"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
)

func (s *Server) executionMCDSelection(ctx context.Context, e execution.Envelope, root string, budget *execution.Budget) *mcddining.Selection {
	authorization, ok := ctx.Value(executionAuthorizationKey{}).(executionAuthorization)
	if !ok || s.mcdSelection == nil || !authorization.AllowMCDSelection && !authorization.AllowMCDStatus {
		return nil
	}
	principal := mcdintegration.Principal{DeploymentID: s.cfg.Gateway.DeploymentID, OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID}
	var selection *mcddining.Selection
	if authorization.AllowMCDSelection {
		selection, _ = mcddining.NewSelection(ctx, s.mcdSelection, principal, e.RequestID)
	}
	purchase := selection != nil && authorization.AllowMCDPurchase
	if selection == nil && authorization.AllowMCDStatus {
		selection, _ = mcddining.NewRecoverySelection(ctx, s.mcdSelection, principal, e.RequestID)
	}
	if selection == nil || budget.Reserve(4<<20) != nil {
		return nil
	}
	// Status reconciliation uses the original host and cart bookkeeping even
	// after rollback has disabled new cart writes or purchases.
	selection.WithCart(filepath.Join(root, "mcd-cart"), mcdHostRoute{&s.browserRoutes, principal})
	selection.WithPurchase(purchase).WithStatus(authorization.AllowMCDStatus)
	return selection
}
