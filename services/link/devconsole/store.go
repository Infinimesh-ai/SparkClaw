package devconsole

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
)

// Files in the data directory.
const (
	stateFile   = "state.json"
	accountFile = "account.json"
	signerFile  = "signer.json"
)

// Domain states.
const (
	DomainActive    = "active"
	DomainSuspended = "suspended"
)

// Audit results, as the official console names them.
const (
	resultAllow = "allow"
	resultDeny  = "deny"
)

// maxAudit bounds the audit record kept in the state file.
const maxAudit = 5000

// account is the single administrator. It lives in its own file so the
// command line can change the password or rotate the API token while the
// console runs; the console reads it on every sign-in.
type account struct {
	UserID       string    `json:"user_id"`
	DisplayName  string    `json:"display_name"`
	PasswordHash string    `json:"password_hash"`
	PasswordSalt string    `json:"password_salt"`
	Iterations   int       `json:"iterations"`
	APITokenHash string    `json:"api_token_hash,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type domainRec struct {
	DomainID     string         `json:"domain_id"`
	Name         string         `json:"name"`
	State        string         `json:"state"`
	OwnerUserID  string         `json:"owner_user_id"`
	HostDeviceID string         `json:"host_device_id,omitempty"`
	HostLAN      consoleapi.LAN `json:"host_lan"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// deviceRec is a device and its current Grant. GrantID and GrantExpiresAt
// of the embedded record are derived from Grant when the record is shown.
type deviceRec struct {
	consoleapi.DeviceRecord
	Grant *trust.Grant `json:"grant,omitempty"`
}

type credentialRec struct {
	CredentialID string     `json:"credential_id"`
	DomainID     string     `json:"domain_id"`
	CodeDigest   string     `json:"code_digest"`
	Type         string     `json:"target_device_type"`
	Role         string     `json:"target_device_role"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	UsedByDevice string     `json:"used_by_device,omitempty"`
	CreatedBy    string     `json:"created_by"`
}

func (c *credentialRec) status(now time.Time) string {
	switch {
	case c.UsedAt != nil:
		return "consumed"
	case !now.Before(c.ExpiresAt):
		return "expired"
	}
	return "pending"
}

type auditRec struct {
	AuditID      string    `json:"audit_id"`
	DomainID     string    `json:"domain_id"`
	CreatedAt    time.Time `json:"created_at"`
	Action       string    `json:"action"`
	Decision     string    `json:"decision"`
	ActorType    string    `json:"actor_type"`
	ActorID      string    `json:"actor_id"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Reason       string    `json:"reason,omitempty"`
}

type state struct {
	Version     int              `json:"version"`
	ConsoleID   string           `json:"console_id"`
	Revision    int64            `json:"revision"`
	Domains     []*domainRec     `json:"domains"`
	Devices     []*deviceRec     `json:"devices"`
	Credentials []*credentialRec `json:"access_credentials"`
	Audit       []*auditRec      `json:"audit"`
}

func (st *state) domain(id string) *domainRec {
	for _, d := range st.Domains {
		if d.DomainID == id {
			return d
		}
	}
	return nil
}

func (st *state) device(id string) *deviceRec {
	for _, d := range st.Devices {
		if d.DeviceID == id {
			return d
		}
	}
	return nil
}

func (st *state) credentialByDigest(digest string) *credentialRec {
	for _, c := range st.Credentials {
		if subtle.ConstantTimeCompare([]byte(c.CodeDigest), []byte(digest)) == 1 {
			return c
		}
	}
	return nil
}

func (st *state) domainDevices(domainID string) []*deviceRec {
	var out []*deviceRec
	for _, d := range st.Devices {
		if d.DomainID == domainID {
			out = append(out, d)
		}
	}
	return out
}

func (st *state) audit(a *auditRec) {
	st.Audit = append(st.Audit, a)
	if n := len(st.Audit) - maxAudit; n > 0 {
		st.Audit = append([]*auditRec(nil), st.Audit[n:]...)
	}
}

// loadState reads the state file; a missing file yields a fresh state.
func loadState(dir string) (*state, error) {
	raw, err := os.ReadFile(filepath.Join(dir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return &state{Version: 1, ConsoleID: "sparkclaw-dev-console-" + randomID(8)}, nil
	}
	if err != nil {
		return nil, err
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("state file is damaged: %w", err)
	}
	if st.ConsoleID == "" {
		return nil, errors.New("state file names no console_id")
	}
	return &st, nil
}

func saveState(dir string, st *state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, stateFile), b)
}

// writeFileAtomic writes through a temporary file, fsyncs it, renames it
// over the target and fsyncs the directory. The file is 0600.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// EnsureDataDir creates the data directory (0700).
func EnsureDataDir(dir string) error {
	if dir == "" {
		return errors.New("data directory is not set")
	}
	return os.MkdirAll(dir, 0o700)
}

// --- account -------------------------------------------------------------

const (
	pbkdf2Iterations  = 200_000
	minPasswordLength = 8
	adminUserID       = "admin"
)

func loadAccount(dir string) (*account, error) {
	raw, err := os.ReadFile(filepath.Join(dir, accountFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a account
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("account file is damaged: %w", err)
	}
	return &a, nil
}

func saveAccount(dir string, a *account) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, accountFile), b)
}

func hashPassword(password string, salt []byte, iterations int) (string, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return crypto.Base64URL(key), nil
}

func (a *account) checkPassword(password string) bool {
	if a == nil || a.PasswordHash == "" {
		return false
	}
	salt, err := crypto.DecodeBase64URL(a.PasswordSalt)
	if err != nil {
		return false
	}
	got, err := hashPassword(password, salt, a.Iterations)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.PasswordHash)) == 1
}

func (a *account) checkAPIToken(token string) bool {
	if a == nil || a.APITokenHash == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tokenHash(token)), []byte(a.APITokenHash)) == 1
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte("sparkclaw-dev-console-api-token-v1\x00" + token))
	return crypto.Base64URL(sum[:])
}

// SetPassword sets the administrator password, creating the account when
// it does not exist. It works while the console runs.
func SetPassword(dir, password string) error {
	if len(password) < minPasswordLength {
		return fmt.Errorf("password must have at least %d characters", minPasswordLength)
	}
	if err := EnsureDataDir(dir); err != nil {
		return err
	}
	a, err := loadAccount(dir)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if a == nil {
		a = &account{UserID: adminUserID, DisplayName: "管理员", CreatedAt: now}
	}
	salt := crypto.RandomBytes(16)
	h, err := hashPassword(password, salt, pbkdf2Iterations)
	if err != nil {
		return err
	}
	a.PasswordHash, a.PasswordSalt, a.Iterations, a.UpdatedAt = h, crypto.Base64URL(salt), pbkdf2Iterations, now
	return saveAccount(dir, a)
}

// RotateAPIToken creates a new administrator API token, stores only its
// hash (replacing any earlier token) and returns the token.
func RotateAPIToken(dir string) (string, error) {
	a, err := loadAccount(dir)
	if err != nil {
		return "", err
	}
	if a == nil {
		return "", errors.New("no administrator account yet: set a password first")
	}
	token := "scdc_" + crypto.Base64URL(crypto.RandomBytes(32))
	a.APITokenHash = tokenHash(token)
	a.UpdatedAt = time.Now().UTC()
	if err := saveAccount(dir, a); err != nil {
		return "", err
	}
	return token, nil
}

// --- signer --------------------------------------------------------------

type signerFileBody struct {
	Identity   identity.DeviceIdentity `json:"identity"`
	PrivateKey string                  `json:"private_key"`
}

// loadOrCreateSigner reads the console's trust-root signing key, creating
// it on first start.
func loadOrCreateSigner(dir, consoleID, domainID string, provider crypto.Provider) (identity.Device, error) {
	path := filepath.Join(dir, signerFile)
	raw, err := os.ReadFile(path)
	if err == nil {
		var body signerFileBody
		if err := json.Unmarshal(raw, &body); err != nil {
			return identity.Device{}, fmt.Errorf("signer file is damaged: %w", err)
		}
		keyBytes, err := crypto.DecodeBase64URL(body.PrivateKey)
		if err != nil {
			return identity.Device{}, fmt.Errorf("signer key is damaged: %w", err)
		}
		priv, err := crypto.Ed25519PrivateKeyFromBytes(keyBytes)
		if err != nil {
			return identity.Device{}, err
		}
		if crypto.Base64URL(priv.Public().Bytes()) != body.Identity.PublicKey.Public {
			return identity.Device{}, errors.New("signer key does not match its identity")
		}
		return identity.Device{Identity: body.Identity, Private: priv}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return identity.Device{}, err
	}
	dev, err := identity.NewDevice(provider, domainID, consoleID+"-signer", time.Now().UTC())
	if err != nil {
		return identity.Device{}, err
	}
	b, err := json.MarshalIndent(signerFileBody{Identity: dev.Identity, PrivateKey: crypto.Base64URL(dev.Private.BytesForDevStore())}, "", "  ")
	if err != nil {
		return identity.Device{}, err
	}
	if err := writeFileAtomic(path, b); err != nil {
		return identity.Device{}, err
	}
	return dev, nil
}

// randomID returns n random bytes as lowercase base32.
func randomID(n int) string {
	return strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(crypto.RandomBytes(n)), "="))
}
