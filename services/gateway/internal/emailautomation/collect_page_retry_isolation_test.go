package emailautomation

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestProviderRetryFailureAndNewOriginalShareValidPage(t *testing.T) {
	for _, id := range []string{app.EmailProviderGmail, app.EmailProviderOutlook, app.EmailProviderQQMail} {
		for _, listed := range []bool{false, true} {
			t.Run(id+map[bool]string{true: "/listed", false: "/outside_interval"}[listed], func(t *testing.T) {
				provider, _ := DefaultRegistry().Get(id)
				request := pageRequest()
				request.Provider = id
				wire := pageWire(t)
				wire["provider"] = id
				discovery := wire["discovery"].(app.EmailDiscoveryResult)
				discovery.Provider = id
				old := app.EmailCaptureTarget{AccountAddress: request.Discovery.AccountAddress, ProviderMessageID: "old", ProviderSelectionID: "thread-old", ProviderThreadID: "thread-old", Folder: "inbox"}
				if id != app.EmailProviderGmail {
					received := request.Discovery.IntervalStart.Add(226778200 * time.Nanosecond)
					if id == app.EmailProviderQQMail {
						received = received.Truncate(time.Second)
					}
					old.ReceivedAt = &received
				}
				if id == app.EmailProviderOutlook {
					old.ProviderNativeID = "native-old"
				}
				if listed {
					discovery.Candidates = append(discovery.Candidates, old)
					discovery.Coverage.ScannedRows++
				}
				request.Discovery.RetryTargets = []app.EmailCaptureTarget{old}
				wire["discovery"], wire["discovery_options"] = discovery, request.Discovery
				wire["captures"].([]any)[0].(map[string]any)["result"].(map[string]any)["provider"] = id
				wire["failures"] = []app.EmailPageFailure{{Target: old, Scope: app.EmailSyncFailureProviderOperational, ErrorCode: "email_provider_unavailable"}}
				output, err := decodePageResult(pageWireBytes(t, wire), provider, request)
				if err != nil || len(output.Captures) != 1 || len(output.Failures) != 1 {
					t.Fatalf("old per-mail failure rejected the fresh original: %v", err)
				}
				// Isolation must not authorize a different locator in the outcome.
				failures := wire["failures"].([]app.EmailPageFailure)
				failures[0].Target.ProviderSelectionID = "different"
				if _, err := decodePageResult(pageWireBytes(t, wire), provider, request); err == nil {
					t.Fatal("unbound retry outcome accepted")
				}
			})
		}
	}
}
