// Package consoleapi defines the console side of the link layer: the
// SparkClaw access credential, the device-facing console interface and the
// console adapter clients and the backend use (design 3.11, console design
// 3.6). Only this package knows the console's address and interfaces;
// connecting the real console replaces only this package's client.
package consoleapi

import (
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

// Device types and roles (console design 3.2).
const (
	TypeSparkClaw = "sparkclaw"
	TypeDesktop   = "desktop"
	TypePhone     = "phone"
	TypeWatch     = "watch"

	RoleDomainHost  = "domain_host"
	RoleOwnerDevice = "owner_device"
)

// Permission is the single Grant permission.
const Permission = "sparkclaw.link"

// Device states.
const (
	StateTrusted = "trusted"
	StateRevoked = "revoked"
)

// ValidDeviceType reports whether t can be issued a credential.
func ValidDeviceType(t string) bool {
	switch t {
	case TypeSparkClaw, TypeDesktop, TypePhone, TypeWatch:
		return true
	}
	return false
}

// RoleFor returns the role of a device type.
func RoleFor(deviceType string) string {
	if deviceType == TypeSparkClaw {
		return RoleDomainHost
	}
	return RoleOwnerDevice
}

// Trust is what the console publishes about itself before a device joins.
// The device reaches it over the pinned TLS from its trust configuration.
type Trust struct {
	ConsoleID string                  `json:"console_id"`
	Name      string                  `json:"name,omitempty"`
	Signer    identity.DeviceIdentity `json:"signer"`
	Relays    []relay.Endpoint        `json:"relays"`
}

// LAN is how the backend is reached on the local network.
type LAN struct {
	Endpoints []string `json:"endpoints,omitempty"` // wss://host:port/link/v1
	TLSPin    string   `json:"tls_pin,omitempty"`   // base64url SHA-256 of the leaf DER
}

// Host is the domain_host as devices see it.
type Host struct {
	DeviceID string                  `json:"device_id"`
	Identity identity.DeviceIdentity `json:"identity"`
	Name     string                  `json:"name,omitempty"`
	LAN      LAN                     `json:"lan"`
}

// DeviceRecord is a device in the domain.
type DeviceRecord struct {
	DeviceID          string                  `json:"device_id"`
	DomainID          string                  `json:"domain_id"`
	Name              string                  `json:"name"`
	Type              string                  `json:"type"`
	Role              string                  `json:"role"`
	Identity          identity.DeviceIdentity `json:"identity"`
	Fingerprint       string                  `json:"fingerprint"`
	State             string                  `json:"state"`
	Platform          string                  `json:"platform,omitempty"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
	RevokedAt         *time.Time              `json:"revoked_at,omitempty"`
	LastSeenAt        *time.Time              `json:"last_seen_at,omitempty"`
	GrantID           string                  `json:"grant_id,omitempty"`
	GrantExpiresAt    *time.Time              `json:"grant_expires_at,omitempty"`
	RenewalExpiresAt  *time.Time              `json:"renewal_expires_at,omitempty"`
	RevocationEpoch   uint64                  `json:"revocation_epoch"`
	DirectoryRevision int64                   `json:"directory_revision"`
}

// Membership is everything a device receives when it joins and keeps
// afterwards; it no longer needs the access credential.
type Membership struct {
	ConsoleURL       string                  `json:"console_url"`
	ConsoleTLSPin    string                  `json:"console_tls_pin"`
	ConsoleID        string                  `json:"console_id"`
	Signer           identity.DeviceIdentity `json:"signer"`
	DomainID         string                  `json:"domain_id"`
	DomainName       string                  `json:"domain_name,omitempty"`
	Device           DeviceRecord            `json:"device"`
	Host             *Host                   `json:"host,omitempty"`
	Grant            *trust.Grant            `json:"grant,omitempty"`
	RenewalExpiresAt *time.Time              `json:"renewal_expires_at,omitempty"`
	Relays           []relay.Endpoint        `json:"relays"`
	RelayCredentials *relay.Credentials      `json:"relay_credentials,omitempty"`
}

// JoinRequest registers a device with a one-time access credential code.
type JoinRequest struct {
	Code       string                  `json:"code"`
	Identity   identity.DeviceIdentity `json:"identity"`
	Proof      identity.DeviceProof    `json:"proof"`
	RelayProof *identity.DeviceProof   `json:"relay_proof,omitempty"`
	Name       string                  `json:"name"`
	Platform   string                  `json:"platform,omitempty"`
	LAN        *LAN                    `json:"lan,omitempty"` // backend only
}

// SignedRequest is the device-authenticated envelope for every other
// device-facing call: the proof's audience is the console ID and its
// challenge is the operation name.
type SignedRequest struct {
	DeviceID   string                `json:"device_id"`
	Proof      identity.DeviceProof  `json:"proof"`
	RelayProof *identity.DeviceProof `json:"relay_proof,omitempty"`
	LAN        *LAN                  `json:"lan,omitempty"`
	Name       string                `json:"name,omitempty"`
	Since      int64                 `json:"since,omitempty"`
}

// GrantResponse is the result of obtaining or renewing a Grant.
type GrantResponse struct {
	Device           DeviceRecord `json:"device"`
	Host             *Host        `json:"host,omitempty"`
	Grant            *trust.Grant `json:"grant,omitempty"`
	RenewalExpiresAt *time.Time   `json:"renewal_expires_at,omitempty"`
}

// RelayAccessResponse carries fresh Relay credentials.
type RelayAccessResponse struct {
	Relay       relay.Endpoint    `json:"relay"`
	Credentials relay.Credentials `json:"credentials"`
}

// Directory is the domain's device list for the backend.
type Directory struct {
	DomainID string                  `json:"domain_id"`
	Revision int64                   `json:"revision"`
	Devices  []DeviceRecord          `json:"devices"`
	Signer   identity.DeviceIdentity `json:"signer"`
	SyncedAt time.Time               `json:"synced_at"`
}

// Operation names used as proof challenges.
const (
	OpJoin        = "sparkclaw.console.join"
	OpGrant       = "sparkclaw.console.grant"
	OpRelayAccess = "sparkclaw.console.relay-access"
	OpDirectory   = "sparkclaw.console.directory"
	OpHost        = "sparkclaw.console.host"
	OpLeave       = "sparkclaw.console.leave"
)

// Console error codes returned in {"error":{"code":...}}.
const (
	ErrCodeCredentialInvalid = "credential_invalid"
	ErrCodeCredentialUsed    = "credential_used"
	ErrCodeCredentialExpired = "credential_expired"
	ErrCodeRevoked           = "device_revoked"
	ErrCodeUnknownDevice     = "device_unknown"
	ErrCodeRenewalExpired    = "renewal_authorization_expired"
	ErrCodeHostExists        = "domain_host_exists"
	ErrCodeProofInvalid      = "proof_invalid"
	ErrCodeNotHost           = "not_domain_host"
)

// Error is the console's error body.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return "console: " + e.Code + ": " + e.Message
	}
	return "console: " + e.Code
}

// GrantTTL is how long one Grant lasts; RenewalLimit is the absolute bound
// of automatic renewal before the user authorizes again (90 days, as on the
// official console).
const (
	GrantTTL     = 72 * time.Hour
	RenewalLimit = 90 * 24 * time.Hour
)

// RenewBefore reports how long before expiry a device should renew.
func RenewBefore(g trust.Grant) time.Duration {
	ttl := g.ExpiresAt.Sub(g.NotBefore)
	w := ttl / 3
	if w > 24*time.Hour {
		w = 24 * time.Hour
	}
	if w < time.Minute {
		w = time.Minute
	}
	return w
}
