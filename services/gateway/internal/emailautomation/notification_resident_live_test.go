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
	routes := [][2]string{{"outlook", "qq_mail"}, {"gmail", "qq_mail"}, {"qq_mail", "gmail"}, {"gmail", "outlook"}, {"outlook", "gmail"}, {"qq_mail", "outlook"}}
	recipients := map[string]string{}
	for id, identity := range accounts {
		if id != "outlook" {
			recipients[id] = identity.address
		}
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
		t.Logf("sending %s -> %s marker=%s", route[0], route[1], marker)
		sendRequest := SendRequest{Provider: route[0], Account: app.EmailAccountDefault, Recipient: recipients[route[1]], Subject: marker, Body: "SparkClaw resident notification qualification " + marker, InvocationID: app.NewID("resident_send"), BrowserCredentialGeneration: from.probe.Generation, ProbeRevision: from.provider.Probe.Revision, ScriptRevision: from.provider.Send.Revision}
		if route[0] == "outlook" {
			sendRequest.Mode = "compose"
			sendRequest.AccountAddress = from.address
			sendRequest.To = []string{recipients[route[1]]}
			sendRequest.Recipient = ""
		}
		_, err := runner.Send(ctx, from.provider, sendRequest)
		if err != nil {
			t.Logf("send outcome uncertain (%s); reconcile without resend", ErrorCode(err))
		} else {
			t.Log("send confirmed")
		}
		time.Sleep(15 * time.Second)
		t.Logf("%s after send: %s", route[1], observe(route[1], "status"))
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
