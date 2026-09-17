package emailautomation

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestTimelineProductionAllocatedEmptyRetriesSurviveWireRoundTrip(t *testing.T) {
	for _, id := range []string{"gmail", "qq_mail", "outlook"} {
		t.Run(id, func(t *testing.T) {
			provider, _ := DefaultRegistry().Get(id)
			request := pageRequest()
			request.Provider = id
			request.Discovery.ProviderMode = app.EmailProviderModeTimeRange
			// collectPages constructs this even when there are no retry failures.
			request.Discovery.RetryTargets = make([]app.EmailCaptureTarget, 0)
			wire := pageWire(t)
			wire["provider"], wire["status"] = id, "empty"
			wire["discovery_options"] = request.Discovery
			wire["captures"] = []any{}
			discovery := wire["discovery"].(app.EmailDiscoveryResult)
			discovery.Provider, discovery.Status = id, "empty"
			discovery.Candidates = []app.EmailCaptureTarget{}
			discovery.Coverage = app.EmailDiscoveryCoverage{Scope: "inbound_received", Lane: "recent_inbound", ScanComplete: true, BoundaryQualified: true}
			wire["discovery"] = discovery
			result, err := decodePageResult(pageWireBytes(t, wire), provider, request)
			if err != nil || !validPageResult(result, provider, request) {
				t.Fatalf("production empty retries rejected after omitempty round-trip: %v", err)
			}
			if result.DiscoveryOptions.RetryTargets != nil {
				t.Fatal("fixture must exercise omitted retry_targets decoding to nil")
			}
		})
	}
}

func TestRetryTargetEqualityPreservesExactDescriptorAndOrder(t *testing.T) {
	first := app.EmailCaptureTarget{ProviderMessageID: "one", RecoveryCapture: &app.EmailCaptureVersion{ManifestJSON: "original", ManifestSHA256: "hash"}}
	second := app.EmailCaptureTarget{ProviderMessageID: "two"}
	if !equalRetryTargets(nil, []app.EmailCaptureTarget{}) || !equalRetryTargets([]app.EmailCaptureTarget{first}, []app.EmailCaptureTarget{first}) {
		t.Fatal("equivalent retry lists rejected")
	}
	changed := first
	changed.RecoveryCapture = &app.EmailCaptureVersion{ManifestJSON: "changed", ManifestSHA256: "hash"}
	for _, other := range [][]app.EmailCaptureTarget{nil, {changed}, {second}} {
		if equalRetryTargets([]app.EmailCaptureTarget{first}, other) {
			t.Fatal("changed retry identity or descriptor accepted")
		}
	}
	if equalRetryTargets([]app.EmailCaptureTarget{first, second}, []app.EmailCaptureTarget{second, first}) {
		t.Fatal("changed retry order accepted")
	}
}
