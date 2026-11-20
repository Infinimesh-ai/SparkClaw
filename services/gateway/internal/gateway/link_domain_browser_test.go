package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

func TestLinkBrowserAndMCDMethodsNeedTheBrowserSurface(t *testing.T) {
	b, desk, ctx := linkDomainTestDevice(t)
	var receipt struct {
		Fences []any `json:"fences"`
	}
	if err := linkDomainCall(ctx, desk, "browser.receipt", map[string]any{}, "", &receipt); err != nil || receipt.Fences == nil {
		t.Fatalf("receipt %+v %v", receipt, err)
	}
	if err := linkDomainCall(ctx, desk, "browser.reconcile", map[string]any{"command_id": "missing", "digest": "x", "outcome": "observed_completed"}, newLinkUUID(), nil); contract.AsError(err).Reason == "" {
		t.Fatalf("reconcile of an unknown command: %v", err)
	}
	// McDonald's ordering stays behind the backend's own switch.
	if err := linkDomainCall(ctx, desk, "mcd.session.status", map[string]any{}, "", nil); contract.AsError(err).Reason != "mcd_selection_unavailable" {
		t.Fatal(err)
	}
	phone, _, rec, _ := newLinkTestDevice(t, b, contract.ClassPhone, contract.Conditions{Screen: contract.ScreenSmall})
	writeLinkDirectory(t, b.stateDir, rec)
	pctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	phone.Start(pctx)
	defer phone.Close()
	if phone.WaitReady(pctx) != nil {
		t.Fatal("not ready")
	}
	for _, method := range []string{"browser.receipt", "mcd.session.status"} {
		if err := linkDomainCall(pctx, phone, method, map[string]any{}, "", nil); contract.AsError(err).Code != contract.CodeUnsupportedCapability {
			t.Fatalf("%s on a phone: %v", method, err)
		}
	}
}
