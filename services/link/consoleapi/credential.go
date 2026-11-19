package consoleapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// CredentialPrefix marks a full SparkClaw access credential.
const CredentialPrefix = "sparkclaw-link-v1."

// Credential is the one-time SparkClaw access credential the console issues
// (console design 3.4). The full form carries the console address and its
// TLS pin; the short form is the code alone, used by devices whose trust
// configuration already names the console (design 3.5), such as the watch.
type Credential struct {
	V         int    `json:"v"`
	Console   string `json:"console"`
	TLSPin    string `json:"tls_pin,omitempty"`
	Code      string `json:"code"`
	Type      string `json:"type"`
	ExpiresAt string `json:"exp,omitempty"`
}

// Encode renders the full credential text.
func (c Credential) Encode() string {
	c.V = 1
	b, _ := json.Marshal(c)
	return CredentialPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// ParseCredential accepts the full credential or a bare code. A bare code
// returns a credential with only Code set.
func ParseCredential(text string) (Credential, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, CredentialPrefix) {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(text, CredentialPrefix))
		if err != nil {
			return Credential{}, errors.New("credential is damaged")
		}
		var c Credential
		if err := json.Unmarshal(raw, &c); err != nil || c.V != 1 {
			return Credential{}, errors.New("credential is damaged")
		}
		u, err := url.Parse(c.Console)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return Credential{}, errors.New("credential names no valid console")
		}
		code, err := NormalizeCode(c.Code)
		if err != nil {
			return Credential{}, err
		}
		c.Code = code
		if !ValidDeviceType(c.Type) {
			return Credential{}, errors.New("credential names an unknown device type")
		}
		return c, nil
	}
	code, err := NormalizeCode(text)
	if err != nil {
		return Credential{}, err
	}
	return Credential{V: 1, Code: code}, nil
}

// Expired reports whether the credential's stated expiry has passed.
func (c Credential) Expired(now time.Time) bool {
	if c.ExpiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, c.ExpiresAt)
	return err == nil && !now.Before(t)
}

// CodeDigits is the length of an access code.
const CodeDigits = 12

// NewCode returns a random 12-digit code.
func NewCode() string {
	var b strings.Builder
	for i := 0; i < CodeDigits; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			panic(err)
		}
		b.WriteByte(byte('0' + n.Int64()))
	}
	return b.String()
}

// NormalizeCode strips separators and checks the code shape.
func NormalizeCode(s string) (string, error) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || unicode.IsSpace(r):
		default:
			return "", errors.New("access code may contain only digits")
		}
	}
	if b.Len() != CodeDigits {
		return "", errors.New("access code must have 12 digits")
	}
	return b.String(), nil
}

// FormatCode groups a code for display: 1234 5678 9012.
func FormatCode(code string) string {
	if len(code) != CodeDigits {
		return code
	}
	return code[0:4] + " " + code[4:8] + " " + code[8:12]
}

// CodeDigest is what the console stores and what a join proof binds to.
func CodeDigest(code string) string {
	sum := sha256.Sum256([]byte("sparkclaw-link-code-v1\x00" + code))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// JoinChallenge is the proof challenge for a join with this code.
func JoinChallenge(code string) string {
	return OpJoin + ":" + CodeDigest(code)
}
