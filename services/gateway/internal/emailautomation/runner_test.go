package emailautomation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

type fakeBrowserController struct {
	endpoint      browserautomation.HostCDPEndpoint
	headlessErr   error
	loginErr      error
	headlessCalls int
	loginURLs     []string
}

func (f *fakeBrowserController) EnsureHeadless(context.Context) (browserautomation.HostCDPEndpoint, error) {
	f.headlessCalls++
	return f.endpoint, f.headlessErr
}

func (f *fakeBrowserController) OpenLogin(_ context.Context, targetURL string) error {
	f.loginURLs = append(f.loginURLs, targetURL)
	return f.loginErr
}

func TestRunnerProbeAndSendBindHeadlessGenerationAndRecipientDigest(t *testing.T) {
	t.Setenv("GO_WANT_EMAIL_RUNNER_HELPER", "1")
	t.Setenv("AGENT_BROWSER_PROFILE", "forbidden-profile")
	browser := &fakeBrowserController{endpoint: testHeadlessEndpoint(7)}
	runner := NewRunner(browser, "/opt/sparkclaw/bin/agent-browser")
	provider := helperProvider("probe-ok", "send-ok")

	probe, err := runner.Probe(t.Context(), provider, "probe:one", 0)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Provider != app.EmailProviderGmail || probe.AccountHint != "a***@gmail.com" || probe.Generation != 7 || probe.Revision != 1 || probe.CheckedAt.IsZero() {
		t.Fatalf("probe result = %#v", probe)
	}

	result, err := runner.Send(t.Context(), provider, SendRequest{
		Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, Recipient: "alice@example.com",
		Subject: "", Body: "Exact body\nsecond line", InvocationID: "send:one", BrowserGeneration: 7,
		ProbeRevision: 1, ScriptRevision: 2, SettingVersion: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "sent" || result.Provider != app.EmailProviderGmail || result.RecipientDigest != recipientDigest("alice@example.com") ||
		result.BrowserGeneration != 7 || result.ScriptRevision != 2 || browser.headlessCalls != 2 {
		t.Fatalf("send result = %#v browser=%#v", result, browser)
	}
}

func TestRunnerRejectsStaleGenerationWrongDigestAndUnstructuredSendFailure(t *testing.T) {
	t.Setenv("GO_WANT_EMAIL_RUNNER_HELPER", "1")
	provider := helperProvider("probe-ok", "send-wrong-digest")
	browser := &fakeBrowserController{endpoint: testHeadlessEndpoint(8)}
	runner := NewRunner(browser, "")
	request := SendRequest{
		Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, Recipient: "alice@example.com", Body: "body",
		InvocationID: "send:stale", BrowserGeneration: 7, ProbeRevision: 1, ScriptRevision: 2, SettingVersion: 1,
	}
	if _, err := runner.Send(t.Context(), provider, request); ErrorCode(err) != CodeAdmissionStale {
		t.Fatalf("stale generation error = %v code=%q", err, ErrorCode(err))
	}

	browser.endpoint = testHeadlessEndpoint(7)
	if _, err := runner.Send(t.Context(), provider, request); ErrorCode(err) != CodeScriptInvalidOutput {
		t.Fatalf("wrong digest error = %v code=%q", err, ErrorCode(err))
	}

	provider.Send = helperScript("send-unstructured-error", 2)
	if _, err := runner.Send(t.Context(), provider, request); ErrorCode(err) != CodeSendOutcomeUnknown {
		t.Fatalf("unstructured send failure = %v code=%q", err, ErrorCode(err))
	}
}

func TestEmailRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EMAIL_RUNNER_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if os.Getenv("AGENT_BROWSER_CDP") == "" || os.Getenv("AGENT_BROWSER_SESSION") == "" || os.Getenv("AGENT_BROWSER_PROFILE") != "" {
		fmt.Fprintln(os.Stderr, "runner environment is invalid")
		os.Exit(3)
	}
	var input struct {
		Operation string `json:"operation"`
		Provider  string `json:"provider"`
		Message   struct {
			Recipient string `json:"recipient"`
			Subject   string `json:"subject"`
			Body      struct {
				Format  string `json:"format"`
				Content string `json:"content"`
			} `json:"body"`
		} `json:"message"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		os.Exit(4)
	}
	switch mode {
	case "probe-ok":
		if input.Operation != "probe" || input.Provider != app.EmailProviderGmail {
			os.Exit(5)
		}
		fmt.Fprintln(os.Stdout, `{"schema_version":1,"status":"ready","provider":"gmail","account_hint":"a***@gmail.com"}`)
	case "send-ok", "send-wrong-digest":
		if input.Operation != "send" || input.Provider != app.EmailProviderGmail || input.Message.Recipient != "alice@example.com" ||
			input.Message.Subject != "" || input.Message.Body.Format != "text" || input.Message.Body.Content != "Exact body\nsecond line" && input.Message.Body.Content != "body" {
			os.Exit(6)
		}
		digest := recipientDigest(input.Message.Recipient)
		if mode == "send-wrong-digest" {
			digest = "sha256:" + strings.Repeat("0", 64)
		}
		fmt.Fprintf(os.Stdout, "{\"schema_version\":1,\"status\":\"sent\",\"provider\":\"gmail\",\"recipient_digest\":%q,\"provider_message_id\":\"message-1\"}\n", digest)
	case "send-unstructured-error":
		os.Exit(7)
	default:
		os.Exit(8)
	}
	os.Exit(0)
}

func helperProvider(probeMode, sendMode string) Provider {
	return Provider{
		ID: app.EmailProviderGmail, DisplayName: "Gmail", LoginURL: "https://mail.google.com/",
		Probe: helperScript(probeMode, 1), Send: helperScript(sendMode, 2),
	}
}

func helperScript(mode string, revision int) Script {
	return Script{
		ID: mode, Revision: revision,
		Command: []string{os.Args[0], "-test.run=TestEmailRunnerHelperProcess", "--", mode},
		Timeout: 5 * time.Second,
	}
}

func testHeadlessEndpoint(generation uint64) browserautomation.HostCDPEndpoint {
	return browserautomation.HostCDPEndpoint{
		Version: 1, ProfileID: "default", Presentation: "headless", BrowserPID: 42, Generation: generation,
		BrowserVersion:   "148.0.7778.0",
		WebSocketURL:     "ws://host.docker.internal:18791/abcdefghijklmnopqrstuvwxyz123456/devtools/browser/browser-id",
		HostWebSocketURL: "ws://127.0.0.1:18791/abcdefghijklmnopqrstuvwxyz123456/devtools/browser/browser-id",
	}
}
