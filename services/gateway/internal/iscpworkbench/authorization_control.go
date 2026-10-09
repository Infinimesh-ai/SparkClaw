package iscpworkbench

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
)

// These proof-bound issuer calls intentionally remain available after session
// revocation. They reveal only the original deletion operation's receipt.
func (e *Endpoint) DeleteAuthorization(ctx context.Context, operationID string, revision uint64) (iscpauth.DeletionReceipt, error) {
	if e.lifecycle == nil || e.config.Role != RoleInitiator {
		return iscpauth.DeletionReceipt{}, errors.New("authorization deletion unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := e.lifecycle.DeleteAuthorization(ctx, operationID, revision)
	if err == nil {
		e.observeAuthorizationError(iscpbridge.ErrAuthorizationRevoked)
	}
	return r, err
}
func (e *Endpoint) AuthorizationDeletionReceipt(ctx context.Context, operationID string, revision uint64) (iscpauth.DeletionReceipt, error) {
	if e.lifecycle == nil || e.config.Role != RoleInitiator {
		return iscpauth.DeletionReceipt{}, errors.New("authorization receipt unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := e.lifecycle.AuthorizationDeletionReceipt(ctx, operationID, revision)
	if err == nil {
		e.observeAuthorizationError(iscpbridge.ErrAuthorizationRevoked)
	}
	return r, err
}

// AuthorizationControl never constructs a Relay client. Its pipe remains usable
// when business credentials or a revoked session can no longer reconnect.
type AuthorizationControl struct {
	lifecycle *iscpbridge.GrantLifecycleClient
	identity  PublicIdentity
}

func NewAuthorizationControl(cfg Config) (*AuthorizationControl, error) {
	m, err := loadMaterialMode(cfg, true)
	if err != nil {
		return nil, err
	}
	if cfg.Role != RoleInitiator {
		return nil, errors.New("authorization control requires initiator identity")
	}
	client, err := iscpbridge.NewGrantLifecycleClient(cfg.GrantRenewal.URL, cfg.GrantRenewal.PendingFile, m.device, m.issuer, m.enrollment.RelayID, m.grant, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &AuthorizationControl{lifecycle: client, identity: PublicIdentity{DomainID: m.enrollment.DomainID, InitiatorDeviceID: m.device.Identity.DeviceID, ResponderDeviceID: m.peer.DeviceID, ResponderKeyThumbprint: m.peer.PublicKey.KID, RelayURL: m.enrollment.RelayBaseURL, RelayProfile: cfg.EffectiveRelayProfile()}}, nil
}
func (c *AuthorizationControl) PublicIdentity() PublicIdentity { return c.identity }
func (c *AuthorizationControl) Run(ctx context.Context) error  { <-ctx.Done(); return ctx.Err() }
func (c *AuthorizationControl) Close() error                   { return nil }
func (c *AuthorizationControl) Call(context.Context, Request) (Response, error) {
	return Response{}, errors.New("business calls are unavailable in authorization control mode")
}
func (c *AuthorizationControl) DeleteAuthorization(ctx context.Context, id string, revision uint64) (iscpauth.DeletionReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return c.lifecycle.DeleteAuthorization(ctx, id, revision)
}
func (c *AuthorizationControl) AuthorizationDeletionReceipt(ctx context.Context, id string, revision uint64) (iscpauth.DeletionReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return c.lifecycle.AuthorizationDeletionReceipt(ctx, id, revision)
}
