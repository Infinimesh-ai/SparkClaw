package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"slices"
	"time"
)

type TransportLimits struct {
	RequestBytes   int `json:"request_bytes"`
	ResponseBytes  int `json:"response_bytes"`
	BodyBytes      int `json:"body_bytes"`
	Concurrent     int `json:"concurrent"`
	ChunkBytes     int `json:"chunk_bytes"`
	TransferWindow int `json:"transfer_window"`
}
type TransportCapabilities struct {
	SchemaVersion         int             `json:"schema_version"`
	Profile               string          `json:"profile"`
	SessionID             string          `json:"session_id"`
	Revision              string          `json:"revision"`
	AuthorizationRevision uint64          `json:"authorization_revision"`
	ExpiresAt             time.Time       `json:"expires_at"`
	Operations            []string        `json:"operations"`
	QualifiedOperations   []string        `json:"qualified_operations"`
	Binding               *Binding        `json:"binding,omitempty"`
	Limits                TransportLimits `json:"limits"`
}
type SessionInfo struct {
	SessionID, Profile          string
	GrantRevision               uint64
	Binding                     *Binding
	Scopes, QualifiedOperations []string
	CheckAuthorization          func(context.Context) (iscpauth.Policy, error)
}
type sessionInfoKey struct{}

func SessionFromContext(ctx context.Context) (SessionInfo, bool) {
	s, ok := ctx.Value(sessionInfoKey{}).(SessionInfo)
	s.Scopes = slices.Clone(s.Scopes)
	s.QualifiedOperations = slices.Clone(s.QualifiedOperations)
	if s.Binding != nil {
		b := *s.Binding
		s.Binding = &b
	}
	return s, ok
}
func (e *Endpoint) CheckAuthorization(ctx context.Context) error {
	_, err := e.AuthorizationPolicy(ctx)
	return err
}
func (e *Endpoint) AuthorizationPolicy(ctx context.Context) (iscpauth.Policy, error) {
	if err := verifyGrant(e.config, e.grantMaterial(), time.Now().UTC()); err != nil {
		return iscpauth.Policy{}, err
	}
	if e.lifecycle == nil {
		return iscpauth.Policy{Version: 1, Lifetime: iscpauth.Bounded, Revision: e.grantMaterial().grant.RevocationEpoch, State: iscpauth.Active}, nil
	}
	p, err := e.lifecycle.AuthorizationPolicy(ctx)
	e.observeAuthorizationError(err)
	return p, err
}
func (e *Endpoint) Negotiated() (TransportCapabilities, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.session.capabilities == nil || !time.Now().Before(e.session.capabilities.ExpiresAt) {
		return TransportCapabilities{}, false
	}
	c := *e.session.capabilities
	c.Operations = slices.Clone(c.Operations)
	c.QualifiedOperations = slices.Clone(c.QualifiedOperations)
	if c.Binding != nil {
		b := *c.Binding
		c.Binding = &b
	}
	return c, true
}
func (e *Endpoint) SetCapabilitiesHandler(handler func(TransportCapabilities)) {
	e.mu.Lock()
	e.onCapabilities = handler
	e.mu.Unlock()
}
func (c Config) supportsV2() bool { return slices.Contains(c.ApplicationProfiles, ProfileV2) }

type negotiationOffer struct {
	Negotiation struct {
		Profiles []string `json:"profiles"`
	} `json:"transport_negotiation"`
}
type negotiationReply struct {
	Negotiation *TransportCapabilities `json:"transport_negotiation"`
}

func negotiationRequest(r Request) bool {
	var fields map[string]json.RawMessage
	if r.Operation != OperationIdentity || json.Unmarshal(r.Body, &fields) != nil {
		return false
	}
	_, ok := fields["transport_negotiation"]
	return ok
}
func (e *Endpoint) capabilitiesFor(id string, profile string) TransportCapabilities {
	ops := Operations()
	version := 1
	if profile == ProfileV2 {
		ops = OperationsV2()
		version = 2
	}
	return TransportCapabilities{SchemaVersion: version, Profile: profile, SessionID: id, Revision: "2", AuthorizationRevision: e.grantMaterial().grant.RevocationEpoch, ExpiresAt: time.Now().UTC().Add(5 * time.Minute), Operations: ops, QualifiedOperations: slices.Clone(e.config.QualifiedCapabilities), Binding: e.config.Binding, Limits: TransportLimits{MaxRequestBytes, MaxResponseBytes, MaxBodyBytes, MaxConcurrent, 8192, 2}}
}
func (e *Endpoint) answerNegotiation(ctx context.Context, id string, r Request) error {
	var offer negotiationOffer
	if err := strictDecode(r.Body, &offer); err != nil || len(offer.Negotiation.Profiles) == 0 || len(offer.Negotiation.Profiles) > 2 {
		return e.sendResponse(ctx, id, Response{Profile: Profile, ID: r.ID, Status: 400, Error: "invalid transport negotiation"})
	}
	profile := Profile
	for _, p := range offer.Negotiation.Profiles {
		if p != Profile && p != ProfileV2 {
			return e.sendResponse(ctx, id, Response{Profile: Profile, ID: r.ID, Status: 400, Error: "unsupported transport profile"})
		}
	}
	if e.config.supportsV2() && slices.Contains(offer.Negotiation.Profiles, ProfileV2) {
		profile = ProfileV2
	}
	caps := e.capabilitiesFor(id, profile)
	body, _ := json.Marshal(negotiationReply{&caps})
	e.installCapabilities(caps)
	if err := e.sendResponse(ctx, id, Response{Profile: Profile, ID: r.ID, Status: 200, Body: body}); err != nil {
		return err
	}
	e.installCapabilities(caps)
	return nil
}
func (e *Endpoint) installCapabilities(c TransportCapabilities) {
	e.mu.Lock()
	if e.session == nil || e.session.id != c.SessionID {
		e.mu.Unlock()
		return
	}
	e.session.capabilities = &c
	e.session.negotiating = false
	notify := e.onCapabilities
	e.mu.Unlock()
	if notify != nil {
		notify(c)
	}
	e.setState("transport_ready")
}
func (e *Endpoint) negotiate(ctx context.Context, id string) {
	defer e.workers.Done()
	body := json.RawMessage(`{"transport_negotiation":{"profiles":["sparkclaw.workbench.transport.v2","sparkclaw.workbench.transport.v1"]}}`)
	r, err := e.Call(ctx, Request{Type: RequestType, Profile: Profile, ID: newUUID(), Operation: OperationIdentity, Body: body})
	if err != nil {
		e.negotiationFailed(id)
		return
	}
	if r.Status == 400 || r.Status == 404 || r.Status == 501 {
		e.installCapabilities(e.capabilitiesFor(id, Profile))
		return
	}
	if r.Status != 200 {
		e.negotiationFailed(id)
		return
	}
	var reply negotiationReply
	if json.Unmarshal(r.Body, &reply) != nil || reply.Negotiation == nil {
		e.installCapabilities(e.capabilitiesFor(id, Profile))
		return
	}
	c := *reply.Negotiation
	if err := validateNegotiation(c, id); err != nil || c.AuthorizationRevision != e.grantMaterial().grant.RevocationEpoch || (e.config.Binding != nil && (c.Binding == nil || *c.Binding != *e.config.Binding)) {
		e.negotiationFailed(id)
		return
	}
	e.installCapabilities(c)
}
func validateNegotiation(c TransportCapabilities, id string) error {
	version := 1
	ops := Operations()
	if c.Profile == ProfileV2 {
		version = 2
		ops = OperationsV2()
	} else if c.Profile != Profile {
		return errors.New("incompatible selected profile")
	}
	now := time.Now()
	if c.SessionID != id || c.SchemaVersion != version || c.Revision != "2" || c.AuthorizationRevision == 0 || !c.ExpiresAt.After(now) || c.ExpiresAt.After(now.Add(6*time.Minute)) || !slices.Equal(c.Operations, ops) || c.Limits != (TransportLimits{MaxRequestBytes, MaxResponseBytes, MaxBodyBytes, MaxConcurrent, 8192, 2}) {
		return errors.New("invalid negotiated capability binding")
	}
	for _, name := range c.QualifiedOperations {
		if !slices.Contains(ops, name) {
			return errors.New("unregistered qualified operation")
		}
	}
	return nil
}

func (e *Endpoint) negotiationFailed(id string) {
	e.mu.Lock()
	current := e.session != nil && e.session.id == id
	if current {
		e.session.negotiating = false
		e.session.nextNegotiationAt = time.Now().Add(5 * time.Second)
	}
	e.mu.Unlock()
	if current {
		e.setState("capability_negotiation_failed")
	}
}
