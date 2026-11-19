package consoleapi

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

// Client is the console adapter: the only code that talks to the console.
type Client struct {
	base     string
	pin      string
	http     *http.Client
	provider crypto.Provider
}

// NewClient builds an adapter for a console reached over a pinned TLS
// certificate (tlsPin empty = system trust store).
func NewClient(consoleURL, tlsPin string) *Client {
	var cfg = relay.PinnedSHA256Config(tlsPin)
	if tlsPin == "" {
		cfg = nil
	}
	return &Client{base: strings.TrimRight(consoleURL, "/"), pin: tlsPin, http: relay.HTTPClient(cfg, 30*time.Second), provider: crypto.NewProvider()}
}

// URL returns the console address.
func (c *Client) URL() string { return c.base }

// Pin returns the console TLS pin.
func (c *Client) Pin() string { return c.pin }

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("console unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var wrapper struct {
			Error *Error `json:"error"`
		}
		if json.Unmarshal(raw, &wrapper) == nil && wrapper.Error != nil {
			wrapper.Error.Status = res.StatusCode
			return wrapper.Error
		}
		return &Error{Code: "http_" + fmt.Sprint(res.StatusCode), Message: strings.TrimSpace(string(raw)), Status: res.StatusCode}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Trust reads the console's published identity and Relays.
func (c *Client) Trust(ctx context.Context) (Trust, error) {
	var t Trust
	err := c.do(ctx, http.MethodGet, "/v1/link/trust", nil, &t)
	return t, err
}

// NewDeviceIdentity creates a fresh device key in the Relay's domain.
func NewDeviceIdentity(domainID, deviceType string) (identity.Device, error) {
	id := deviceType + "-" + strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(crypto.RandomBytes(15)), "="))
	return identity.NewDevice(crypto.NewProvider(), domainID, id, time.Now().UTC())
}

// JoinOptions describes a device joining a domain.
type JoinOptions struct {
	Name     string
	Platform string
	LAN      *LAN
	// Device, when set, is the key to register (the backend reuses its
	// key); otherwise a new key is generated.
	Device *identity.Device
}

// Join registers a device with a credential. It returns the membership and
// the device key; the caller persists both (the key stays on the device).
func (c *Client) Join(ctx context.Context, cred Credential, opts JoinOptions) (Membership, identity.Device, error) {
	var none identity.Device
	trust, err := c.Trust(ctx)
	if err != nil {
		return Membership{}, none, err
	}
	if len(trust.Relays) == 0 {
		return Membership{}, none, errors.New("console publishes no Relay")
	}
	rel := trust.Relays[0]
	dev := identity.Device{}
	if opts.Device != nil {
		dev = *opts.Device
	} else {
		deviceType := cred.Type
		if deviceType == "" {
			deviceType = "device"
		}
		dev, err = NewDeviceIdentity(rel.DomainID, deviceType)
		if err != nil {
			return Membership{}, none, err
		}
	}
	now := time.Now().UTC()
	proof, err := dev.CreateProof(c.provider, trust.ConsoleID, JoinChallenge(cred.Code), nonce(), now)
	if err != nil {
		return Membership{}, none, err
	}
	relayProof, err := dev.CreateProof(c.provider, rel.RelayID, OpRelayAccess, nonce(), now)
	if err != nil {
		return Membership{}, none, err
	}
	req := JoinRequest{Code: cred.Code, Identity: dev.Identity, Proof: proof, RelayProof: &relayProof, Name: opts.Name, Platform: opts.Platform, LAN: opts.LAN}
	var m Membership
	if err := c.do(ctx, http.MethodPost, "/v1/link/join", req, &m); err != nil {
		return Membership{}, none, err
	}
	if m.Device.DeviceID != dev.Identity.DeviceID || m.Signer.PublicKey.KID != trust.Signer.PublicKey.KID {
		return Membership{}, none, errors.New("console answered for another device")
	}
	m.ConsoleURL = c.base
	m.ConsoleTLSPin = c.pin
	return m, dev, nil
}

func (c *Client) signed(dev identity.Device, consoleID, op string) (SignedRequest, error) {
	proof, err := dev.CreateProof(c.provider, consoleID, op, nonce(), time.Now().UTC())
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{DeviceID: dev.Identity.DeviceID, Proof: proof}, nil
}

// Grant obtains the current Grant, renewing it when it nears expiry.
func (c *Client) Grant(ctx context.Context, dev identity.Device, consoleID string) (GrantResponse, error) {
	req, err := c.signed(dev, consoleID, OpGrant)
	if err != nil {
		return GrantResponse{}, err
	}
	var out GrantResponse
	err = c.do(ctx, http.MethodPost, "/v1/link/grant", req, &out)
	return out, err
}

// RelayAccess asks the console to (re)register the device at the Relay and
// returns fresh Relay credentials.
func (c *Client) RelayAccess(ctx context.Context, dev identity.Device, consoleID, relayID string) (RelayAccessResponse, error) {
	req, err := c.signed(dev, consoleID, OpRelayAccess)
	if err != nil {
		return RelayAccessResponse{}, err
	}
	rp, err := dev.CreateProof(c.provider, relayID, OpRelayAccess, nonce(), time.Now().UTC())
	if err != nil {
		return RelayAccessResponse{}, err
	}
	req.RelayProof = &rp
	var out RelayAccessResponse
	err = c.do(ctx, http.MethodPost, "/v1/link/relay-access", req, &out)
	return out, err
}

// Directory reads the domain's device directory (backend only).
func (c *Client) Directory(ctx context.Context, dev identity.Device, consoleID string, since int64) (Directory, error) {
	req, err := c.signed(dev, consoleID, OpDirectory)
	if err != nil {
		return Directory{}, err
	}
	req.Since = since
	var out Directory
	err = c.do(ctx, http.MethodPost, "/v1/link/directory", req, &out)
	return out, err
}

// PublishHost updates the backend's LAN information (backend only).
func (c *Client) PublishHost(ctx context.Context, dev identity.Device, consoleID string, lan LAN, name string) (Host, error) {
	req, err := c.signed(dev, consoleID, OpHost)
	if err != nil {
		return Host{}, err
	}
	req.LAN = &lan
	req.Name = name
	var out Host
	err = c.do(ctx, http.MethodPost, "/v1/link/host", req, &out)
	return out, err
}

// Leave removes this device's binding.
func (c *Client) Leave(ctx context.Context, dev identity.Device, consoleID string) error {
	req, err := c.signed(dev, consoleID, OpLeave)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/v1/link/leave", req, nil)
}

// RelayRebinder adapts RelayAccess to relay.Rebinder.
func (c *Client) RelayRebinder(dev identity.Device, consoleID, relayID string) relay.Rebinder {
	return func(ctx context.Context) (relay.Credentials, error) {
		out, err := c.RelayAccess(ctx, dev, consoleID, relayID)
		if err != nil {
			return relay.Credentials{}, err
		}
		return out.Credentials, nil
	}
}

func nonce() string { return crypto.Base64URL(crypto.RandomBytes(18)) }

// IsCode reports whether err is a console error with this code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
