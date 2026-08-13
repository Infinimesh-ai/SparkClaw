package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) SaveMCPAccessTicket(ticket app.MCPAccessTicket) (app.MCPAccessTicket, error) {
	ticket = normalizeMCPAccessTicket(ticket, time.Now().UTC())
	_, err := s.db.Exec(context.Background(), `
		INSERT INTO mcp_access_tickets (id, secret_hash, owner_id, domain_id, status, expires_at, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET secret_hash=EXCLUDED.secret_hash, owner_id=EXCLUDED.owner_id,
			domain_id=EXCLUDED.domain_id, status=EXCLUDED.status, expires_at=EXCLUDED.expires_at, payload=EXCLUDED.payload
	`, ticket.ID, ticket.SecretHash, ticket.OwnerID, ticket.DomainID, ticket.Status, ticket.ExpiresAt, mustJSON(ticket))
	return ticket, err
}

func (s *PostgresStore) GetMCPAccessTicket(id string) (app.MCPAccessTicket, bool) {
	var raw []byte
	err := s.db.QueryRow(context.Background(), `SELECT payload FROM mcp_access_tickets WHERE id=$1`, id).Scan(&raw)
	var ticket app.MCPAccessTicket
	return ticket, err == nil && json.Unmarshal(raw, &ticket) == nil
}

func (s *PostgresStore) FindMCPAccessTicketBySecretHash(secretHash string) (app.MCPAccessTicket, bool) {
	var raw []byte
	err := s.db.QueryRow(context.Background(), `SELECT payload FROM mcp_access_tickets WHERE secret_hash=$1`, secretHash).Scan(&raw)
	var ticket app.MCPAccessTicket
	return ticket, err == nil && json.Unmarshal(raw, &ticket) == nil
}

func (s *PostgresStore) ListMCPAccessTickets(ownerID string) []app.MCPAccessTicket {
	rows, err := s.db.Query(context.Background(), `
		SELECT payload FROM mcp_access_tickets WHERE ($1='' OR owner_id=$1) ORDER BY expires_at DESC
	`, ownerID)
	if err != nil {
		return []app.MCPAccessTicket{}
	}
	defer rows.Close()
	out := []app.MCPAccessTicket{}
	for rows.Next() {
		var raw []byte
		var ticket app.MCPAccessTicket
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &ticket) == nil {
			out = append(out, ticket)
		}
	}
	return out
}

func (s *PostgresStore) RedeemMCPAccessTicket(secretHash string, peer app.MCPPeerIdentity, now time.Time) (app.MCPBinding, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.MCPBinding{}, err
	}
	defer rollbackTx(ctx, tx)
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT payload FROM mcp_access_tickets WHERE secret_hash=$1 FOR UPDATE`, secretHash).Scan(&raw); err != nil {
		return app.MCPBinding{}, ErrMCPAccessTicketInvalid
	}
	var ticket app.MCPAccessTicket
	if json.Unmarshal(raw, &ticket) != nil || ticket.Status != app.MCPAccessPending || ticket.UseCount >= ticket.MaxUses ||
		ticket.DomainID != peer.DomainID || !now.Before(ticket.ExpiresAt) {
		return app.MCPBinding{}, ErrMCPAccessTicketInvalid
	}
	var existingID string
	if err := tx.QueryRow(ctx, `
		SELECT id FROM mcp_bindings WHERE domain_id=$1 AND requester_device_id=$2 AND requester_key_thumbprint=$3 AND status='active'
	`, peer.DomainID, peer.DeviceID, peer.KeyThumbprint).Scan(&existingID); err == nil {
		return app.MCPBinding{}, ErrMCPAccessTicketInvalid
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return app.MCPBinding{}, err
	}
	ticket.Status, ticket.UseCount, ticket.ConsumedAt = app.MCPAccessConsumed, ticket.UseCount+1, &now
	if _, err := tx.Exec(ctx, `UPDATE mcp_access_tickets SET status=$2,payload=$3 WHERE id=$1`, ticket.ID, ticket.Status, mustJSON(ticket)); err != nil {
		return app.MCPBinding{}, err
	}
	binding := normalizeMCPBinding(app.MCPBinding{
		ID: app.NewID("mcp_binding"), OwnerID: ticket.OwnerID, ActorID: ticket.ActorID, DomainID: ticket.DomainID,
		RequesterDeviceID: peer.DeviceID, RequesterKeyThumbprint: peer.KeyThumbprint,
		AuthorizationRevision: ticket.AuthorizationRevision, CatalogRevision: ticket.CatalogRevision,
		Grants: append([]app.MCPLeafGrant(nil), ticket.Grants...), LatestISCPSessionID: peer.ISCPSessionID,
	}, now)
	binding.LinkedSessionID = "s_" + binding.ID
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (id,owner_id,title,source,hidden,created_at,updated_at) VALUES ($1,$2,$3,'mcp',true,$4,$4)
	`, binding.LinkedSessionID, binding.OwnerID, "External MCP", now); err != nil {
		return app.MCPBinding{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO mcp_bindings (id,owner_id,domain_id,requester_device_id,requester_key_thumbprint,status,updated_at,payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, binding.ID, binding.OwnerID, binding.DomainID, binding.RequesterDeviceID, binding.RequesterKeyThumbprint, binding.Status, binding.UpdatedAt, mustJSON(binding)); err != nil {
		return app.MCPBinding{}, ErrMCPAccessTicketInvalid
	}
	if err := tx.Commit(ctx); err != nil {
		return app.MCPBinding{}, err
	}
	return binding, nil
}

func (s *PostgresStore) RevokeMCPAccessTicket(id string, now time.Time) (app.MCPAccessTicket, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	defer rollbackTx(ctx, tx)
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT payload FROM mcp_access_tickets WHERE id=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
		return app.MCPAccessTicket{}, ErrMCPAccessTicketInvalid
	}
	var ticket app.MCPAccessTicket
	if json.Unmarshal(raw, &ticket) != nil || ticket.Status != app.MCPAccessPending {
		return app.MCPAccessTicket{}, ErrMCPAccessTicketInvalid
	}
	ticket.Status, ticket.RevokedAt = app.MCPAccessRevoked, &now
	if _, err := tx.Exec(ctx, `UPDATE mcp_access_tickets SET status=$2,payload=$3 WHERE id=$1`, id, ticket.Status, mustJSON(ticket)); err != nil {
		return app.MCPAccessTicket{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	return ticket, nil
}

func (s *PostgresStore) GetMCPBinding(id string) (app.MCPBinding, bool) {
	var raw []byte
	err := s.db.QueryRow(context.Background(), `SELECT payload FROM mcp_bindings WHERE id=$1`, id).Scan(&raw)
	var binding app.MCPBinding
	return binding, err == nil && json.Unmarshal(raw, &binding) == nil
}

func (s *PostgresStore) FindMCPBindingForPeer(domainID, deviceID, thumbprint string) (app.MCPBinding, bool) {
	var raw []byte
	err := s.db.QueryRow(context.Background(), `
		SELECT payload FROM mcp_bindings WHERE domain_id=$1 AND requester_device_id=$2 AND requester_key_thumbprint=$3 AND status='active'
	`, domainID, deviceID, thumbprint).Scan(&raw)
	var binding app.MCPBinding
	return binding, err == nil && json.Unmarshal(raw, &binding) == nil
}

func (s *PostgresStore) ListMCPBindings(ownerID string) []app.MCPBinding {
	rows, err := s.db.Query(context.Background(), `SELECT payload FROM mcp_bindings WHERE ($1='' OR owner_id=$1) ORDER BY updated_at DESC`, ownerID)
	if err != nil {
		return []app.MCPBinding{}
	}
	defer rows.Close()
	out := []app.MCPBinding{}
	for rows.Next() {
		var raw []byte
		var binding app.MCPBinding
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &binding) == nil {
			out = append(out, binding)
		}
	}
	return out
}

func (s *PostgresStore) RevokeMCPBinding(id string, now time.Time) (app.MCPBinding, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.MCPBinding{}, err
	}
	defer rollbackTx(ctx, tx)
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT payload FROM mcp_bindings WHERE id=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
		return app.MCPBinding{}, ErrMCPBindingUnavailable
	}
	var binding app.MCPBinding
	if json.Unmarshal(raw, &binding) != nil {
		return app.MCPBinding{}, ErrMCPBindingUnavailable
	}
	if binding.Status != app.MCPBindingRevoked {
		binding.Status, binding.RevokedAt, binding.UpdatedAt = app.MCPBindingRevoked, &now, now
		if _, err := tx.Exec(ctx, `UPDATE mcp_bindings SET status=$2,updated_at=$3,payload=$4 WHERE id=$1`, id, binding.Status, now, mustJSON(binding)); err != nil {
			return app.MCPBinding{}, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT payload FROM mcp_operations WHERE binding_id=$1 FOR UPDATE`, id)
	if err != nil {
		return app.MCPBinding{}, err
	}
	operations := make([]app.MCPOperation, 0)
	for rows.Next() {
		var operationRaw []byte
		var operation app.MCPOperation
		if err := rows.Scan(&operationRaw); err != nil || json.Unmarshal(operationRaw, &operation) != nil {
			rows.Close()
			return app.MCPBinding{}, errors.New("MCP operation could not be decoded during binding revocation")
		}
		if !mcpOperationTerminal(operation.State) {
			operations = append(operations, operation)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return app.MCPBinding{}, err
	}
	rows.Close()
	for _, operation := range operations {
		operation.State = app.MCPOperationRevoked
		operation.ErrorCode = "binding_revoked"
		operation.ErrorMessage = "The MCP binding was revoked by the local owner"
		operation.CompletedAt = &now
		operation.Version++
		operation.UpdatedAt = now
		if _, err := tx.Exec(ctx, `UPDATE mcp_operations SET version=$2,updated_at=$3,payload=$4 WHERE id=$1`, operation.ID, operation.Version, now, mustJSON(operation)); err != nil {
			return app.MCPBinding{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return app.MCPBinding{}, err
	}
	return binding, nil
}

func (s *PostgresStore) TouchMCPBinding(id, iscpSessionID string, now time.Time) error {
	binding, ok := s.GetMCPBinding(id)
	if !ok || binding.Status != app.MCPBindingActive {
		return ErrMCPBindingUnavailable
	}
	binding.LatestISCPSessionID, binding.LastUsedAt, binding.UpdatedAt = iscpSessionID, &now, now
	command, err := s.db.Exec(context.Background(), `
		UPDATE mcp_bindings SET updated_at=$2,payload=$3 WHERE id=$1 AND status='active'
	`, id, now, mustJSON(binding))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrMCPBindingUnavailable
	}
	return nil
}

func (s *PostgresStore) CreateMCPOperation(operation app.MCPOperation) (app.MCPOperation, bool, error) {
	operation = normalizeMCPOperation(operation, time.Now().UTC())
	command, err := s.db.Exec(context.Background(), `
		INSERT INTO mcp_operations (id,binding_id,idempotency_key,fingerprint,version,updated_at,payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (binding_id,idempotency_key) DO NOTHING
	`, operation.ID, operation.BindingID, operation.IdempotencyKey, operation.Fingerprint, operation.Version, operation.UpdatedAt, mustJSON(operation))
	if err != nil {
		return app.MCPOperation{}, false, err
	}
	if command.RowsAffected() == 1 {
		return operation, true, nil
	}
	existing, ok := s.FindMCPOperationByIdempotency(operation.BindingID, operation.IdempotencyKey)
	if !ok {
		return app.MCPOperation{}, false, errors.New("MCP operation could not be read after conflict")
	}
	if existing.Fingerprint != operation.Fingerprint {
		return app.MCPOperation{}, false, ErrMCPOperationConflict
	}
	return existing, false, nil
}

func (s *PostgresStore) GetMCPOperation(id string) (app.MCPOperation, bool) {
	var raw []byte
	err := s.db.QueryRow(context.Background(), `SELECT payload FROM mcp_operations WHERE id=$1`, id).Scan(&raw)
	var operation app.MCPOperation
	return operation, err == nil && json.Unmarshal(raw, &operation) == nil
}

func (s *PostgresStore) FindMCPOperationByIdempotency(bindingID, idempotencyKey string) (app.MCPOperation, bool) {
	var raw []byte
	err := s.db.QueryRow(context.Background(), `SELECT payload FROM mcp_operations WHERE binding_id=$1 AND idempotency_key=$2`, bindingID, idempotencyKey).Scan(&raw)
	var operation app.MCPOperation
	return operation, err == nil && json.Unmarshal(raw, &operation) == nil
}

func (s *PostgresStore) ListMCPOperations(bindingID string) []app.MCPOperation {
	rows, err := s.db.Query(context.Background(), `SELECT payload FROM mcp_operations WHERE ($1='' OR binding_id=$1) ORDER BY updated_at DESC`, bindingID)
	if err != nil {
		return []app.MCPOperation{}
	}
	defer rows.Close()
	out := []app.MCPOperation{}
	for rows.Next() {
		var raw []byte
		var operation app.MCPOperation
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &operation) == nil {
			out = append(out, operation)
		}
	}
	return out
}

func (s *PostgresStore) UpdateMCPOperation(operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error) {
	existing, ok := s.GetMCPOperation(operation.ID)
	if !ok {
		return app.MCPOperation{}, errors.New("MCP operation not found")
	}
	operation.Version, operation.CreatedAt, operation.UpdatedAt = expectedVersion+1, existing.CreatedAt, time.Now().UTC()
	command, err := s.db.Exec(context.Background(), `
		UPDATE mcp_operations SET version=$2,updated_at=$3,payload=$4 WHERE id=$1 AND version=$5
	`, operation.ID, operation.Version, operation.UpdatedAt, mustJSON(operation), expectedVersion)
	if err != nil {
		return app.MCPOperation{}, err
	}
	if command.RowsAffected() != 1 {
		return app.MCPOperation{}, ErrMCPOperationVersionConflict
	}
	return operation, nil
}
