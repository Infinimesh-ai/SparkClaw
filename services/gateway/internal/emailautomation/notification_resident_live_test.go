package emailautomation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"golang.org/x/net/html/charset"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
)

// All sends are opt-in, directed only between freshly proved signed-in account
// identities. Start/inspect return promptly, leaving the same Controller free
// for a real Reader query and original acquisition throughout observation.
func TestEmailResidentNotificationLive(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_RESIDENT_NOTIFICATION") != "1" {
		t.Skip("explicit resident qualification required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	cfg, err := config.Load(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if err != nil {
		t.Fatal("live config unavailable")
	}
	runtime, err := newPlaywrightEmailLiveStoreRuntime(ctx, cfg)
	if err != nil {
		t.Fatal("credential store unavailable")
	}
	defer runtime.Close(context.Background())
	vault := credential.New(runtime.CredentialRepository(), credential.Options{Key: cfg.State.CredentialKey, KeyFile: cfg.State.CredentialKeyFile})
	if err := vault.Ready(); err != nil {
		t.Fatal("credential vault unavailable")
	}
	client, err := browsercontrol.NewHTTPControllerClient(cfg.Adapters.BrowserAutomation.PlaywrightExtension.ControllerSocket, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	controller := browsercontrol.New(vault, client, "default")
	controller.Initialize(ctx)
	defer controller.Close()
	status, err := controller.Check(ctx)
	if err != nil || !status.Configured {
		t.Fatal("browser credential unavailable")
	}
	runner := NewPlaywrightRunner(liveWaitingController{Service: controller, t: t})
	registry := DefaultRegistry()
	scopeHash := sha256.Sum256([]byte(app.NewID("resident_qualification")))
	scope := hex.EncodeToString(scopeHash[:])
	ids := strings.Split(os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_PROVIDERS"), ",")
	type identity struct {
		address  string
		probe    ProbeResult
		provider Provider
	}
	accounts := map[string]identity{}
	for _, id := range ids {
		provider, ok := registry.Get(id)
		if !ok {
			t.Fatal("invalid provider")
		}
		probe, err := runner.Probe(ctx, provider, app.NewID("resident_probe"), uint64(status.CredentialGeneration))
		if err != nil {
			t.Fatalf("%s probe: %s", id, ErrorCode(err))
		}
		discovery, err := runner.Discover(ctx, provider, ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: scope, InvocationID: app.NewID("resident_identity"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision})
		if err != nil || !mailAddressPattern.MatchString(discovery.AccountAddress) {
			t.Fatalf("%s identity: %s", id, ErrorCode(err))
		}
		accounts[id] = identity{discovery.AccountAddress, probe, provider}
		t.Logf("%s signed-in identity verified", id)
	}
	observe := func(id, action string) json.RawMessage {
		t.Helper()
		account := accounts[id]
		result, err := controller.RunScript(ctx, browsercontrol.RunScriptRequest{TaskID: app.NewID("resident_watch"), CredentialGeneration: status.CredentialGeneration, WaitTimeoutMS: 30000, Provider: id, Operation: "observe", ScriptID: id + ".observe", Revision: 1,
			Input: map[string]any{"schema_version": 1, "action": action, "account_address": account.address, "owner_scope": scope}})
		if err != nil || result.State != "completed" {
			t.Fatalf("%s observer %s failed: %v", id, action, err)
		}
		return result.Result
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 2*time.Minute)
		defer done()
		for _, id := range ids {
			a := accounts[id]
			for _, action := range []string{"status", "stop"} {
				result, err := controller.RunScript(cleanup, browsercontrol.RunScriptRequest{TaskID: app.NewID("resident_cleanup"), CredentialGeneration: status.CredentialGeneration, WaitTimeoutMS: 30000, Provider: id, Operation: "observe", ScriptID: id + ".observe", Revision: 1,
					Input: map[string]any{"schema_version": 1, "action": action, "account_address": a.address, "owner_scope": scope}})
				if err != nil || result.State != "completed" {
					t.Errorf("%s cleanup %s did not complete", id, action)
					continue
				}
				if action == "status" {
					t.Logf("%s final observation: %s", id, result.Result)
				}
			}
		}
	}()
	assertQuiet := func(id string, raw json.RawMessage) {
		t.Helper()
		var value struct {
			State string `json:"state"`
			Hints int    `json:"hints"`
		}
		if json.Unmarshal(raw, &value) != nil || value.State != "watching" || value.Hints != 0 {
			t.Fatalf("%s no-send control is not quiet and healthy; inspect evidence before qualifying", id)
		}
	}

	for _, id := range ids {
		t.Logf("%s resident start: %s", id, observe(id, "start"))
	}
	time.Sleep(30 * time.Second)
	for _, id := range ids {
		raw := observe(id, "status")
		t.Logf("%s no-send baseline: %s", id, raw)
		assertQuiet(id, raw)
	}
	for _, id := range ids {
		a := accounts[id]
		start := time.Now()
		result, err := runner.Discover(ctx, a.provider, ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: scope, InvocationID: app.NewID("resident_negative"), BrowserCredentialGeneration: a.probe.Generation, ProbeRevision: a.provider.Probe.Revision, ScriptRevision: a.provider.Discover.Revision,
			Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: a.address, IntervalStart: time.Now().UTC().Add(-time.Minute), IntervalEnd: time.Now().UTC().Add(time.Second), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}})
		if err != nil {
			t.Fatalf("%s Reader with resident observer: %s", id, ErrorCode(err))
		}
		t.Logf("%s simultaneous Reader completed in %s with %d candidates", id, time.Since(start), len(result.Candidates))
		raw := observe(id, "status")
		t.Logf("%s Reader control: %s", id, raw)
		assertQuiet(id, raw)
	}
	if seconds, _ := strconv.Atoi(os.Getenv("SPARKCLAW_TEST_RESIDENT_HOLD_SECONDS")); seconds > 0 && seconds <= 900 {
		t.Log("resident observation holding; all Reader reservations are free")
		time.Sleep(time.Duration(seconds) * time.Second)
	}
	if marker := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_RECONCILE_MARKER"); marker != "" {
		a := accounts["gmail"]
		verifyLiveNotificationReceipt(t, ctx, runner, a.provider, scope, a.address, a.probe, marker, time.Now().UTC().Add(-20*time.Minute))
	}
	if os.Getenv("SPARKCLAW_TEST_RESIDENT_SEND") != "1" {
		return
	}
	sendRunner := runner
	if socket := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_SENDER_CONTROLLER_SOCKET"); socket != "" {
		sendClient, senderErr := browsercontrol.NewHTTPControllerClient(socket, 15*time.Second)
		if senderErr != nil {
			t.Fatal("independent sender Controller unavailable")
		}
		defer sendClient.Close()
		sender := browsercontrol.New(vault, sendClient, "default")
		sender.Initialize(ctx)
		defer sender.Close()
		if senderStatus, checkErr := sender.Check(ctx); checkErr != nil || !senderStatus.Configured || senderStatus.CredentialGeneration != status.CredentialGeneration {
			t.Fatal("independent sender credential does not match receiver")
		}
		sendRunner = NewPlaywrightRunner(liveWaitingController{Service: sender, t: t})
	}
	routes := [][2]string{{"outlook", "qq_mail"}, {"gmail", "qq_mail"}, {"qq_mail", "gmail"}, {"gmail", "outlook"}, {"outlook", "gmail"}, {"qq_mail", "outlook"}}
	recipients := map[string]string{}
	for id, identity := range accounts {
		if id != "outlook" {
			recipients[id] = identity.address
		}
	}
	if proofPath := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_OUTLOOK_IDENTITY"); proofPath != "" {
		var proof struct {
			Marker string `json:"marker"`
			Sender string `json:"sender"`
		}
		raw, readErr := os.ReadFile(proofPath)
		if !filepath.IsAbs(proofPath) || readErr != nil || json.Unmarshal(raw, &proof) != nil ||
			!strings.HasPrefix(proof.Marker, "SCW-mail-") || !mailAddressPattern.MatchString(proof.Sender) {
			t.Fatal("verified Outlook outgoing identity required")
		}
		recipients["outlook"] = proof.Sender
	}
	if route := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_ROUTE"); route != "" {
		parts := strings.Split(route, ":")
		if len(parts) != 2 {
			t.Fatal("invalid route")
		}
		routes = [][2]string{{parts[0], parts[1]}}
	}
	for _, route := range routes {
		from, ok1 := accounts[route[0]]
		to, ok2 := accounts[route[1]]
		if !ok1 || !ok2 || route[0] == route[1] {
			t.Fatal("unproved route")
		}
		if recipients[route[1]] == "" {
			t.Fatal("recipient needs a verified outgoing From header; sign-in alias is not delivery evidence")
		}
		marker := "SCW-" + strings.ReplaceAll(app.NewID("mail"), "_", "-")
		start := time.Now().UTC().Add(-time.Minute)
		type readInterval struct{ began, ended time.Time }
		type readLoopResult struct {
			intervals []readInterval
			failure   string
		}
		var completedIntervals []readInterval
		var readDone chan readLoopResult
		var stopReads func()
		if os.Getenv("SPARKCLAW_TEST_NOTIFICATION_SHARED_READ_LOOP") == "1" {
			workspace := os.Getenv("SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT")
			if !filepath.IsAbs(workspace) {
				t.Fatal("shared read loop needs an absolute workspace root")
			}
			inventoryStart := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
			if route[1] == "outlook" {
				inventoryStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			inventory, inventoryErr := runner.Discover(ctx, to.provider, ReadRequest{Provider: route[1], Account: app.EmailAccountDefault,
				OwnerScope: scope, InvocationID: app.NewID("resident_shared_inventory"), BrowserCredentialGeneration: to.probe.Generation,
				ProbeRevision: to.provider.Probe.Revision, ScriptRevision: to.provider.Discover.Revision,
				Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: to.address,
					IntervalStart: inventoryStart, IntervalEnd: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
					Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}})
			if inventoryErr != nil || len(inventory.Candidates) == 0 {
				t.Fatal("shared read loop requires a historical target")
			}
			target := inventory.Candidates[0]
			stopChannel := make(chan struct{})
			stopReads = func() { close(stopChannel) }
			readDone = make(chan readLoopResult, 1)
			go func() {
				intervals := []readInterval{}
				failure := ""
				emptyStart := map[string]time.Time{
					"qq_mail": time.UnixMilli(1789477863000).UTC(),
					"gmail":   time.UnixMilli(1789533548432).UTC(),
					"outlook": time.UnixMilli(1788851630069).UTC(),
				}[route[1]].Truncate(time.Second).Add(time.Second)
				for len(intervals) < 300 {
					select {
					case <-stopChannel:
						readDone <- readLoopResult{intervals, failure}
						return
					default:
					}
					request := ReadRequest{Provider: route[1], Account: app.EmailAccountDefault, OwnerScope: scope,
						InvocationID: app.NewID("resident_shared_round"), BrowserCredentialGeneration: to.probe.Generation,
						ProbeRevision: to.provider.Probe.Revision, ScriptRevision: to.provider.CollectPage.Revision,
						Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: to.address,
							IntervalStart: emptyStart, IntervalEnd: emptyStart.Add(time.Second), Limit: 50,
							ProviderMode: app.EmailProviderModeTimeRange, RetryTargets: []app.EmailCaptureTarget{target}}}
					began := time.Now().UTC()
					page, readErr := runner.CollectPage(ctx, to.provider, request)
					ended := time.Now().UTC()
					if readErr != nil {
						failure = string(ErrorCode(readErr))
						break
					}
					if len(page.Captures) != 1 || len(page.Failures) != 0 {
						failure = "incomplete_original"
						break
					}
					bind := request
					bind.Target = &page.Captures[0].Target
					bind.InvocationID = PageCaptureInvocationID(request.InvocationID, route[1], page.Captures[0].Target)
					if verifyCapture(ctx, workspace, bind, page.Captures[0].Result) != nil {
						failure = "original_verification_failed"
						break
					}
					intervals = append(intervals, readInterval{began, ended})
					t.Logf("shared receiver collect_page round=%d began=%s ended=%s", len(intervals), began.Format(time.RFC3339Nano), ended.Format(time.RFC3339Nano))
				}
				readDone <- readLoopResult{intervals, failure}
			}()
		}
		t.Logf("sending %s -> %s marker=%s", route[0], route[1], marker)
		sendRequest := SendRequest{Provider: route[0], Account: app.EmailAccountDefault, Recipient: recipients[route[1]], Subject: marker, Body: "SparkClaw resident notification qualification " + marker, InvocationID: app.NewID("resident_send"), BrowserCredentialGeneration: from.probe.Generation, ProbeRevision: from.provider.Probe.Revision, ScriptRevision: from.provider.Send.Revision}
		if route[0] == "outlook" {
			sendRequest.Mode = "compose"
			sendRequest.AccountAddress = from.address
			sendRequest.To = []string{recipients[route[1]]}
			sendRequest.Recipient = ""
		}
		_, err := sendRunner.Send(ctx, from.provider, sendRequest)
		if err != nil {
			t.Logf("send outcome uncertain (%s); reconcile without resend", ErrorCode(err))
		} else {
			t.Log("send confirmed")
		}
		if stopReads != nil {
			time.Sleep(25 * time.Second)
			stopReads()
			readResult := <-readDone
			completedIntervals = readResult.intervals
			t.Logf("%s shared receiver completed rounds=%d", route[1], len(readResult.intervals))
			if readResult.failure != "" || len(readResult.intervals) == 0 {
				t.Fatalf("shared receiver Reader loop failed: %s", readResult.failure)
			}
		} else {
			time.Sleep(15 * time.Second)
		}
		afterSend := observe(route[1], "status")
		t.Logf("%s after send: %s", route[1], afterSend)
		if len(completedIntervals) > 0 {
			var watch struct {
				Events []struct {
					At   string `json:"at"`
					Kind string `json:"kind"`
				} `json:"events"`
			}
			if json.Unmarshal(afterSend, &watch) != nil {
				t.Fatal("shared receiver watch evidence invalid")
			}
			overlaps := 0
			for _, event := range watch.Events {
				if event.Kind != "mailbox_changed" {
					continue
				}
				at, parseErr := time.Parse(time.RFC3339Nano, event.At)
				if parseErr != nil {
					continue
				}
				for _, interval := range completedIntervals {
					if !at.Before(interval.began) && !at.After(interval.ended) {
						overlaps++
						break
					}
				}
			}
			t.Logf("%s native event overlapping whole collect_page=%d", route[1], overlaps)
		}
		original := verifyLiveNotificationReceipt(t, ctx, runner, to.provider, scope, to.address, to.probe, marker, start)
		message, err := mail.ReadMessage(bytes.NewReader(original))
		if err != nil {
			t.Fatal("verified original header invalid")
		}
		senders, err := (&mail.AddressParser{WordDecoder: &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}}).ParseList(message.Header.Get("From"))
		if err != nil || len(senders) != 1 {
			t.Fatal("outgoing sender ambiguous")
		}
		recipients[route[0]] = senders[0].Address
		t.Logf("%s after original: %s", route[1], observe(route[1], "status"))
	}
}
