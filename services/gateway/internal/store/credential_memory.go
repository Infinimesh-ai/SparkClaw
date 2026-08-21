package store

import (
	"context"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveCredentialSecret(ctx context.Context, command CredentialSaveCommand) (app.CredentialSecret, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretSave, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	command, err := normalizeCredentialSaveCommand(command)
	if err != nil {
		return app.CredentialSecret{}, storeError(ctx, OperationCredentialSecretSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationCredentialSecretSave, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	current, exists := s.credentialSecrets[command.secret.Ref]
	if exists {
		current, err = normalizePersistedCredentialSecret(current)
		if err != nil {
			return app.CredentialSecret{}, storeError(ctx, OperationCredentialSecretSave, StoreErrorCorrupt, err)
		}
	}
	if command.mode == credentialSaveCreate {
		if exists {
			return app.CredentialSecret{}, storeError(ctx, OperationCredentialSecretSave, StoreErrorConflict, errors.New("credential already exists"))
		}
	} else if !exists || credentialSecretDigest(current) != command.expected {
		return app.CredentialSecret{}, storeError(ctx, OperationCredentialSecretSave, StoreErrorConflict, errors.New("credential changed"))
	}
	commandAt := nextRepositoryTime(s.credentialNow(), s.credentialWriteHighWater[command.secret.Ref], latestCredentialTime(current))
	candidate := command.secret
	if exists {
		candidate.CreatedAt = current.CreatedAt
		candidate.UpdatedAt = commandAt
	} else {
		candidate.CreatedAt = commandAt
		candidate.UpdatedAt = commandAt
	}
	s.credentialWriteHighWater[candidate.Ref] = commandAt
	s.credentialSecrets[candidate.Ref] = candidate
	s.appendAuditLockedAt(commandAt, "credential_secret.saved", "", "", "gateway", candidate.Kind, map[string]any{
		"ref": candidate.Ref, "kind": candidate.Kind,
	})
	return candidate, nil
}

func (s *MemoryStore) GetCredentialSecret(ctx context.Context, ref string) (app.CredentialSecret, bool, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretGet, ctx); err != nil {
		return app.CredentialSecret{}, false, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return app.CredentialSecret{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationCredentialSecretGet, ctx); err != nil {
		return app.CredentialSecret{}, false, err
	}
	secret, ok := s.credentialSecrets[ref]
	if !ok {
		return app.CredentialSecret{}, false, nil
	}
	secret, err := normalizePersistedCredentialSecret(secret)
	if err != nil {
		return app.CredentialSecret{}, false, storeError(ctx, OperationCredentialSecretGet, StoreErrorCorrupt, err)
	}
	return secret, true, nil
}

func (s *MemoryStore) DeleteCredentialSecret(ctx context.Context, condition CredentialDeleteCondition) (app.CredentialSecret, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretDelete, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	condition, err := normalizeCredentialDeleteCondition(condition)
	if err != nil {
		return app.CredentialSecret{}, storeError(ctx, OperationCredentialSecretDelete, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationCredentialSecretDelete, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	secret, ok := s.credentialSecrets[condition.ref]
	if !ok {
		return app.CredentialSecret{}, storeError(ctx, OperationCredentialSecretDelete, StoreErrorNotFound, errors.New("credential not found"))
	}
	secret, err = normalizePersistedCredentialSecret(secret)
	if err != nil {
		return app.CredentialSecret{}, storeError(ctx, OperationCredentialSecretDelete, StoreErrorCorrupt, err)
	}
	if credentialSecretDigest(secret) != condition.expected {
		return app.CredentialSecret{}, storeError(ctx, OperationCredentialSecretDelete, StoreErrorConflict, errors.New("credential changed"))
	}
	commandAt := nextRepositoryTime(s.credentialNow(), s.credentialWriteHighWater[secret.Ref], latestCredentialTime(secret))
	s.credentialWriteHighWater[secret.Ref] = commandAt
	delete(s.credentialSecrets, secret.Ref)
	s.appendAuditLockedAt(commandAt, "credential_secret.deleted", "", "", "gateway", "credential deleted", map[string]any{"ref": secret.Ref})
	return secret, nil
}
