package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) SaveMCPAccessTicket(ctx context.Context, ticket app.MCPAccessTicket) (app.MCPAccessTicket, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketSave, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	ticket = normalizeMCPAccessTicket(ticket, time.Now())
	if ticket.SchemaVersion != app.MCPAccessTicketSchemaVersion || ticket.Scope != app.MCPAccessConversation {
		return app.MCPAccessTicket{}, storeError(ctx, OperationMCPAccessTicketSave, StoreErrorInvalid, ErrMCPAccessTicketInvalid)
	}
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPAccessTicketSave)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction,
		mcpAdvisoryKey("owner", ticket.OwnerID),
		mcpAdvisoryKey("ticket-id", ticket.ID),
		mcpAdvisoryKey("ticket-secret", ticket.SecretHash),
	); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketSave, app.MCPAccessTicket{}, false, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO mcp_access_tickets (id, secret_hash, owner_id, domain_id, status, expires_at, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET secret_hash=EXCLUDED.secret_hash, owner_id=EXCLUDED.owner_id,
			domain_id=EXCLUDED.domain_id, status=EXCLUDED.status, expires_at=EXCLUDED.expires_at, payload=EXCLUDED.payload
	`, ticket.ID, ticket.SecretHash, ticket.OwnerID, ticket.DomainID, ticket.Status, ticket.ExpiresAt, mustJSON(ticket)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketSave, ticket, true, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	return commitMCPPostgresWrite(ctx, OperationMCPAccessTicketSave, ticket, session, transaction)
}

func (s *PostgresStore) GetMCPAccessTicket(ctx context.Context, id string) (app.MCPAccessTicket, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketGet, ctx); err != nil {
		return app.MCPAccessTicket{}, false, err
	}
	return runMCPPostgresRead(ctx, s, OperationMCPAccessTicketGet, []int64{mcpAdvisoryKey("ticket-id", id)}, func(transaction onboardingPostgresTx) (app.MCPAccessTicket, bool, error) {
		var raw []byte
		err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_access_tickets WHERE id=$1`, id).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.MCPAccessTicket{}, false, nil
		}
		if err != nil {
			return app.MCPAccessTicket{}, false, err
		}
		ticket, err := decodeMCPAccessTicket(raw, id)
		if err != nil {
			return app.MCPAccessTicket{}, false, storeError(ctx, OperationMCPAccessTicketGet, StoreErrorCorrupt, err)
		}
		return ticket, true, nil
	})
}

func (s *PostgresStore) FindMCPAccessTicketBySecretHash(ctx context.Context, secretHash string) (app.MCPAccessTicket, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketFindHash, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketFindHash, ctx); err != nil {
		return app.MCPAccessTicket{}, false, err
	}
	return runMCPPostgresRead(ctx, s, OperationMCPAccessTicketFindHash, []int64{mcpAdvisoryKey("ticket-secret", secretHash)}, func(transaction onboardingPostgresTx) (app.MCPAccessTicket, bool, error) {
		var raw []byte
		err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_access_tickets WHERE secret_hash=$1`, secretHash).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.MCPAccessTicket{}, false, nil
		}
		if err != nil {
			return app.MCPAccessTicket{}, false, err
		}
		ticket, err := decodeMCPAccessTicket(raw, "")
		if err != nil || ticket.SecretHash != secretHash {
			if err == nil {
				err = fmt.Errorf("ticket secret hash does not match lookup")
			}
			return app.MCPAccessTicket{}, false, storeError(ctx, OperationMCPAccessTicketFindHash, StoreErrorCorrupt, err)
		}
		return ticket, true, nil
	})
}

func (s *PostgresStore) ListMCPAccessTickets(ctx context.Context, ownerID string) ([]app.MCPAccessTicket, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketList, ctx); err != nil {
		return nil, err
	}
	keys := []int64(nil)
	if ownerID != "" {
		keys = append(keys, mcpAdvisoryKey("owner", ownerID))
	}
	out, _, err := runMCPPostgresRead(ctx, s, OperationMCPAccessTicketList, keys, func(transaction onboardingPostgresTx) ([]app.MCPAccessTicket, bool, error) {
		rows, err := transaction.Query(ctx, `
			SELECT id,payload FROM mcp_access_tickets WHERE ($1='' OR owner_id=$1) ORDER BY expires_at DESC,id ASC
		`, ownerID)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		out := make([]app.MCPAccessTicket, 0)
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				return nil, false, err
			}
			ticket, err := decodeMCPAccessTicket(raw, id)
			if err != nil || (ownerID != "" && ticket.OwnerID != ownerID) {
				if err == nil {
					err = fmt.Errorf("ticket owner does not match list scope")
				}
				return nil, false, storeError(ctx, OperationMCPAccessTicketList, StoreErrorCorrupt, err)
			}
			out = append(out, ticket)
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return out, true, nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(left, right app.MCPAccessTicket) int {
		if order := right.IssuedAt.Compare(left.IssuedAt); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
	return out, nil
}

func (s *PostgresStore) RedeemMCPAccessTicket(ctx context.Context, secretHash string, peer app.MCPPeerIdentity, now time.Time) (app.MCPBinding, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketRedeem, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketRedeem, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	now = normalizeMCPTime(now)
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPAccessTicketRedeem)
	if err != nil {
		return app.MCPBinding{}, err
	}
	defer release()
	peerKey := peer.DomainID + "\x00" + peer.DeviceID + "\x00" + peer.KeyThumbprint
	if err := acquireMCPPostgresBarriers(ctx, transaction,
		mcpAdvisoryKey("ticket-secret", secretHash),
		mcpAdvisoryKey("peer", peerKey),
	); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRedeem, app.MCPBinding{}, false, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	var raw []byte
	if err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_access_tickets WHERE secret_hash=$1 FOR UPDATE`, secretHash).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPAccessTicketRedeem, StoreErrorInvalid, session, transaction, ErrMCPAccessTicketInvalid)
		}
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRedeem, app.MCPBinding{}, false, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	ticket, err := decodeMCPAccessTicket(raw, "")
	if err != nil {
		return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPAccessTicketRedeem, StoreErrorCorrupt, session, transaction, err)
	}
	if ticket.SecretHash != secretHash || ticket.Status != app.MCPAccessPending || ticket.UseCount >= ticket.MaxUses ||
		ticket.DomainID != peer.DomainID || !now.Before(ticket.ExpiresAt) ||
		ticket.SchemaVersion != app.MCPAccessTicketSchemaVersion || ticket.Scope != app.MCPAccessConversation {
		return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPAccessTicketRedeem, StoreErrorInvalid, session, transaction, ErrMCPAccessTicketInvalid)
	}
	var existingID string
	if err := transaction.QueryRow(ctx, `
		SELECT id FROM mcp_bindings WHERE domain_id=$1 AND requester_device_id=$2 AND requester_key_thumbprint=$3 AND status='active'
	`, peer.DomainID, peer.DeviceID, peer.KeyThumbprint).Scan(&existingID); err == nil {
		return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPAccessTicketRedeem, StoreErrorConflict, session, transaction, ErrMCPAccessTicketInvalid)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRedeem, app.MCPBinding{}, false, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	ticket.Status, ticket.UseCount, ticket.ConsumedAt = app.MCPAccessConsumed, ticket.UseCount+1, &now
	binding := normalizeMCPBinding(app.MCPBinding{
		SchemaVersion: app.MCPBindingSchemaVersion, ID: app.NewID("mcp_binding"), OwnerID: ticket.OwnerID, ActorID: ticket.ActorID, DomainID: ticket.DomainID,
		RequesterDeviceID: peer.DeviceID, RequesterKeyThumbprint: peer.KeyThumbprint,
		AuthorizationRevision: ticket.AuthorizationRevision, Scope: ticket.Scope, LatestISCPSessionID: peer.ISCPSessionID,
	}, now)
	binding.LinkedSessionID = "s_" + binding.ID
	if _, err := transaction.Exec(ctx, `UPDATE mcp_access_tickets SET status=$2,payload=$3 WHERE id=$1`, ticket.ID, ticket.Status, mustJSON(ticket)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRedeem, binding, true, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	sessionTime := normalizeSessionTime(now)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO sessions (id,owner_id,title,source,hidden,created_at,updated_at) VALUES ($1,$2,$3,'mcp',false,$4,$4)
	`, binding.LinkedSessionID, binding.OwnerID, mcpSessionTitle(binding.RequesterDeviceID), sessionTime); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRedeem, binding, true, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO mcp_bindings (id,owner_id,domain_id,requester_device_id,requester_key_thumbprint,status,updated_at,payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, binding.ID, binding.OwnerID, binding.DomainID, binding.RequesterDeviceID, binding.RequesterKeyThumbprint, binding.Status, binding.UpdatedAt, mustJSON(binding)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRedeem, binding, true, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	return commitMCPPostgresWrite(ctx, OperationMCPAccessTicketRedeem, binding, session, transaction)
}

func (s *PostgresStore) RevokeMCPAccessTicket(ctx context.Context, id string, now time.Time) (app.MCPAccessTicket, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketRevoke, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	now = normalizeMCPTime(now)
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPAccessTicketRevoke)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction, mcpAdvisoryKey("ticket-id", id)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRevoke, app.MCPAccessTicket{}, false, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	var raw []byte
	if err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_access_tickets WHERE id=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mcpPostgresBusinessError[app.MCPAccessTicket](ctx, OperationMCPAccessTicketRevoke, StoreErrorConflict, session, transaction, ErrMCPAccessTicketInvalid)
		}
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRevoke, app.MCPAccessTicket{}, false, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	ticket, err := decodeMCPAccessTicket(raw, id)
	if err != nil {
		return mcpPostgresBusinessError[app.MCPAccessTicket](ctx, OperationMCPAccessTicketRevoke, StoreErrorCorrupt, session, transaction, err)
	}
	if ticket.Status != app.MCPAccessPending {
		return mcpPostgresBusinessError[app.MCPAccessTicket](ctx, OperationMCPAccessTicketRevoke, StoreErrorConflict, session, transaction, ErrMCPAccessTicketInvalid)
	}
	ticket.Status, ticket.RevokedAt = app.MCPAccessRevoked, &now
	if _, err := transaction.Exec(ctx, `UPDATE mcp_access_tickets SET status=$2,payload=$3 WHERE id=$1`, id, ticket.Status, mustJSON(ticket)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketRevoke, ticket, true, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	return commitMCPPostgresWrite(ctx, OperationMCPAccessTicketRevoke, ticket, session, transaction)
}

func (s *PostgresStore) DeleteMCPAccessTicket(ctx context.Context, ownerID, id string) (app.MCPAccessTicket, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessTicketDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessTicketDelete, ctx); err != nil {
		return app.MCPAccessTicket{}, err
	}
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPAccessTicketDelete)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction, mcpAdvisoryKey("ticket-id", id)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketDelete, app.MCPAccessTicket{}, false, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	var raw []byte
	if err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_access_tickets WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, ownerID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mcpPostgresBusinessError[app.MCPAccessTicket](ctx, OperationMCPAccessTicketDelete, StoreErrorNotFound, session, transaction, ErrMCPAccessTicketInvalid)
		}
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketDelete, app.MCPAccessTicket{}, false, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	ticket, err := decodeMCPAccessTicket(raw, id)
	if err != nil {
		return mcpPostgresBusinessError[app.MCPAccessTicket](ctx, OperationMCPAccessTicketDelete, StoreErrorCorrupt, session, transaction, err)
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM mcp_access_tickets WHERE id=$1 AND owner_id=$2`, id, ownerID); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessTicketDelete, ticket, true, session, transaction, err, ErrMCPAccessTicketInvalid)
	}
	return commitMCPPostgresWrite(ctx, OperationMCPAccessTicketDelete, ticket, session, transaction)
}

func (s *PostgresStore) GetMCPBinding(ctx context.Context, id string) (app.MCPBinding, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingGet, ctx); err != nil {
		return app.MCPBinding{}, false, err
	}
	return runMCPPostgresRead(ctx, s, OperationMCPBindingGet, []int64{mcpAdvisoryKey("binding", id)}, func(transaction onboardingPostgresTx) (app.MCPBinding, bool, error) {
		var raw []byte
		err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_bindings WHERE id=$1`, id).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.MCPBinding{}, false, nil
		}
		if err != nil {
			return app.MCPBinding{}, false, err
		}
		binding, err := decodeMCPBinding(raw, id)
		if err != nil {
			return app.MCPBinding{}, false, storeError(ctx, OperationMCPBindingGet, StoreErrorCorrupt, err)
		}
		return binding, true, nil
	})
}

func (s *PostgresStore) FindMCPBindingForPeer(ctx context.Context, domainID, deviceID, thumbprint string) (app.MCPBinding, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingFindPeer, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingFindPeer, ctx); err != nil {
		return app.MCPBinding{}, false, err
	}
	peerKey := domainID + "\x00" + deviceID + "\x00" + thumbprint
	return runMCPPostgresRead(ctx, s, OperationMCPBindingFindPeer, []int64{mcpAdvisoryKey("peer", peerKey)}, func(transaction onboardingPostgresTx) (app.MCPBinding, bool, error) {
		var raw []byte
		err := transaction.QueryRow(ctx, `
			SELECT payload FROM mcp_bindings WHERE domain_id=$1 AND requester_device_id=$2 AND requester_key_thumbprint=$3 AND status='active'
		`, domainID, deviceID, thumbprint).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.MCPBinding{}, false, nil
		}
		if err != nil {
			return app.MCPBinding{}, false, err
		}
		binding, err := decodeMCPBinding(raw, "")
		if err != nil || binding.DomainID != domainID || binding.RequesterDeviceID != deviceID || binding.RequesterKeyThumbprint != thumbprint || binding.Status != app.MCPBindingActive {
			if err == nil {
				err = fmt.Errorf("binding payload does not match peer lookup")
			}
			return app.MCPBinding{}, false, storeError(ctx, OperationMCPBindingFindPeer, StoreErrorCorrupt, err)
		}
		return binding, true, nil
	})
}

func (s *PostgresStore) ListMCPBindings(ctx context.Context, ownerID string) ([]app.MCPBinding, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingList, ctx); err != nil {
		return nil, err
	}
	keys := []int64(nil)
	if ownerID != "" {
		keys = append(keys, mcpAdvisoryKey("owner", ownerID))
	}
	out, _, err := runMCPPostgresRead(ctx, s, OperationMCPBindingList, keys, func(transaction onboardingPostgresTx) ([]app.MCPBinding, bool, error) {
		rows, err := transaction.Query(ctx, `SELECT id,payload FROM mcp_bindings WHERE ($1='' OR owner_id=$1) ORDER BY updated_at DESC,id ASC`, ownerID)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		out := make([]app.MCPBinding, 0)
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				return nil, false, err
			}
			binding, err := decodeMCPBinding(raw, id)
			if err != nil || (ownerID != "" && binding.OwnerID != ownerID) {
				if err == nil {
					err = fmt.Errorf("binding owner does not match list scope")
				}
				return nil, false, storeError(ctx, OperationMCPBindingList, StoreErrorCorrupt, err)
			}
			out = append(out, binding)
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return out, true, nil
	})
	return out, err
}

func (s *PostgresStore) RevokeMCPBinding(ctx context.Context, id string, now time.Time) (app.MCPBinding, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingRevoke, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	now = normalizeMCPTime(now)
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPBindingRevoke)
	if err != nil {
		return app.MCPBinding{}, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction, mcpAdvisoryKey("binding", id)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPBindingRevoke, app.MCPBinding{}, false, session, transaction, err, ErrMCPBindingUnavailable)
	}
	var raw []byte
	if err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_bindings WHERE id=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPBindingRevoke, StoreErrorNotFound, session, transaction, ErrMCPBindingUnavailable)
		}
		return finishMCPPostgresWrite(ctx, OperationMCPBindingRevoke, app.MCPBinding{}, false, session, transaction, err, ErrMCPBindingUnavailable)
	}
	binding, err := decodeMCPBinding(raw, id)
	if err != nil {
		return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPBindingRevoke, StoreErrorCorrupt, session, transaction, err)
	}
	if binding.Status != app.MCPBindingRevoked {
		binding.Status, binding.RevokedAt, binding.UpdatedAt = app.MCPBindingRevoked, &now, now
		if _, err := transaction.Exec(ctx, `UPDATE mcp_bindings SET status=$2,updated_at=$3,payload=$4 WHERE id=$1`, id, binding.Status, now, mustJSON(binding)); err != nil {
			return finishMCPPostgresWrite(ctx, OperationMCPBindingRevoke, binding, true, session, transaction, err, ErrMCPBindingUnavailable)
		}
	}
	rows, err := transaction.Query(ctx, `SELECT id,payload FROM mcp_operations WHERE binding_id=$1 FOR UPDATE`, id)
	if err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPBindingRevoke, binding, true, session, transaction, err, ErrMCPBindingUnavailable)
	}
	operations := make([]app.MCPOperation, 0)
	for rows.Next() {
		var operationID string
		var operationRaw []byte
		if err := rows.Scan(&operationID, &operationRaw); err != nil {
			rows.Close()
			return finishMCPPostgresWrite(ctx, OperationMCPBindingRevoke, binding, true, session, transaction, err, ErrMCPBindingUnavailable)
		}
		operation, err := decodeMCPOperation(operationRaw, operationID)
		if err != nil {
			rows.Close()
			return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPBindingRevoke, StoreErrorCorrupt, session, transaction, err)
		}
		if !mcpOperationTerminal(operation.State) {
			operations = append(operations, operation)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return finishMCPPostgresWrite(ctx, OperationMCPBindingRevoke, binding, true, session, transaction, err, ErrMCPBindingUnavailable)
	}
	rows.Close()
	for _, operation := range operations {
		operation.State = app.MCPOperationRevoked
		operation.ErrorCode = "binding_revoked"
		operation.ErrorMessage = "The MCP binding was revoked by the local owner"
		operation.CompletedAt = &now
		operation.Version++
		operation.UpdatedAt = now
		if _, err := transaction.Exec(ctx, `UPDATE mcp_operations SET version=$2,updated_at=$3,payload=$4 WHERE id=$1`, operation.ID, operation.Version, now, mustJSON(operation)); err != nil {
			return finishMCPPostgresWrite(ctx, OperationMCPBindingRevoke, binding, true, session, transaction, err, ErrMCPBindingUnavailable)
		}
	}
	return commitMCPPostgresWrite(ctx, OperationMCPBindingRevoke, binding, session, transaction)
}

func (s *PostgresStore) DeleteMCPBinding(ctx context.Context, ownerID, id string) (app.MCPBinding, error) {
	ctx, cancel := operationContext(ctx, OperationMCPBindingDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingDelete, ctx); err != nil {
		return app.MCPBinding{}, err
	}
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPBindingDelete)
	if err != nil {
		return app.MCPBinding{}, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction, mcpAdvisoryKey("binding", id)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPBindingDelete, app.MCPBinding{}, false, session, transaction, err, ErrMCPBindingUnavailable)
	}
	var raw []byte
	if err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_bindings WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, ownerID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPBindingDelete, StoreErrorNotFound, session, transaction, ErrMCPBindingUnavailable)
		}
		return finishMCPPostgresWrite(ctx, OperationMCPBindingDelete, app.MCPBinding{}, false, session, transaction, err, ErrMCPBindingUnavailable)
	}
	binding, err := decodeMCPBinding(raw, id)
	if err != nil {
		return mcpPostgresBusinessError[app.MCPBinding](ctx, OperationMCPBindingDelete, StoreErrorCorrupt, session, transaction, err)
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM mcp_operations WHERE binding_id=$1`, id); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPBindingDelete, binding, true, session, transaction, err, ErrMCPBindingUnavailable)
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM mcp_bindings WHERE id=$1 AND owner_id=$2`, id, ownerID); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPBindingDelete, binding, true, session, transaction, err, ErrMCPBindingUnavailable)
	}
	return commitMCPPostgresWrite(ctx, OperationMCPBindingDelete, binding, session, transaction)
}

func (s *PostgresStore) DeleteMCPAccessRecords(ctx context.Context, ownerID string) (MCPAccessRecordDeletion, error) {
	ctx, cancel := operationContext(ctx, OperationMCPAccessRecordsDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPAccessRecordsDelete, ctx); err != nil {
		return MCPAccessRecordDeletion{}, err
	}
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete)
	if err != nil {
		return MCPAccessRecordDeletion{}, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction, mcpAdvisoryKey("owner", ownerID)); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete, MCPAccessRecordDeletion{}, false, session, transaction, err, nil)
	}
	lockedCounts := [2]int{}
	for index, query := range []string{
		`SELECT id FROM mcp_access_tickets WHERE owner_id=$1 FOR UPDATE`,
		`SELECT id FROM mcp_bindings WHERE owner_id=$1 FOR UPDATE`,
	} {
		rows, err := transaction.Query(ctx, query, ownerID)
		if err != nil {
			return finishMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete, MCPAccessRecordDeletion{}, false, session, transaction, err, nil)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return finishMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete, MCPAccessRecordDeletion{}, false, session, transaction, err, nil)
			}
			lockedCounts[index]++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return finishMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete, MCPAccessRecordDeletion{}, false, session, transaction, err, nil)
		}
		rows.Close()
	}
	expected := MCPAccessRecordDeletion{DeletedTickets: lockedCounts[0], DeletedBindings: lockedCounts[1]}
	if _, err := transaction.Exec(ctx, `DELETE FROM mcp_operations operation USING mcp_bindings binding WHERE operation.binding_id=binding.id AND binding.owner_id=$1`, ownerID); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete, expected, true, session, transaction, err, nil)
	}
	bindings, err := transaction.Exec(ctx, `DELETE FROM mcp_bindings WHERE owner_id=$1`, ownerID)
	if err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete, expected, true, session, transaction, err, nil)
	}
	tickets, err := transaction.Exec(ctx, `DELETE FROM mcp_access_tickets WHERE owner_id=$1`, ownerID)
	if err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete, expected, true, session, transaction, err, nil)
	}
	deleted := MCPAccessRecordDeletion{DeletedTickets: int(tickets.RowsAffected()), DeletedBindings: int(bindings.RowsAffected())}
	if deleted != expected {
		return mcpPostgresBusinessError[MCPAccessRecordDeletion](ctx, OperationMCPAccessRecordsDelete, StoreErrorConflict, session, transaction, errors.New("MCP access record set changed while deleting"))
	}
	return commitMCPPostgresWrite(ctx, OperationMCPAccessRecordsDelete, expected, session, transaction)
}

func (s *PostgresStore) TouchMCPBinding(ctx context.Context, id, iscpSessionID string, now time.Time) error {
	ctx, cancel := operationContext(ctx, OperationMCPBindingTouch, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPBindingTouch, ctx); err != nil {
		return err
	}
	now = normalizeMCPTime(now)
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPBindingTouch)
	if err != nil {
		return err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction, mcpAdvisoryKey("binding", id)); err != nil {
		_, err = finishMCPPostgresWrite(ctx, OperationMCPBindingTouch, struct{}{}, false, session, transaction, err, ErrMCPBindingUnavailable)
		return err
	}
	var raw []byte
	if err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_bindings WHERE id=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mcpPostgresBusinessErrorOnly(ctx, OperationMCPBindingTouch, StoreErrorConflict, session, transaction, ErrMCPBindingUnavailable)
		}
		_, err = finishMCPPostgresWrite(ctx, OperationMCPBindingTouch, struct{}{}, false, session, transaction, err, ErrMCPBindingUnavailable)
		return err
	}
	binding, err := decodeMCPBinding(raw, id)
	if err != nil {
		return mcpPostgresBusinessErrorOnly(ctx, OperationMCPBindingTouch, StoreErrorCorrupt, session, transaction, err)
	}
	if binding.Status != app.MCPBindingActive {
		return mcpPostgresBusinessErrorOnly(ctx, OperationMCPBindingTouch, StoreErrorConflict, session, transaction, ErrMCPBindingUnavailable)
	}
	binding.LatestISCPSessionID, binding.LastUsedAt, binding.UpdatedAt = iscpSessionID, &now, now
	if _, err := transaction.Exec(ctx, `UPDATE mcp_bindings SET updated_at=$2,payload=$3 WHERE id=$1 AND status='active'`, id, now, mustJSON(binding)); err != nil {
		_, err = finishMCPPostgresWrite(ctx, OperationMCPBindingTouch, struct{}{}, true, session, transaction, err, ErrMCPBindingUnavailable)
		return err
	}
	_, err = commitMCPPostgresWrite(ctx, OperationMCPBindingTouch, struct{}{}, session, transaction)
	return err
}

func (s *PostgresStore) CreateMCPOperation(ctx context.Context, operation app.MCPOperation) (app.MCPOperation, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationCreate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationCreate, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	operation = normalizeMCPOperation(operation, time.Now())
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPOperationCreate)
	if err != nil {
		return app.MCPOperation{}, false, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction,
		mcpAdvisoryKey("binding", operation.BindingID),
		mcpAdvisoryKey("operation", operation.ID),
		mcpAdvisoryKey("operation-idempotency", operation.BindingID+"\x00"+operation.IdempotencyKey),
	); err != nil {
		candidate, writeErr := finishMCPPostgresWrite(ctx, OperationMCPOperationCreate, app.MCPOperation{}, false, session, transaction, err, ErrMCPOperationConflict)
		return candidate, false, writeErr
	}
	command, err := transaction.Exec(ctx, `
		INSERT INTO mcp_operations (id,binding_id,idempotency_key,fingerprint,version,updated_at,payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (binding_id,idempotency_key) DO NOTHING
	`, operation.ID, operation.BindingID, operation.IdempotencyKey, operation.Fingerprint, operation.Version, operation.UpdatedAt, mustJSON(operation))
	if err != nil {
		candidate, writeErr := finishMCPPostgresWrite(ctx, OperationMCPOperationCreate, operation, true, session, transaction, err, ErrMCPOperationConflict)
		return candidate, false, writeErr
	}
	if command.RowsAffected() == 1 {
		candidate, err := commitMCPPostgresWrite(ctx, OperationMCPOperationCreate, operation, session, transaction)
		return candidate, true, err
	}
	var raw []byte
	err = transaction.QueryRow(ctx, `SELECT payload FROM mcp_operations WHERE binding_id=$1 AND idempotency_key=$2 FOR UPDATE`, operation.BindingID, operation.IdempotencyKey).Scan(&raw)
	if err != nil {
		candidate, readErr := finishMCPPostgresWrite(ctx, OperationMCPOperationCreate, app.MCPOperation{}, false, session, transaction, err, ErrMCPOperationConflict)
		return candidate, false, readErr
	}
	existing, err := decodeMCPOperation(raw, "")
	if err != nil {
		return app.MCPOperation{}, false, mcpPostgresBusinessErrorOnly(ctx, OperationMCPOperationCreate, StoreErrorCorrupt, session, transaction, err)
	}
	if existing.BindingID != operation.BindingID || existing.IdempotencyKey != operation.IdempotencyKey {
		return app.MCPOperation{}, false, mcpPostgresBusinessErrorOnly(ctx, OperationMCPOperationCreate, StoreErrorCorrupt, session, transaction, errors.New("MCP operation payload does not match idempotency lookup"))
	}
	if existing.Fingerprint != operation.Fingerprint {
		return app.MCPOperation{}, false, mcpPostgresBusinessErrorOnly(ctx, OperationMCPOperationCreate, StoreErrorConflict, session, transaction, ErrMCPOperationConflict)
	}
	existing, err = commitMCPPostgresWrite(ctx, OperationMCPOperationCreate, existing, session, transaction)
	return existing, false, err
}

func (s *PostgresStore) GetMCPOperation(ctx context.Context, id string) (app.MCPOperation, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationGet, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	return runMCPPostgresRead(ctx, s, OperationMCPOperationGet, []int64{mcpAdvisoryKey("operation", id)}, func(transaction onboardingPostgresTx) (app.MCPOperation, bool, error) {
		var raw []byte
		err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_operations WHERE id=$1`, id).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.MCPOperation{}, false, nil
		}
		if err != nil {
			return app.MCPOperation{}, false, err
		}
		operation, err := decodeMCPOperation(raw, id)
		if err != nil {
			return app.MCPOperation{}, false, storeError(ctx, OperationMCPOperationGet, StoreErrorCorrupt, err)
		}
		return operation, true, nil
	})
}

func (s *PostgresStore) FindMCPOperationByIdempotency(ctx context.Context, bindingID, idempotencyKey string) (app.MCPOperation, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationFindIdempotency, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationFindIdempotency, ctx); err != nil {
		return app.MCPOperation{}, false, err
	}
	key := bindingID + "\x00" + idempotencyKey
	return runMCPPostgresRead(ctx, s, OperationMCPOperationFindIdempotency, []int64{mcpAdvisoryKey("operation-idempotency", key)}, func(transaction onboardingPostgresTx) (app.MCPOperation, bool, error) {
		var raw []byte
		err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_operations WHERE binding_id=$1 AND idempotency_key=$2`, bindingID, idempotencyKey).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.MCPOperation{}, false, nil
		}
		if err != nil {
			return app.MCPOperation{}, false, err
		}
		operation, err := decodeMCPOperation(raw, "")
		if err != nil || operation.BindingID != bindingID || operation.IdempotencyKey != idempotencyKey {
			if err == nil {
				err = errors.New("MCP operation payload does not match idempotency lookup")
			}
			return app.MCPOperation{}, false, storeError(ctx, OperationMCPOperationFindIdempotency, StoreErrorCorrupt, err)
		}
		return operation, true, nil
	})
}

func (s *PostgresStore) ListMCPOperations(ctx context.Context, bindingID string) ([]app.MCPOperation, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationList, ctx); err != nil {
		return nil, err
	}
	keys := []int64(nil)
	if bindingID != "" {
		keys = append(keys, mcpAdvisoryKey("binding", bindingID))
	}
	out, _, err := runMCPPostgresRead(ctx, s, OperationMCPOperationList, keys, func(transaction onboardingPostgresTx) ([]app.MCPOperation, bool, error) {
		rows, err := transaction.Query(ctx, `SELECT id,payload FROM mcp_operations WHERE ($1='' OR binding_id=$1) ORDER BY updated_at DESC,id ASC`, bindingID)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		out := make([]app.MCPOperation, 0)
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				return nil, false, err
			}
			operation, err := decodeMCPOperation(raw, id)
			if err != nil || (bindingID != "" && operation.BindingID != bindingID) {
				if err == nil {
					err = errors.New("MCP operation binding does not match list scope")
				}
				return nil, false, storeError(ctx, OperationMCPOperationList, StoreErrorCorrupt, err)
			}
			out = append(out, operation)
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return out, true, nil
	})
	return out, err
}

func (s *PostgresStore) UpdateMCPOperation(ctx context.Context, operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error) {
	ctx, cancel := operationContext(ctx, OperationMCPOperationUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMCPOperationUpdate, ctx); err != nil {
		return app.MCPOperation{}, err
	}
	session, transaction, release, err := s.beginMCPPostgresWrite(ctx, OperationMCPOperationUpdate)
	if err != nil {
		return app.MCPOperation{}, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction,
		mcpAdvisoryKey("binding", operation.BindingID),
		mcpAdvisoryKey("operation", operation.ID),
	); err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPOperationUpdate, app.MCPOperation{}, false, session, transaction, err, ErrMCPOperationVersionConflict)
	}
	var raw []byte
	if err := transaction.QueryRow(ctx, `SELECT payload FROM mcp_operations WHERE id=$1 FOR UPDATE`, operation.ID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mcpPostgresBusinessError[app.MCPOperation](ctx, OperationMCPOperationUpdate, StoreErrorNotFound, session, transaction, errors.New("MCP operation not found"))
		}
		return finishMCPPostgresWrite(ctx, OperationMCPOperationUpdate, app.MCPOperation{}, false, session, transaction, err, ErrMCPOperationVersionConflict)
	}
	existing, err := decodeMCPOperation(raw, operation.ID)
	if err != nil {
		return mcpPostgresBusinessError[app.MCPOperation](ctx, OperationMCPOperationUpdate, StoreErrorCorrupt, session, transaction, err)
	}
	if existing.Version != expectedVersion {
		return mcpPostgresBusinessError[app.MCPOperation](ctx, OperationMCPOperationUpdate, StoreErrorConflict, session, transaction, ErrMCPOperationVersionConflict)
	}
	if !mcpOperationIdentityEqual(existing, operation) {
		return mcpPostgresBusinessError[app.MCPOperation](ctx, OperationMCPOperationUpdate, StoreErrorConflict, session, transaction, ErrMCPOperationConflict)
	}
	operation.Version = expectedVersion + 1
	operation.CreatedAt = existing.CreatedAt
	operation.UpdatedAt = normalizeMCPTime(time.Now())
	operation.CompletedAt = normalizeMCPTimePointer(operation.CompletedAt)
	command, err := transaction.Exec(ctx, `
		UPDATE mcp_operations SET version=$2,updated_at=$3,payload=$4 WHERE id=$1 AND version=$5
	`, operation.ID, operation.Version, operation.UpdatedAt, mustJSON(operation), expectedVersion)
	if err != nil {
		return finishMCPPostgresWrite(ctx, OperationMCPOperationUpdate, operation, true, session, transaction, err, ErrMCPOperationVersionConflict)
	}
	if command.RowsAffected() != 1 {
		return mcpPostgresBusinessError[app.MCPOperation](ctx, OperationMCPOperationUpdate, StoreErrorConflict, session, transaction, ErrMCPOperationVersionConflict)
	}
	return commitMCPPostgresWrite(ctx, OperationMCPOperationUpdate, operation, session, transaction)
}

func runMCPPostgresRead[T any](ctx context.Context, repository *PostgresStore, operation StoreOperation, barrierKeys []int64, read func(onboardingPostgresTx) (T, bool, error)) (T, bool, error) {
	var zero T
	session, transaction, release, err := repository.beginMCPPostgresTransaction(ctx, operation, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return zero, false, err
	}
	defer release()
	if err := acquireMCPPostgresBarriers(ctx, transaction, barrierKeys...); err != nil {
		_, err = finishMCPPostgresWrite(ctx, operation, struct{}{}, false, session, transaction, err, nil)
		return zero, false, err
	}
	value, found, err := read(transaction)
	if err != nil {
		if StoreErrorCodeOf(err) != "" {
			return zero, false, storeError(ctx, operation, StoreErrorCodeOf(err), rollbackMCPPostgres(ctx, session, transaction, err))
		}
		_, err = finishMCPPostgresWrite(ctx, operation, struct{}{}, false, session, transaction, err, nil)
		return zero, false, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return zero, false, classifyMCPPostgresRead(operation, ctx, errors.Join(err, session.Terminate(ctx)))
	}
	return value, found, nil
}

func acquireMCPPostgresBarriers(ctx context.Context, transaction onboardingPostgresTx, keys ...int64) error {
	keys = append([]int64(nil), keys...)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	for _, key := range keys {
		if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
			return err
		}
	}
	return nil
}

func mcpAdvisoryKey(domain, value string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/mcp/v1\x00" + domain + "\x00" + strings.TrimSpace(value)))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func (s *PostgresStore) beginMCPPostgresWrite(ctx context.Context, operation StoreOperation) (onboardingPostgresSession, onboardingPostgresTx, func(), error) {
	return s.beginMCPPostgresTransaction(ctx, operation, pgx.TxOptions{})
}

func (s *PostgresStore) beginMCPPostgresTransaction(ctx context.Context, operation StoreOperation, options pgx.TxOptions) (onboardingPostgresSession, onboardingPostgresTx, func(), error) {
	rawSession, err := s.mcpPostgres.Acquire(ctx)
	if err != nil {
		return nil, nil, func() {}, classifyPostgresPreTransaction(operation, ctx, err)
	}
	session := &mcpPostgresSessionState{onboardingPostgresSession: rawSession}
	transaction, err := session.Begin(ctx, options)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) || pgconn.SafeToRetry(err) {
			session.Release()
			if postgresError != nil {
				return nil, nil, func() {}, storeError(ctx, operation, StoreErrorInternal, err)
			}
			return nil, nil, func() {}, classifyPostgresPreTransaction(operation, ctx, err)
		}
		return nil, nil, func() {}, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return session, transaction, session.Release, nil
}

type mcpPostgresSessionState struct {
	onboardingPostgresSession
	terminated bool
}

func (s *mcpPostgresSessionState) Release() {
	if !s.terminated {
		s.onboardingPostgresSession.Release()
	}
}

func (s *mcpPostgresSessionState) Terminate(ctx context.Context) error {
	s.terminated = true
	return s.onboardingPostgresSession.Terminate(ctx)
}

func finishMCPPostgresWrite[T any](ctx context.Context, operation StoreOperation, candidate T, candidateKnown bool, session onboardingPostgresSession, transaction onboardingPostgresTx, cause, conflictCause error) (T, error) {
	var zero T
	var postgresError *pgconn.PgError
	if !candidateKnown || errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackMCPPostgres(ctx, session, transaction, cause)
		if postgresError != nil && postgresError.Code == "23505" {
			return zero, storeError(ctx, operation, StoreErrorConflict, errors.Join(conflictCause, cause))
		}
		if postgresError != nil {
			return zero, storeError(ctx, operation, StoreErrorInternal, cause)
		}
		return zero, classifyPostgresPreTransaction(operation, ctx, cause)
	}
	return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func commitMCPPostgresWrite[T any](ctx context.Context, operation StoreOperation, candidate T, session onboardingPostgresSession, transaction onboardingPostgresTx) (T, error) {
	if err := transaction.Commit(ctx); err != nil {
		return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func mcpPostgresBusinessError[T any](ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, cause error) (T, error) {
	var zero T
	return zero, storeError(ctx, operation, code, rollbackMCPPostgres(ctx, session, transaction, cause))
}

func mcpPostgresBusinessErrorOnly(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, cause error) error {
	return storeError(ctx, operation, code, rollbackMCPPostgres(ctx, session, transaction, cause))
}

func rollbackMCPPostgres(ctx context.Context, session onboardingPostgresSession, transaction onboardingPostgresTx, cause error) error {
	if err := transaction.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return errors.Join(cause, err, session.Terminate(ctx))
	}
	return cause
}

func classifyMCPPostgresRead(operation StoreOperation, ctx context.Context, cause error) error {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) {
		return storeError(ctx, operation, StoreErrorInternal, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}

func decodeMCPAccessTicket(raw []byte, expectedID string) (app.MCPAccessTicket, error) {
	var ticket app.MCPAccessTicket
	if err := json.Unmarshal(raw, &ticket); err != nil {
		return app.MCPAccessTicket{}, err
	}
	if ticket.ID == "" || (expectedID != "" && ticket.ID != expectedID) {
		return app.MCPAccessTicket{}, fmt.Errorf("ticket payload ID %q does not match row ID %q", ticket.ID, expectedID)
	}
	return ticket, nil
}

func decodeMCPBinding(raw []byte, expectedID string) (app.MCPBinding, error) {
	var binding app.MCPBinding
	if err := json.Unmarshal(raw, &binding); err != nil {
		return app.MCPBinding{}, err
	}
	if binding.ID == "" || (expectedID != "" && binding.ID != expectedID) {
		return app.MCPBinding{}, fmt.Errorf("binding payload ID %q does not match row ID %q", binding.ID, expectedID)
	}
	return binding, nil
}

func decodeMCPOperation(raw []byte, expectedID string) (app.MCPOperation, error) {
	var operation app.MCPOperation
	if err := json.Unmarshal(raw, &operation); err != nil {
		return app.MCPOperation{}, err
	}
	if operation.ID == "" || (expectedID != "" && operation.ID != expectedID) {
		return app.MCPOperation{}, fmt.Errorf("operation payload ID %q does not match row ID %q", operation.ID, expectedID)
	}
	return operation, nil
}
