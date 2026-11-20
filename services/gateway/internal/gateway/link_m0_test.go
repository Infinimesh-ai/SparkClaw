package gateway

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
	"github.com/Chiiz0/SparkClaw/services/link/socketwire"
)

// linkTestClientFor opens another client for an existing device identity,
// as the same device does on its second path.
func linkTestClientFor(t *testing.T, b *linkTestBackend, dev identity.Device, class string, conditions contract.Conditions, reverse []string, onReverse linkclient.ReverseHandler) (*linkclient.Client, chan contract.Frame) {
	t.Helper()
	events := make(chan contract.Frame, 4096)
	c, err := linkclient.New(linkclient.Options{Device: dev, Host: consoleapi.Host{DeviceID: b.host.Identity.DeviceID, Identity: b.host.Identity, LAN: b.lan}, Describe: contract.Device{Class: class, Name: class, Conditions: conditions}, Reverse: reverse, OnReverse: onReverse, OnEvent: func(f contract.Frame) {
		select {
		case events <- f:
		default:
			t.Error("event buffer full")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	return c, events
}

func nextRunEvent(t *testing.T, events chan contract.Frame) contract.RunEvent {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-events:
			if f.Topic != contract.TopicRun {
				continue
			}
			var ev contract.RunEvent
			if err := json.Unmarshal(f.Data, &ev); err != nil {
				t.Fatal(err)
			}
			return ev
		case <-deadline:
			t.Fatal("no run event")
		}
	}
}

func TestLinkRunStreamContinuesAfterAPathSwitch(t *testing.T) {
	b := newLinkTestBackend(t)
	first, dev, rec, events := newLinkTestDevice(t, b, contract.ClassDesktop, contract.Conditions{Screen: contract.ScreenLarge})
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first.Start(ctx)
	if first.WaitReady(ctx) != nil {
		t.Fatal("first path not ready")
	}
	var h contract.SessionHeader
	if err := first.Request(ctx, contract.OpSessionCreate, contract.SessionCreateParams{Title: "Switch", ClientRequestID: "switch"}, &h); err != nil {
		t.Fatal(err)
	}
	var sub contract.RunSubscribeResult
	if err := first.Request(ctx, contract.OpRunSubscribe, contract.RunSubscribeParams{SessionID: h.ID}, &sub); err != nil {
		t.Fatal(err)
	}
	runs := b.server.link.runs
	runs.begin(h.ID, "run-switch", execution.WorkbenchBinding{})
	runs.delta(h.ID, "run-switch", "one ")
	if ev := nextRunEvent(t, events); ev.Kind != "state" || ev.Seq != sub.Status.Seq+1 {
		t.Fatalf("first event %+v after snapshot seq %d", ev, sub.Status.Seq)
	}
	if ev := nextRunEvent(t, events); ev.Kind != "delta" || ev.Text != "one " {
		t.Fatalf("delta %+v", ev)
	}
	// The path drops; the device comes back on its other path.
	first.Close()
	runs.delta(h.ID, "run-switch", "two ")
	second, again := linkTestClientFor(t, b, dev, contract.ClassDesktop, contract.Conditions{Screen: contract.ScreenLarge}, nil, nil)
	second.Start(ctx)
	defer second.Close()
	if second.WaitReady(ctx) != nil {
		t.Fatal("second path not ready")
	}
	var resumed contract.RunSubscribeResult
	if err := second.Request(ctx, contract.OpRunSubscribe, contract.RunSubscribeParams{SessionID: h.ID}, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Status.State != contract.RunRunning || resumed.Status.Preview != "one two " || resumed.Status.RunID != "run-switch" {
		t.Fatalf("snapshot after the switch %+v", resumed.Status)
	}
	runs.delta(h.ID, "run-switch", "three")
	ev := nextRunEvent(t, again)
	if ev.Kind != "delta" || ev.Text != "three" || ev.Seq != resumed.Status.Seq+1 {
		t.Fatalf("stream did not continue from the snapshot: %+v (snapshot seq %d)", ev, resumed.Status.Seq)
	}
}

func TestLinkRunDeltasArriveInOrder(t *testing.T) {
	b := newLinkTestBackend(t)
	desk, _, rec, events := newLinkTestDevice(t, b, contract.ClassDesktop, contract.Conditions{Screen: contract.ScreenLarge})
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	desk.Start(ctx)
	defer desk.Close()
	if desk.WaitReady(ctx) != nil {
		t.Fatal("not ready")
	}
	var h contract.SessionHeader
	if err := desk.Request(ctx, contract.OpSessionCreate, contract.SessionCreateParams{Title: "Burst", ClientRequestID: "burst"}, &h); err != nil {
		t.Fatal(err)
	}
	var sub contract.RunSubscribeResult
	if err := desk.Request(ctx, contract.OpRunSubscribe, contract.RunSubscribeParams{SessionID: h.ID}, &sub); err != nil {
		t.Fatal(err)
	}
	runs := b.server.link.runs
	runs.begin(h.ID, "run-burst", execution.WorkbenchBinding{})
	var want strings.Builder
	for i := 0; i < 500; i++ {
		text := string(rune('a'+i%26)) + " "
		want.WriteString(text)
		runs.delta(h.ID, "run-burst", text)
	}
	seq := sub.Status.Seq
	var got strings.Builder
	for i := 0; i < 501; i++ {
		ev := nextRunEvent(t, events)
		if ev.Seq != seq+1 {
			t.Fatalf("event %d has seq %d after %d", i, ev.Seq, seq)
		}
		seq = ev.Seq
		if ev.Kind == "delta" {
			got.WriteString(ev.Text)
		}
	}
	if got.String() != want.String() {
		t.Fatal("deltas arrived out of order")
	}
	var status contract.RunStatus
	if err := desk.Request(ctx, contract.OpRunStatus, contract.RunRef{SessionID: h.ID}, &status); err != nil || status.Seq != seq || status.Preview != want.String() {
		t.Fatalf("run.status seq %d preview %q: %v", status.Seq, status.Preview, err)
	}
}

func TestLinkToolsDomainIsGone(t *testing.T) {
	b := newLinkTestBackend(t)
	desk, _, rec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, contract.Conditions{Screen: contract.ScreenLarge, BrowserSurface: true})
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	desk.Start(ctx)
	defer desk.Close()
	if desk.WaitReady(ctx) != nil {
		t.Fatal("not ready")
	}
	for _, method := range []string{"tools.list", "tools.invoke"} {
		err := desk.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: "tools", Method: method, IdempotencyKey: newLinkUUID()}, nil)
		if contract.AsError(err).Code != contract.CodeUnsupportedCapability {
			t.Fatalf("%s: %v", method, err)
		}
	}
	var described contract.DeviceDescribeResult
	if err := desk.Request(ctx, contract.OpDeviceDescribe, nil, &described); err != nil {
		t.Fatal(err)
	}
	for _, d := range described.Domains {
		if d == "tools" {
			t.Fatal("tools is still listed as a domain")
		}
	}
}

func TestLinkReverseCallsPreferTheLANConnection(t *testing.T) {
	b := newLinkTestBackend(t)
	conditions := contract.Conditions{Screen: contract.ScreenLarge, BrowserSurface: true}
	lan, dev, rec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, conditions)
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The ISCP path: the connector's socket attach, then hello with the
	// reverse operations; it answers every reverse call with "iscp".
	conn, err := net.Dial("unix", filepath.Join(b.stateDir, link.SocketFile))
	if err != nil {
		t.Fatal(err)
	}
	st := socketwire.NewStream(conn)
	defer st.Close()
	attach, _ := json.Marshal(socketwire.Attach{DeviceID: dev.Identity.DeviceID, Transport: "iscp"})
	if err := st.WriteFrame(ctx, attach); err != nil {
		t.Fatal(err)
	}
	if raw, err := st.ReadFrame(ctx); err != nil || !strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("attach %s %v", raw, err)
	}
	hello, _ := contract.Encode(contract.Frame{Type: contract.FrameHello, ID: "hello", Hello: &contract.Hello{Versions: []string{contract.Version}, Device: &contract.Device{Class: contract.ClassDesktop, Conditions: conditions}, Reverse: []string{"browser.controller.status"}}})
	if err := st.WriteFrame(ctx, hello); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadFrame(ctx); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			raw, err := st.ReadFrame(context.Background())
			if err != nil {
				return
			}
			f, err := contract.Decode(raw)
			if err != nil || f.Type != contract.FrameRequest {
				continue
			}
			resp, _ := contract.Success(f.ID, map[string]any{"status": 200, "body": map[string]string{"path": "iscp"}})
			out, _ := contract.Encode(resp)
			_ = st.WriteFrame(context.Background(), out)
		}
	}()
	installation := linkInstallationID(dev.Identity.DeviceID)
	clientID := linkClientID(dev.Identity.DeviceID)
	call := func() string {
		t.Helper()
		route, err := b.server.linkPeerRoute(b.server.link.opts.OwnerID, clientID, installation)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := route.Caller(ctx, "browser.controller.status", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(resp.Body, &body)
		return body.Path
	}
	waitUntil(t, 5*time.Second, func() bool { return len(b.server.link.service.Connections(dev.Identity.DeviceID)) == 1 })
	if got := call(); got != "iscp" {
		t.Fatalf("only the ISCP path is up, answered by %q", got)
	}
	// The LAN path comes up as well: reverse calls move to it.
	_ = lan
	desk, _ := linkTestClientFor(t, b, dev, contract.ClassDesktop, conditions, []string{"browser.controller.status"}, func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]any{"status": 200, "body": map[string]string{"path": "lan"}}, nil
	})
	desk.Start(ctx)
	defer desk.Close()
	if desk.WaitReady(ctx) != nil {
		t.Fatal("LAN not ready")
	}
	waitUntil(t, 5*time.Second, func() bool { return len(b.server.link.service.Connections(dev.Identity.DeviceID)) == 2 })
	for i := 0; i < 5; i++ {
		if got := call(); got != "lan" {
			t.Fatalf("reverse call %d went over %q while the LAN connection is open", i, got)
		}
	}
}
