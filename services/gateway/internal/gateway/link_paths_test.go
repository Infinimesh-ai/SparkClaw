package gateway

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/socketwire"
)

// The LAN and the ISCP Relay are two ways to the same entry: a device gets
// the same principal, the same features and the same reverse operations on
// both, and both replies name the LAN connector so that a device on the
// Relay can find the LAN.
func TestLinkBothPathsGiveTheSameRightsAndNameTheLAN(t *testing.T) {
	b := newLinkTestBackend(t)
	conditions := contract.Conditions{Screen: contract.ScreenLarge, BrowserSurface: true, Attachments: true, Microphone: true}
	reverse := []string{"browser.controller.status"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	probe, dev, rec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, conditions)
	_ = probe
	writeLinkDirectory(t, b.stateDir, rec)
	lan, _ := linkTestClientFor(t, b, dev, contract.ClassDesktop, conditions, reverse, func(context.Context, string, json.RawMessage) (any, error) { return nil, nil })
	lan.Start(ctx)
	defer lan.Close()
	if err := lan.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	overLAN := lan.Hello()

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
	hello, _ := contract.Encode(contract.Frame{Type: contract.FrameHello, ID: "hello", Hello: &contract.Hello{Versions: []string{contract.Version}, Device: &contract.Device{Class: contract.ClassDesktop, Name: contract.ClassDesktop, Conditions: conditions}, Reverse: reverse}})
	if err := st.WriteFrame(ctx, hello); err != nil {
		t.Fatal(err)
	}
	raw, err := st.ReadFrame(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f, err := contract.Decode(raw)
	if err != nil || f.Hello == nil {
		t.Fatalf("hello reply %s %v", raw, err)
	}
	overISCP := f.Hello

	if len(overLAN.Features) == 0 || !reflect.DeepEqual(overLAN.Features, overISCP.Features) {
		t.Fatalf("features differ between the paths:\nlan  %v\niscp %v", overLAN.Features, overISCP.Features)
	}
	if !reflect.DeepEqual(overLAN.Principal, overISCP.Principal) {
		t.Fatalf("principal differs: lan %+v iscp %+v", overLAN.Principal, overISCP.Principal)
	}
	if !reflect.DeepEqual(overLAN.Reverse, reverse) || !reflect.DeepEqual(overISCP.Reverse, reverse) {
		t.Fatalf("reverse operations: lan %v iscp %v", overLAN.Reverse, overISCP.Reverse)
	}
	for name, h := range map[string]*contract.Hello{"lan": overLAN, "iscp": overISCP} {
		if h.LAN == nil || h.LAN.TLSPin != b.lan.TLSPin || !reflect.DeepEqual(h.LAN.Endpoints, b.lan.Endpoints) {
			t.Fatalf("hello over %s names the LAN connector %+v, want %+v", name, h.LAN, b.lan)
		}
	}
}

// Without configured names the LAN connector publishes the host's own
// private addresses.
func TestLinkLANPublishesTheHostsAddressesWhenNoneAreConfigured(t *testing.T) {
	dir := t.TempDir()
	svc := link.NewService()
	lan, err := link.NewLANServer(link.LANConfig{Addr: "127.0.0.1:18792", StateDir: dir, Directory: link.NewDirectory(dir), Service: svc, Authorize: func(context.Context, string, string) (link.Principal, error) { return link.Principal{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	addrs, _ := net.InterfaceAddrs()
	private := 0
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			private++
		}
	}
	info := lan.Info()
	if private == 0 {
		if info != nil {
			t.Fatalf("no private address, yet %+v was published", info)
		}
		return
	}
	if info == nil || len(info.Endpoints) != private || info.TLSPin != lan.Pin() {
		t.Fatalf("published %+v for %d private addresses", info, private)
	}
	for _, e := range info.Endpoints {
		if !strings.HasPrefix(e, "wss://") || !strings.HasSuffix(e, ":18792"+contract.LANPath) {
			t.Fatalf("endpoint %q", e)
		}
	}
}
