package iscpworkbench

import (
	"context"
	"errors"

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
