package emailautomation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html/charset"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Opt-in real-mail qualification. The address of each recipient is taken only
// from that provider's authenticated Reader. Receipt-only mode validates managed
// sends independently of notifications. An uncertain send is never repeated.
func TestEmailNotificationMutualSendLive(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_NOTIFICATION_OBSERVE") != "1" {
		t.Skip("explicit real-mail notification qualification required")
	}
	senderID, receiverID := app.EmailProviderGmail, app.EmailProviderQQMail
	if route := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_ROUTE"); route != "" {
		parts := strings.Split(route, ":")
		if len(parts) != 2 || parts[0] == parts[1] {
			t.Fatal("notification route must name two distinct providers")
		}
		allowed := map[string]bool{app.EmailProviderGmail: true, app.EmailProviderQQMail: true, app.EmailProviderOutlook: true}
		if !allowed[parts[0]] || !allowed[parts[1]] {
			t.Fatal("notification route contains an unsupported provider")
		}
		senderID, receiverID = parts[0], parts[1]
	}
	observerSocket := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_OBSERVER_SOCKET")
	receiptOnly := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_RECEIPT_ONLY") == "1" || os.Getenv("SPARKCLAW_TEST_NOTIFICATION_RESIDENT_SEND") == "1"
	reconcileOnly := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_RECONCILE_MARKER") != ""
	if observerSocket == "" && !receiptOnly && !reconcileOnly {
		t.Fatal("isolated observer socket required")
	}
	readyDir := os.Getenv("SPARKCLAW_NOTIFICATION_OBSERVER_READY_DIR")
	if !filepath.IsAbs(readyDir) && !receiptOnly && !reconcileOnly {
		t.Fatal("isolated observer readiness directory required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, err := config.Load(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if err != nil {
		t.Fatal("live configuration unavailable")
	}
	runtime, err := newPlaywrightEmailLiveStoreRuntime(ctx, cfg)
	if err != nil {
		t.Fatal("live credential backend unavailable")
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	vault := credential.New(runtime.CredentialRepository(), credential.Options{Key: cfg.State.CredentialKey, KeyFile: cfg.State.CredentialKeyFile})
	if err := vault.Ready(); err != nil {
		t.Fatal("live credential vault unavailable")
	}
	ec := cfg.Adapters.BrowserAutomation.PlaywrightExtension
	openController := func(name, socket string) *browsercontrol.Service {
		t.Helper()
		client, err := browsercontrol.NewHTTPControllerClient(socket, time.Duration(ec.ConnectTimeoutMS)*time.Millisecond)
		if err != nil {
			t.Fatal("controller socket unavailable")
		}
		t.Cleanup(client.Close)
		service := browsercontrol.New(vault, client, ec.ProfileID)
		service.Initialize(ctx)
		t.Cleanup(func() { _ = service.Close() })
		var status browsercontrol.Status
		var checkErr error
		for attempt := 0; attempt < 3; attempt++ {
			status, checkErr = service.Check(ctx)
			if checkErr == nil {
				break
			}
			if !browsercontrol.ErrorRetryable(checkErr) {
				break
			}
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				t.Fatal("browser proof wait timed out")
			}
		}
		if checkErr != nil || !status.Configured || status.CredentialGeneration < 1 {
			t.Fatalf("%s browser credential unavailable: %v", name, checkErr)
		}
		return service
	}
	dryRun := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_DRY_RUN") == "1"
	triggerRead := dryRun && os.Getenv("SPARKCLAW_TEST_NOTIFICATION_TRIGGER_READ") == "1"
	inspectMarker := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_NOTIFICATION_INSPECT_MARKER"))
	if inspectMarker != "" && (!strings.HasPrefix(inspectMarker, "SCW-mail-") || len(inspectMarker) > 64) {
		t.Fatal("invalid inspection marker")
	}
	reconcileMarker := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_NOTIFICATION_RECONCILE_MARKER"))
	if reconcileMarker != "" && (!strings.HasPrefix(reconcileMarker, "SCW-mail-") || len(reconcileMarker) > 64) {
		t.Fatal("invalid qualification marker")
	}
	var runner *PlaywrightRunner
	registry := DefaultRegistry()
	sender, _ := registry.Get(senderID)
	receiver, _ := registry.Get(receiverID)
	ownerScope := sha256.Sum256([]byte("isolated-notification-qualification:" + app.NewID("run")))
	scope := hex.EncodeToString(ownerScope[:])
	type identity struct {
		address string
		probe   ProbeResult
	}
	identities := map[string]identity{}
	if !dryRun || triggerRead || reconcileMarker != "" {
		t.Log("validating production controller")
		production := openController("production", ec.ControllerSocket)
		runner = NewPlaywrightRunner(liveWaitingController{Service: production, t: t})
		providers := []Provider{sender, receiver}
		if reconcileMarker != "" || triggerRead {
			providers = []Provider{receiver}
		}
		for _, provider := range providers {
			t.Logf("checking authenticated %s account", provider.ID)
			var probe ProbeResult
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				probe, err = runner.Probe(ctx, provider, app.NewID("notification_probe"), uint64(production.Status(ctx).CredentialGeneration))
				if err == nil {
					break
				}
				if attempt < 2 {
					select {
					case <-time.After(3 * time.Second):
					case <-ctx.Done():
						t.Fatal("login proof timed out")
					}
				}
			}
			if err != nil {
				t.Fatalf("%s login proof failed: %s", provider.ID, ErrorCode(err))
			}
			request := ReadRequest{Provider: provider.ID, Account: app.EmailAccountDefault, OwnerScope: scope, InvocationID: app.NewID("notification_identity"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision}
			result, err := runner.Discover(ctx, provider, request)
			if err != nil || !mailAddressPattern.MatchString(result.AccountAddress) {
				t.Fatalf("%s authenticated account discovery failed: %s", provider.ID, ErrorCode(err))
			}
			identities[provider.ID] = identity{result.AccountAddress, probe}
		}
		if reconcileMarker == "" && !triggerRead && strings.EqualFold(identities[senderID].address, identities[receiverID].address) {
			t.Fatal("sender and recipient resolve to one account")
		}
	}
	if triggerRead && os.Getenv("SPARKCLAW_TEST_NOTIFICATION_QUERY_ONLY") == "1" {
		probe := identities[receiverID].probe
		request := ReadRequest{Provider: receiverID, Account: app.EmailAccountDefault, OwnerScope: scope, InvocationID: app.NewID("notification_negative_read"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: receiver.Probe.Revision, ScriptRevision: receiver.Discover.Revision,
			Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: identities[receiverID].address, IntervalStart: time.Now().UTC().Add(-5 * time.Minute), IntervalEnd: time.Now().UTC().Add(time.Second), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}}
		result, err := runner.Discover(ctx, receiver, request)
		if err != nil {
			t.Fatalf("isolated negative-control Reader query failed: %s", ErrorCode(err))
		}
		t.Logf("isolated negative-control Reader query completed with %d candidate(s)", len(result.Candidates))
		return
	}
	if reconcileMarker != "" {
		verifyLiveNotificationReceipt(t, ctx, runner, receiver, scope, identities[receiverID].address, identities[receiverID].probe, reconcileMarker, time.Now().UTC().Add(-20*time.Minute))
		return
	}
	// Send once and verify the original through the real Reader. This can run
	// independently or alongside an already running resident observer.
	if receiptOnly {
		recipient := identities[receiverID].address
		if receiverID == app.EmailProviderOutlook {
			var proof struct {
				Marker string `json:"marker"`
				Sender string `json:"sender"`
			}
			proofPath := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_OUTLOOK_IDENTITY")
			raw, err := os.ReadFile(proofPath)
			if !filepath.IsAbs(proofPath) || err != nil || json.Unmarshal(raw, &proof) != nil || !strings.HasPrefix(proof.Marker, "SCW-mail-") || !mailAddressPattern.MatchString(proof.Sender) {
				t.Fatal("verified Outlook outgoing identity required")
			}
			recipient = proof.Sender
		}
		marker := "SCW-" + strings.ReplaceAll(app.NewID("mail"), "_", "-")
		start := time.Now().UTC().Add(-time.Minute)
		t.Logf("sending %s -> %s marker=%s", senderID, receiverID, marker)
		sendRequest := SendRequest{Provider: senderID, Account: app.EmailAccountDefault, Mode: "compose", AccountAddress: identities[senderID].address, To: []string{recipient}, Subject: marker, Body: "SparkClaw qualification " + marker + "\nPrivate field check: \"quoted\" 多行正文", InvocationID: app.NewID("resident_send"), BrowserCredentialGeneration: identities[senderID].probe.Generation, ProbeRevision: sender.Probe.Revision, ScriptRevision: sender.Send.Revision}
		_, sendErr := runner.Send(ctx, sender, sendRequest)
		if sendErr != nil {
			t.Logf("send not confirmed (%s), check receipt without resend", ErrorCode(sendErr))
		} else {
			t.Log("send confirmed")
		}
		if os.Getenv("SPARKCLAW_TEST_NOTIFICATION_GATEWAY_RECEIPT") == "1" {
			verifyGatewayNotificationReceipt(t, ctx, runtime, receiverID, marker, start)
			return
		}
		original := verifyLiveNotificationReceipt(t, ctx, runner, receiver, scope, identities[receiverID].address, identities[receiverID].probe, marker, start)
		message, err := mail.ReadMessage(bytes.NewReader(original))
		parser := &mail.AddressParser{WordDecoder: &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}}
		if err != nil {
			t.Fatal("verified original header could not be parsed")
		}
		to, err := parser.ParseList(message.Header.Get("To"))
		if err != nil || len(to) != 1 || !strings.EqualFold(to[0].Address, recipient) {
			t.Fatal("verified original recipient does not match the requested route")
		}
		from, err := parser.ParseList(message.Header.Get("From"))
		if err != nil || len(from) != 1 || (senderID != app.EmailProviderOutlook && !strings.EqualFold(from[0].Address, identities[senderID].address)) {
			t.Fatal("verified original sender does not match the requested route")
		}
		t.Log("verified original sender and recipient match the route")
		return
	}

	t.Log("validating isolated observer controller")
	observer := openController("observer", observerSocket)
	start := time.Now().UTC().Add(-time.Minute)
	readyDigest := sha256.Sum256([]byte(app.NewID("watch_ready")))
	readyToken := hex.EncodeToString(readyDigest[:16])
	readyPath := filepath.Join(readyDir, readyToken)
	t.Cleanup(func() { _ = os.Remove(readyPath) })
	watchResult := make(chan browsercontrol.ScriptExecutionResult, 1)
	watchError := make(chan error, 1)
	marker := ""
	watchMarker := inspectMarker
	if !dryRun {
		marker = "SCW-" + strings.ReplaceAll(app.NewID("mail"), "_", "-")
		watchMarker = marker
	}
	watchDuration := 120000
	if dryRun && inspectMarker != "" {
		watchDuration = 1000
	} else if dryRun {
		watchDuration = 45000
	}
	if raw := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_DURATION_MS"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1000 || value > 120000 {
			t.Fatal("notification observation duration is invalid")
		}
		watchDuration = value
	}
	go func() {
		result, err := observer.RunScript(ctx, browsercontrol.RunScriptRequest{
			TaskID: app.NewID("notification_watch"), CredentialGeneration: observer.Status(ctx).CredentialGeneration,
			Provider: receiverID, Operation: "read", ScriptID: receiverID + ".notification_observe", Revision: 1,
			Input: map[string]any{"schema_version": 1, "provider": receiverID, "operation": "read", "account": "default", "invocation_id": app.NewID("watch"), "duration_ms": watchDuration, "ready_token": readyToken, "marker": watchMarker},
		})
		watchResult <- result
		watchError <- err
	}()
	ready := false
	for attempt := 0; attempt < 30; attempt++ {
		if _, err := os.Stat(readyPath); err == nil {
			ready = true
			break
		}
		select {
		case early := <-watchResult:
			watchErr := <-watchError
			t.Fatalf("observer finished before readiness: state=%s code=%s err=%v", early.State, notificationScriptCode(early.Result), watchErr)
		case <-time.After(time.Second):
		case <-ctx.Done():
			t.Fatal("watch setup timed out")
		}
	}
	if !ready {
		t.Fatal("observer did not establish a ready page")
	}
	t.Logf("owned %s notification observer is ready", receiverID)
	if dryRun {
		if triggerRead {
			readRunner := runner
			if os.Getenv("SPARKCLAW_TEST_NOTIFICATION_SAME_CONTROLLER") == "1" {
				readRunner = NewPlaywrightRunner(liveWaitingController{Service: observer, t: t})
			}
			probe := identities[receiverID].probe
			request := ReadRequest{Provider: receiverID, Account: app.EmailAccountDefault, OwnerScope: scope, InvocationID: app.NewID("notification_negative_read"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: receiver.Probe.Revision, ScriptRevision: receiver.Discover.Revision,
				Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: identities[receiverID].address, IntervalStart: time.Now().UTC().Add(-5 * time.Minute), IntervalEnd: time.Now().UTC().Add(time.Second), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}}
			result, err := readRunner.Discover(ctx, receiver, request)
			if err != nil {
				t.Fatalf("negative-control Reader query failed: %s", ErrorCode(err))
			}
			t.Logf("negative-control Reader query completed with %d candidate(s)", len(result.Candidates))
		}
		observed := <-watchResult
		if err := <-watchError; err != nil || observed.State != "completed" {
			t.Fatalf("dry observer failed: %v", err)
		}
		var probe struct {
			MarkerVisible bool            `json:"marker_visible"`
			Events        json.RawMessage `json:"events"`
			Counts        struct {
				ChannelOpens           int `json:"channel_opens"`
				ChannelRecords         int `json:"channel_records"`
				ChannelResponses       int `json:"channel_responses"`
				ChannelOK              int `json:"channel_ok"`
				ChannelEmpty           int `json:"channel_empty"`
				ChannelStreamDone      int `json:"channel_stream_done"`
				ChannelErrors          int `json:"channel_errors"`
				NativeChannelResources int `json:"native_channel_resources"`
			} `json:"counts"`
		}
		if json.Unmarshal(observed.Result, &probe) != nil {
			t.Fatal("dry observer output invalid")
		}
		if inspectMarker != "" {
			t.Logf("unique marker visible in owned %s page: %t", receiverID, probe.MarkerVisible)
		}
		t.Logf("notification channel opens=%d responses=%d ok=%d empty=%d stream_done=%d errors=%d records=%d resource_entries=%d", probe.Counts.ChannelOpens, probe.Counts.ChannelResponses, probe.Counts.ChannelOK, probe.Counts.ChannelEmpty, probe.Counts.ChannelStreamDone, probe.Counts.ChannelErrors, probe.Counts.ChannelRecords, probe.Counts.NativeChannelResources)
		if len(probe.Events) < 3000 {
			t.Logf("sanitized observer events: %s", probe.Events)
		}
		t.Log("owned observer remained active for the full qualification window")
		return
	}
	t.Logf("unique qualification marker: %s", marker)
	_, sendErr := runner.Send(ctx, sender, SendRequest{
		Provider: senderID, Account: app.EmailAccountDefault, Recipient: identities[receiverID].address,
		Subject: marker, Body: "SparkClaw notification qualification " + marker,
		InvocationID: app.NewID("notification_send"), BrowserCredentialGeneration: identities[senderID].probe.Generation,
		ProbeRevision: sender.Probe.Revision, ScriptRevision: sender.Send.Revision,
	})
	if sendErr != nil {
		t.Logf("send outcome uncertain: %s", ErrorCode(sendErr))
	} else {
		t.Log("test message send confirmed")
	}
	observed := <-watchResult
	if err := <-watchError; err != nil || observed.State != "completed" {
		t.Fatalf("observer failed: %v", err)
	}
	var evidence struct {
		SchemaVersion int    `json:"schema_version"`
		Provider      string `json:"provider"`
		MarkerVisible bool   `json:"marker_visible"`
		Counts        struct {
			WebSocketFrames    int `json:"websocket_frames"`
			WebSocketOpens     int `json:"websocket_opens"`
			Responses          int `json:"responses"`
			NativeSyncRequests int `json:"native_sync_requests"`
			ChannelRecords     int `json:"channel_records"`
		} `json:"counts"`
		Events []struct {
			Kind  string `json:"kind"`
			Shape any    `json:"shape"`
		} `json:"events"`
	}
	if json.Unmarshal(observed.Result, &evidence) != nil || evidence.SchemaVersion != 1 || evidence.Provider != receiverID {
		t.Fatal("owned observer evidence is invalid")
	}
	t.Logf("observer websocket_opens=%d frames=%d channel_responses=%d native_sync_requests=%d envelope_records=%d", evidence.Counts.WebSocketOpens, evidence.Counts.WebSocketFrames, evidence.Counts.Responses, evidence.Counts.NativeSyncRequests, len(evidence.Events))
	t.Logf("unique marker visible in owned %s page: %t", receiverID, evidence.MarkerVisible)
	changeFrames := 0
	for _, event := range evidence.Events {
		shape, ok := event.Shape.(map[string]any)
		if event.Kind == "qq_frame" && ok && shape["cmd"] == float64(1) {
			changeFrames++
		}
	}
	if receiverID == app.EmailProviderQQMail {
		if evidence.Counts.WebSocketOpens == 0 || changeFrames == 0 {
			t.Fatal("no QQ change frame was observed after the qualified send")
		}
		t.Logf("QQ change-frame hints observed: %d", changeFrames)
	} else if evidence.Counts.Responses == 0 && evidence.Counts.ChannelRecords == 0 {
		t.Fatal("receiver notification channel was not observed")
	}
	if len(evidence.Events) > 0 {
		limit := min(5, len(evidence.Events))
		shapes, _ := json.Marshal(evidence.Events[:limit])
		t.Logf("bounded sanitized observer shapes: %s", shapes)
	}
	if sendErr != nil {
		t.Logf("send confirmation uncertain; reconciling the unique marker, code=%s", ErrorCode(sendErr))
	}
	receiptRunner := NewPlaywrightRunner(liveWaitingController{Service: observer, t: t})
	verifyLiveNotificationReceipt(t, ctx, receiptRunner, receiver, scope, identities[receiverID].address, identities[receiverID].probe, marker, start)
	if sendErr != nil {
		t.Log("provider receipt resolved an uncertain send outcome")
	}
}

// The production smoke sends once, then only reads Store/local originals. It
// never invokes discovery or capture to manufacture a successful receipt.
func verifyGatewayNotificationReceipt(t *testing.T, ctx context.Context, runtime *store.Runtime, provider, marker string, start time.Time) {
	t.Helper()
	owners, err := runtime.OwnerRepository().ListOwnerProfiles(ctx)
	if err != nil {
		t.Fatal("owners unavailable")
	}
	repo := runtime.EmailRepository()
	root, err := os.OpenRoot(os.Getenv("SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT"))
	if err != nil {
		t.Fatal("original workspace unavailable")
	}
	defer root.Close()
	for attempt := 0; attempt < 90; attempt++ {
		for _, owner := range owners {
			boxes, err := repo.ListEmailMailboxes(ctx, owner.ID)
			if err != nil {
				t.Fatal("mailbox status unavailable")
			}
			for _, box := range boxes {
				if box.Provider != provider || !box.Active || !box.IntakeEnabled {
					continue
				}
				page, err := repo.ListEmailMails(ctx, store.EmailQuery{OwnerID: owner.ID, MailboxID: box.ID, Limit: 50})
				if err != nil {
					t.Fatal("mail receipts unavailable")
				}
				for _, message := range page.Items {
					if message.CaptureID == "" || message.DiscoveredAt.Before(start) {
						continue
					}
					capture, found, err := repo.GetEmailCapture(ctx, owner.ID, message.CaptureID)
					if err != nil || !found {
						continue
					}
					original, err := root.ReadFile(capture.OriginalPath)
					if err != nil || !bytes.Contains(original, []byte(marker)) {
						continue
					}
					sum := sha256.Sum256(original)
					if "sha256:"+hex.EncodeToString(sum[:]) != capture.OriginalSHA256 {
						t.Fatal("production original checksum mismatch")
					}
					expectedReason := map[string]string{app.EmailProviderQQMail: "qq_inbound_envelope", app.EmailProviderGmail: "gmail_topic_invalidation", app.EmailProviderOutlook: "outlook_delivery_change"}[provider]
					if box.ObserverEpoch == "" || box.LastNotificationAt.Before(start.Add(time.Minute)) || box.LastNotificationReason != expectedReason || box.SignalRevision < 1 {
						t.Fatal("original exists without persisted notification")
					}
					t.Logf("production Gateway original verified: provider=%s watch=%s signal_revision=%d reconciled_revision=%d receipt_age=%s", provider, box.WatchState, box.SignalRevision, box.ReconciledRevision, time.Since(start.Add(time.Minute)).Round(time.Millisecond))
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("production receipt timed out")
		case <-time.After(2 * time.Second):
		}
	}
	t.Fatal("production Gateway did not publish the test original")
}

func verifyLiveNotificationReceipt(t *testing.T, ctx context.Context, runner *PlaywrightRunner, receiver Provider, scope, address string, probe ProbeResult, marker string, start time.Time) []byte {
	t.Helper()
	seen := map[string]bool{}
	root := os.Getenv("SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT")
	maximumAttempts := 8
	if os.Getenv("SPARKCLAW_TEST_NOTIFICATION_ONE_RECEIPT_QUERY") == "1" {
		maximumAttempts = 1
	}
	for attempt := 0; attempt < maximumAttempts; attempt++ {
		readFailures, verificationFailures, manifestFailures, otherOriginals := 0, 0, 0, 0
		readFailureCode := ""
		request := ReadRequest{Provider: receiver.ID, Account: app.EmailAccountDefault, OwnerScope: scope, InvocationID: app.NewID("notification_discovery"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: receiver.Probe.Revision, ScriptRevision: receiver.Discover.Revision,
			Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: address, IntervalStart: start, IntervalEnd: time.Now().UTC().Add(time.Second), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}}
		result, err := runner.Discover(ctx, receiver, request)
		if err != nil {
			t.Logf("receipt discovery temporarily failed: %s", ErrorCode(err))
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				t.Fatal("receipt check timed out")
			}
			continue
		}
		t.Logf("receipt interval query %d returned %d candidate(s)", attempt+1, len(result.Candidates))
		for _, target := range result.Candidates {
			t.Logf("receipt target has qualified time=%t native_id=%t", target.ReceivedAt != nil, target.ProviderNativeID != "")
			if seen[target.ProviderMessageID] {
				continue
			}
			readRequest := ReadRequest{Provider: receiver.ID, Account: app.EmailAccountDefault, OwnerScope: scope, InvocationID: app.NewID("notification_capture"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: receiver.Probe.Revision, ScriptRevision: receiver.Capture.Revision, Target: &target}
			readResult, err := runner.Read(ctx, receiver, readRequest)
			if err != nil {
				readFailures++
				readFailureCode = string(ErrorCode(err))
				continue
			}
			if verifyCapture(ctx, root, readRequest, readResult) != nil {
				verificationFailures++
				continue
			}
			var manifest struct {
				Files []struct {
					Path string `json:"path"`
				} `json:"files"`
			}
			raw, err := os.ReadFile(filepath.Join(root, readResult.Capture.ManifestPath))
			if err != nil || json.Unmarshal(raw, &manifest) != nil || len(manifest.Files) != 1 {
				manifestFailures++
				continue
			}
			original, err := os.ReadFile(filepath.Join(root, manifest.Files[0].Path))
			if err == nil && bytes.Contains(original, []byte(marker)) {
				message, parseErr := mail.ReadMessage(bytes.NewReader(original))
				if parseErr != nil {
					t.Fatal("verified receipt header could not be parsed")
				}
				subject, decodeErr := (&mime.WordDecoder{CharsetReader: charset.NewReaderLabel}).DecodeHeader(message.Header.Get("Subject"))
				if decodeErr != nil || subject != marker {
					seen[target.ProviderMessageID] = true
					otherOriginals++
					continue
				}
				t.Log("unique test message was received and its original verified")
				if destination := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_RECEIPT_IDENTITY"); filepath.IsAbs(destination) {
					message, parseErr := mail.ReadMessage(bytes.NewReader(original))
					if parseErr != nil {
						t.Fatal("verified receipt header could not be parsed")
					}
					from, parseErr := (&mail.AddressParser{WordDecoder: &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}}).ParseList(message.Header.Get("From"))
					if parseErr != nil || len(from) != 1 {
						t.Fatal("verified sender identity is ambiguous")
					}
					evidence, _ := json.Marshal(map[string]string{"marker": marker, "sender": from[0].Address})
					if os.WriteFile(destination, evidence, 0600) != nil {
						t.Fatal("private receipt identity could not be written")
					}
				}
				return original
			}
			if err == nil {
				seen[target.ProviderMessageID] = true
				otherOriginals++
			} else {
				manifestFailures++
			}
		}
		t.Logf("receipt capture diagnostics: read_failures=%d last_read_code=%s verification_failures=%d manifest_failures=%d other_originals=%d", readFailures, readFailureCode, verificationFailures, manifestFailures, otherOriginals)
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			t.Fatal("receipt check timed out")
		}
	}
	t.Fatal("unique test message was not verified in the receiver's original")
	return nil
}

type liveWaitingController struct {
	*browsercontrol.Service
	t *testing.T
}

func notificationScriptCode(raw json.RawMessage) string {
	var failure struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &failure) == nil {
		return failure.Code
	}
	return ""
}

func (c liveWaitingController) RunScript(ctx context.Context, request browsercontrol.RunScriptRequest) (browsercontrol.ScriptExecutionResult, error) {
	request.WaitTimeoutMS = 30_000
	result, err := c.Service.RunScript(ctx, request)
	if request.Operation == "capture" || request.Operation == "discover" || request.Operation == "send" {
		if err != nil {
			c.t.Logf("%s controller code: %s", request.Operation, browsercontrol.ErrorCode(err))
		} else if result.State == "failed" {
			var failure struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(result.Result, &failure) == nil {
				c.t.Logf("%s script code: %s", request.Operation, failure.Code)
			}
		}
	}
	return result, err
}
