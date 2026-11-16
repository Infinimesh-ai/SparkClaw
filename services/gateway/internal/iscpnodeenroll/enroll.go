// Package iscpnodeenroll redeems one node-managed connection credential
// (sparkclaw-iscp-connect-v1.) and writes the private workbench profile. The
// desktop helper and the mobile library share it so every client kind follows
// the same issuer exchange, activation wait and recovery rules.
package iscpnodeenroll

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// Options selects where and how the native identity is stored. DeviceIDPrefix
// and HardwareClass are local identity metadata only; the issuer binds the
// public key, not these labels.
type Options struct {
	Directory      string
	KeyBackend     string
	KeyringService string
	InstallationID string
	DeviceIDPrefix string
	HardwareClass  string
}

type localBinding struct {
	CredentialDigest string `json:"credential_digest"`
	InstallationID   string `json:"installation_id"`
	KeyBackend       string `json:"key_backend"`
	KeyringService   string `json:"keyring_service"`
}

// Descriptor is the public connection summary returned to the host.
type Descriptor struct {
	SchemaVersion          int    `json:"schema_version"`
	Transport              string `json:"transport"`
	Mode                   string `json:"mode"`
	TestMode               bool   `json:"test_mode"`
	Origin                 string `json:"origin"`
	DeploymentID           string `json:"deployment_id"`
	OwnerID                string `json:"owner_id"`
	ClientID               string `json:"client_id"`
	DomainID               string `json:"domain_id"`
	InitiatorDeviceID      string `json:"initiator_device_id"`
	ResponderDeviceID      string `json:"responder_device_id"`
	ResponderKeyThumbprint string `json:"responder_key_thumbprint"`
	RelayURL               string `json:"relay_url"`
	RelayProfile           string `json:"relay_profile"`
}

// Result names the written connection profile and the helper configuration it
// selects.
type Result struct {
	ProfilePath  string     `json:"profile_path"`
	HelperConfig string     `json:"helper_config"`
	Descriptor   Descriptor `json:"descriptor"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var labelPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidUUID reports whether s is a lowercase RFC 4122 UUID string.
func ValidUUID(s string) bool { return uuidPattern.MatchString(s) }

// NewUUID returns a random version-4 UUID.
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("secure random unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Enroll persists the native identity before any network mutation and writes a
// pending independent profile; the caller verifies business readiness before
// selecting that profile. Retrying with the same credential, installation and
// key store resumes the original operation.
func Enroll(ctx context.Context, encoded string, opts Options) (Result, error) {
	directory, backend, installation := opts.Directory, opts.KeyBackend, opts.InstallationID
	if !filepath.IsAbs(directory) || !ValidUUID(installation) || !iscpbridge.ValidIdentityKeyBackend(backend) || !labelPattern.MatchString(opts.DeviceIDPrefix) || !labelPattern.MatchString(opts.HardwareClass) {
		return Result{}, errors.New("enrollment requires private absolute directory, supported key backend and installation UUID")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return Result{}, err
	}
	st, err := os.Lstat(directory)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return Result{}, errors.New("enrollment directory must be private and regular")
	}
	credential, err := iscpnodeissuer.ParseCredential(encoded, time.Now().UTC(), true)
	if err != nil {
		return Result{}, errors.New("invalid node enrollment credential")
	}
	bind := localBinding{iscpnodeissuer.Digest(credential), installation, backend, opts.KeyringService}
	bindPath := filepath.Join(directory, "enrollment.binding.json")
	if raw, e := os.ReadFile(bindPath); e == nil {
		var old localBinding
		if iscpnodeissuer.StrictDecode(raw, &old) != nil || old != bind {
			return Result{}, errors.New("pending enrollment belongs to another credential, installation or native key store")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return Result{}, errors.New("pending enrollment binding unavailable")
	} else {
		raw, _ := json.Marshal(bind)
		if err = iscpnodeissuer.WritePrivateAtomic(bindPath, raw); err != nil {
			return Result{}, err
		}
	}
	deviceDir := filepath.Join(directory, "identity")
	identityPath := filepath.Join(deviceDir, iscpbridge.IdentityFileName)
	if _, err := os.Lstat(identityPath); errors.Is(err, os.ErrNotExist) {
		if !time.Now().Before(credential.ExpiresAt) {
			return Result{}, errors.New("new enrollment credential expired")
		}
		if _, _, err = iscpbridge.GenerateEnrollmentRequestWithKeyBackend(deviceDir, credential.DomainID, opts.DeviceIDPrefix+"-"+NewUUID(), opts.HardwareClass, backend, opts.KeyringService, time.Now().UTC()); err != nil {
			return Result{}, errors.New("native device key creation failed")
		}
	} else if err != nil {
		return Result{}, errors.New("native device identity unavailable")
	}
	device, err := iscpbridge.LoadDeviceWithKeyBackend(identityPath, filepath.Join(deviceDir, iscpbridge.IdentityKeyFileName), backend, opts.KeyringService)
	if err != nil || device.Identity.DomainID != credential.DomainID {
		return Result{}, errors.New("native device key loading failed")
	}
	client, err := iscpnodeclient.New(encoded, device, filepath.Join(deviceDir, "node-control"))
	if err != nil {
		return Result{}, errors.New("node enrollment initialization failed")
	}
	if err = client.SetInstallationID(installation); err != nil {
		return Result{}, err
	}
	result, err := client.Enroll(ctx)
	if err != nil {
		return Result{}, err
	}
	if result.InstallationID != installation {
		return Result{}, errors.New("issuer result differs from local installation")
	}
	// Enroll authenticated and durably retained this exact result. The issuer's
	// clock may be slightly ahead of ours; wait within the original deadline
	// before strict runtime validation instead of replacing the signed Grant.
	if err = WaitForActivation(ctx, result.Grant.NotBefore); err != nil {
		return Result{}, err
	}
	paths := map[string]any{"relay.enrollment.json": result.Enrollment, "grant.json": result.Grant, "peer.identity.json": result.ResponderIdentity, "issuer.identity.json": result.IssuerIdentity, "invitation.identity.json": result.InvitationSignerIdentity, "issuer.control.json": client.StoredConfig()}
	for name, value := range paths {
		raw, e := json.Marshal(value)
		if e != nil {
			return Result{}, e
		}
		if e = iscpnodeissuer.WritePrivateAtomic(filepath.Join(directory, name), raw); e != nil {
			return Result{}, e
		}
	}
	cfg := iscpworkbench.Config{ExpectedInstallationID: installation, SchemaVersion: 2, Mode: "node-managed", Role: iscpworkbench.RoleInitiator, RelayProfile: iscpbridge.ProfileProduction, ApplicationProfiles: []string{iscpworkbench.Profile, iscpworkbench.ProfileV2}, IdentityDirectory: deviceDir, IdentityKeyBackend: backend, IdentityKeyringService: opts.KeyringService, EnrollmentFile: filepath.Join(directory, "relay.enrollment.json"), PeerIdentityFile: filepath.Join(directory, "peer.identity.json"), IssuerIdentityFile: filepath.Join(directory, "issuer.identity.json"), InvitationIdentityFile: filepath.Join(directory, "invitation.identity.json"), IssuerControlFile: filepath.Join(directory, "issuer.control.json"), GrantFile: filepath.Join(directory, "grant.json"), Binding: &iscpworkbench.Binding{DeploymentID: result.Binding.DeploymentID, OwnerID: result.Binding.OwnerID, ClientID: result.Binding.ClientID}}
	helperPath := filepath.Join(directory, "workbench.json")
	raw, _ := json.Marshal(cfg)
	if err = iscpnodeissuer.WritePrivateAtomic(helperPath, raw); err != nil {
		return Result{}, err
	}
	if !time.Now().Before(result.Grant.ExpiresAt) || !time.Now().Before(result.Enrollment.Access.ExpiresAt) || !time.Now().Before(result.Enrollment.Refresh.ExpiresAt) || !time.Now().Before(result.Enrollment.ExpiresAt) {
		if _, err = iscpworkbench.RecoverNodeEnrollment(ctx, helperPath); err != nil {
			return Result{}, err
		}
	}
	if _, err = iscpworkbench.LoadConfig(helperPath); err != nil {
		return Result{}, errors.New("node-managed enrollment result failed private runtime validation")
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	descriptor := Descriptor{4, "iscp", "node-managed", false, "https://iscp.invalid", result.Binding.DeploymentID, result.Binding.OwnerID, result.Binding.ClientID, result.Identity.DomainID, result.Identity.DeviceID, result.ResponderIdentity.DeviceID, result.ResponderIdentity.PublicKey.KID, result.Enrollment.RelayBaseURL, iscpbridge.ProfileProduction}
	profilePath := filepath.Join(directory, "connection.profile.json")
	raw, _ = json.Marshal(struct {
		SchemaVersion int        `json:"schema_version"`
		Transport     string     `json:"transport"`
		Mode          string     `json:"mode"`
		Backend       Descriptor `json:"backend"`
		HelperConfig  string     `json:"helper_config"`
	}{2, "iscp", "node-managed", descriptor, helperPath})
	if err = iscpnodeissuer.WritePrivateAtomic(profilePath, raw); err != nil {
		return Result{}, err
	}
	return Result{ProfilePath: profilePath, HelperConfig: helperPath, Descriptor: descriptor}, nil
}

// WaitForActivation blocks until notBefore or ctx ends.
func WaitForActivation(ctx context.Context, notBefore time.Time) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		delay := time.Until(notBefore)
		if delay <= 0 {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Recheck wall time in case the local clock moved while waiting.
		}
	}
}
