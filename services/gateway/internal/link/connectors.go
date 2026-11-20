package link

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/gorilla/websocket"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
	"github.com/Chiiz0/SparkClaw/services/link/socketwire"
)

// Authorizer turns a verified device into a Principal; it checks the
// device directory every time (design 3.3, 3.4).
type Authorizer func(ctx context.Context, deviceID, transport string) (Principal, error)

// ErrDeviceNotTrusted is returned for unknown or revoked devices.
var ErrDeviceNotTrusted = errors.New("device is not a trusted member of this domain")

// LAN protocol constants (shared with clients through the contract).
const (
	LANPath            = contract.LANPath
	LANProtocol        = contract.LANProtocol
	LANChallengePrefix = contract.LANChallengePrefix
)

// LANConfig configures the LAN connector.
type LANConfig struct {
	Addr string // listen address, for example :18792
	// Hosts are the names and addresses put into the certificate and the
	// endpoints. Empty means the host's own private addresses, read again
	// whenever they change.
	Hosts     []string
	StateDir  string
	Directory *Directory
	Service   *Service
	Authorize Authorizer
}

// LANServer is the LAN connector: pinned TLS plus WebSocket inside the
// Gateway process.
type LANServer struct {
	cfg  LANConfig
	srv  *http.Server
	pin  string
	up   websocket.Upgrader
	wg   sync.WaitGroup
	ln   net.Listener
	lock sync.Mutex
	// info is what devices are told to dial (lan.json and every hello).
	info consoleapi.LAN
	stop chan struct{}
	once sync.Once
}

// LAN keepalive. A device that left the network cannot say so: the
// connector pings often and ends a connection that stays silent, so
// reverse calls stop choosing it within lanSilence.
const (
	lanPingInterval = 10 * time.Second
	lanSilence      = 35 * time.Second
	lanHostsRefresh = 30 * time.Second
)

// NewLANServer prepares the certificate and publishes lan.json.
func NewLANServer(cfg LANConfig) (*LANServer, error) {
	if cfg.Addr == "" || cfg.StateDir == "" || cfg.Directory == nil || cfg.Service == nil || cfg.Authorize == nil {
		return nil, errors.New("link LAN configuration incomplete")
	}
	cert, der, err := loadOrCreateCertificate(filepath.Join(cfg.StateDir, LANCertFile), filepath.Join(cfg.StateDir, LANKeyFile), cfg.Hosts)
	if err != nil {
		return nil, err
	}
	l := &LANServer{cfg: cfg, pin: relay.CertificatePin(der), stop: make(chan struct{})}
	l.up = websocket.Upgrader{Subprotocols: []string{LANProtocol}, CheckOrigin: func(*http.Request) bool { return true }, ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10}
	mux := http.NewServeMux()
	mux.HandleFunc(LANPath, l.serveWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	l.srv = &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}
	if err := l.publish(); err != nil {
		return nil, err
	}
	return l, nil
}

// Pin returns the LAN certificate pin.
func (l *LANServer) Pin() string { return l.pin }

// Info returns the LAN connector as devices should dial it now.
func (l *LANServer) Info() *contract.LAN {
	l.lock.Lock()
	defer l.lock.Unlock()
	if len(l.info.Endpoints) == 0 {
		return nil
	}
	return &contract.LAN{Endpoints: append([]string(nil), l.info.Endpoints...), TLSPin: l.info.TLSPin}
}

// publish writes lan.json when the endpoints changed.
func (l *LANServer) publish() error {
	_, port, err := net.SplitHostPort(l.cfg.Addr)
	if err != nil {
		return err
	}
	hosts := l.cfg.Hosts
	if len(hosts) == 0 {
		hosts = privateAddresses()
	}
	lan := consoleapi.LAN{TLSPin: l.pin}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		lan.Endpoints = append(lan.Endpoints, "wss://"+net.JoinHostPort(h, port)+LANPath)
	}
	l.lock.Lock()
	same := l.info.TLSPin == lan.TLSPin && strings.Join(l.info.Endpoints, " ") == strings.Join(lan.Endpoints, " ")
	l.info = lan
	l.lock.Unlock()
	if same {
		return nil
	}
	return WriteJSONAtomic(filepath.Join(l.cfg.StateDir, LANFile), lan)
}

// privateAddresses lists the host's private IPv4 addresses: where devices
// on the same network reach it when no host name was configured.
func privateAddresses() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip := n.IP.To4(); ip != nil && ip.IsPrivate() {
			out = append(out, ip.String())
		}
	}
	return out
}

// Start begins serving in the background.
func (l *LANServer) Start() error {
	ln, err := net.Listen("tcp", l.cfg.Addr)
	if err != nil {
		return err
	}
	l.ln = ln
	go func() {
		if err := l.srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("link LAN connector stopped", "error", err)
		}
	}()
	if len(l.cfg.Hosts) == 0 {
		// The host's addresses follow its network; devices learn the
		// current ones from the next hello on either path.
		go func() {
			t := time.NewTicker(lanHostsRefresh)
			defer t.Stop()
			for {
				select {
				case <-l.stop:
					return
				case <-t.C:
					if err := l.publish(); err != nil {
						slog.Warn("link LAN endpoints not published", "error", err)
					}
				}
			}
		}()
	}
	return nil
}

// Addr returns the bound address (after Start).
func (l *LANServer) Addr() string {
	if l.ln == nil {
		return l.cfg.Addr
	}
	return l.ln.Addr().String()
}

// Shutdown stops the listener and waits for connections.
func (l *LANServer) Shutdown(ctx context.Context) error {
	l.once.Do(func() { close(l.stop) })
	err := l.srv.Shutdown(ctx)
	done := make(chan struct{})
	go func() { l.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return err
}

type lanControl = contract.LANControl

func (l *LANServer) serveWS(w http.ResponseWriter, r *http.Request) {
	m, ok := l.cfg.Directory.Membership()
	if !ok {
		http.Error(w, "backend has not joined a domain", http.StatusServiceUnavailable)
		return
	}
	ws, err := l.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	l.wg.Add(1)
	defer l.wg.Done()
	defer ws.Close()
	ws.SetReadLimit(contract.MaxFrameBytes + 4096)
	nonce := crypto.Base64URL(crypto.RandomBytes(24))
	if err := ws.WriteJSON(lanControl{Type: "challenge", Version: LANProtocol, Host: m.Device.DeviceID, Nonce: nonce}); err != nil {
		return
	}
	_ = ws.SetReadDeadline(time.Now().Add(20 * time.Second))
	var auth lanControl
	var proof identity.DeviceProof
	if err := ws.ReadJSON(&auth); err != nil || auth.Type != "auth" || len(auth.Proof) == 0 || json.Unmarshal(auth.Proof, &proof) != nil {
		_ = ws.WriteJSON(lanControl{Type: "error", Code: contract.CodeUnauthorized, Message: "expected auth"})
		return
	}
	rec, ok := l.cfg.Directory.Lookup(auth.DeviceID)
	if !ok {
		_ = ws.WriteJSON(lanControl{Type: "error", Code: contract.CodeRevoked, Message: "device is not trusted"})
		return
	}
	if err := identity.VerifyProof(crypto.NewProvider(), rec.Identity, proof, m.Device.DeviceID, LANChallengePrefix+nonce, time.Now().UTC(), 2*time.Minute); err != nil {
		_ = ws.WriteJSON(lanControl{Type: "error", Code: contract.CodeUnauthorized, Message: "device proof invalid"})
		return
	}
	p, err := l.cfg.Authorize(r.Context(), auth.DeviceID, "lan")
	if err != nil {
		_ = ws.WriteJSON(lanControl{Type: "error", Code: contract.CodeUnauthorized, Message: err.Error()})
		return
	}
	pr := p.Principal
	if err := ws.WriteJSON(lanControl{Type: "authenticated", Principal: &pr}); err != nil {
		return
	}
	// Any frame or pong from the device keeps the connection.
	_ = ws.SetReadDeadline(time.Now().Add(lanSilence))
	stream := &wsStream{ws: ws, silence: lanSilence}
	ws.SetPongHandler(func(string) error { _ = ws.SetReadDeadline(time.Now().Add(lanSilence)); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		t := time.NewTicker(lanPingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				stream.mu.Lock()
				err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				stream.mu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	_ = l.cfg.Service.Serve(ctx, p, stream)
}

type wsStream struct {
	ws *websocket.Conn
	mu sync.Mutex
	// silence is how long the peer may stay silent; zero means forever.
	silence time.Duration
}

func (s *wsStream) keepalive() {
	if s.silence > 0 {
		_ = s.ws.SetReadDeadline(time.Now().Add(s.silence))
	} else {
		_ = s.ws.SetReadDeadline(time.Time{})
	}
}

func (s *wsStream) ReadFrame(ctx context.Context) ([]byte, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = s.ws.SetReadDeadline(dl)
		defer s.keepalive()
	}
	kind, b, err := s.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	s.keepalive()
	if kind != websocket.TextMessage {
		return nil, errors.New("link frames are text")
	}
	return b, nil
}

func (s *wsStream) WriteFrame(ctx context.Context, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dl := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	_ = s.ws.SetWriteDeadline(dl)
	return s.ws.WriteMessage(websocket.TextMessage, b)
}

func (s *wsStream) Close() error { return s.ws.Close() }

func loadOrCreateCertificate(certPath, keyPath string, hosts []string) (tls.Certificate, []byte, error) {
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && len(c.Certificate) > 0 {
		leaf, perr := x509.ParseCertificate(c.Certificate[0])
		if perr == nil && coversHosts(leaf, hosts) && time.Now().Before(leaf.NotAfter.Add(-30*24*time.Hour)) {
			return c, c.Certificate[0], nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "SparkClaw link LAN"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else if h != "" {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return tls.Certificate{}, nil, err
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	return c, der, err
}

func coversHosts(leaf *x509.Certificate, hosts []string) bool {
	for _, h := range hosts {
		if h == "" {
			continue
		}
		if leaf.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

// --- connector Unix socket ---

// SocketServer accepts the ISCP connector's per-session streams.
type SocketServer struct {
	path      string
	service   *Service
	authorize Authorizer
	ln        net.Listener
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewSocketServer listens on the connector socket (0600, private dir).
func NewSocketServer(path string, service *Service, authorize Authorizer) (*SocketServer, error) {
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return nil, fmt.Errorf("link socket %s is already served", path)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &SocketServer{path: path, service: service, authorize: authorize, ln: ln, ctx: ctx, cancel: cancel}
	go s.accept()
	return s, nil
}

func (s *SocketServer) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(c)
		}()
	}
}

func (s *SocketServer) serve(c net.Conn) {
	stream := socketwire.NewStream(c)
	defer stream.Close()
	actx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	raw, err := stream.ReadFrame(actx)
	cancel()
	if err != nil {
		return
	}
	var attach socketwire.Attach
	reply := func(r socketwire.AttachResult) error {
		b, _ := json.Marshal(r)
		return stream.WriteFrame(s.ctx, b)
	}
	if json.Unmarshal(raw, &attach) != nil || attach.DeviceID == "" {
		_ = reply(socketwire.AttachResult{Code: contract.CodeInvalidRequest, Message: "bad attach"})
		return
	}
	transport := attach.Transport
	if transport == "" {
		transport = "iscp"
	}
	p, err := s.authorize(s.ctx, attach.DeviceID, transport)
	if err != nil {
		code := contract.CodeUnauthorized
		if errors.Is(err, ErrDeviceNotTrusted) {
			code = contract.CodeRevoked
		}
		_ = reply(socketwire.AttachResult{Code: code, Message: err.Error()})
		return
	}
	pr := p.Principal
	if reply(socketwire.AttachResult{OK: true, Principal: &pr}) != nil {
		return
	}
	_ = s.service.Serve(s.ctx, p, stream)
}

// Close stops accepting and ends every stream.
func (s *SocketServer) Close() error {
	s.cancel()
	err := s.ln.Close()
	s.wg.Wait()
	_ = os.Remove(s.path)
	return err
}
