package gateway

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

func TestLinkExecutionAuthorizationComesFromThePrincipal(t *testing.T) {
	s, _ := credentialServer(t)
	desktop := link.Principal{Principal: contract.Principal{OwnerID: "owner", DeviceID: "desk", Class: contract.ClassDesktop}, Conditions: contract.Conditions{BrowserSurface: true, Attachments: true}}
	reverse := []string{linkReverseMCDAccountEnsure, linkReverseMCDCheckoutPrepare, linkReverseMCDCheckoutCommit, linkReverseMCDCheckoutAbort, linkReverseMCDOperationStatus, linkReverseMCDOrdersQuery}

	// With McDonald's switched off a device gets every other registered
	// tool, approval-governed ones included.
	a := s.linkExecutionAuthorization(desktop, reverse)
	var want []string
	for _, d := range s.tools.Definitions() {
		if !strings.HasPrefix(d.Name, "mcd.") {
			want = append(want, d.Name)
		}
	}
	if len(want) == 0 || !slices.Equal(a.AllowedTools, want) || !a.AllowFiles {
		t.Fatalf("tools %v, want %v", a.AllowedTools, want)
	}
	if a.AllowMCDSelection || a.AllowMCDStatus {
		t.Fatal("McDonald's allowed while switched off")
	}
	if a.AuthorizeTool == nil || a.AuthorizeTool(context.Background(), "files.read") == nil {
		t.Fatal("a device missing from the directory still passes the dispatch check")
	}

	s.cfg.MCDSelection.Enabled, s.cfg.MCDSelection.CartEnabled, s.cfg.MCDSelection.PurchaseEnabled = true, true, true
	s.mcdSelection = &mcdintegration.Manager{}
	a = s.linkExecutionAuthorization(desktop, reverse)
	if !a.AllowMCDSelection || !a.AllowMCDPrepare || !a.AllowMCDPurchase || !a.AllowMCDStatus {
		t.Fatalf("a desktop serving the ordering operations is not authorized: %+v", a)
	}
	// Each condition closes only what depends on it.
	if a := s.linkExecutionAuthorization(desktop, []string{linkReverseMCDAccountEnsure}); !a.AllowMCDSelection || a.AllowMCDPrepare || a.AllowMCDStatus {
		t.Fatalf("selection only: %+v", a)
	}
	phone := desktop
	phone.Class, phone.Conditions = contract.ClassPhone, contract.Conditions{}
	if a := s.linkExecutionAuthorization(phone, reverse); a.AllowMCDSelection || a.AllowMCDStatus || a.AllowFiles {
		t.Fatalf("a device without the browser surface: %+v", a)
	}
	s.cfg.MCDSelection.PurchaseEnabled = false
	if a := s.linkExecutionAuthorization(desktop, reverse); !a.AllowMCDPrepare || a.AllowMCDPurchase {
		t.Fatalf("purchase switched off: %+v", a)
	}
}
