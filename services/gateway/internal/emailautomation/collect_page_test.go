package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func pageRequest() ReadRequest {
	request := validReadRequest()
	request.InvocationID = "email_page_job_recent_inbound"
	start := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: "owner@example.test", IntervalStart: start, IntervalEnd: start.Add(time.Hour), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}
	return request
}

func TestTimelineCaptureIdentityIgnoresCheckpointRevision(t *testing.T) {
	target := app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "message", ProviderSelectionID: "selection", Folder: "inbox"}
	interval := "email_changes_" + strings.Repeat("a", 64)
	first := PageCaptureInvocationID(interval+"_r1", "gmail", target)
	if first != PageCaptureInvocationID(interval+"_r2", "gmail", target) {
		t.Fatal("overflow confirmation changed original identity")
	}
	if first != PageCaptureInvocationID("email_changes_"+strings.Repeat("b", 64)+"_r1", "gmail", target) {
		t.Fatal("boundary overlap in a later interval changed original identity")
	}
	if PageCaptureInvocationID("legacy_r1", "gmail", target) == PageCaptureInvocationID("legacy_r2", "gmail", target) {
		t.Fatal("revision stripping affected legacy invocation IDs")
	}
}
func pageWire(t *testing.T) map[string]any {
	t.Helper()
	target := app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "message", ProviderSelectionID: "selection", Folder: "inbox"}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(validReadOutput()), &receipt); err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"schema_version": 1, "provider": "gmail", "status": "partial", "account_address": target.AccountAddress, "page_id": "page_" + strings.Repeat("f", 64),
		"discovery_options": pageRequest().Discovery,
		"discovery":         app.EmailDiscoveryResult{SchemaVersion: 1, Provider: "gmail", Status: "partial", AccountAddress: target.AccountAddress, Candidates: []app.EmailCaptureTarget{target}, Coverage: app.EmailDiscoveryCoverage{Scope: "inbound_received", Lane: "recent_inbound", ScannedRows: 1, Limited: true, Reason: "network_page_continues"}, ObservedAt: time.Now().UTC()},
		"captures":          []any{map[string]any{"target": target, "result": receipt}}, "failures": []app.EmailPageFailure{}, "observed_at": time.Now().UTC(),
	}
}
func pageWireBytes(t *testing.T, wire map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCollectPageRunnerBindsSingleTimelineScriptWithoutPageAck(t *testing.T) {
	provider, _ := DefaultRegistry().Get("gmail")
	request := pageRequest()
	browser := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: pageWireBytes(t, pageWire(t))}}
	output, err := NewPlaywrightRunner(browser).CollectPage(t.Context(), provider, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(browser.requests) != 1 || browser.requests[0].Operation != "collect_page" || browser.requests[0].ScriptID != provider.CollectPage.ID || len(output.Captures) != 1 {
		t.Fatal("page did not use one collect script")
	}
	if input := browser.requests[0].Input.(map[string]any); input["ack_page_id"] != nil || input["discovery"] != request.Discovery {
		t.Fatal("timeline gained a page acknowledgement or lost discovery binding")
	}
	if capture := output.Captures[0].Result; capture.BrowserCredentialGeneration != 7 || capture.ScriptRevision != provider.CollectPage.Revision {
		t.Fatal("receipt omitted actual script binding")
	}
	if provider.CollectPage.Timeout != 30*time.Minute {
		t.Fatal("page budget differs from Controller contract")
	}
}

func TestCollectPageRejectsUnboundRequestsBeforeBrowser(t *testing.T) {
	provider, _ := DefaultRegistry().Get("gmail")
	for name, mutate := range map[string]func(*ReadRequest){
		"no discovery": func(r *ReadRequest) { r.Discovery = nil }, "too many": func(r *ReadRequest) { r.Discovery.Limit = 51 }, "zero limit": func(r *ReadRequest) { r.Discovery.Limit = 0 },
		"wrong lane": func(r *ReadRequest) { r.Discovery.Lane = "sent" }, "no interval": func(r *ReadRequest) { r.Discovery.IntervalStart = time.Time{}; r.Discovery.IntervalEnd = time.Time{} },
		"target":               func(r *ReadRequest) { r.Target = &app.EmailCaptureTarget{} },
		"retired mode":         func(r *ReadRequest) { r.Discovery.ProviderMode = "" },
		"unknown mode":         func(r *ReadRequest) { r.Discovery.ProviderMode = "legacy" },
		"retired continuation": func(r *ReadRequest) { r.Discovery.Continuation = "n1:" + strings.Repeat("a", 64) + ":1" },
		"account":              func(r *ReadRequest) { r.Discovery.AccountAddress = "" }, "scope": func(r *ReadRequest) { r.OwnerScope = "../owner" }, "generation": func(r *ReadRequest) { r.BrowserCredentialGeneration = 8 },
	} {
		t.Run(name, func(t *testing.T) {
			request := pageRequest()
			mutate(&request)
			browser := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}}
			if _, err := NewPlaywrightRunner(browser).CollectPage(t.Context(), provider, request); err == nil || len(browser.requests) != 0 {
				t.Fatalf("unbound request reached browser: %v", err)
			}
		})
	}
}

func TestCollectPageRejectsAmbiguousOrUnverifiableWireReceipts(t *testing.T) {
	provider, _ := DefaultRegistry().Get("gmail")
	for name, mutate := range map[string]func(map[string]any){
		"missing page":              func(w map[string]any) { delete(w, "page_id") },
		"missing discovery options": func(w map[string]any) { delete(w, "discovery_options") },
		"missing failures":          func(w map[string]any) { delete(w, "failures") },
		"wrong provider":            func(w map[string]any) { w["provider"] = "outlook" },
		"wrong account":             func(w map[string]any) { w["account_address"] = "other@example.test" },
		"missing outcome":           func(w map[string]any) { w["captures"] = []any{} },
		"duplicate capture":         func(w map[string]any) { w["captures"] = append(w["captures"].([]any), w["captures"].([]any)[0]) },
		"duplicate outcome": func(w map[string]any) {
			target := w["discovery"].(app.EmailDiscoveryResult).Candidates[0]
			w["failures"] = []app.EmailPageFailure{{Target: target, ErrorCode: "email_script_timeout", Scope: app.EmailSyncFailureProviderOperational}}
		},
		"foreign target": func(w map[string]any) {
			capture := w["captures"].([]any)[0].(map[string]any)
			target := capture["target"].(app.EmailCaptureTarget)
			target.ProviderMessageID = "other"
			capture["target"] = target
		},
		"foreign selection": func(w map[string]any) {
			capture := w["captures"].([]any)[0].(map[string]any)
			target := capture["target"].(app.EmailCaptureTarget)
			target.ProviderSelectionID = "other"
			capture["target"] = target
		},
		"source traversal": func(w map[string]any) {
			receipt := w["captures"].([]any)[0].(map[string]any)["result"].(map[string]any)
			receipt["capture"].(map[string]any)["manifest_path"] = "../private"
		},
		"missing receipt count": func(w map[string]any) {
			receipt := w["captures"].([]any)[0].(map[string]any)["result"].(map[string]any)
			delete(receipt["capture"].(map[string]any), "attachments_count")
		},
		"false empty": func(w map[string]any) { w["status"] = "empty" },
		"bad discovery": func(w map[string]any) {
			d := w["discovery"].(app.EmailDiscoveryResult)
			d.Coverage.Lane = "unread"
			w["discovery"] = d
		},
		"unbounded failure": func(w map[string]any) {
			target := w["discovery"].(app.EmailDiscoveryResult).Candidates[0]
			w["captures"] = []any{}
			w["failures"] = []app.EmailPageFailure{{Target: target, ErrorCode: "private raw error text", Scope: app.EmailSyncFailureProviderOperational}}
		},
		"unexpected content": func(w map[string]any) { w["body"] = "private" },
	} {
		t.Run(name, func(t *testing.T) {
			wire := pageWire(t)
			mutate(wire)
			if _, err := decodePageResult(pageWireBytes(t, wire), provider, pageRequest()); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
				t.Fatalf("invalid wire accepted: %v", err)
			}
		})
	}
}

func TestCollectPageHandlesFiftyBoundedLongIdentitiesBeyondSmallScriptCap(t *testing.T) {
	provider, _ := DefaultRegistry().Get("gmail")
	wire := pageWire(t)
	discovery := wire["discovery"].(app.EmailDiscoveryResult)
	discovery.Candidates = nil
	failures := make([]app.EmailPageFailure, 0, 50)
	for i := range 50 {
		target := app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: fmt.Sprintf("mail%d", i) + strings.Repeat("a", 950), ProviderSelectionID: strings.Repeat("b", 950), Folder: "inbox"}
		discovery.Candidates = append(discovery.Candidates, target)
		failures = append(failures, app.EmailPageFailure{Target: target, ErrorCode: "email_script_timeout", Scope: app.EmailSyncFailureProviderOperational})
	}
	discovery.Coverage.ScannedRows = 50
	wire["discovery"] = discovery
	wire["captures"] = []any{}
	wire["failures"] = failures
	raw := pageWireBytes(t, wire)
	if len(raw) <= maxScriptOutputBytes {
		t.Fatal("fixture did not exercise page output budget")
	}
	if result, err := decodePageResult(raw, provider, pageRequest()); err != nil || len(result.Failures) != 50 {
		t.Fatalf("bounded full page rejected: %v", err)
	}
	raw = append(make([]byte, maxPageOutputBytes), raw...)
	if _, err := decodePageResult(raw, provider, pageRequest()); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
		t.Fatal("oversized page accepted")
	}
}

type capturedPageRunner struct {
	*fakeScriptRunner
	page app.EmailPageResult
}

func (r *capturedPageRunner) CollectPage(context.Context, Provider, ReadRequest) (app.EmailPageResult, error) {
	return r.page, nil
}

func TestCollectPageControllerVerifiesDurableBytesAndStableTargetInvocation(t *testing.T) {
	root, request, result, source := captureFixture(t)
	request.InvocationID = "email_page_job_recent_inbound"
	request.Discovery = pageRequest().Discovery
	target := app.EmailCaptureTarget{AccountAddress: request.Discovery.AccountAddress, ProviderMessageID: "message", ProviderSelectionID: "selection", Folder: "inbox"}
	manifestPath := filepath.Join(root, filepath.FromSlash(result.Capture.ManifestPath))
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	invocation := PageCaptureInvocationID(request.InvocationID, request.Provider, target)
	manifest["invocation_id"] = invocation
	manifest["account_address"] = target.AccountAddress
	manifest["provider_message_id"] = target.ProviderMessageID
	writeManifest := func() {
		t.Helper()
		raw, _ := json.Marshal(manifest)
		if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		result.Capture.ManifestSHA256 = captureTestDigest(raw)
	}
	writeManifest()
	result.BrowserCredentialGeneration = request.BrowserCredentialGeneration
	result.ScriptRevision = 1
	provider, _ := DefaultRegistry().Get(request.Provider)
	page, err := decodePageResult(pageWireBytes(t, pageWire(t)), provider, pageRequest())
	if err != nil {
		t.Fatal(err)
	}
	page.Captures[0].Result = result
	st := store.NewMemoryStore()
	checked := time.Now().UTC()
	setting, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: request.Provider, Account: request.Account, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked}, 0)
	if err != nil {
		t.Fatal(err)
	}
	request.SettingVersion = setting.Version
	runner := &capturedPageRunner{fakeScriptRunner: &fakeScriptRunner{}, page: page}
	controller := NewController(st, DefaultRegistry(), nil, runner).WithCaptureWorkspaceRoot(root)
	if _, err := controller.CollectPageForOwner(t.Context(), "owner", request); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CollectPageForOwner(t.Context(), "other", request); ErrorCode(err) != app.ToolErrorEmailAdmissionStale {
		t.Fatal("cross-owner page accepted")
	}
	manifest["invocation_id"] = "another-page"
	writeManifest()
	if _, err := controller.CollectPageForOwner(t.Context(), "owner", request); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
		t.Fatal("foreign page invocation accepted")
	}
	manifest["invocation_id"] = invocation
	writeManifest()
	if err := os.WriteFile(source, []byte("tampered source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CollectPageForOwner(t.Context(), "owner", request); err != nil {
		t.Fatalf("same-size source should be deferred to the one-pass parser: %v", err)
	}
	if err := os.Truncate(source, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CollectPageForOwner(t.Context(), "owner", request); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
		t.Fatal("source with the wrong durable size accepted")
	}
}

func TestCollectPageRejectsLegacyCheckpointInterval(t *testing.T) {
	provider, _ := DefaultRegistry().Get("gmail")
	request := pageRequest()
	request.Discovery.Lane = "recent_inbound"
	request.Discovery.IntervalStart = time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	request.Discovery.IntervalEnd = request.Discovery.IntervalStart.Add(time.Hour)
	wire := pageWire(t)
	original := *request.Discovery
	original.IntervalStart = original.IntervalStart.Add(-24 * time.Hour)
	original.IntervalEnd = original.IntervalEnd.Add(-24 * time.Hour)
	original.Continuation = strings.Repeat("a", 64) + ":1"
	wire["discovery_options"] = original
	discovery := wire["discovery"].(app.EmailDiscoveryResult)
	discovery.Coverage.Lane = "recent_inbound"
	discovery.Coverage.Scope = "inbound_received"
	wire["discovery"] = discovery
	if _, err := decodePageResult(pageWireBytes(t, wire), provider, request); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
		t.Fatal("retired checkpoint replaced current timeline interval")
	}
	original.Continuation = ""
	wire["discovery_options"] = original
	if _, err := decodePageResult(pageWireBytes(t, wire), provider, request); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
		t.Fatal("different timeline interval accepted without continuation")
	}
	original.AccountAddress = "other@example.test"
	wire["discovery_options"] = original
	if _, err := decodePageResult(pageWireBytes(t, wire), provider, request); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
		t.Fatal("foreign checkpoint accepted")
	}
}

func TestPageCaptureInvocationMatchesControllerHashContract(t *testing.T) {
	target := app.EmailCaptureTarget{AccountAddress: "Owner@Example.Test", ProviderMessageID: "message", ProviderSelectionID: "selection"}
	want := "email_capture_c3ac21b3fbf0a810eddacec1bdf30c1221425019c39edb35686a4d7b7b1ff496"
	if got := PageCaptureInvocationID("email_page_job_recent_inbound", "gmail", target); got != want {
		t.Fatalf("cross-language journal invocation differs: %s", got)
	}
	target.ProviderSelectionID = "changed-selection"
	if PageCaptureInvocationID("email_page_job_recent_inbound", "gmail", target) != want {
		t.Fatal("selection navigation changed stable message identity")
	}
}

func TestPageCaptureInvocationSharesLanesAndPreservesMailboxIdentity(t *testing.T) {
	target := app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "message", ProviderSelectionID: "selection"}
	baseline := PageCaptureInvocationID("email_page_mailbox", "gmail", target)
	for _, suffix := range []string{"_recent_observation", "_recent_inbound"} {
		if PageCaptureInvocationID("email_page_mailbox"+suffix, "gmail", target) != baseline {
			t.Fatalf("lane %s changed immutable source identity", suffix)
		}
	}
	for _, namespace := range []string{"email_page_other_recent_inbound", "email_page_mailbox_recent_inbound_extra"} {
		if PageCaptureInvocationID(namespace, "gmail", target) == baseline {
			t.Fatal("different mailbox or nonterminal suffix collapsed")
		}
	}
	target.AccountAddress = "other@example.test"
	if PageCaptureInvocationID("email_page_mailbox_recent_inbound", "gmail", target) == baseline {
		t.Fatal("different account collapsed")
	}
	// Inputs without one of the three known terminal suffixes retain their exact
	// historical hashing semantics; suffix-like text in the middle is significant.
	target.AccountAddress = "owner@example.test"
	digest := sha256.Sum256([]byte("email_page_mailbox_recent_inbound_extra\ngmail\nowner@example.test\nmessage"))
	if PageCaptureInvocationID("email_page_mailbox_recent_inbound_extra", "gmail", target) != "email_capture_"+hex.EncodeToString(digest[:]) {
		t.Fatal("unknown suffix changed hashing")
	}
}

func TestCollectPageEmptyBatchPreservesPartialDiscoveryCoverage(t *testing.T) {
	provider, _ := DefaultRegistry().Get("gmail")
	for _, reason := range []string{"network_page_continues", "network_page_changed", "folder_scope_and_pagination_unqualified", "network_rows_unqualified", "unknown_evidence_gap"} {
		for _, unsupported := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/%d", reason, unsupported), func(t *testing.T) {
				wire := pageWire(t)
				wire["status"] = "empty"
				wire["captures"] = []any{}
				discovery := wire["discovery"].(app.EmailDiscoveryResult)
				discovery.Candidates = []app.EmailCaptureTarget{}
				discovery.Coverage.Reason = reason
				discovery.Coverage.UnsupportedRows = unsupported
				discovery.Coverage.ScannedRows = unsupported
				wire["discovery"] = discovery
				output, err := decodePageResult(pageWireBytes(t, wire), provider, pageRequest())
				allowed := unsupported == 0 && (reason == "network_page_continues" || reason == "network_page_changed" || reason == "folder_scope_and_pagination_unqualified")
				if !allowed {
					if ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
						t.Fatalf("unqualified empty accepted: %v", err)
					}
					wire["status"] = "partial"
					if _, err = decodePageResult(pageWireBytes(t, wire), provider, pageRequest()); err != nil {
						t.Fatalf("valid partial evidence rejected: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if output.Status != "empty" || output.Discovery.Status != "partial" || output.Discovery.Coverage.ScanComplete || output.Discovery.Coverage.Reason != reason {
					t.Fatal("batch emptiness changed discovery coverage")
				}
				// The ordinary discovery decoder must still reject using empty to claim
				// complete coverage of a bounded partial observation.
				discovery.Status = "empty"
				raw, _ := json.Marshal(discovery)
				if _, err = decodeDiscoveryResult(raw, provider, pageRequest(), maxPageOutputBytes); err == nil {
					t.Fatal("discovery empty contract was weakened")
				}
			})
		}
	}
}
