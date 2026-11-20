package gateway

import (
	"context"
	"os"
	"path/filepath"
	"time"

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

// linkRunMCDSelection builds the McDonald's ordering resources for a run
// started by a link device, as executionMCDSelection did for the previous
// execution envelope. The returned release closes them and removes the
// cart bookkeeping when the run ends.
func (s *Server) linkRunMCDSelection(ctx context.Context, authorization executionAuthorization, ownerID, clientID, installationID, requestID string) (*mcddining.Selection, func()) {
	if s.mcdSelection == nil || !authorization.AllowMCDSelection && !authorization.AllowMCDStatus {
		return nil, func() {}
	}
	root, err := os.MkdirTemp("", "sparkclaw-link-mcd-")
	if err != nil {
		return nil, func() {}
	}
	principal := mcdintegration.Principal{DeploymentID: s.cfg.Gateway.DeploymentID, OwnerID: ownerID, ClientID: clientID, InstallationID: installationID}
	var selection *mcddining.Selection
	if authorization.AllowMCDSelection {
		selection, _ = mcddining.NewSelection(ctx, s.mcdSelection, principal, requestID)
	}
	purchase := selection != nil && authorization.AllowMCDPurchase
	if selection == nil && authorization.AllowMCDStatus {
		selection, _ = mcddining.NewRecoverySelection(ctx, s.mcdSelection, principal, requestID)
	}
	if selection == nil {
		_ = os.RemoveAll(root)
		return nil, func() {}
	}
	selection.WithCart(filepath.Join(root, "mcd-cart"), mcdHostRoute{&s.browserRoutes, principal})
	selection.WithPurchase(purchase).WithStatus(authorization.AllowMCDStatus)
	return selection, func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = selection.Close(cleanup)
		_ = os.RemoveAll(root)
	}
}
