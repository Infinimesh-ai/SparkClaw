package iscpbridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	c "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
)

// RelayOriginMigration moves one stopped service's Relay ingress (port) while
// retaining its identity, binding, credentials and CAS history. Credentials,
// signatures and grants bind the Relay ID, signer and TLS certificate, never
// the URL, but the enrollment file and the sealed lease each keep a copy that
// must equal the Relay's signed discovery descriptor.
type RelayOriginMigration struct {
	// EnrollmentFile is the file read and written on this host.
	EnrollmentFile string
	// RuntimeEnrollmentFile is the path the service itself opens; the sealed
	// state binds it as associated data (a container mount path, for example).
	// Empty means EnrollmentFile.
	RuntimeEnrollmentFile string
	Device                identity.Device
	FromBaseURL           string
	BaseURL               string
	DryRun                bool
}

type RelayOriginMigrationResult struct {
	EnrollmentChanged bool `json:"enrollment_changed"`
	StatePresent      bool `json:"state_present"`
	LeasePresent      bool `json:"lease_present"`
	StateChanged      bool `json:"state_changed"`
}

// NodeRelayWebSocketURL is the only WSS endpoint a node-managed origin may use.
func NodeRelayWebSocketURL(baseURL string) string {
	return "wss" + baseURL[len("https"):] + "/v2/relay/duplex"
}

// MigrateRelayOrigin rewrites only relay_base_url/relay_websocket_url. Every
// other field, including ones unknown to this build, is retained verbatim.
// Each object is independent and idempotent, so an interrupted run repeats.
func MigrateRelayOrigin(ctx context.Context, m RelayOriginMigration) (RelayOriginMigrationResult, error) {
	var result RelayOriginMigrationResult
	if err := validateOriginMove(m.FromBaseURL, m.BaseURL); err != nil {
		return result, err
	}
	runtimePath := m.RuntimeEnrollmentFile
	if runtimePath == "" {
		runtimePath = m.EnrollmentFile
	}
	if !filepath.IsAbs(m.EnrollmentFile) || !filepath.IsAbs(runtimePath) {
		return result, errors.New("relay origin migration requires absolute enrollment paths")
	}
	if err := CheckLegacyCredentialPending(m.EnrollmentFile); err != nil {
		return result, err
	}
	statePath := m.EnrollmentFile + ".credentials.v2"
	unlock, err := lockCredentialFile(ctx, statePath+".lock")
	if err != nil {
		return result, err
	}
	defer unlock()

	from := relayOrigin{m.FromBaseURL, NodeRelayWebSocketURL(m.FromBaseURL)}
	to := relayOrigin{m.BaseURL, NodeRelayWebSocketURL(m.BaseURL)}

	// The sealed lease overrides the enrollment file at load, so it moves first.
	sealed, err := readSealedState(statePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return result, err
	default:
		result.StatePresent = true
		key, err := c.NewProvider().HKDF(m.Device.Private.BytesForDevStore(), nil, []byte("iscp/credential-state/v2\x00"+m.Device.Identity.PublicKey.KID), 32)
		if err != nil {
			return result, err
		}
		aad := []byte(runtimePath + ".credentials.v2")
		plain, err := c.NewProvider().Open(key, sealed[:12], sealed[12:], aad)
		if err != nil {
			return result, errors.New("credential state does not open with this device and runtime path")
		}
		state, err := decodeObject(plain)
		if err != nil {
			return result, errors.New("invalid credential state")
		}
		if err := requireStateDevice(state, m.Device); err != nil {
			return result, err
		}
		if lease, ok := state["Lease"].(map[string]any); ok {
			result.LeasePresent = true
			enrollment, ok := lease["Enrollment"].(map[string]any)
			if !ok {
				return result, errors.New("credential lease has no enrollment")
			}
			changed, err := moveOrigin(enrollment, m.Device, from, to)
			if err != nil {
				return result, errors.New("credential lease: " + err.Error())
			}
			if changed {
				result.StateChanged = true
				if !m.DryRun {
					raw, err := json.Marshal(state)
					if err != nil {
						return result, err
					}
					nonce := c.RandomBytes(12)
					ct, err := c.NewProvider().Seal(key, nonce, raw, aad)
					if err != nil {
						return result, err
					}
					if err = writeSecretFile(statePath, []byte(c.Base64URL(append(nonce, ct...)))); err != nil {
						return result, err
					}
					if err = syncCredentialDirectory(filepath.Dir(statePath)); err != nil {
						return result, err
					}
				}
			}
		} else if state["Lease"] != nil {
			return result, errors.New("invalid credential lease")
		}
	}

	if err := requirePrivateFile(m.EnrollmentFile); err != nil {
		return result, err
	}
	raw, err := os.ReadFile(m.EnrollmentFile)
	if err != nil {
		return result, err
	}
	bundle, err := decodeObject(raw)
	if err != nil {
		return result, errors.New("decode enrollment bundle")
	}
	changed, err := moveOrigin(bundle, m.Device, from, to)
	if err != nil {
		return result, errors.New("enrollment: " + err.Error())
	}
	if changed {
		result.EnrollmentChanged = true
		if !m.DryRun {
			out, err := json.MarshalIndent(bundle, "", "  ")
			if err != nil {
				return result, err
			}
			if err = writeSecretFile(m.EnrollmentFile, append(out, '\n')); err != nil {
				return result, err
			}
			if err = syncCredentialDirectory(filepath.Dir(m.EnrollmentFile)); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

type relayOrigin struct{ base, websocket string }

// A port move keeps the scheme and IP host; any other change is a new Relay.
func validateOriginMove(fromBase, toBase string) error {
	for _, base := range []string{fromBase, toBase} {
		if len(base) < len("https://") || ValidateNodeRelayURLs(base, NodeRelayWebSocketURL(base)) != nil {
			return errors.New("relay origin migration requires canonical IP HTTPS origins")
		}
	}
	from, _ := url.Parse(fromBase)
	to, _ := url.Parse(toBase)
	// Clients compare origins as exact strings; ":443" is not the implicit form.
	if from.Port() == "443" || to.Port() == "443" {
		return errors.New("relay origin migration requires the implicit form of port 443")
	}
	if from.Hostname() != to.Hostname() || fromBase == toBase {
		return errors.New("relay origin migration changes only the port of the same host")
	}
	return nil
}

func moveOrigin(bundle map[string]any, device identity.Device, from, to relayOrigin) (bool, error) {
	if bundle["domain_id"] != device.Identity.DomainID || bundle["device_id"] != device.Identity.DeviceID {
		return false, errors.New("bundle belongs to another device")
	}
	base, _ := bundle["relay_base_url"].(string)
	websocket, _ := bundle["relay_websocket_url"].(string)
	switch (relayOrigin{base, websocket}) {
	case to:
		return false, nil
	case from:
		bundle["relay_base_url"], bundle["relay_websocket_url"] = to.base, to.websocket
		return true, nil
	default:
		return false, errors.New("unexpected original Relay origin")
	}
}

func requireStateDevice(state map[string]any, device identity.Device) error {
	binding, ok := state["Binding"].(map[string]any)
	if !ok || binding["DomainID"] != device.Identity.DomainID || binding["DeviceID"] != device.Identity.DeviceID {
		return errors.New("credential state belongs to another device")
	}
	return nil
}

func readSealedState(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 1<<20 {
		return nil, errors.New("invalid private credential state")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(string(raw))
	if err != nil || len(sealed) < 28 {
		return nil, errors.New("invalid credential state encoding")
	}
	return sealed, nil
}

// decodeObject keeps numbers exact (CAS versions are uint64) so that fields
// this build does not model survive a rewrite byte-for-value.
func decodeObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil || decoder.More() {
		return nil, errors.New("invalid JSON object")
	}
	return value, nil
}
