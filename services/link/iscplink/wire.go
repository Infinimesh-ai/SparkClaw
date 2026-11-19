// Package iscplink carries link frames over an ISCP Relay inside an
// end-to-end ISCP session. The device (initiator) first presents its Grant
// in a plaintext envelope, then runs the ISCP Hello/Ready handshake; every
// link frame afterwards travels in one encrypted envelope. The backend's
// connector (responder) verifies each device against the domain directory
// and the console's Grant signer.
package iscplink

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/session"
)

// Payload types.
const (
	PayloadGrant = "sparkclaw.link.grant.v1" // plaintext: trust.Grant
	PayloadHello = session.TypeHello         // plaintext: session.Hello
	PayloadReady = session.TypeReady         // plaintext: session.Ready
	PayloadReset = "sparkclaw.link.reset.v1" // plaintext: {reason}; the peer must handshake again
	PayloadFrame = "sparkclaw.link.frame.v1" // encrypted: one link frame
)

// TTLs.
const (
	HandshakeTTL = 30
	FrameTTL     = 60
)

// Reset reasons.
const (
	ResetUnknownSession = "unknown_session"
	ResetGrantInvalid   = "grant_invalid"
	ResetRevoked        = "revoked"
	ResetSuperseded     = "superseded"
)

type resetBody struct {
	Reason string `json:"reason"`
}

func plaintext(domainID, sessionID, sender, recipient, relayID, payloadType string, ttl int, body any) (envelope.SecureEnvelope, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return envelope.SecureEnvelope{}, err
	}
	return envelope.SecureEnvelope{
		Type:              envelope.TypeSecureEnvelope,
		DomainID:          domainID,
		MessageID:         newID(),
		SessionID:         sessionID,
		SenderDeviceID:    sender,
		RecipientDeviceID: recipient,
		Sequence:          0,
		Nonce:             crypto.Base64URL(crypto.RandomBytes(12)),
		PayloadType:       payloadType,
		Route:             envelope.Route{RelayID: relayID, TTLSeconds: ttl, Priority: 5},
		Ciphertext:        crypto.Base64URL(raw),
	}, nil
}

func decodePlain(env envelope.SecureEnvelope, out any) error {
	raw, err := crypto.DecodeBase64URL(env.Ciphertext)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func newID() string { return crypto.Base64URL(crypto.RandomBytes(18)) }

// secureState serializes access to an ISCP session state, which is not safe
// for concurrent use.
type secureState struct {
	mu      sync.Mutex
	state   *session.State
	relayID string
	created time.Time
}

func (s *secureState) seal(provider crypto.Provider, payload []byte) (envelope.SecureEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil || !s.state.Ready() {
		return envelope.SecureEnvelope{}, errors.New("session not ready")
	}
	return envelope.Encrypt(provider, s.state, newID(), PayloadFrame, envelope.Route{RelayID: s.relayID, TTLSeconds: FrameTTL, Priority: 5}, payload)
}

func (s *secureState) open(provider crypto.Provider, env envelope.SecureEnvelope) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return envelope.Decrypt(provider, s.state, env)
}

// MaxSessionAge bounds one session's lifetime (its replay window grows with
// every message); the initiator handshakes again after it.
const MaxSessionAge = 6 * time.Hour
