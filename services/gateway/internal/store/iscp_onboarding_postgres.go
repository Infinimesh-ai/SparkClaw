package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) SaveISCPOnboarding(onboarding app.ISCPOnboarding) (app.ISCPOnboarding, error) {
	onboarding, err := normalizeISCPOnboarding(onboarding, time.Now().UTC())
	if err != nil {
		return app.ISCPOnboarding{}, err
	}
	_, err = s.db.Exec(context.Background(), `
		INSERT INTO iscp_onboardings (id,owner_id,domain_id,authority_ref,ticket_id,status,created_at,payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, onboarding.ID, onboarding.OwnerID, onboarding.DomainID, onboarding.AuthorityRef, onboarding.TicketID,
		onboarding.Status, onboarding.CreatedAt, mustJSON(onboarding))
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return app.ISCPOnboarding{}, ErrISCPOnboardingConflict
	}
	return onboarding, err
}

func (s *PostgresStore) GetISCPOnboarding(id string) (app.ISCPOnboarding, bool) {
	var raw []byte
	err := s.db.QueryRow(context.Background(), `SELECT payload FROM iscp_onboardings WHERE id=$1`, id).Scan(&raw)
	var onboarding app.ISCPOnboarding
	return onboarding, err == nil && json.Unmarshal(raw, &onboarding) == nil
}

func (s *PostgresStore) ListISCPOnboardings(ownerID string) []app.ISCPOnboarding {
	rows, err := s.db.Query(context.Background(), `
		SELECT payload FROM iscp_onboardings WHERE ($1='' OR owner_id=$1) ORDER BY created_at DESC
	`, ownerID)
	if err != nil {
		return []app.ISCPOnboarding{}
	}
	defer rows.Close()
	out := []app.ISCPOnboarding{}
	for rows.Next() {
		var raw []byte
		var onboarding app.ISCPOnboarding
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &onboarding) == nil {
			out = append(out, onboarding)
		}
	}
	return out
}
