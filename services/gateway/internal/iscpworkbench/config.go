package iscpworkbench

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

type Binding struct {
	DeploymentID string `json:"deployment_id"`
	OwnerID      string `json:"owner_id"`
	ClientID     string `json:"client_id"`
}

type Config struct {
	SchemaVersion          int                 `json:"schema_version"`
	Mode                   string              `json:"mode"`
	Role                   string              `json:"role"`
	RelayProfile           string              `json:"relay_profile,omitempty"`
	IdentityDirectory      string              `json:"identity_directory"`
	IdentityKeyBackend     string              `json:"identity_key_backend"`
	IdentityKeyringService string              `json:"identity_keyring_service,omitempty"`
	EnrollmentFile         string              `json:"enrollment_file"`
	PeerIdentityFile       string              `json:"peer_identity_file"`
	IssuerIdentityFile     string              `json:"issuer_identity_file"`
	GrantFile              string              `json:"grant_file"`
	Permission             string              `json:"permission,omitempty"`
	Binding                *Binding            `json:"binding,omitempty"`
	GrantRenewal           *GrantRenewalConfig `json:"grant_renewal,omitempty"`
}

// GrantRenewalConfig points to the separately pinned local issuer. Relay
// credentials and the issuer's management credential are never used here.
type GrantRenewalConfig struct {
	URL                 string `json:"url"`
	PendingFile         string `json:"pending_file"`
	PollIntervalSeconds int    `json:"poll_interval_seconds,omitempty"`
}

// PublicIdentity is derived from the validated enrollment and pinned peer,
// allowing Electron to bind its public connection descriptor to this helper.
// It contains no access tokens, grants or signing keys.
type PublicIdentity struct {
	DomainID               string `json:"domain_id"`
	InitiatorDeviceID      string `json:"initiator_device_id"`
	ResponderDeviceID      string `json:"responder_device_id"`
	ResponderKeyThumbprint string `json:"responder_key_thumbprint"`
	RelayURL               string `json:"relay_url"`
	RelayProfile           string `json:"relay_profile"`
}

func (c Config) PublicIdentity() (PublicIdentity, error) {
	m, err := loadMaterial(c)
	if err != nil {
		return PublicIdentity{}, err
	}
	initiator, responder := m.device.Identity, m.peer
	if c.Role == RoleResponder {
		initiator, responder = m.peer, m.device.Identity
	}
	return PublicIdentity{DomainID: m.enrollment.DomainID, InitiatorDeviceID: initiator.DeviceID, ResponderDeviceID: responder.DeviceID, ResponderKeyThumbprint: responder.PublicKey.KID, RelayURL: m.enrollment.RelayBaseURL, RelayProfile: c.EffectiveRelayProfile()}, nil
}

func (c Config) EffectiveRelayProfile() string {
	if c.RelayProfile == "" {
		return iscpbridge.ProfileProduction
	}
	return c.RelayProfile
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	if err := readJSONFile(path, &cfg, true); err != nil {
		return cfg, fmt.Errorf("load workbench configuration: %w", err)
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return cfg, errors.New("resolve workbench configuration directory")
	}
	paths := []*string{&cfg.IdentityDirectory, &cfg.EnrollmentFile, &cfg.PeerIdentityFile, &cfg.IssuerIdentityFile, &cfg.GrantFile}
	for _, field := range paths {
		if *field != "" && !filepath.IsAbs(*field) {
			*field = filepath.Join(base, *field)
		}
	}
	if cfg.GrantRenewal != nil && !filepath.IsAbs(cfg.GrantRenewal.PendingFile) {
		cfg.GrantRenewal.PendingFile = filepath.Join(base, cfg.GrantRenewal.PendingFile)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	if _, err := loadMaterial(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != 1 || c.Mode != "local-test" {
		return errors.New("workbench requires explicit schema 1 local-test configuration")
	}
	if c.Role != RoleInitiator && c.Role != RoleResponder {
		return errors.New("workbench role must be initiator or responder")
	}
	if c.EffectiveRelayProfile() != iscpbridge.ProfileProduction && c.EffectiveRelayProfile() != iscpbridge.ProfileLocalLab {
		return errors.New("unsupported workbench Relay profile")
	}
	if c.IdentityKeyBackend != iscpbridge.IdentityKeyBackendFile && c.IdentityKeyBackend != iscpbridge.IdentityKeyBackendKeyring {
		return errors.New("workbench identity key backend must be file or keyring")
	}
	for _, path := range []string{c.IdentityDirectory, c.EnrollmentFile, c.PeerIdentityFile, c.IssuerIdentityFile, c.GrantFile} {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
			return errors.New("workbench identity and authorization paths must be absolute")
		}
	}
	if c.Permission != "" && c.Permission != Permission {
		return errors.New("unsupported workbench permission")
	}
	if c.GrantRenewal != nil {
		if err := iscpbridge.ValidateGrantLifecycleURL(c.GrantRenewal.URL); err != nil {
			return err
		}
		if c.GrantRenewal.PendingFile == "" || !filepath.IsAbs(c.GrantRenewal.PendingFile) || c.GrantRenewal.PendingFile == c.GrantFile || c.GrantRenewal.PendingFile == c.EnrollmentFile {
			return errors.New("grant renewal requires an independent absolute private pending file")
		}
		if c.GrantRenewal.PollIntervalSeconds < 0 || c.GrantRenewal.PollIntervalSeconds > 300 {
			return errors.New("grant renewal polling interval must be between 1 and 300 seconds")
		}
	}
	if c.Binding != nil {
		for _, id := range []string{c.Binding.DeploymentID, c.Binding.OwnerID, c.Binding.ClientID} {
			if strings.TrimSpace(id) == "" || len(id) > 200 {
				return errors.New("workbench binding must identify deployment, owner and client")
			}
		}
	}
	return nil
}

type material struct {
	device     identity.Device
	enrollment iscpbridge.EnrollmentBundle
	peer       identity.DeviceIdentity
	issuer     identity.DeviceIdentity
	grant      trust.Grant
}

func loadMaterial(cfg Config) (material, error) {
	var m material
	if err := cfg.Validate(); err != nil {
		return m, err
	}
	info, err := os.Stat(cfg.IdentityDirectory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return m, errors.New("workbench identity directory must be private (0700)")
	}
	var boundedIdentity identity.DeviceIdentity
	if err := readJSONFile(filepath.Join(cfg.IdentityDirectory, iscpbridge.IdentityFileName), &boundedIdentity, false); err != nil {
		return m, errors.New("workbench device identity must be a bounded regular file")
	}
	if cfg.IdentityKeyBackend == iscpbridge.IdentityKeyBackendFile {
		info, err := os.Lstat(filepath.Join(cfg.IdentityDirectory, iscpbridge.IdentityKeyFileName))
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 || info.Mode().Perm()&0o077 != 0 {
			return m, errors.New("workbench private identity key file is invalid")
		}
	}
	m.device, err = iscpbridge.LoadDeviceWithKeyBackend(filepath.Join(cfg.IdentityDirectory, iscpbridge.IdentityFileName), filepath.Join(cfg.IdentityDirectory, iscpbridge.IdentityKeyFileName), cfg.IdentityKeyBackend, cfg.IdentityKeyringService)
	if err != nil {
		return m, errors.New("load workbench device identity")
	}
	if err := readJSONFile(cfg.EnrollmentFile, &m.enrollment, true); err != nil {
		return m, errors.New("load workbench Relay enrollment")
	}
	if err := readJSONFile(cfg.PeerIdentityFile, &m.peer, false); err != nil {
		return m, errors.New("load pinned workbench peer")
	}
	if err := readJSONFile(cfg.IssuerIdentityFile, &m.issuer, false); err != nil {
		return m, errors.New("load pinned local grant issuer")
	}
	if err := readJSONFile(cfg.GrantFile, &m.grant, true); err != nil {
		return m, errors.New("load local workbench grant")
	}
	if err := validateEnrollment(m.enrollment, m.device.Identity, cfg.EffectiveRelayProfile(), time.Now().UTC()); err != nil {
		return m, err
	}
	if m.peer.DomainID != m.device.Identity.DomainID || m.peer.DeviceID == m.device.Identity.DeviceID || m.peer.DeviceID == "" {
		return m, errors.New("workbench peer identity binding is invalid")
	}
	for _, id := range []identity.DeviceIdentity{m.device.Identity, m.peer, m.issuer} {
		if id.Type != identity.TypeDeviceIdentity || id.DomainID == "" || id.DeviceID == "" || id.PublicKey.KTY != "Ed25519" || id.PublicKey.Use != "identity-signature" || id.PublicKey.KID == "" {
			return m, errors.New("workbench pinned identity key is invalid")
		}
		pub, err := iscpcrypto.DecodeBase64URL(id.PublicKey.Public)
		if err != nil || len(pub) != 32 || id.PublicKey.KID != iscpcrypto.Thumbprint("Ed25519", pub) {
			return m, errors.New("workbench pinned public key is invalid")
		}
	}
	// Cloud root key IDs are operator names (for example cloud-trust-prod-1),
	// unlike enrolled device IDs. Pin their exact KID and public bytes without
	// substituting a device thumbprint convention for the cloud key namespace.
	if cfg.EffectiveRelayProfile() == iscpbridge.ProfileLocalLab {
		if err := iscpbridge.ValidateLocalRelaySigner(*m.enrollment.RelaySignerIdentity); err != nil {
			return m, err
		}
		if m.issuer.PublicKey.Public == m.enrollment.RelaySignerIdentity.PublicKey.Public {
			return m, errors.New("local session issuer must be separate from the Relay signer")
		}
	} else {
		cloud := m.enrollment.TrustRootIdentity
		cloudPublic, err := iscpcrypto.DecodeBase64URL(cloud.PublicKey.Public)
		if cloud.Type != identity.TypeDeviceIdentity || cloud.DomainID == "" || cloud.DeviceID == "" || cloud.PublicKey.KTY != "Ed25519" || cloud.PublicKey.KID == "" || len(cloud.PublicKey.KID) > 200 || err != nil || len(cloudPublic) != 32 {
			return m, errors.New("pinned cloud Trust Root key is invalid")
		}
		if m.issuer.PublicKey.Public == m.enrollment.TrustRootIdentity.PublicKey.Public {
			return m, errors.New("local test grant issuer must be separate from the cloud Trust Root")
		}
	}
	verificationTime := time.Now().UTC()
	if cfg.GrantRenewal != nil && !verificationTime.Before(m.grant.ExpiresAt) {
		// A signed expired seed can recover through the explicitly configured
		// lifecycle. Runtime admission still verifies against the actual time.
		verificationTime = m.grant.ExpiresAt.Add(-time.Nanosecond)
	}
	if err := verifyGrant(cfg, m, verificationTime); err != nil {
		return m, err
	}
	return m, nil
}

func validateEnrollment(b iscpbridge.EnrollmentBundle, local identity.DeviceIdentity, profile string, now time.Time) error {
	if b.Type != iscpbridge.EnrollmentBundleType || b.DomainID != local.DomainID || b.DeviceID != local.DeviceID || b.RelayID == "" {
		return errors.New("workbench enrollment does not match device")
	}
	if err := b.ValidateCredentials(now); err != nil {
		return err
	}
	if profile == iscpbridge.ProfileLocalLab {
		if b.Mode != iscpbridge.BundleModeWorkbenchLocalLab {
			return errors.New("local-lab workbench requires local reference enrollment")
		}
	} else if b.Mode == iscpbridge.BundleModeWorkbenchLocalLab {
		return errors.New("production workbench rejects local reference enrollment")
	}
	if b.IssuedAt.IsZero() || !b.IssuedAt.Before(b.ExpiresAt) || !now.Before(b.ExpiresAt) {
		return errors.New("workbench enrollment is incomplete or expired")
	}
	for _, credential := range []iscpbridge.RelayCredential{b.Access, b.Refresh} {
		if credential.DomainID != b.DomainID || credential.DeviceID != b.DeviceID || strings.TrimSpace(credential.Token) == "" || credential.ExpiresAt.IsZero() {
			return errors.New("workbench Relay credential binding is invalid")
		}
	}
	if !now.Before(b.Refresh.ExpiresAt) {
		return errors.New("workbench Relay refresh credential expired")
	}
	return iscpbridge.ValidateWorkbenchRelayURLs(profile, b.RelayBaseURL, b.RelayWebSocketURL)
}

func verifyGrant(cfg Config, m material, now time.Time) error {
	subject, audience := m.device.Identity, m.peer
	if cfg.Role == RoleResponder {
		subject, audience = m.peer, m.device.Identity
	}
	thumbprint, err := identity.Thumbprint(subject)
	if err != nil {
		return errors.New("workbench subject thumbprint is invalid")
	}
	g := m.grant
	if g.GrantID == "" || g.Issuer != m.issuer.DeviceID || g.Signature.Alg != "Ed25519" || g.Signature.KID != m.issuer.PublicKey.KID || !g.NotBefore.Before(g.ExpiresAt) || len(g.Permissions) != 1 || g.Permissions[0] != Permission || len(g.RelayConstraints) != 1 || g.RelayConstraints[0] != m.enrollment.RelayID {
		return errors.New("local workbench grant issuer, permission or Relay binding is invalid")
	}
	if err := trust.VerifyGrant(iscpcrypto.NewProvider(), g, m.issuer, trust.VerifyOptions{Audience: audience.DeviceID, SubjectDeviceID: subject.DeviceID, ConfirmationThumbprint: thumbprint, Permission: Permission, RelayID: m.enrollment.RelayID, Now: now}); err != nil {
		return errors.New("local workbench grant verification failed")
	}
	return nil
}

func readJSONFile(path string, value any, private bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("configuration file is unavailable")
	}
	if !info.Mode().IsRegular() || info.Size() > 512<<10 || (private && info.Mode().Perm()&0o077 != 0) {
		return errors.New("configuration file must be a bounded regular file with private permissions")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New("read configuration file")
	}
	if err := strictDecode(raw, value); err != nil {
		return errors.New("decode configuration file")
	}
	return nil
}
