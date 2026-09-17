package emailmanagement

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func retryBudgetTarget(manifest string) app.EmailCaptureTarget {
	return app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "one", ProviderSelectionID: "one", RecoveryCapture: &app.EmailCaptureVersion{ManifestJSON: manifest}}
}

func TestTimelineRetryBudgetOversizedLegalManifestGetsExclusiveRound(t *testing.T) {
	// The JSON document is <=64KiB, but escaped backslashes make its containing
	// descriptor exceed the ordinary 64KiB budget.
	raw := `{"value":"` + strings.Repeat(`\\`, (64<<10-12)/2) + `"}`
	if len(raw) > 64<<10 || !json.Valid([]byte(raw)) {
		t.Fatal("invalid test manifest")
	}
	var budget timelineRetryBudget
	if admitted, err := budget.admit(retryBudgetTarget(raw)); err != nil || !admitted {
		t.Fatalf("head item starved: %v %v", admitted, err)
	}
	if budget.bytes <= timelineRetrySoftBytes {
		t.Fatal("fixture did not exceed soft limit")
	}
	if admitted, err := budget.admit(retryBudgetTarget(`{}`)); err != nil || admitted {
		t.Fatalf("large round not exclusive: %v %v", admitted, err)
	}
	// A skipped item is eligible in the next fresh poll, with no terminal mark.
	next := timelineRetryBudget{}
	if admitted, err := next.admit(retryBudgetTarget(`{}`)); err != nil || !admitted {
		t.Fatalf("next round did not carry deferred target: %v %v", admitted, err)
	}
}

func TestTimelineRetryBudgetDoesNotOvercountHTMLEscapes(t *testing.T) {
	target := retryBudgetTarget(`{"value":"` + strings.Repeat("<&>", 10000) + `"}`)
	defaultWire, _ := json.Marshal(target)
	if len(defaultWire) < timelineRetrySingleBytes {
		t.Fatal("fixture must inflate under Go default encoding")
	}
	var budget timelineRetryBudget
	if admitted, err := budget.admit(target); err != nil || !admitted {
		t.Fatalf("HTML content starved: %v %v", admitted, err)
	}
	if budget.bytes > timelineRetrySoftBytes {
		t.Fatal("HTML escaping inflated actual wire budget")
	}
}

func TestTimelineRetryBudgetHardLimitFailsExplicitly(t *testing.T) {
	var budget timelineRetryBudget
	if admitted, err := budget.admit(retryBudgetTarget(strings.Repeat("x", timelineRetrySingleBytes))); err == nil || admitted {
		t.Fatalf("oversized item silently deferred: %v %v", admitted, err)
	}
}

func TestTimelineRetryBudgetDeferredLargeTargetFitsNextRound(t *testing.T) {
	large := retryBudgetTarget(`{"value":"` + strings.Repeat(`\\`, (64<<10-12)/2) + `"}`)
	var first timelineRetryBudget
	if ok, err := first.admit(retryBudgetTarget(`{}`)); err != nil || !ok {
		t.Fatal("small head rejected")
	}
	if ok, err := first.admit(large); err != nil || ok {
		t.Fatal("large tail should remain open")
	}
	var next timelineRetryBudget
	if ok, err := next.admit(large); err != nil || !ok {
		t.Fatalf("deferred head starved: %v %v", ok, err)
	}
	// The same target occurs in options, capture outcome and journal entry.
	// Even its hard admission ceiling leaves >500KiB for non-retry metadata;
	// the actual complete journal still has its independent 1MiB rejection gate.
	if 3*next.bytes >= 1<<20 {
		t.Fatal("descriptor replicas alone exhaust journal")
	}
}
