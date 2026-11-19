// Package linkmobile is the gomobile binding of the link layer for the
// Android phone and watch clients (64-bit and 32-bit builds): joining a
// console domain with a SparkClaw access credential and a LinkClient.
// Device state (membership and key) lives in a private file in the app's
// data directory.
package linkmobile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

const stateFile = "link-device.json"
const relayFile = "link-relay.json"

type deviceState struct {
	Membership consoleapi.Membership   `json:"membership"`
	Identity   identity.DeviceIdentity `json:"identity"`
	Private    string                  `json:"private"`
	Class      string                  `json:"class"`
	Name       string                  `json:"name"`
	JoinedAt   time.Time               `json:"joined_at"`
}

var (
	defaultMu      sync.Mutex
	defaultConsole string
	defaultPin     string
)

// SetDefaultConsole names the console a bare 12-digit code is redeemed at
// (the device's trust configuration, design 3.5).
func SetDefaultConsole(url, tlsPin string) {
	defaultMu.Lock()
	defaultConsole, defaultPin = strings.TrimSpace(url), strings.TrimSpace(tlsPin)
	defaultMu.Unlock()
}

func writePrivate(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func load(dir string) (deviceState, identity.Device, error) {
	var st deviceState
	raw, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return st, identity.Device{}, errors.New("not joined")
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, identity.Device{}, err
	}
	kb, err := crypto.DecodeBase64URL(st.Private)
	if err != nil {
		return st, identity.Device{}, err
	}
	priv, err := crypto.Ed25519PrivateKeyFromBytes(kb)
	if err != nil {
		return st, identity.Device{}, err
	}
	return st, identity.Device{Identity: st.Identity, Private: priv}, nil
}

func errJSON(err error) error {
	if err == nil {
		return nil
	}
	var cerr *contract.Error
	if errors.As(err, &cerr) {
		raw, _ := json.Marshal(cerr)
		return errors.New(string(raw))
	}
	var con *consoleapi.Error
	if errors.As(err, &con) {
		raw, _ := json.Marshal(map[string]string{"code": con.Code, "message": con.Message})
		return errors.New(string(raw))
	}
	raw, _ := json.Marshal(map[string]string{"code": "internal", "message": err.Error()})
	return errors.New(string(raw))
}

// Join redeems a credential (full text or 12-digit code), creates the
// device key and stores the membership. class is phone or watch.
func Join(dir, credential, name, class string, timeoutMillis int64) (string, error) {
	cred, err := consoleapi.ParseCredential(credential)
	if err != nil {
		return "", errJSON(err)
	}
	url, pin := cred.Console, cred.TLSPin
	if url == "" {
		defaultMu.Lock()
		url, pin = defaultConsole, defaultPin
		defaultMu.Unlock()
		if url == "" {
			return "", errJSON(errors.New("a 12-digit code needs the configured console; scan or paste the full credential"))
		}
		cred.Type = class
	}
	if cred.Type != "" && class != "" && cred.Type != class {
		return "", errJSON(errors.New("this credential is for a " + cred.Type + ", not a " + class))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMillis)*time.Millisecond)
	defer cancel()
	m, dev, err := consoleapi.NewClient(url, pin).Join(ctx, cred, consoleapi.JoinOptions{Name: name, Platform: "android"})
	if err != nil {
		return "", errJSON(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errJSON(err)
	}
	if m.RelayCredentials != nil {
		_ = writePrivate(filepath.Join(dir, relayFile), *m.RelayCredentials)
	}
	st := deviceState{Membership: m, Identity: dev.Identity, Private: crypto.Base64URL(dev.Private.BytesForDevStore()), Class: class, Name: name, JoinedAt: time.Now().UTC()}
	st.Membership.RelayCredentials = nil
	if err := writePrivate(filepath.Join(dir, stateFile), st); err != nil {
		return "", errJSON(err)
	}
	return Status(dir), nil
}

// Status describes the stored membership as JSON.
func Status(dir string) string {
	st, _, err := load(dir)
	if err != nil {
		return `{"joined":false}`
	}
	out := map[string]any{"joined": true, "domain_id": st.Membership.DomainID, "domain_name": st.Membership.DomainName, "device_id": st.Identity.DeviceID, "device_name": st.Name, "class": st.Class, "console": st.Membership.ConsoleURL, "host_joined": st.Membership.Host != nil, "joined_at": st.JoinedAt}
	if st.Membership.Host != nil {
		out["host_name"] = st.Membership.Host.Name
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

// Leave removes the binding on the console and the local state.
func Leave(dir string, timeoutMillis int64) error {
	st, dev, err := load(dir)
	if err != nil {
		return errJSON(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMillis)*time.Millisecond)
	defer cancel()
	if err := consoleapi.NewClient(st.Membership.ConsoleURL, st.Membership.ConsoleTLSPin).Leave(ctx, dev, st.Membership.ConsoleID); err != nil && !consoleapi.IsCode(err, consoleapi.ErrCodeRevoked) {
		return errJSON(err)
	}
	return Forget(dir)
}

// Forget deletes the local state only.
func Forget(dir string) error {
	_ = os.Remove(filepath.Join(dir, relayFile))
	err := os.Remove(filepath.Join(dir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Listener receives connection state and pushed events.
type Listener interface {
	// OnState reports the active path ("", "lan" or "iscp") and the
	// backend's hello (features) as JSON.
	OnState(path string, helloJSON string)
	// OnEvent receives one event frame as JSON.
	OnEvent(frameJSON string)
}

type fileRelayStore struct{ path string }

func (f fileRelayStore) LoadRelayCredentials() (relay.Credentials, error) {
	var c relay.Credentials
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	return c, err
}
func (f fileRelayStore) SaveRelayCredentials(c relay.Credentials) error {
	return writePrivate(f.path, c)
}

// Client is an open link.
type Client struct {
	c      *linkclient.Client
	cancel context.CancelFunc
	dir    string
}

// Open connects using the stored membership. describeJSON is a
// contract.Device ({"class","name","app","conditions":{...}}) plus an
// optional "profile" ("duplex" or "pull").
func Open(dir, describeJSON string, listener Listener) (*Client, error) {
	st, dev, err := load(dir)
	if err != nil {
		return nil, errJSON(err)
	}
	var describe struct {
		contract.Device
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal([]byte(describeJSON), &describe); err != nil {
		return nil, errJSON(err)
	}
	if describe.Class == "" {
		describe.Class = st.Class
	}
	keeper := linkclient.NewGrantKeeper(st.Membership, dev)
	keeper.OnUpdate = func(r consoleapi.GrantResponse) {
		st.Membership.Grant = r.Grant
		if r.Host != nil {
			st.Membership.Host = r.Host
		}
		_ = writePrivate(filepath.Join(dir, stateFile), st)
	}
	if keeper.Host() == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, err := keeper.RefreshHost(ctx)
		cancel()
		if err != nil {
			return nil, errJSON(err)
		}
	}
	store := fileRelayStore{path: filepath.Join(dir, relayFile)}
	opts, err := linkclient.FromMembership(st.Membership, dev, keeper, store, describe.Device)
	if err != nil {
		return nil, errJSON(err)
	}
	if creds, err := store.LoadRelayCredentials(); err == nil {
		opts.RelayCreds = creds
	}
	opts.Profile = describe.Profile
	if listener != nil {
		opts.OnState = func(path string, hello *contract.Hello) {
			raw, _ := json.Marshal(hello)
			listener.OnState(path, string(raw))
		}
		opts.OnEvent = func(f contract.Frame) {
			raw, _ := json.Marshal(f)
			listener.OnEvent(string(raw))
		}
	}
	c, err := linkclient.New(opts)
	if err != nil {
		return nil, errJSON(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	return &Client{c: c, cancel: cancel, dir: dir}, nil
}

// WaitReady blocks until a path is up.
func (c *Client) WaitReady(timeoutMillis int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMillis)*time.Millisecond)
	defer cancel()
	return errJSON(c.c.WaitReady(ctx))
}

// Request sends one operation; params and the result are JSON. Errors are
// JSON contract errors ({"code","message","retryable"}).
func (c *Client) Request(op, paramsJSON string, timeoutMillis int64) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMillis)*time.Millisecond)
	defer cancel()
	var params any
	if strings.TrimSpace(paramsJSON) != "" {
		params = json.RawMessage(paramsJSON)
	}
	var out json.RawMessage
	if err := c.c.Request(ctx, op, params, &out); err != nil {
		return "", errJSON(err)
	}
	if len(out) == 0 {
		return "null", nil
	}
	return string(out), nil
}

// Path returns the active path.
func (c *Client) Path() string { return c.c.Path() }

// HelloJSON returns the backend's hello on the active path.
func (c *Client) HelloJSON() string {
	raw, _ := json.Marshal(c.c.Hello())
	return string(raw)
}

// Close stops the link.
func (c *Client) Close() {
	c.cancel()
	c.c.Close()
}
