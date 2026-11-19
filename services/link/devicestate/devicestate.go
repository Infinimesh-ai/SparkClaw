// Package devicestate keeps a device's console membership, key and Relay
// credentials in private files and opens a LinkClient from them. It is
// shared by the mobile binding and the desktop helper.
package devicestate

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

const (
	StateFile = "link-device.json"
	RelayFile = "link-relay.json"
)

// State is the persisted device state.
type State struct {
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

// SetDefaultConsole names the console a bare 12-digit code is redeemed at.
func SetDefaultConsole(url, tlsPin string) {
	defaultMu.Lock()
	defaultConsole, defaultPin = strings.TrimSpace(url), strings.TrimSpace(tlsPin)
	defaultMu.Unlock()
}

// WritePrivate writes JSON atomically with mode 0600.
func WritePrivate(path string, v any) error {
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

// ErrNotJoined is returned when no membership is stored.
var ErrNotJoined = errors.New("not joined")

// Load reads the state and the device key.
func Load(dir string) (State, identity.Device, error) {
	var st State
	raw, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		return st, identity.Device{}, ErrNotJoined
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

// Join redeems a credential and stores the membership.
func Join(ctx context.Context, dir, credential, name, class, platform string) (State, error) {
	cred, err := consoleapi.ParseCredential(credential)
	if err != nil {
		return State{}, err
	}
	url, pin := cred.Console, cred.TLSPin
	if url == "" {
		defaultMu.Lock()
		url, pin = defaultConsole, defaultPin
		defaultMu.Unlock()
		if url == "" {
			return State{}, errors.New("a 12-digit code needs the configured console; scan or paste the full credential")
		}
		cred.Type = class
	}
	if cred.Type != "" && class != "" && cred.Type != class {
		return State{}, errors.New("this credential is for a " + cred.Type + ", not a " + class)
	}
	m, dev, err := consoleapi.NewClient(url, pin).Join(ctx, cred, consoleapi.JoinOptions{Name: name, Platform: platform})
	if err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return State{}, err
	}
	if m.RelayCredentials != nil {
		_ = WritePrivate(filepath.Join(dir, RelayFile), *m.RelayCredentials)
	}
	st := State{Membership: m, Identity: dev.Identity, Private: crypto.Base64URL(dev.Private.BytesForDevStore()), Class: class, Name: name, JoinedAt: time.Now().UTC()}
	st.Membership.RelayCredentials = nil
	if err := WritePrivate(filepath.Join(dir, StateFile), st); err != nil {
		return State{}, err
	}
	return st, nil
}

// Summary describes the stored membership.
func Summary(dir string) map[string]any {
	st, _, err := Load(dir)
	if err != nil {
		return map[string]any{"joined": false}
	}
	out := map[string]any{"joined": true, "domain_id": st.Membership.DomainID, "domain_name": st.Membership.DomainName, "device_id": st.Identity.DeviceID, "device_name": st.Name, "class": st.Class, "console": st.Membership.ConsoleURL, "host_joined": st.Membership.Host != nil, "joined_at": st.JoinedAt}
	if st.Membership.Host != nil {
		out["host_name"] = st.Membership.Host.Name
		out["host_device_id"] = st.Membership.Host.DeviceID
	}
	return out
}

// Leave removes the binding on the console and forgets the local state.
func Leave(ctx context.Context, dir string) error {
	st, dev, err := Load(dir)
	if err != nil {
		return err
	}
	if err := consoleapi.NewClient(st.Membership.ConsoleURL, st.Membership.ConsoleTLSPin).Leave(ctx, dev, st.Membership.ConsoleID); err != nil && !consoleapi.IsCode(err, consoleapi.ErrCodeRevoked) {
		return err
	}
	return Forget(dir)
}

// Forget deletes the local state only.
func Forget(dir string) error {
	_ = os.Remove(filepath.Join(dir, RelayFile))
	err := os.Remove(filepath.Join(dir, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
func (f fileRelayStore) SaveRelayCredentials(c relay.Credentials) error { return WritePrivate(f.path, c) }

// Options builds LinkClient options from the stored state; the caller adds
// callbacks and reverse operations.
func Options(ctx context.Context, dir string, describe contract.Device) (linkclient.Options, error) {
	st, dev, err := Load(dir)
	if err != nil {
		return linkclient.Options{}, err
	}
	if describe.Class == "" {
		describe.Class = st.Class
	}
	keeper := linkclient.NewGrantKeeper(st.Membership, dev)
	var mu sync.Mutex
	keeper.OnUpdate = func(r consoleapi.GrantResponse) {
		mu.Lock()
		defer mu.Unlock()
		st.Membership.Grant = r.Grant
		if r.Host != nil {
			st.Membership.Host = r.Host
		}
		_ = WritePrivate(filepath.Join(dir, StateFile), st)
	}
	if keeper.Host() == nil {
		hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, err := keeper.RefreshHost(hctx)
		cancel()
		if err != nil {
			return linkclient.Options{}, err
		}
	}
	store := fileRelayStore{path: filepath.Join(dir, RelayFile)}
	opts, err := linkclient.FromMembership(st.Membership, dev, keeper, store, describe)
	if err != nil {
		return linkclient.Options{}, err
	}
	if creds, err := store.LoadRelayCredentials(); err == nil {
		opts.RelayCreds = creds
	}
	return opts, nil
}
