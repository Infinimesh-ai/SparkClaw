package store

import (
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
	}
	return ticket
}

func normalizeMCPBinding(binding app.MCPBinding, now time.Time) app.MCPBinding {
	if binding.ID == "" {
		binding.ID = app.NewID("mcp_binding")
	}
	if binding.Status == "" {
		binding.Status = app.MCPBindingActive
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	return binding
}

func normalizeMCPOperation(operation app.MCPOperation, now time.Time) app.MCPOperation {
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
	}
	operation.UpdatedAt = now
	return operation
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

func (s *MemoryStore) SaveMCPAccessTicket(ticket app.MCPAccessTicket) (app.MCPAccessTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket = normalizeMCPAccessTicket(ticket, time.Now().UTC())
	if ticket.SchemaVersion != app.MCPAccessTicketSchemaVersion || ticket.Scope != app.MCPAccessConversation {
		return app.MCPAccessTicket{}, ErrMCPAccessTicketInvalid
	}
	s.mcpAccessTickets[ticket.ID] = cloneMCPAccessTicket(ticket)
	return cloneMCPAccessTicket(ticket), nil
}

func (s *MemoryStore) GetMCPAccessTicket(id string) (app.MCPAccessTicket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ticket, ok := s.mcpAccessTickets[id]
	return cloneMCPAccessTicket(ticket), ok
}

func (s *MemoryStore) FindMCPAccessTicketBySecretHash(secretHash string) (app.MCPAccessTicket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ticket := range s.mcpAccessTickets {
		if ticket.SecretHash == secretHash {
			return cloneMCPAccessTicket(ticket), true
		}
	}
	return app.MCPAccessTicket{}, false
}

func (s *MemoryStore) ListMCPAccessTickets(ownerID string) []app.MCPAccessTicket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.MCPAccessTicket, 0)
	for _, ticket := range s.mcpAccessTickets {
		if ownerID == "" || ticket.OwnerID == ownerID {
			out = append(out, cloneMCPAccessTicket(ticket))
		}
	}
	slices.SortFunc(out, func(a, b app.MCPAccessTicket) int { return b.IssuedAt.Compare(a.IssuedAt) })
	return out
}

func (s *MemoryStore) RedeemMCPAccessTicket(secretHash string, peer app.MCPPeerIdentity, now time.Time) (app.MCPBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
				return app.MCPBinding{}, ErrMCPAccessTicketInvalid
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
		s.sessions[binding.LinkedSessionID] = app.Session{
			ID: binding.LinkedSessionID, OwnerID: binding.OwnerID, Title: mcpSessionTitle(binding.RequesterDeviceID), Source: "mcp", Hidden: false,
			CreatedAt: now, UpdatedAt: now,
		}
		s.mcpBindings[binding.ID] = cloneMCPBinding(binding)
		return cloneMCPBinding(binding), nil
	}
	return app.MCPBinding{}, ErrMCPAccessTicketInvalid
}

func (s *MemoryStore) RevokeMCPAccessTicket(id string, now time.Time) (app.MCPAccessTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.mcpAccessTickets[id]
	if !ok || ticket.Status != app.MCPAccessPending {
		return app.MCPAccessTicket{}, ErrMCPAccessTicketInvalid
	}
	ticket.Status, ticket.RevokedAt = app.MCPAccessRevoked, &now
	s.mcpAccessTickets[id] = ticket
	return cloneMCPAccessTicket(ticket), nil
}

func (s *MemoryStore) DeleteMCPAccessTicket(ownerID, id string) (app.MCPAccessTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.mcpAccessTickets[id]
	if !ok || ticket.OwnerID != ownerID {
		return app.MCPAccessTicket{}, ErrMCPAccessTicketInvalid
	}
	delete(s.mcpAccessTickets, id)
	return cloneMCPAccessTicket(ticket), nil
}

func (s *MemoryStore) GetMCPBinding(id string) (app.MCPBinding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.mcpBindings[id]
	return cloneMCPBinding(binding), ok
}

func (s *MemoryStore) FindMCPBindingForPeer(domainID, deviceID, thumbprint string) (app.MCPBinding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.mcpBindings {
		if binding.Status == app.MCPBindingActive && binding.DomainID == domainID && binding.RequesterDeviceID == deviceID && binding.RequesterKeyThumbprint == thumbprint {
			return cloneMCPBinding(binding), true
		}
	}
	return app.MCPBinding{}, false
}

func (s *MemoryStore) ListMCPBindings(ownerID string) []app.MCPBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.MCPBinding, 0)
	for _, binding := range s.mcpBindings {
		if ownerID == "" || binding.OwnerID == ownerID {
			out = append(out, cloneMCPBinding(binding))
		}
	}
	slices.SortFunc(out, func(a, b app.MCPBinding) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out
}

func (s *MemoryStore) RevokeMCPBinding(id string, now time.Time) (app.MCPBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.mcpBindings[id]
	if !ok {
		return app.MCPBinding{}, ErrMCPBindingUnavailable
	}
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

func (s *MemoryStore) DeleteMCPBinding(ownerID, id string) (app.MCPBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.mcpBindings[id]
	if !ok || binding.OwnerID != ownerID {
		return app.MCPBinding{}, ErrMCPBindingUnavailable
	}
	delete(s.mcpBindings, id)
	for operationID, operation := range s.mcpOperations {
		if operation.BindingID == id {
			delete(s.mcpOperations, operationID)
		}
	}
	return cloneMCPBinding(binding), nil
}

func (s *MemoryStore) DeleteMCPAccessRecords(ownerID string) (MCPAccessRecordDeletion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *MemoryStore) TouchMCPBinding(id, iscpSessionID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.mcpBindings[id]
	if !ok || binding.Status != app.MCPBindingActive {
		return ErrMCPBindingUnavailable
	}
	binding.LatestISCPSessionID, binding.LastUsedAt, binding.UpdatedAt = iscpSessionID, &now, now
	s.mcpBindings[id] = binding
	return nil
}

func (s *MemoryStore) CreateMCPOperation(operation app.MCPOperation) (app.MCPOperation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.mcpOperations {
		if existing.BindingID == operation.BindingID && existing.IdempotencyKey == operation.IdempotencyKey {
			if existing.Fingerprint != operation.Fingerprint {
				return app.MCPOperation{}, false, ErrMCPOperationConflict
			}
			return cloneMCPOperation(existing), false, nil
		}
	}
	operation = normalizeMCPOperation(operation, time.Now().UTC())
	s.mcpOperations[operation.ID] = cloneMCPOperation(operation)
	return cloneMCPOperation(operation), true, nil
}

func (s *MemoryStore) GetMCPOperation(id string) (app.MCPOperation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operation, ok := s.mcpOperations[id]
	return cloneMCPOperation(operation), ok
}

func (s *MemoryStore) FindMCPOperationByIdempotency(bindingID, idempotencyKey string) (app.MCPOperation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, operation := range s.mcpOperations {
		if operation.BindingID == bindingID && operation.IdempotencyKey == idempotencyKey {
			return cloneMCPOperation(operation), true
		}
	}
	return app.MCPOperation{}, false
}

func (s *MemoryStore) ListMCPOperations(bindingID string) []app.MCPOperation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.MCPOperation, 0)
	for _, operation := range s.mcpOperations {
		if bindingID == "" || operation.BindingID == bindingID {
			out = append(out, cloneMCPOperation(operation))
		}
	}
	slices.SortFunc(out, func(a, b app.MCPOperation) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out
}

func (s *MemoryStore) UpdateMCPOperation(operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.mcpOperations[operation.ID]
	if !ok {
		return app.MCPOperation{}, errors.New("MCP operation not found")
	}
	if existing.Version != expectedVersion {
		return app.MCPOperation{}, ErrMCPOperationVersionConflict
	}
	operation.Version = expectedVersion + 1
	operation.CreatedAt = existing.CreatedAt
	operation.UpdatedAt = time.Now().UTC()
	s.mcpOperations[operation.ID] = cloneMCPOperation(operation)
	return cloneMCPOperation(operation), nil
}

func mcpOperationTerminal(state app.MCPOperationState) bool {
	return state == app.MCPOperationSucceeded || state == app.MCPOperationFailed || state == app.MCPOperationCancelled || state == app.MCPOperationRevoked
}
