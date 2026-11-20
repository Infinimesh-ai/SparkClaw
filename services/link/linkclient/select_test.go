package linkclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/gorilla/websocket"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

const testHost = "host-1"

// fakeLAN is a LAN connector that authenticates anyone and answers pings.
// silent makes its open connections stop answering, as a network that is
// gone does; refuse turns new connections away.
type fakeLAN struct {
	srv      *httptest.Server
	silent   atomic.Bool
	refuse   atomic.Bool
	requests atomic.Int64
	hello    contract.Hello
}

func newFakeLAN(t *testing.T) *fakeLAN {
	t.Helper()
	f := &fakeLAN{hello: contract.Hello{Version: contract.Version, Profile: contract.ProfileDuplex, Features: []string{contract.OpPing}}}
	up := websocket.Upgrader{Subprotocols: []string{contract.LANProtocol}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.refuse.Load() {
			http.Error(w, "away", http.StatusServiceUnavailable)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.WriteJSON(contract.LANControl{Type: "challenge", Host: testHost, Nonce: "n"})
		var auth contract.LANControl
		if ws.ReadJSON(&auth) != nil {
			return
		}
		_ = ws.WriteJSON(contract.LANControl{Type: "authenticated"})
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			fr, err := contract.Decode(raw)
			if err != nil || f.silent.Load() {
				continue
			}
			var out contract.Frame
			switch fr.Type {
			case contract.FrameHello:
				h := f.hello
				out = contract.Frame{Type: contract.FrameHello, ID: fr.ID, Hello: &h}
			case contract.FrameRequest:
				f.requests.Add(1)
				out, _ = contract.Success(fr.ID, map[string]string{"via": PathLAN})
			default:
				continue
			}
			b, _ := contract.Encode(out)
			_ = ws.WriteMessage(websocket.TextMessage, b)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLAN) lan() consoleapi.LAN {
	return consoleapi.LAN{Endpoints: []string{"wss" + strings.TrimPrefix(f.srv.URL, "https") + contract.LANPath}, TLSPin: relay.CertificatePin(f.srv.Certificate().Raw)}
}

// fakePath is a path put into the client directly, standing in for the
// Relay path (or for a LAN path with a chosen failure).
type fakePath struct {
	p        *pathConn
	silent   atomic.Bool
	failSend atomic.Bool
	requests atomic.Int64
}

func addFakePath(c *Client, name string) *fakePath {
	f := &fakePath{}
	f.p = &pathConn{name: name, hello: &contract.Hello{Version: contract.Version}, pending: map[string]chan contract.Frame{}, done: make(chan struct{})}
	f.p.send = func(_ context.Context, raw []byte) error {
		if f.failSend.Load() {
			return errors.New("broken pipe")
		}
		fr, err := contract.Decode(raw)
		if err != nil || fr.Type != contract.FrameRequest || f.silent.Load() {
			return nil
		}
		f.requests.Add(1)
		out, _ := contract.Success(fr.ID, map[string]string{"via": name})
		b, _ := contract.Encode(out)
		go c.receive(f.p, b)
		return nil
	}
	c.setPath(name, f.p)
	return f
}

type states struct {
	mu   sync.Mutex
	seen []string
}

func (s *states) add(path string, _ *contract.Hello) {
	s.mu.Lock()
	s.seen = append(s.seen, path)
	s.mu.Unlock()
}

func (s *states) list() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.seen, ">")
}

func startClient(t *testing.T, lan consoleapi.LAN, disableLAN bool, st *states, onLAN func(consoleapi.LAN)) *Client {
	t.Helper()
	dev, err := identity.NewDevice(crypto.NewProvider(), "domain", "phone-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Device: dev, Host: consoleapi.Host{DeviceID: testHost, LAN: lan}, Describe: contract.Device{Class: contract.ClassPhone}, DisableLAN: disableLAN, DisableNetworkWatch: true, OnHostLAN: onLAN}
	if st != nil {
		opts.OnState = st.add
	}
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	c.Start(context.Background())
	t.Cleanup(c.Close)
	return c
}

func waitPath(t *testing.T, c *Client, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if c.Path() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("path = %q, want %q", c.Path(), want)
}

func via(t *testing.T, c *Client, timeout time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out struct {
		Via string `json:"via"`
	}
	if err := c.Request(ctx, contract.OpPing, nil, &out); err != nil {
		t.Fatalf("request: %v", err)
	}
	return out.Via
}

// Leaving the backend's network moves the device to the Relay, and coming
// back moves it to the LAN again; nobody chooses.
func TestNetworkChangeMovesBetweenLANAndRelay(t *testing.T) {
	lan := newFakeLAN(t)
	st := &states{}
	c := startClient(t, lan.lan(), false, st, nil)
	waitPath(t, c, PathLAN, 5*time.Second)
	remote := addFakePath(c, PathISCP)
	if got := via(t, c, time.Second); got != PathLAN {
		t.Fatalf("with both paths up the request went over %q", got)
	}

	// The device leaves the LAN: the open connection goes silent.
	lan.silent.Store(true)
	lan.refuse.Store(true)
	c.NetworkChanged()
	started := time.Now()
	// A request made during the check waits and then uses the Relay.
	if got := via(t, c, settleWindow); got != PathISCP {
		t.Fatalf("after leaving the LAN the request went over %q", got)
	}
	if took := time.Since(started); took > lanVerifyTimeout+2*time.Second {
		t.Fatalf("the switch to the Relay took %s", took)
	}
	waitPath(t, c, PathISCP, time.Second)

	// The device comes back.
	lan.silent.Store(false)
	lan.refuse.Store(false)
	c.NetworkChanged()
	waitPath(t, c, PathLAN, 3*time.Second)
	if got := via(t, c, time.Second); got != PathLAN {
		t.Fatalf("back on the LAN the request went over %q", got)
	}
	if got := st.list(); got != "lan>iscp>lan" {
		t.Fatalf("reported paths %q", got)
	}
	if remote.requests.Load() == 0 {
		t.Fatal("the Relay path was never verified")
	}
}

// A network change that leaves the LAN reachable reports no switch.
func TestNetworkChangeOnAHealthyLANReportsNoSwitch(t *testing.T) {
	lan := newFakeLAN(t)
	st := &states{}
	c := startClient(t, lan.lan(), false, st, nil)
	waitPath(t, c, PathLAN, 5*time.Second)
	addFakePath(c, PathISCP)
	for i := 0; i < 3; i++ {
		c.NetworkChanged()
		if got := via(t, c, settleWindow); got != PathLAN {
			t.Fatalf("round %d went over %q", i, got)
		}
	}
	time.Sleep(networkDebounce + 300*time.Millisecond)
	if got := st.list(); got != "lan" {
		t.Fatalf("reported paths %q", got)
	}
}

// The backend's hello over the Relay names its LAN connector; a device
// that did not know one finds the LAN with it.
func TestLANConnectorLearnedFromTheHello(t *testing.T) {
	lan := newFakeLAN(t)
	learned := make(chan consoleapi.LAN, 1)
	c := startClient(t, consoleapi.LAN{}, false, nil, func(l consoleapi.LAN) { learned <- l })
	addFakePath(c, PathISCP)
	if got := via(t, c, time.Second); got != PathISCP {
		t.Fatalf("without a LAN connector the request went over %q", got)
	}
	want := lan.lan()
	c.learnLAN(&contract.LAN{Endpoints: want.Endpoints, TLSPin: want.TLSPin})
	select {
	case got := <-learned:
		if got.TLSPin != want.TLSPin || len(got.Endpoints) != 1 || got.Endpoints[0] != want.Endpoints[0] {
			t.Fatalf("kept %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the LAN connector was not handed to the caller")
	}
	waitPath(t, c, PathLAN, 3*time.Second)
	// The same connector again changes nothing.
	c.learnLAN(&contract.LAN{Endpoints: want.Endpoints, TLSPin: want.TLSPin})
	select {
	case <-learned:
		t.Fatal("an unchanged connector was reported again")
	case <-time.After(100 * time.Millisecond):
	}
}

// With no path a request fails at once and unsent; right after a network
// change it waits for the path being dialed.
func TestRequestsWaitOnlyWhileTheNetworkSettles(t *testing.T) {
	c := startClient(t, consoleapi.LAN{}, true, nil, nil)
	started := time.Now()
	err := c.Request(context.Background(), contract.OpPing, nil, nil)
	var cerr *contract.Error
	if !errors.As(err, &cerr) || !cerr.NotSent || cerr.Code != contract.CodeBackendUnavailable {
		t.Fatalf("error %v", err)
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatal("an offline request did not fail at once")
	}
	c.NetworkChanged()
	go func() {
		time.Sleep(300 * time.Millisecond)
		addFakePath(c, PathISCP)
	}()
	if got := via(t, c, settleWindow); got != PathISCP {
		t.Fatalf("went over %q", got)
	}
}

// A request that could not be written to the LAN goes over the Relay, and
// the LAN path that no longer answers is closed.
func TestAnUnwrittenRequestMovesToTheOtherPath(t *testing.T) {
	c := startClient(t, consoleapi.LAN{}, true, nil, nil)
	local := addFakePath(c, PathLAN)
	addFakePath(c, PathISCP)
	if got := via(t, c, time.Second); got != PathLAN {
		t.Fatalf("went over %q", got)
	}
	local.failSend.Store(true)
	if got := via(t, c, time.Second); got != PathISCP {
		t.Fatalf("after the failed write the request went over %q", got)
	}
	select {
	case <-local.p.done:
	case <-time.After(lanVerifyTimeout + time.Second):
		t.Fatal("the dead LAN path stayed open")
	}
}
