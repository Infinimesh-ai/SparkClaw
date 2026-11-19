// Package connector is the ISCP connector (design 3.3): a separate process
// that holds the backend's device key, keeps the backend's membership in
// the console domain (directory sync, LAN publication, Relay access),
// terminates ISCP sessions from devices and passes each session's link
// frames to the Gateway over the private Unix socket. It does not restart
// when the Gateway does: a broken socket is re-attached and the device's
// hello is replayed.
package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/iscplink"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
	"github.com/Chiiz0/SparkClaw/services/link/socketwire"
)

// File names in the shared state directory (equal to the Gateway's).
const (
	MembershipFile = "membership.json"
	DirectoryFile  = "directory.json"
	LANFile        = "lan.json"
	SocketFile     = "gateway.sock"
	HostKeyFile    = "host-key.json"
	RelayCredsFile = "relay-credentials.json"
)

type hostKey struct {
	Identity identity.DeviceIdentity `json:"identity"`
	Private  string                  `json:"private"`
}

// LoadOrCreateHostKey returns the backend's device key in the Relay domain.
func LoadOrCreateHostKey(dir, relayDomain string) (identity.Device, error) {
	path := filepath.Join(dir, HostKeyFile)
	raw, err := os.ReadFile(path)
	if err == nil {
		var k hostKey
		if err := json.Unmarshal(raw, &k); err != nil {
			return identity.Device{}, err
		}
		b, err := crypto.DecodeBase64URL(k.Private)
		if err != nil {
			return identity.Device{}, err
		}
		priv, err := crypto.Ed25519PrivateKeyFromBytes(b)
		if err != nil {
			return identity.Device{}, err
		}
		if relayDomain != "" && k.Identity.DomainID != relayDomain {
			return identity.Device{}, fmt.Errorf("host key belongs to Relay domain %s, not %s", k.Identity.DomainID, relayDomain)
		}
		return identity.Device{Identity: k.Identity, Private: priv}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return identity.Device{}, err
	}
	if relayDomain == "" {
		return identity.Device{}, errors.New("no host key yet and no Relay domain to create one")
	}
	dev, err := consoleapi.NewDeviceIdentity(relayDomain, consoleapi.TypeSparkClaw)
	if err != nil {
		return identity.Device{}, err
	}
	if err := writeJSON(path, hostKey{Identity: dev.Identity, Private: crypto.Base64URL(dev.Private.BytesForDevStore())}); err != nil {
		return identity.Device{}, err
	}
	return dev, nil
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// Join registers the backend in a domain with a sparkclaw access
// credential and writes the membership.
func Join(ctx context.Context, dir string, cred consoleapi.Credential, consoleURL, consolePin, name string) (consoleapi.Membership, error) {
	if cred.Console != "" {
		consoleURL, consolePin = cred.Console, cred.TLSPin
	}
	if consoleURL == "" {
		return consoleapi.Membership{}, errors.New("console address is required (full credential or -console)")
	}
	if cred.Type != "" && cred.Type != consoleapi.TypeSparkClaw {
		return consoleapi.Membership{}, fmt.Errorf("this credential is for a %s, not the backend", cred.Type)
	}
	client := consoleapi.NewClient(consoleURL, consolePin)
	trust, err := client.Trust(ctx)
	if err != nil {
		return consoleapi.Membership{}, err
	}
	if len(trust.Relays) == 0 {
		return consoleapi.Membership{}, errors.New("console publishes no Relay")
	}
	dev, err := LoadOrCreateHostKey(dir, trust.Relays[0].DomainID)
	if err != nil {
		return consoleapi.Membership{}, err
	}
	var lan consoleapi.LAN
	_ = readJSON(filepath.Join(dir, LANFile), &lan)
	opts := consoleapi.JoinOptions{Name: name, Platform: "linux", Device: &dev}
	if len(lan.Endpoints) > 0 {
		opts.LAN = &lan
	}
	m, _, err := client.Join(ctx, cred, opts)
	if err != nil {
		return consoleapi.Membership{}, err
	}
	if m.RelayCredentials != nil {
		if err := writeJSON(filepath.Join(dir, RelayCredsFile), *m.RelayCredentials); err != nil {
			return consoleapi.Membership{}, err
		}
		m.RelayCredentials = nil
	}
	if err := writeJSON(filepath.Join(dir, MembershipFile), m); err != nil {
		return consoleapi.Membership{}, err
	}
	return m, nil
}

type fileCreds struct{ path string }

func (f fileCreds) LoadRelayCredentials() (relay.Credentials, error) {
	var c relay.Credentials
	err := readJSON(f.path, &c)
	return c, err
}
func (f fileCreds) SaveRelayCredentials(c relay.Credentials) error { return writeJSON(f.path, c) }

// Options configures Run.
type Options struct {
	StateDir string
	// SyncEvery is the directory sync interval.
	SyncEvery time.Duration
	Logger    *slog.Logger
}

// Connector is a running connector.
type Connector struct {
	opts       Options
	log        *slog.Logger
	membership consoleapi.Membership
	device     identity.Device
	console    *consoleapi.Client

	mu        sync.RWMutex
	directory map[string]consoleapi.DeviceRecord
	responder *iscplink.Responder
	lanMod    time.Time
	syncNow   chan struct{}
	lastEager time.Time
}

// requestSync asks for an immediate directory sync (a device that just
// joined), at most every few seconds.
func (c *Connector) requestSync() {
	c.mu.Lock()
	recent := time.Since(c.lastEager) < 3*time.Second
	if !recent {
		c.lastEager = time.Now()
	}
	c.mu.Unlock()
	if recent {
		return
	}
	select {
	case c.syncNow <- struct{}{}:
	default:
	}
}

// Run serves until ctx ends.
func Run(ctx context.Context, opts Options) error {
	if opts.SyncEvery <= 0 {
		opts.SyncEvery = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	var m consoleapi.Membership
	if err := readJSON(filepath.Join(opts.StateDir, MembershipFile), &m); err != nil {
		return fmt.Errorf("the backend has not joined a domain (run join first): %w", err)
	}
	if len(m.Relays) == 0 {
		return errors.New("membership names no Relay")
	}
	dev, err := LoadOrCreateHostKey(opts.StateDir, "")
	if err != nil {
		return err
	}
	if dev.Identity.DeviceID != m.Device.DeviceID {
		return errors.New("host key does not match the membership")
	}
	c := &Connector{opts: opts, log: opts.Logger, membership: m, device: dev, console: consoleapi.NewClient(m.ConsoleURL, m.ConsoleTLSPin), directory: map[string]consoleapi.DeviceRecord{}, syncNow: make(chan struct{}, 1)}
	var cached consoleapi.Directory
	if readJSON(filepath.Join(opts.StateDir, DirectoryFile), &cached) == nil {
		c.applyDirectory(cached)
	}
	ep := m.Relays[0]
	store := fileCreds{path: filepath.Join(opts.StateDir, RelayCredsFile)}
	initial, _ := store.LoadRelayCredentials()
	rc, err := relay.NewClient(ep, dev, store, initial, c.console.RelayRebinder(dev, m.ConsoleID, ep.RelayID))
	if err != nil {
		return err
	}
	resp, err := iscplink.NewResponder(iscplink.ResponderConfig{Relay: rc, Device: dev, Signer: func() identity.DeviceIdentity { return m.Signer }, Directory: c, OnSession: func(s *iscplink.PeerSession) { c.serveSession(ctx, s) }, OnUnknown: func(string) { c.requestSync() }})
	if err != nil {
		return err
	}
	c.responder = resp
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.syncLoop(ctx) }()
	go func() {
		defer wg.Done()
		_ = resp.Run(ctx, func(up bool) { c.log.Info("relay connection", "ready", up) })
	}()
	c.log.Info("link connector running", "host", dev.Identity.DeviceID, "relay", ep.RelayID, "domain", m.DomainID)
	wg.Wait()
	return ctx.Err()
}

// Lookup implements iscplink.Directory from the synced copy.
func (c *Connector) Lookup(deviceID string) (iscplink.DeviceEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.directory[deviceID]
	if !ok {
		return iscplink.DeviceEntry{}, false
	}
	return iscplink.DeviceEntry{Identity: rec.Identity, Trusted: rec.State == consoleapi.StateTrusted && rec.Role == consoleapi.RoleOwnerDevice, RevocationEpoch: rec.RevocationEpoch, Class: rec.Type, Name: rec.Name}, true
}

func (c *Connector) applyDirectory(d consoleapi.Directory) []string {
	next := map[string]consoleapi.DeviceRecord{}
	for _, r := range d.Devices {
		next[r.DeviceID] = r
	}
	c.mu.Lock()
	var revoked []string
	for id, old := range c.directory {
		n, ok := next[id]
		if old.State == consoleapi.StateTrusted && (!ok || n.State != consoleapi.StateTrusted || n.RevocationEpoch > old.RevocationEpoch) {
			revoked = append(revoked, id)
		}
	}
	c.directory = next
	c.mu.Unlock()
	return revoked
}

func (c *Connector) syncLoop(ctx context.Context) {
	c.syncOnce(ctx)
	t := time.NewTicker(c.opts.SyncEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.syncOnce(ctx)
		case <-c.syncNow:
			c.syncOnce(ctx)
		}
	}
}

func (c *Connector) syncOnce(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Publish LAN information whenever the Gateway rewrote it.
	if st, err := os.Stat(filepath.Join(c.opts.StateDir, LANFile)); err == nil && !st.ModTime().Equal(c.lanMod) {
		var lan consoleapi.LAN
		if readJSON(filepath.Join(c.opts.StateDir, LANFile), &lan) == nil {
			if _, err := c.console.PublishHost(sctx, c.device, c.membership.ConsoleID, lan, c.membership.Device.Name); err == nil {
				c.lanMod = st.ModTime()
			} else {
				c.log.Warn("LAN publication failed", "error", err)
			}
		}
	}
	d, err := c.console.Directory(sctx, c.device, c.membership.ConsoleID, 0)
	if err != nil {
		// The link layer does not depend on the console being online; the
		// last copy keeps serving.
		c.log.Warn("directory sync failed; keeping the last copy", "error", err)
		return
	}
	if err := writeJSON(filepath.Join(c.opts.StateDir, DirectoryFile), d); err != nil {
		c.log.Warn("directory write failed", "error", err)
	}
	for _, id := range c.applyDirectory(d) {
		c.log.Info("device revoked; closing its session", "device", id)
		c.responder.Revoke(id)
	}
}

// serveSession bridges one ISCP session to the Gateway.
func (c *Connector) serveSession(ctx context.Context, s *iscplink.PeerSession) {
	sockPath := filepath.Join(c.opts.StateDir, SocketFile)
	var (
		mu     sync.Mutex
		stream *socketwire.Stream
		hello  []byte
	)
	attach := func() (*socketwire.Stream, error) {
		conn, err := net.DialTimeout("unix", sockPath, 3*time.Second)
		if err != nil {
			return nil, err
		}
		st := socketwire.NewStream(conn)
		b, _ := json.Marshal(socketwire.Attach{DeviceID: s.DeviceID, Transport: "iscp", SessionID: s.SessionID})
		actx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := st.WriteFrame(actx, b); err != nil {
			st.Close()
			return nil, err
		}
		raw, err := st.ReadFrame(actx)
		if err != nil {
			st.Close()
			return nil, err
		}
		var res socketwire.AttachResult
		if json.Unmarshal(raw, &res) != nil || !res.OK {
			st.Close()
			if res.Code == contract.CodeRevoked {
				return nil, errRevoked
			}
			return nil, fmt.Errorf("gateway refused attach: %s %s", res.Code, res.Message)
		}
		return st, nil
	}
	// Gateway → device.
	pump := func(st *socketwire.Stream, swallowHello bool) {
		for {
			raw, err := st.ReadFrame(ctx)
			if err != nil {
				iscplink.Debugf("connector: gateway stream for %s ended: %v", s.DeviceID, err)
				return
			}
			if swallowHello {
				swallowHello = false
				if f, err := contract.Decode(raw); err == nil && f.Type == contract.FrameHello {
					continue
				}
			}
			sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = s.Send(sctx, raw)
			cancel()
			iscplink.Debugf("connector: gateway frame to device (%d bytes): %v", len(raw), err)
			if err != nil {
				return
			}
		}
	}
	reconnect := func() *socketwire.Stream {
		for attempt := 0; attempt < 60 && ctx.Err() == nil; attempt++ {
			select {
			case <-s.Done():
				return nil
			default:
			}
			st, err := attach()
			iscplink.Debugf("connector: attach %s: %v", s.DeviceID, err)
			if errors.Is(err, errRevoked) {
				s.Close(iscplink.ResetRevoked)
				return nil
			}
			if err == nil {
				mu.Lock()
				h := hello
				mu.Unlock()
				if h != nil {
					if st.WriteFrame(ctx, h) != nil {
						st.Close()
						continue
					}
					go func() {
						pump(st, true)
						st.Close()
					}()
				} else {
					go func() {
						pump(st, false)
						st.Close()
					}()
				}
				return st
			}
			select {
			case <-ctx.Done():
				return nil
			case <-s.Done():
				return nil
			case <-time.After(time.Second):
			}
		}
		return nil
	}
	first := reconnect()
	mu.Lock()
	stream = first
	mu.Unlock()
	defer func() {
		mu.Lock()
		if stream != nil {
			stream.Close()
		}
		mu.Unlock()
	}()
	// Device → Gateway.
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.Done():
			return
		case raw := <-s.Frames():
			mu.Lock()
			if hello == nil {
				if f, err := contract.Decode(raw); err == nil && f.Type == contract.FrameHello {
					hello = append([]byte(nil), raw...)
				}
			}
			st := stream
			mu.Unlock()
			var werr error
			if st != nil {
				werr = st.WriteFrame(ctx, raw)
			}
			iscplink.Debugf("connector: device frame to gateway (%d bytes) stream=%v err=%v", len(raw), st != nil, werr)
			if st == nil || werr != nil {
				if st != nil {
					st.Close()
				}
				// Answer the request at once so the device does not wait.
				if f, err := contract.Decode(raw); err == nil && f.Type == contract.FrameRequest {
					fail, _ := contract.Encode(contract.Failure(f.ID, &contract.Error{Code: contract.CodeBackendUnavailable, Message: "the backend is restarting", Retryable: true}))
					sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
					_ = s.Send(sctx, fail)
					cancel()
				}
				next := reconnect()
				mu.Lock()
				stream = next
				mu.Unlock()
			}
		}
	}
}

var errRevoked = errors.New("device revoked")

// Leave removes the backend's binding.
func Leave(ctx context.Context, dir string) error {
	var m consoleapi.Membership
	if err := readJSON(filepath.Join(dir, MembershipFile), &m); err != nil {
		return err
	}
	dev, err := LoadOrCreateHostKey(dir, "")
	if err != nil {
		return err
	}
	if err := consoleapi.NewClient(m.ConsoleURL, m.ConsoleTLSPin).Leave(ctx, dev, m.ConsoleID); err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, MembershipFile))
}
