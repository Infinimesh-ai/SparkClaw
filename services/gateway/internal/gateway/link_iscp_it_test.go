package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/link/connector"
	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/devconsole"
	"github.com/Chiiz0/SparkClaw/services/link/iscplink"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
	"github.com/Chiiz0/SparkClaw/services/link/relaytest"
)

type itConsole struct {
	url, pin, token string
	http            *http.Client
}

func (c *itConsole) do(t *testing.T, method, path string, in, out any) {
	t.Helper()
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest(method, c.url+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
}

func (c *itConsole) issue(t *testing.T, domain, deviceType string) consoleapi.Credential {
	var out struct {
		Data struct {
			Credential string `json:"credential"`
		} `json:"data"`
	}
	c.do(t, "POST", "/v1/domains/"+domain+"/access-credentials", map[string]any{"target_device_type": deviceType, "expires_in_seconds": 900}, &out)
	cred, err := consoleapi.ParseCredential(out.Data.Credential)
	if err != nil {
		t.Fatal(err)
	}
	return cred
}

// TestLinkOverRealRelayWithConsoleAndConnector runs the target chain: the
// backend joins the self-hosted console's domain through the connector, a
// phone joins with its own credential, and the phone talks to the Gateway
// over the ISCP Relay only (LAN disabled), then is revoked.
func TestLinkOverRealRelayWithConsoleAndConnector(t *testing.T) {
	r := relaytest.Start(t)
	iscplink.Debugf = func(f string, a ...any) { t.Logf("iscplink: "+f, a...) }
	defer func() { iscplink.Debugf = func(string, ...any) {} }()
	ep := relay.Endpoint{DomainID: r.DomainID, RelayID: r.RelayID, BaseURL: r.BaseURL, WebSocketURL: r.WSURL}

	// Console.
	consoleDir := t.TempDir()
	cert, pin, err := devconsole.EnsureCertificate(consoleDir, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	url := "https://" + ts.Listener.Addr().String()
	console, err := devconsole.New(devconsole.Options{DataDir: consoleDir, PublicURL: url, TLSPin: pin, Relay: ep, RelayAdminToken: r.AdminToken, AdminPassword: "pw-for-test-only", RelayHTTPClient: http.DefaultClient})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = console.Handler()
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	defer ts.Close()
	token, err := devconsole.RotateAPIToken(consoleDir)
	if err != nil {
		t.Fatal(err)
	}
	cc := &itConsole{url: url, pin: pin, token: token, http: relay.HTTPClient(relay.PinnedSHA256Config(pin), 10*time.Second)}
	var dom struct {
		Data struct {
			DomainID string `json:"domain_id"`
		} `json:"data"`
	}
	cc.do(t, "POST", "/v1/domains", map[string]any{"metadata": map[string]string{"name": "IT"}}, &dom)

	// Backend: Gateway link entry + connector joined with a sparkclaw credential.
	b := newLinkTestBackend(t)
	_ = os.Remove(filepath.Join(b.stateDir, "membership.json"))
	_ = os.Remove(filepath.Join(b.stateDir, "directory.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := connector.Join(ctx, b.stateDir, cc.issue(t, dom.Data.DomainID, consoleapi.TypeSparkClaw), "", "", "SparkClaw IT"); err != nil {
		t.Fatal(err)
	}
	connCtx, stopConnector := context.WithCancel(ctx)
	connDone := make(chan struct{})
	go func() {
		defer close(connDone)
		if err := connector.Run(connCtx, connector.Options{StateDir: b.stateDir, SyncEvery: time.Second, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}); err != nil && connCtx.Err() == nil {
			t.Errorf("connector stopped: %v", err)
		}
	}()
	defer func() { stopConnector(); <-connDone }()

	// Phone joins and connects over ISCP only.
	pcred := cc.issue(t, dom.Data.DomainID, consoleapi.TypePhone)
	m, dev, err := consoleapi.NewClient(pcred.Console, pcred.TLSPin).Join(ctx, pcred, consoleapi.JoinOptions{Name: "Phone IT", Platform: "android"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Grant == nil || m.Host == nil || m.RelayCredentials == nil {
		t.Fatalf("membership incomplete: grant=%v host=%v relay=%v", m.Grant != nil, m.Host != nil, m.RelayCredentials != nil)
	}
	keeper := linkclient.NewGrantKeeper(m, dev)
	opts, err := linkclient.FromMembership(m, dev, keeper, nil, contract.Device{Class: contract.ClassPhone, Name: "Phone IT", Conditions: contract.Conditions{Screen: contract.ScreenSmall, Attachments: true}})
	if err != nil {
		t.Fatal(err)
	}
	opts.DisableLAN = true
	events := make(chan contract.Frame, 128)
	opts.OnEvent = func(f contract.Frame) {
		select {
		case events <- f:
		default:
		}
	}
	phone, err := linkclient.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the connector's directory sync to include the phone.
	waitUntil(t, 20*time.Second, func() bool {
		_, ok := b.server.link.directory.Lookup(dev.Identity.DeviceID)
		return ok
	})
	phone.Start(ctx)
	defer phone.Close()
	rctx, rcancel := context.WithTimeout(ctx, 60*time.Second)
	defer rcancel()
	if err := phone.WaitReady(rctx); err != nil {
		t.Fatalf("phone not ready over ISCP: %v\n%s", err, r.Log())
	}
	if phone.Path() != linkclient.PathISCP {
		t.Fatal("expected the ISCP path")
	}
	var h contract.SessionHeader
	if err := phone.Request(ctx, contract.OpSessionCreate, contract.SessionCreateParams{Title: "Over Relay", ClientRequestID: "it-1"}, &h); err != nil {
		t.Fatal(err)
	}
	var sub contract.RunSubscribeResult
	if err := phone.Request(ctx, contract.OpRunSubscribe, contract.RunSubscribeParams{SessionID: h.ID}, &sub); err != nil {
		t.Fatal(err)
	}
	var sent contract.MessageSendResult
	if err := phone.Request(ctx, contract.OpMessageSend, contract.MessageSendParams{SessionID: h.ID, ClientMessageID: "it-msg", Content: "通过 Relay 发送"}, &sent); err != nil {
		t.Fatal(err)
	}
	final := false
	deadline := time.After(60 * time.Second)
	for !final {
		select {
		case f := <-events:
			if f.Topic == contract.TopicRun {
				var ev contract.RunEvent
				_ = json.Unmarshal(f.Data, &ev)
				final = ev.Kind == "final" && ev.RunID == sent.RunID
			}
		case <-deadline:
			t.Fatal("no final run event over ISCP")
		}
	}
	var page contract.SessionMessagesResult
	if err := phone.Request(ctx, contract.OpSessionMessages, contract.SessionMessagesParams{SessionID: h.ID}, &page); err != nil || len(page.Messages) == 0 {
		t.Fatalf("history over ISCP %+v %v", page, err)
	}
	// The host WebChat sees the same conversation.
	if stored, ok, _ := b.server.store.GetSession(ctx, h.ID); !ok || stored.OwnerID != app.DefaultOwnerID {
		t.Fatal("conversation not stored on the backend")
	}
	// A change made elsewhere is announced over ISCP as a sync event.
	for len(events) > 0 {
		<-events
	}
	if _, err := b.server.store.CreateSession(ctx, "Made on the host"); err != nil {
		t.Fatal(err)
	}
	syncDeadline := time.After(10 * time.Second)
	for synced := false; !synced; {
		select {
		case f := <-events:
			synced = f.Topic == contract.TopicSync
		case <-syncDeadline:
			t.Fatal("no sync event over ISCP")
		}
	}

	// Revoke on the console: the connector syncs, closes the session and the
	// Gateway refuses the device.
	var devices struct {
		Items []struct {
			DeviceID string `json:"device_id"`
		} `json:"items"`
	}
	cc.do(t, "GET", "/v1/domains/"+dom.Data.DomainID+"/devices", nil, &devices)
	cc.do(t, "POST", "/v1/domains/"+dom.Data.DomainID+"/devices/"+dev.Identity.DeviceID+"/revoke", map[string]any{}, nil)
	waitUntil(t, 30*time.Second, func() bool { return phone.Path() == linkclient.PathNone })
	// The Relay drops the device at once; the Gateway learns of the
	// revocation at the connector's next directory sync.
	waitUntil(t, 15*time.Second, func() bool {
		_, ok := b.server.link.directory.Lookup(dev.Identity.DeviceID)
		return !ok
	})
	if c, found, _ := b.server.store.GetClient(ctx, linkClientID(dev.Identity.DeviceID)); !found || c.RevokedAt == nil {
		t.Fatal("the device's client row was not revoked")
	}
}
