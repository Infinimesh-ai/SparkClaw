package emailautomation

import (
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

func warmIntakeRequest(binding AdmissionResult) ReadRequest {
	request := pageRequest()
	request.SettingVersion = binding.SettingVersion
	request.Account = binding.Account
	request.BrowserCredentialGeneration = binding.BrowserCredentialGeneration
	request.ProbeRevision = binding.ProbeRevision
	request.Discovery.ProviderMode = app.EmailProviderModeTimeRange
	return request
}

func warmIntakeWire(t *testing.T, request ReadRequest, observed time.Time) map[string]any {
	t.Helper()
	wire := pageWire(t)
	wire["status"], wire["captures"] = "empty", []any{}
	wire["discovery_options"], wire["observed_at"] = request.Discovery, observed
	discovery := wire["discovery"].(app.EmailDiscoveryResult)
	discovery.Status, discovery.Candidates, discovery.ObservedAt = "empty", []app.EmailCaptureTarget{}, observed
	discovery.Coverage = app.EmailDiscoveryCoverage{Scope: "inbound_received", Lane: "recent_inbound", ScanComplete: true, BoundaryQualified: true}
	wire["discovery"] = discovery
	return wire
}

func TestIntakeCompleteTimelineRenewsAcrossTwentyMinutePolls(t *testing.T) {
	controller, browser, st, now := intakeCacheFixture(t)
	first, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := st.GetEmailProviderSetting(t.Context(), "owner", "gmail")
	for round := 0; round < 3; round++ {
		*now = now.Add(20 * time.Minute)
		binding, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
		if err != nil || !binding.ValidatedAt.Equal(first.ValidatedAt) {
			t.Fatalf("warm admission changed actual probe timestamp: %v", err)
		}
		request := warmIntakeRequest(binding)
		browser.result = browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: pageWireBytes(t, warmIntakeWire(t, request, *now))}
		if _, err := controller.CollectPageForOwner(t.Context(), "owner", request); err != nil {
			t.Fatal(err)
		}
	}
	probes := 0
	for _, request := range browser.requests {
		if request.Operation == "probe" {
			probes++
		}
	}
	if probes != 1 {
		t.Fatalf("normal twenty-minute rounds launched %d probes", probes)
	}
	after, _, _ := st.GetEmailProviderSetting(t.Context(), "owner", "gmail")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read health modified human send approval or settings")
	}
	*now = now.Add(intakeProbeTTL)
	browser.result = browsercontrol.ScriptExecutionResult{}
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil || browser.requests[len(browser.requests)-1].Operation != "probe" {
		t.Fatalf("idle expiry did not re-probe: %v", err)
	}
}

func TestIntakeTimelineUnhealthyOrReplayedResultsDoNotRenew(t *testing.T) {
	for _, mode := range []string{"replay", "future", "partial", "failure", "account_mismatch", "invalid", "script_error"} {
		t.Run(mode, func(t *testing.T) {
			controller, browser, _, now := intakeCacheFixture(t)
			binding, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
			if err != nil {
				t.Fatal(err)
			}
			*now = now.Add(20 * time.Minute)
			request := warmIntakeRequest(binding)
			wire := warmIntakeWire(t, request, *now)
			d := wire["discovery"].(app.EmailDiscoveryResult)
			switch mode {
			case "replay":
				d.ObservedAt = now.Add(-time.Minute)
			case "future":
				d.ObservedAt = now.Add(time.Minute)
			case "partial":
				d.Status, d.Coverage.ScanComplete, d.Coverage.BoundaryQualified = "partial", false, false
				d.Coverage.Limited, d.Coverage.Reason = true, "network_page_continues"
			case "failure":
				target := app.EmailCaptureTarget{AccountAddress: request.Discovery.AccountAddress, ProviderMessageID: "mail", ProviderSelectionID: "mail", Folder: "inbox"}
				d.Status, d.Candidates, d.Coverage.ScannedRows = "listed", []app.EmailCaptureTarget{target}, 1
				wire["status"], wire["failures"] = "partial", []app.EmailPageFailure{{Target: target, ErrorCode: "email_script_timeout", Scope: app.EmailSyncFailureProviderOperational}}
			case "account_mismatch":
				d.AccountAddress = "other@example.test"
			case "invalid":
				delete(wire, "page_id")
			}
			wire["discovery"] = d
			browser.result = browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: pageWireBytes(t, wire)}
			if mode == "script_error" {
				browser.result.State = "failed"
			}
			_, err = controller.CollectPageForOwner(t.Context(), "owner", request)
			if (mode == "replay" || mode == "future" || mode == "partial" || mode == "failure") && err != nil {
				t.Fatalf("valid non-renewable output rejected: %v", err)
			}
			*now = now.Add(10 * time.Minute)
			browser.result = browsercontrol.ScriptExecutionResult{}
			if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil || browser.requests[len(browser.requests)-1].Operation != "probe" {
				t.Fatalf("non-fresh result renewed proof: %v", err)
			}
		})
	}
}

func TestIntakeRenewalCannotReviveExpiredOrMismatchedBindings(t *testing.T) {
	for _, mode := range []string{"owner", "provider", "setting", "account", "credential", "revision", "expired", "clock_rollback", "legacy", "partial_capture"} {
		t.Run(mode, func(t *testing.T) {
			controller, _, _, now := intakeCacheFixture(t)
			binding, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
			if err != nil {
				t.Fatal(err)
			}
			key := intakeProbeKey{ownerID: "owner", providerID: "gmail"}
			original := controller.intakeProbes[key]
			*now = now.Add(20 * time.Minute)
			request := warmIntakeRequest(binding)
			owner := "owner"
			switch mode {
			case "owner":
				owner = "other"
			case "provider":
				request.Provider = "outlook"
			case "setting":
				request.SettingVersion++
			case "account":
				request.Account = "other"
			case "credential":
				request.BrowserCredentialGeneration++
			case "revision":
				request.ProbeRevision++
			case "expired":
				*now = original.cachedAt.Add(intakeProbeTTL)
			case "clock_rollback":
				*now = original.cachedAt.Add(-time.Second)
			case "legacy":
				request.Discovery.ProviderMode = ""
			}
			output := app.EmailPageResult{Status: "empty", Discovery: app.EmailDiscoveryResult{ObservedAt: *now, Coverage: app.EmailDiscoveryCoverage{ScanComplete: true, BoundaryQualified: true}}}
			if mode == "partial_capture" {
				output.Status = "collected"
				output.Captures = []app.EmailPageCapture{{Result: ReadResult{Status: "partial"}}}
			}
			controller.renewIntakeProbe(owner, request, output, *now)
			if !reflect.DeepEqual(original, controller.intakeProbes[key]) {
				t.Fatal("unbound result renewed proof")
			}
		})
	}
}
