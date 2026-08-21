package store

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) GetClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientGet, ctx); err != nil {
		return app.Client{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.Client{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationClientGet, ctx); err != nil {
		return app.Client{}, false, err
	}
	client, ok := s.clients[id]
	if !ok {
		return app.Client{}, false, nil
	}
	if err := validatePersistedClient(client); err != nil {
		return app.Client{}, false, storeError(ctx, OperationClientGet, StoreErrorCorrupt, err)
	}
	return cloneClient(client), true, nil
}

func (s *MemoryStore) ListClients(ctx context.Context) ([]app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationClientList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationClientList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.Client, 0, len(s.clients))
	for _, client := range s.clients {
		if err := validatePersistedClient(client); err != nil {
			return nil, storeError(ctx, OperationClientList, StoreErrorCorrupt, err)
		}
		out = append(out, cloneClient(client))
	}
	slices.SortFunc(out, compareClients)
	return out, nil
}

func compareClients(left, right app.Client) int {
	if ordered := right.CreatedAt.Compare(left.CreatedAt); ordered != 0 {
		return ordered
	}
	return strings.Compare(left.ID, right.ID)
}

func (s *MemoryStore) RevokeClient(ctx context.Context, id string) (app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationClientRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientRevoke, ctx); err != nil {
		return app.Client{}, err
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationClientRevoke, ctx); err != nil {
		return app.Client{}, err
	}
	client, ok := s.clients[id]
	if !ok {
		return app.Client{}, storeError(ctx, OperationClientRevoke, StoreErrorNotFound, errors.New("client not found"))
	}
	if err := validatePersistedClient(client); err != nil {
		return app.Client{}, storeError(ctx, OperationClientRevoke, StoreErrorCorrupt, err)
	}
	now := nextRepositoryTime(s.clientNow(), s.clientWriteHighWater[id], client.CreatedAt, timePointerValue(client.LastSeenAt), timePointerValue(client.RevokedAt))
	client.RevokedAt = &now
	s.clientWriteHighWater[id] = now
	s.clients[id] = cloneClient(client)
	s.appendAuditLockedAt(now, "client.revoked", "", "", "owner", client.Name, map[string]any{"client_id": client.ID})
	s.appendEventLockedAt(now, "client.revoked", "", "", cloneClient(client))
	return cloneClient(client), nil
}

func (s *MemoryStore) FindClientByTokenHash(ctx context.Context, tokenHash string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientFindTokenHash, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientFindTokenHash, ctx); err != nil {
		return app.Client{}, false, err
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return app.Client{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationClientFindTokenHash, ctx); err != nil {
		return app.Client{}, false, err
	}
	for _, client := range s.clients {
		if client.TokenHash != tokenHash {
			continue
		}
		if err := validatePersistedClient(client); err != nil {
			return app.Client{}, false, storeError(ctx, OperationClientFindTokenHash, StoreErrorCorrupt, err)
		}
		if client.RevokedAt == nil {
			return cloneClient(client), true, nil
		}
	}
	return app.Client{}, false, nil
}

func (s *MemoryStore) TouchClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientTouch, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientTouch, ctx); err != nil {
		return app.Client{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.Client{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationClientTouch, ctx); err != nil {
		return app.Client{}, false, err
	}
	client, ok := s.clients[id]
	if !ok {
		return app.Client{}, false, nil
	}
	if err := validatePersistedClient(client); err != nil {
		return app.Client{}, false, storeError(ctx, OperationClientTouch, StoreErrorCorrupt, err)
	}
	if client.RevokedAt != nil {
		return app.Client{}, false, nil
	}
	now := nextRepositoryTime(s.clientNow(), s.clientWriteHighWater[id], client.CreatedAt, timePointerValue(client.LastSeenAt))
	client.LastSeenAt = &now
	s.clientWriteHighWater[id] = now
	s.clients[id] = cloneClient(client)
	return cloneClient(client), true, nil
}

func (s *MemoryStore) SavePairingCode(ctx context.Context, code app.PairingCode) (app.PairingCode, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeSave, ctx); err != nil {
		return app.PairingCode{}, err
	}
	code, err := normalizePairingSave(code)
	if err != nil {
		return app.PairingCode{}, storeError(ctx, OperationPairingCodeSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPairingCodeSave, ctx); err != nil {
		return app.PairingCode{}, err
	}
	if _, exists := s.pairingCodes[code.ID]; exists {
		return app.PairingCode{}, storeError(ctx, OperationPairingCodeSave, StoreErrorConflict, errors.New("pairing ID already exists"))
	}
	for _, existing := range s.pairingCodes {
		if strings.TrimSpace(existing.CodeHash) != "" && existing.CodeHash == code.CodeHash {
			return app.PairingCode{}, storeError(ctx, OperationPairingCodeSave, StoreErrorConflict, errors.New("pairing code hash already exists"))
		}
	}
	createdAt := nextRepositoryTime(s.clientNow(), s.pairingWriteHighWater[code.ID])
	code.CreatedAt = createdAt
	s.pairingWriteHighWater[code.ID] = createdAt
	s.pairingCodes[code.ID] = clonePairingCode(code)
	s.appendAuditLockedAt(createdAt, "pairing_code.created", "", "", "gateway", "Pairing code created", map[string]any{"pairing_id": code.ID})
	s.appendEventLockedAt(createdAt, "pairing_code.created", "", "", clonePairingCode(code))
	return clonePairingCode(code), nil
}

func (s *MemoryStore) GetPairingCode(ctx context.Context, id string) (app.PairingCode, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeGet, ctx); err != nil {
		return app.PairingCode{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.PairingCode{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPairingCodeGet, ctx); err != nil {
		return app.PairingCode{}, false, err
	}
	code, ok := s.pairingCodes[id]
	if !ok {
		return app.PairingCode{}, false, nil
	}
	if err := validatePersistedPairingCode(code, s.clients); err != nil {
		return app.PairingCode{}, false, storeError(ctx, OperationPairingCodeGet, StoreErrorCorrupt, err)
	}
	return clonePairingCode(code), true, nil
}

func (s *MemoryStore) ClaimPairingCode(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeClaim, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeClaim, ctx); err != nil {
		return app.PairingCode{}, app.Client{}, err
	}
	client, err := normalizeClaimClient(client)
	if err != nil {
		return app.PairingCode{}, app.Client{}, storeError(ctx, OperationPairingCodeClaim, StoreErrorInvalid, err)
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPairingCodeClaim, ctx); err != nil {
		return app.PairingCode{}, app.Client{}, err
	}
	code, ok := s.pairingCodes[id]
	if !ok {
		return app.PairingCode{}, app.Client{}, storeError(ctx, OperationPairingCodeClaim, StoreErrorNotFound, errors.New("pairing code not found"))
	}
	if err := validatePersistedPairingCode(code, s.clients); err != nil {
		return app.PairingCode{}, app.Client{}, storeError(ctx, OperationPairingCodeClaim, StoreErrorCorrupt, err)
	}
	now := postgresTime(s.clientNow())
	if code.Status != "pending" || strings.TrimSpace(code.CodeHash) == "" || !code.ExpiresAt.After(now) {
		return app.PairingCode{}, app.Client{}, storeError(ctx, OperationPairingCodeClaim, StoreErrorConflict, errors.New("pairing code is not claimable"))
	}
	if _, exists := s.clients[client.ID]; exists {
		return app.PairingCode{}, app.Client{}, storeError(ctx, OperationPairingCodeClaim, StoreErrorConflict, errors.New("client ID already exists"))
	}
	for _, existing := range s.clients {
		if strings.TrimSpace(existing.TokenHash) != "" && existing.TokenHash == client.TokenHash {
			return app.PairingCode{}, app.Client{}, storeError(ctx, OperationPairingCodeClaim, StoreErrorConflict, errors.New("client token hash already exists"))
		}
	}
	commandAt := nextRepositoryTime(now, s.pairingWriteHighWater[id], s.clientWriteHighWater[client.ID], code.CreatedAt, timePointerValue(code.ClaimedAt))
	client.CreatedAt = commandAt
	code.Status = "claimed"
	code.ClaimedAt = cloneTimePointer(&commandAt)
	code.ClientID = client.ID
	s.clientWriteHighWater[client.ID] = commandAt
	s.pairingWriteHighWater[id] = commandAt
	s.clients[client.ID] = cloneClient(client)
	s.pairingCodes[id] = clonePairingCode(code)
	s.appendAuditLockedAt(commandAt, "client.saved", "", "", "gateway", client.Name, map[string]any{"client_id": client.ID})
	s.appendAuditLockedAt(commandAt, "pairing_code.claimed", "", "", "gateway", "Pairing code claimed", map[string]any{"pairing_id": code.ID, "client_id": client.ID})
	s.appendEventLockedAt(commandAt, "client.saved", "", "", cloneClient(client))
	s.appendEventLockedAt(commandAt, "pairing_code.claimed", "", "", clonePairingCode(code))
	return clonePairingCode(code), cloneClient(client), nil
}
