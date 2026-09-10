package emailautomation

import (
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"testing"
	"time"
)

func TestIntakeAdmissionDoesNotInvalidateHumanApprovalVersion(t *testing.T) {
	st := store.NewMemoryStore()
	at := time.Now().UTC()
	setting, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: "gmail", Account: "default", Enabled: true, State: app.EmailStateReady, LastCheckedAt: &at}, 0)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeScriptRunner{probeResult: ProbeResult{Provider: "gmail", AccountHint: "o***@example.test", Generation: 7, Revision: 1, CheckedAt: at.Add(time.Minute)}}
	controller := NewController(st, DefaultRegistry(), nil, runner)
	for range 2 {
		binding, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
		if err != nil {
			t.Fatal(err)
		}
		if binding.SettingVersion != setting.Version || binding.BrowserCredentialGeneration != 7 {
			t.Fatalf("invalid intake binding: %#v", binding)
		}
	}
	current, _, err := st.GetEmailProviderSetting(t.Context(), "owner", "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != setting.Version || !current.LastCheckedAt.Equal(*setting.LastCheckedAt) || current.AccountHint != setting.AccountHint {
		t.Fatal("background probe rewrote human configuration")
	}
	if _, err := controller.AdmitIntake(t.Context(), "other", "gmail"); ErrorCode(err) != app.ToolErrorEmailNotConfigured {
		t.Fatal(err)
	}
	if len(runner.probeCalls) != 2 {
		t.Fatal("unauthorized intake reached the browser")
	}
}

func TestThreadInventoryRejectsDraftOmissionAndCrossThreadOrAccountMembers(t *testing.T) {
	provider, _ := DefaultRegistry().Get("outlook")
	binding := validReadRequest()
	binding.Provider = "outlook"
	target := app.EmailThreadTarget{AccountAddress: "owner@example.test", ProviderThreadID: "thread", ProviderSelectionID: "thread", Folder: "inbox"}
	request := app.EmailThreadRequest{Binding: binding, Thread: target, Limit: 50}
	raw := `{"schema_version":1,"provider":"outlook","status":"complete_for_observation","thread":{"account_address":"owner@example.test","provider_thread_id":"thread","provider_selection_id":"thread","folder":"inbox"},"members":[{"target":{"account_address":"owner@example.test","provider_message_id":"mail","provider_selection_id":"thread","provider_thread_id":"thread","folder":"inbox"},"direction":"inbound","draft":false,"read_state":"read"}],"coverage":{"scope":"thread","scan_complete":true,"scanned_rows":1,"unsupported_rows":0,"limited":false},"observed_at":"2026-09-08T00:00:00Z"}`
	for _, mutate := range []string{"valid", "draft omitted", "other account", "other thread"} {
		t.Run(mutate, func(t *testing.T) {
			var value map[string]any
			if json.Unmarshal([]byte(raw), &value) != nil {
				t.Fatal("fixture")
			}
			member := value["members"].([]any)[0].(map[string]any)
			mail := member["target"].(map[string]any)
			switch mutate {
			case "draft omitted":
				delete(member, "draft")
			case "other account":
				mail["account_address"] = "other@example.test"
			case "other thread":
				mail["provider_thread_id"] = "other"
			}
			bytes, _ := json.Marshal(value)
			browser := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: bytes}}
			_, err := NewPlaywrightRunner(browser).EnumerateThread(t.Context(), provider, request)
			if mutate == "valid" && err != nil || mutate != "valid" && ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
				t.Fatal(err)
			}
		})
	}
}

func TestRecentDiscoveryRejectsUnqualifiedCompletedBoundary(t *testing.T) {
	provider, _ := DefaultRegistry().Get("gmail")
	request := validReadRequest()
	start := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: "owner@example.test", IntervalStart: start, IntervalEnd: start.Add(time.Hour), Limit: 50}
	raw := `{"schema_version":1,"provider":"gmail","status":"partial","account_address":"owner@example.test","candidates":[],"coverage":{"scope":"inbox_loaded","lane":"recent_inbound","scan_complete":false,"scanned_rows":10,"unsupported_rows":10,"limited":true,"reason":"receipt_order_unqualified"},"observed_at":"2026-09-08T01:00:00Z"}`
	var output map[string]any
	_ = json.Unmarshal([]byte(raw), &output)
	for _, complete := range []bool{false, true} {
		coverage := output["coverage"].(map[string]any)
		coverage["scan_complete"] = complete
		coverage["limited"] = !complete
		if complete {
			output["status"] = "empty"
			coverage["unsupported_rows"] = 0
		}
		bytes, _ := json.Marshal(output)
		browser := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: bytes}}
		_, err := NewPlaywrightRunner(browser).Discover(t.Context(), provider, request)
		if !complete && err != nil || complete && ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
			t.Fatal(err)
		}
	}
}
