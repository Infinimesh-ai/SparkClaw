package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type MCPAccessRecordDeletion struct {
	DeletedTickets  int `json:"deleted_tickets"`
	DeletedBindings int `json:"deleted_bindings"`
}

const mcpSessionDeviceIDLength = 12

func mcpSessionTitle(deviceID string) string {
	var identifier strings.Builder
	for _, char := range strings.TrimSpace(deviceID) {
		if identifier.Len() >= mcpSessionDeviceIDLength {
			break
		}
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("-_.", char) {
			identifier.WriteRune(char)
		}
	}
	shortID := strings.Trim(identifier.String(), "-_.")
	if shortID == "" {
		return "AI"
	}
	return "AI · " + shortID
}

func normalizeMCPAccessTicket(ticket app.MCPAccessTicket, now time.Time) app.MCPAccessTicket {
	now = normalizeMCPTime(now)
	if ticket.ID == "" {
		ticket.ID = app.NewID("mcp_ticket")
	}
	if ticket.OwnerID == "" {
		ticket.OwnerID = app.DefaultOwnerID
	}
	if ticket.ActorID == "" {
		ticket.ActorID = ticket.OwnerID
	}
	if ticket.AuthorizationRevision <= 0 {
		ticket.AuthorizationRevision = 1
	}
	if ticket.Status == "" {
		ticket.Status = app.MCPAccessPending
	}
	if ticket.MaxUses <= 0 {
		ticket.MaxUses = 1
	}
	if ticket.IssuedAt.IsZero() {
		ticket.IssuedAt = now
	} else {
		ticket.IssuedAt = normalizeMCPTime(ticket.IssuedAt)
	}
	ticket.ExpiresAt = normalizeMCPTime(ticket.ExpiresAt)
	ticket.ConsumedAt = normalizeMCPTimePointer(ticket.ConsumedAt)
	ticket.RevokedAt = normalizeMCPTimePointer(ticket.RevokedAt)
	return ticket
}

func normalizeMCPBinding(binding app.MCPBinding, now time.Time) app.MCPBinding {
	now = normalizeMCPTime(now)
	if binding.ID == "" {
		binding.ID = app.NewID("mcp_binding")
	}
	if binding.Status == "" {
		binding.Status = app.MCPBindingActive
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	} else {
		binding.CreatedAt = normalizeMCPTime(binding.CreatedAt)
	}
	binding.UpdatedAt = now
	binding.LastUsedAt = normalizeMCPTimePointer(binding.LastUsedAt)
	binding.RevokedAt = normalizeMCPTimePointer(binding.RevokedAt)
	return binding
}

func normalizeMCPOperation(operation app.MCPOperation, now time.Time) app.MCPOperation {
	now = normalizeMCPTime(now)
	if operation.SchemaVersion == 0 {
		operation.SchemaVersion = app.MCPOperationSchemaVersion
	}
	if operation.ID == "" {
		operation.ID = app.NewID("mcp_operation")
	}
	if operation.Version <= 0 {
		operation.Version = 1
	}
	if operation.State == "" {
		operation.State = app.MCPOperationRunning
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = now
	} else {
		operation.CreatedAt = normalizeMCPTime(operation.CreatedAt)
	}
	operation.UpdatedAt = now
	operation.CompletedAt = normalizeMCPTimePointer(operation.CompletedAt)
	if !operation.Invocation.Deadline.IsZero() {
		operation.Invocation.Deadline = normalizeMCPTime(operation.Invocation.Deadline)
	}
	if !operation.Invocation.CreatedAt.IsZero() {
		operation.Invocation.CreatedAt = normalizeMCPTime(operation.Invocation.CreatedAt)
	}
	return operation
}

func normalizeMCPTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

func normalizeMCPTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := normalizeMCPTime(*value)
	return &normalized
}

func cloneMCPAccessTicket(ticket app.MCPAccessTicket) app.MCPAccessTicket {
	return ticket
}

func cloneMCPBinding(binding app.MCPBinding) app.MCPBinding {
	return binding
}

func cloneMCPJSONMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = cloneMCPJSONValue(value)
	}
	return out
}

func cloneMCPJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMCPJSONMap(typed)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneMCPJSONValue(item)
		}
		return out
	default:
		return value
	}
}

func cloneMCPOperation(operation app.MCPOperation) app.MCPOperation {
	operation.Invocation.Arguments = cloneMCPJSONMap(operation.Invocation.Arguments)
	operation.Result = append([]byte(nil), operation.Result...)
	return operation
}

func cloneMCPAccessTicketMap(values map[string]app.MCPAccessTicket) map[string]app.MCPAccessTicket {
	out := make(map[string]app.MCPAccessTicket, len(values))
	for key, value := range values {
		out[key] = cloneMCPAccessTicket(value)
	}
	return out
}

func cloneMCPBindingMap(values map[string]app.MCPBinding) map[string]app.MCPBinding {
	out := make(map[string]app.MCPBinding, len(values))
	for key, value := range values {
		out[key] = cloneMCPBinding(value)
	}
	return out
}

func cloneMCPOperationMap(values map[string]app.MCPOperation) map[string]app.MCPOperation {
	out := make(map[string]app.MCPOperation, len(values))
	for key, value := range values {
		out[key] = cloneMCPOperation(value)
	}
	return out
}

func mcpRecordsEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func ReconcileMCPAccessTicketWrite(ctx context.Context, repository MCPRepository, candidate app.MCPAccessTicket, writeErr error) (app.MCPAccessTicket, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.SecretHash) == "" {
		return app.MCPAccessTicket{}, writeErr
	}
	current, found, err := repository.GetMCPAccessTicket(ctx, candidate.ID)
	if err != nil {
		return app.MCPAccessTicket{}, errors.Join(writeErr, err)
	}
	if !found || !mcpRecordsEqual(current, candidate) {
		return app.MCPAccessTicket{}, writeErr
	}
	byHash, found, err := repository.FindMCPAccessTicketBySecretHash(ctx, candidate.SecretHash)
	if err != nil {
		return app.MCPAccessTicket{}, errors.Join(writeErr, err)
	}
	if found && byHash.ID == candidate.ID && mcpRecordsEqual(byHash, candidate) {
		return current, nil
	}
	return app.MCPAccessTicket{}, writeErr
}

func ReconcileMCPAccessTicketRevoke(ctx context.Context, repository MCPRepository, candidate app.MCPAccessTicket, writeErr error) (app.MCPAccessTicket, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" || candidate.Status != app.MCPAccessRevoked {
		return app.MCPAccessTicket{}, writeErr
	}
	current, found, err := repository.GetMCPAccessTicket(ctx, candidate.ID)
	if err != nil {
		return app.MCPAccessTicket{}, errors.Join(writeErr, err)
	}
	if found && mcpRecordsEqual(current, candidate) {
		return current, nil
	}
	return app.MCPAccessTicket{}, writeErr
}

func ReconcileMCPAccessTicketDelete(ctx context.Context, repository MCPRepository, candidate app.MCPAccessTicket, writeErr error) (app.MCPAccessTicket, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.MCPAccessTicket{}, writeErr
	}
	_, found, err := repository.GetMCPAccessTicket(ctx, candidate.ID)
	if err != nil {
		return app.MCPAccessTicket{}, errors.Join(writeErr, err)
	}
	if !found {
		return candidate, nil
	}
	return app.MCPAccessTicket{}, writeErr
}

func ReconcileMCPAccessTicketRedemption(ctx context.Context, repository MCPRepository, ticketID, secretHash string, peer app.MCPPeerIdentity, candidate app.MCPBinding, writeErr error) (app.MCPBinding, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(ticketID) == "" || strings.TrimSpace(secretHash) == "" || strings.TrimSpace(candidate.ID) == "" {
		return app.MCPBinding{}, writeErr
	}
	current, found, err := repository.FindMCPBindingForPeer(ctx, peer.DomainID, peer.DeviceID, peer.KeyThumbprint)
	if err != nil {
		return app.MCPBinding{}, errors.Join(writeErr, err)
	}
	if !found || !mcpRecordsEqual(current, candidate) {
		return app.MCPBinding{}, writeErr
	}
	ticket, found, err := repository.FindMCPAccessTicketBySecretHash(ctx, secretHash)
	if err != nil {
		return app.MCPBinding{}, errors.Join(writeErr, err)
	}
	if found && ticket.ID == ticketID && ticket.Status == app.MCPAccessConsumed && ticket.UseCount > 0 && ticket.ConsumedAt != nil {
		return current, nil
	}
	return app.MCPBinding{}, writeErr
}

func ReconcileMCPBindingRevoke(ctx context.Context, repository MCPRepository, candidate app.MCPBinding, writeErr error) (app.MCPBinding, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" || candidate.Status != app.MCPBindingRevoked {
		return app.MCPBinding{}, writeErr
	}
	current, found, err := repository.GetMCPBinding(ctx, candidate.ID)
	if err != nil {
		return app.MCPBinding{}, errors.Join(writeErr, err)
	}
	if !found || !mcpRecordsEqual(current, candidate) {
		return app.MCPBinding{}, writeErr
	}
	operations, err := repository.ListMCPOperations(ctx, candidate.ID)
	if err != nil {
		return app.MCPBinding{}, errors.Join(writeErr, err)
	}
	for _, operation := range operations {
		if !mcpOperationTerminal(operation.State) {
			return app.MCPBinding{}, writeErr
		}
	}
	return current, nil
}

func ReconcileMCPBindingDelete(ctx context.Context, repository MCPRepository, candidate app.MCPBinding, writeErr error) (app.MCPBinding, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.MCPBinding{}, writeErr
	}
	_, found, err := repository.GetMCPBinding(ctx, candidate.ID)
	if err != nil {
		return app.MCPBinding{}, errors.Join(writeErr, err)
	}
	if found {
		return app.MCPBinding{}, writeErr
	}
	operations, err := repository.ListMCPOperations(ctx, candidate.ID)
	if err != nil {
		return app.MCPBinding{}, errors.Join(writeErr, err)
	}
	if len(operations) == 0 {
		return candidate, nil
	}
	return app.MCPBinding{}, writeErr
}

func ReconcileMCPAccessRecordDeletion(ctx context.Context, repository MCPRepository, ownerID string, candidate MCPAccessRecordDeletion, writeErr error) (MCPAccessRecordDeletion, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(ownerID) == "" {
		return MCPAccessRecordDeletion{}, writeErr
	}
	tickets, err := repository.ListMCPAccessTickets(ctx, ownerID)
	if err != nil {
		return MCPAccessRecordDeletion{}, errors.Join(writeErr, err)
	}
	bindings, err := repository.ListMCPBindings(ctx, ownerID)
	if err != nil {
		return MCPAccessRecordDeletion{}, errors.Join(writeErr, err)
	}
	if len(tickets) == 0 && len(bindings) == 0 {
		return candidate, nil
	}
	return MCPAccessRecordDeletion{}, writeErr
}

func ReconcileMCPBindingTouch(ctx context.Context, repository MCPRepository, previous app.MCPBinding, iscpSessionID string, touchedAt time.Time, writeErr error) (app.MCPBinding, error) {
	touchedAt = normalizeMCPTime(touchedAt)
	candidate := cloneMCPBinding(previous)
	candidate.LatestISCPSessionID = iscpSessionID
	candidate.LastUsedAt = &touchedAt
	candidate.UpdatedAt = touchedAt
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.MCPBinding{}, writeErr
	}
	current, found, err := repository.GetMCPBinding(ctx, candidate.ID)
	if err != nil {
		return app.MCPBinding{}, errors.Join(writeErr, err)
	}
	if found && mcpRecordsEqual(current, candidate) {
		return current, nil
	}
	return app.MCPBinding{}, writeErr
}

func ReconcileMCPOperationCreate(ctx context.Context, repository MCPRepository, candidate app.MCPOperation, created bool, writeErr error) (app.MCPOperation, bool, error) {
	if writeErr == nil {
		return candidate, created, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.BindingID) == "" || strings.TrimSpace(candidate.IdempotencyKey) == "" {
		return app.MCPOperation{}, false, writeErr
	}
	current, found, err := repository.FindMCPOperationByIdempotency(ctx, candidate.BindingID, candidate.IdempotencyKey)
	if err != nil {
		return app.MCPOperation{}, false, errors.Join(writeErr, err)
	}
	if found && mcpRecordsEqual(current, candidate) {
		return current, created, nil
	}
	return app.MCPOperation{}, false, writeErr
}

func ReconcileMCPOperationUpdate(ctx context.Context, repository MCPRepository, candidate app.MCPOperation, writeErr error) (app.MCPOperation, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" || candidate.Version <= 0 {
		return app.MCPOperation{}, writeErr
	}
	current, found, err := repository.GetMCPOperation(ctx, candidate.ID)
	if err != nil {
		return app.MCPOperation{}, errors.Join(writeErr, err)
	}
	if found && current.Version == candidate.Version && mcpRecordsEqual(current, candidate) {
		return current, nil
	}
	return app.MCPOperation{}, writeErr
}

func (s *MemoryStore) SaveMCPAccessTicket(ctx context.Context, ticket app.MCPAccessTicket) (app.MCPAccessTicket, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketSave, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPAccessTicketSave, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	ticket = normalizeMCPAccessTicket(ticket, time.Now().UTC())
	if ticket.SchemaVersion != app.MCPAccessTicketSchemaVersion || ticket.Scope != app.MCPAccessConversation {
		return app.MCPAccessTicket{}, storeError(ctx, OperationMCPAccessTicketSave, StoreErrorInvalid, ErrMCPAccessTicketInvalid)
	}
	for id, existing := range s.mcpAccessTickets {
		if id != ticket.ID && existing.SecretHash == ticket.SecretHash {
			return app.MCPAccessTicket{}, storeError(ctx, OperationMCPAccessTicketSave, StoreErrorConflict, ErrMCPAccessTicketInvalid)
		}
	}
	s.mcpAccessTickets[ticket.ID] = cloneMCPAccessTicket(ticket)
	return cloneMCPAccessTicket(ticket), nil
}

func (s *MemoryStore) GetMCPAccessTicket(ctx context.Context, id string) (app.MCPAccessTicket, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketGet, ctx); err != nil {
		return app.MCPAccessTicket{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPAccessTicketGet, ctx); err != nil {
		return app.MCPAccessTicket{}, false, err
	}
	ticket, ok := s.mcpAccessTickets[id]
	return cloneMCPAccessTicket(ticket), ok, nil
}

func (s *MemoryStore) FindMCPAccessTicketBySecretHash(ctx context.Context, secretHash string) (app.MCPAccessTicket, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketFindHash, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketFindHash, ctx); err != nil {
		return app.MCPAccessTicket{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPAccessTicketFindHash, ctx); err != nil {
		return app.MCPAccessTicket{}, false, err
	}
	for _, ticket := range s.mcpAccessTickets {
		if ticket.SecretHash == secretHash {
			return cloneMCPAccessTicket(ticket), true, nil
		}
	}
	return app.MCPAccessTicket{}, false, nil
}

func (s *MemoryStore) ListMCPAccessTickets(ctx context.Context, ownerID string) ([]app.MCPAccessTicket, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPAccessTicketList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.MCPAccessTicket, 0)
	for _, ticket := range s.mcpAccessTickets {
		if ownerID == "" || ticket.OwnerID == ownerID {
			out = append(out, cloneMCPAccessTicket(ticket))
		}
	}
	slices.SortFunc(out, func(a, b app.MCPAccessTicket) int {
		if order := b.IssuedAt.Compare(a.IssuedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) RedeemMCPAccessTicket(ctx context.Context, secretHash string, peer app.MCPPeerIdentity, now time.Time) (app.MCPBinding, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketRedeem, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketRedeem, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPAccessTicketRedeem, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	now = normalizeMCPTime(now)
	secretHash = strings.TrimSpace(secretHash)
	for id, ticket := range s.mcpAccessTickets {
		if ticket.SecretHash != secretHash || ticket.Status != app.MCPAccessPending || ticket.UseCount >= ticket.MaxUses ||
			ticket.DomainID != peer.DomainID || !now.Before(ticket.ExpiresAt) ||
			ticket.SchemaVersion != app.MCPAccessTicketSchemaVersion || ticket.Scope != app.MCPAccessConversation {
			continue
		}
		for _, existing := range s.mcpBindings {
			if existing.Status == app.MCPBindingActive && existing.DomainID == peer.DomainID && existing.RequesterDeviceID == peer.DeviceID &&
				existing.RequesterKeyThumbprint == peer.KeyThumbprint {
				return app.MCPBinding{}, storeError(ctx, OperationMCPAccessTicketRedeem, StoreErrorConflict, ErrMCPAccessTicketInvalid)
			}
		}
		ticket.Status = app.MCPAccessConsumed
		ticket.UseCount++
		ticket.ConsumedAt = &now
		s.mcpAccessTickets[id] = ticket
		binding := normalizeMCPBinding(app.MCPBinding{
			SchemaVersion: app.MCPBindingSchemaVersion, ID: app.NewID("mcp_binding"), OwnerID: ticket.OwnerID, ActorID: ticket.ActorID, DomainID: ticket.DomainID,
			RequesterDeviceID: peer.DeviceID, RequesterKeyThumbprint: peer.KeyThumbprint,
			AuthorizationRevision: ticket.AuthorizationRevision, Scope: ticket.Scope, LatestISCPSessionID: peer.ISCPSessionID,
		}, now)
		binding.LinkedSessionID = "s_" + binding.ID
		sessionTime := normalizeSessionTime(now)
		s.sessions[binding.LinkedSessionID] = app.Session{
			ID: binding.LinkedSessionID, OwnerID: binding.OwnerID, Title: mcpSessionTitle(binding.RequesterDeviceID), Source: "mcp", Hidden: false,
			CreatedAt: sessionTime, UpdatedAt: sessionTime,
		}
		s.sessionWriteHighWater[binding.LinkedSessionID] = sessionTime
		s.mcpBindings[binding.ID] = cloneMCPBinding(binding)
		return cloneMCPBinding(binding), nil
	}
	return app.MCPBinding{}, storeError(ctx, OperationMCPAccessTicketRedeem, StoreErrorInvalid, ErrMCPAccessTicketInvalid)
}

func (s *MemoryStore) RevokeMCPAccessTicket(ctx context.Context, id string, now time.Time) (app.MCPAccessTicket, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketRevoke, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPAccessTicketRevoke, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	ticket, ok := s.mcpAccessTickets[id]
	if !ok || ticket.Status != app.MCPAccessPending {
		return app.MCPAccessTicket{}, storeError(ctx, OperationMCPAccessTicketRevoke, StoreErrorConflict, ErrMCPAccessTicketInvalid)
	}
	now = normalizeMCPTime(now)
	ticket.Status, ticket.RevokedAt = app.MCPAccessRevoked, &now
	s.mcpAccessTickets[id] = ticket
	return cloneMCPAccessTicket(ticket), nil
}

func (s *MemoryStore) DeleteMCPAccessTicket(ctx context.Context, ownerID, id string) (app.MCPAccessTicket, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketDelete, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPAccessTicketDelete, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	ticket, ok := s.mcpAccessTickets[id]
	if !ok || ticket.OwnerID != ownerID {
		return app.MCPAccessTicket{}, storeError(ctx, OperationMCPAccessTicketDelete, StoreErrorNotFound, ErrMCPAccessTicketInvalid)
	}
	delete(s.mcpAccessTickets, id)
	return cloneMCPAccessTicket(ticket), nil
}

func (s *MemoryStore) GetMCPBinding(ctx context.Context, id string) (app.MCPBinding, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingGet, ctx); err != nil {
		return app.MCPBinding{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPBindingGet, ctx); err != nil {
		return app.MCPBinding{}, false, err
	}
	binding, ok := s.mcpBindings[id]
	return cloneMCPBinding(binding), ok, nil
}

func (s *MemoryStore) FindMCPBindingForPeer(ctx context.Context, domainID, deviceID, thumbprint string) (app.MCPBinding, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingFindPeer, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingFindPeer, ctx); err != nil {
		return app.MCPBinding{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPBindingFindPeer, ctx); err != nil {
		return app.MCPBinding{}, false, err
	}
	for _, binding := range s.mcpBindings {
		if binding.Status == app.MCPBindingActive && binding.DomainID == domainID && binding.RequesterDeviceID == deviceID && binding.RequesterKeyThumbprint == thumbprint {
			return cloneMCPBinding(binding), true, nil
		}
	}
	return app.MCPBinding{}, false, nil
}

func (s *MemoryStore) ListMCPBindings(ctx context.Context, ownerID string) ([]app.MCPBinding, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPBindingList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.MCPBinding, 0)
	for _, binding := range s.mcpBindings {
		if ownerID == "" || binding.OwnerID == ownerID {
			out = append(out, cloneMCPBinding(binding))
		}
	}
	slices.SortFunc(out, func(a, b app.MCPBinding) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) RevokeMCPBinding(ctx context.Context, id string, now time.Time) (app.MCPBinding, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingRevoke, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPBindingRevoke, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	binding, ok := s.mcpBindings[id]
	if !ok {
		return app.MCPBinding{}, storeError(ctx, OperationMCPBindingRevoke, StoreErrorNotFound, ErrMCPBindingUnavailable)
	}
	now = normalizeMCPTime(now)
	if binding.Status != app.MCPBindingRevoked {
		binding.Status, binding.RevokedAt, binding.UpdatedAt = app.MCPBindingRevoked, &now, now
		s.mcpBindings[id] = binding
	}
	for operationID, operation := range s.mcpOperations {
		if operation.BindingID != id || mcpOperationTerminal(operation.State) {
			continue
		}
		operation.State = app.MCPOperationRevoked
		operation.ErrorCode = "binding_revoked"
		operation.ErrorMessage = "The MCP binding was revoked by the local owner"
		operation.CompletedAt = &now
		operation.Version++
		operation.UpdatedAt = now
		s.mcpOperations[operationID] = cloneMCPOperation(operation)
	}
	return cloneMCPBinding(binding), nil
}

func (s *MemoryStore) DeleteMCPBinding(ctx context.Context, ownerID, id string) (app.MCPBinding, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingDelete, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPBindingDelete, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	binding, ok := s.mcpBindings[id]
	if !ok || binding.OwnerID != ownerID {
		return app.MCPBinding{}, storeError(ctx, OperationMCPBindingDelete, StoreErrorNotFound, ErrMCPBindingUnavailable)
	}
	delete(s.mcpBindings, id)
	for operationID, operation := range s.mcpOperations {
		if operation.BindingID == id {
			delete(s.mcpOperations, operationID)
		}
	}
	return cloneMCPBinding(binding), nil
}

func (s *MemoryStore) DeleteMCPAccessRecords(ctx context.Context, ownerID string) (MCPAccessRecordDeletion, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessRecordsDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessRecordsDelete, ctx); err != nil {
		return MCPAccessRecordDeletion{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPAccessRecordsDelete, ctx); err != nil {
		return MCPAccessRecordDeletion{}, err
	}
	deleted := MCPAccessRecordDeletion{}
	for id, ticket := range s.mcpAccessTickets {
		if ticket.OwnerID == ownerID {
			delete(s.mcpAccessTickets, id)
			deleted.DeletedTickets++
		}
	}
	deletedBindingIDs := make(map[string]struct{})
	for id, binding := range s.mcpBindings {
		if binding.OwnerID == ownerID {
			delete(s.mcpBindings, id)
			deletedBindingIDs[id] = struct{}{}
			deleted.DeletedBindings++
		}
	}
	for id, operation := range s.mcpOperations {
		if _, ok := deletedBindingIDs[operation.BindingID]; ok {
			delete(s.mcpOperations, id)
		}
	}
	return deleted, nil
}

func (s *MemoryStore) TouchMCPBinding(ctx context.Context, id, iscpSessionID string, now time.Time) error {
	ctx, cancel := operationContext(ctx, OperationMCPBindingTouch, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingTouch, ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPBindingTouch, ctx); err != nil {
		return err
	}
	binding, ok := s.mcpBindings[id]
	if !ok || binding.Status != app.MCPBindingActive {
		return storeError(ctx, OperationMCPBindingTouch, StoreErrorConflict, ErrMCPBindingUnavailable)
	}
	now = normalizeMCPTime(now)
	binding.LatestISCPSessionID, binding.LastUsedAt, binding.UpdatedAt = iscpSessionID, &now, now
	s.mcpBindings[id] = binding
	return nil
}

func (s *MemoryStore) CreateMCPOperation(ctx context.Context, operation app.MCPOperation) (app.MCPOperation, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationCreate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationCreate, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPOperationCreate, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	for _, existing := range s.mcpOperations {
		if existing.BindingID == operation.BindingID && existing.IdempotencyKey == operation.IdempotencyKey {
			if existing.Fingerprint != operation.Fingerprint {
				return app.MCPOperation{}, false, storeError(ctx, OperationMCPOperationCreate, StoreErrorConflict, ErrMCPOperationConflict)
			}
			return cloneMCPOperation(existing), false, nil
		}
	}
	operation = normalizeMCPOperation(operation, time.Now().UTC())
	if _, exists := s.mcpOperations[operation.ID]; exists {
		return app.MCPOperation{}, false, storeError(ctx, OperationMCPOperationCreate, StoreErrorConflict, ErrMCPOperationConflict)
	}
	s.mcpOperations[operation.ID] = cloneMCPOperation(operation)
	return cloneMCPOperation(operation), true, nil
}

func (s *MemoryStore) GetMCPOperation(ctx context.Context, id string) (app.MCPOperation, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationGet, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPOperationGet, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	operation, ok := s.mcpOperations[id]
	return cloneMCPOperation(operation), ok, nil
}

func (s *MemoryStore) FindMCPOperationByIdempotency(ctx context.Context, bindingID, idempotencyKey string) (app.MCPOperation, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationFindIdempotency, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationFindIdempotency, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPOperationFindIdempotency, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	for _, operation := range s.mcpOperations {
		if operation.BindingID == bindingID && operation.IdempotencyKey == idempotencyKey {
			return cloneMCPOperation(operation), true, nil
		}
	}
	return app.MCPOperation{}, false, nil
}

func (s *MemoryStore) ListMCPOperations(ctx context.Context, bindingID string) ([]app.MCPOperation, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMCPOperationList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.MCPOperation, 0)
	for _, operation := range s.mcpOperations {
		if bindingID == "" || operation.BindingID == bindingID {
			out = append(out, cloneMCPOperation(operation))
		}
	}
	slices.SortFunc(out, func(a, b app.MCPOperation) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) UpdateMCPOperation(ctx context.Context, operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationUpdate, ctx); err != nil {
		return app.MCPOperation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMCPOperationUpdate, ctx); err != nil {
		return app.MCPOperation{}, err
	}
	existing, ok := s.mcpOperations[operation.ID]
	if !ok {
		return app.MCPOperation{}, storeError(ctx, OperationMCPOperationUpdate, StoreErrorNotFound, errors.New("MCP operation not found"))
	}
	if existing.Version != expectedVersion {
		return app.MCPOperation{}, storeError(ctx, OperationMCPOperationUpdate, StoreErrorConflict, ErrMCPOperationVersionConflict)
	}
	if !mcpOperationIdentityEqual(existing, operation) {
		return app.MCPOperation{}, storeError(ctx, OperationMCPOperationUpdate, StoreErrorConflict, ErrMCPOperationConflict)
	}
	operation.Version = expectedVersion + 1
	operation.CreatedAt = existing.CreatedAt
	operation.UpdatedAt = normalizeMCPTime(time.Now())
	operation.CompletedAt = normalizeMCPTimePointer(operation.CompletedAt)
	s.mcpOperations[operation.ID] = cloneMCPOperation(operation)
	return cloneMCPOperation(operation), nil
}

func mcpOperationIdentityEqual(existing, candidate app.MCPOperation) bool {
	return existing.ID == candidate.ID && existing.SchemaVersion == candidate.SchemaVersion &&
		existing.BindingID == candidate.BindingID && existing.IdempotencyKey == candidate.IdempotencyKey &&
		existing.Fingerprint == candidate.Fingerprint && mcpRecordsEqual(existing.Invocation, candidate.Invocation)
}

func mcpOperationTerminal(state app.MCPOperationState) bool {
	return state == app.MCPOperationSucceeded || state == app.MCPOperationFailed || state == app.MCPOperationCancelled || state == app.MCPOperationRevoked
}
