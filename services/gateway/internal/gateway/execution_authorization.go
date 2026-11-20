package gateway

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// executionAuthorization is trusted local state derived only from the
// device the link connector authenticated (its Principal and declared
// conditions). It is never decoded from a request.
type executionAuthorization struct {
	AllowedTools      []string
	AuthorizeTool     func(context.Context, string) error
	AllowFiles        bool
	AllowMCDSelection bool
	AllowMCDPrepare   bool
	AllowMCDPurchase  bool
	AllowMCDStatus    bool
}

// The McDonald's reverse operations a desktop serves from its built-in
// browser (link contract section 7).
const (
	linkReverseMCDAccountEnsure   = "mcd.account.ensure"
	linkReverseMCDCheckoutPrepare = "mcd.checkout.prepare"
	linkReverseMCDCheckoutCommit  = "mcd.checkout.commit"
	linkReverseMCDCheckoutAbort   = "mcd.checkout.abort"
	linkReverseMCDOperationStatus = "mcd.operation.status"
	linkReverseMCDOrdersQuery     = "mcd.orders.query"
)

// linkExecutionAuthorization is what a run started by this device may use
// (design 3.9: every feature is open unless the device lacks the
// condition). A trusted device gets files and every registered tool,
// including approval-governed ones, because every link device resolves
// approvals with approval.resolve. McDonald's ordering additionally needs
// the browser surface and the reverse operations the device declared at
// hello, and stays behind the backend's own configuration switches. Each
// dispatch rechecks that the device is still trusted.
func (s *Server) linkExecutionAuthorization(p link.Principal, reverse []string) executionAuthorization {
	a := executionAuthorization{AllowedTools: []string{}, AllowFiles: p.Conditions.Attachments}
	serves := func(ops ...string) bool {
		for _, op := range ops {
			if !slices.Contains(reverse, op) {
				return false
			}
		}
		return true
	}
	mcd := s.cfg.MCDSelection.Enabled && s.mcdSelection != nil && p.Conditions.BrowserSurface
	a.AllowMCDStatus = mcd && serves(linkReverseMCDOperationStatus, linkReverseMCDOrdersQuery)
	a.AllowMCDSelection = mcd && serves(linkReverseMCDAccountEnsure)
	a.AllowMCDPrepare = a.AllowMCDSelection && s.cfg.MCDSelection.CartEnabled && serves(linkReverseMCDCheckoutPrepare, linkReverseMCDCheckoutAbort, linkReverseMCDOperationStatus)
	a.AllowMCDPurchase = a.AllowMCDPrepare && s.cfg.MCDSelection.PurchaseEnabled && serves(linkReverseMCDCheckoutCommit, linkReverseMCDOrdersQuery)
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
		a.AllowedTools = append(a.AllowedTools, definition.Name)
	}
	deviceID := p.DeviceID
	a.AuthorizeTool = func(context.Context, string) error {
		if !s.linkDeviceTrusted(deviceID) {
			return errors.New("this device is no longer trusted on the SparkClaw console")
		}
		return nil
	}
	return a
}

// linkDeviceTrusted reports whether the device is still in the directory
// with a known class.
func (s *Server) linkDeviceTrusted(deviceID string) bool {
	if s.link == nil {
		return false
	}
	rec, ok := s.link.directory.Lookup(deviceID)
	return ok && contract.ValidClass(rec.Type)
}

// linkDeviceReverse lists the reverse operations the device serves on its
// open connections.
func (s *Server) linkDeviceReverse(req *link.Request) []string {
	if req.Conn != nil {
		return slices.Clone(req.Conn.Reverse)
	}
	if s.link == nil {
		return nil
	}
	var out []string
	for _, conn := range s.link.service.Connections(req.Principal.DeviceID) {
		for _, op := range conn.Reverse {
			if !slices.Contains(out, op) {
				out = append(out, op)
			}
		}
	}
	return out
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
