package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

const emailProviderSettingSelectSQL = `SELECT owner_id, provider, enabled, is_default, account, account_hint,
	state, last_checked_at, error_code, version, updated_by, updated_at FROM email_provider_settings`

func (s *PostgresStore) validateEmailProviderState(ctx context.Context) error {
	startupCtx, cancel := postgresMigrationStartupContext(ctx)
	defer cancel()
	rows, err := s.connectorPostgres.Query(startupCtx, emailProviderSettingSelectSQL)
	if err != nil {
		return fmt.Errorf("validate email provider settings: %w", err)
	}
	defer rows.Close()
	settings := map[string]app.EmailProviderSetting{}
	for rows.Next() {
		setting, err := scanEmailProviderSetting(rows)
		if err != nil {
			return fmt.Errorf("validate email provider settings: %w", err)
		}
		settings[emailProviderKey(setting.OwnerID, setting.Provider)] = setting
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("validate email provider settings: %w", err)
	}
	if err := normalizeAndValidatePersistedEmailProviderState(settings); err != nil {
		return fmt.Errorf("validate email provider state: %w", err)
	}
	for key, setting := range settings {
		s.emailProviderWriteHighWater[key] = setting.UpdatedAt
	}
	return nil
}

func (s *PostgresStore) GetEmailProviderSetting(ctx context.Context, ownerID, provider string) (app.EmailProviderSetting, bool, error) {
	ctx, cancel := operationContext(ctx, OperationEmailProviderSettingGet, s.operationTimeouts)
	defer cancel()
	ownerID = normalizeConnectorOwner(ownerID)
	provider = stringsLowerTrim(provider)
	if !supportedEmailProvider(provider) {
		return app.EmailProviderSetting{}, false, nil
	}
	setting, err := scanEmailProviderSetting(s.connectorPostgres.QueryRow(ctx, emailProviderSettingSelectSQL+` WHERE owner_id=$1 AND provider=$2`, ownerID, provider))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.EmailProviderSetting{}, false, nil
	}
	if err != nil {
		return app.EmailProviderSetting{}, false, classifyPostgresReadError(OperationEmailProviderSettingGet, ctx, err)
	}
	if err := validatePersistedEmailProviderSetting(setting); err != nil {
		return app.EmailProviderSetting{}, false, storeError(ctx, OperationEmailProviderSettingGet, StoreErrorCorrupt, err)
	}
	return cloneEmailProviderSetting(setting), true, nil
}

func (s *PostgresStore) ListEmailProviderSettings(ctx context.Context, ownerID string) ([]app.EmailProviderSetting, error) {
	ctx, cancel := operationContext(ctx, OperationEmailProviderSettingList, s.operationTimeouts)
	defer cancel()
	ownerID = normalizeConnectorOwner(ownerID)
	rows, err := s.connectorPostgres.Query(ctx, emailProviderSettingSelectSQL+` WHERE owner_id=$1 ORDER BY provider ASC`, ownerID)
	if err != nil {
		return nil, classifyPostgresReadError(OperationEmailProviderSettingList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.EmailProviderSetting, 0, 3)
	for rows.Next() {
		setting, err := scanEmailProviderSetting(rows)
		if err != nil {
			return nil, classifyPostgresReadError(OperationEmailProviderSettingList, ctx, err)
		}
		if err := validatePersistedEmailProviderSetting(setting); err != nil {
			return nil, storeError(ctx, OperationEmailProviderSettingList, StoreErrorCorrupt, err)
		}
		out = append(out, cloneEmailProviderSetting(setting))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(OperationEmailProviderSettingList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) UpdateEmailProviderSetting(ctx context.Context, setting app.EmailProviderSetting, expectedVersion int64) (app.EmailProviderSetting, error) {
	ctx, cancel := operationContext(ctx, OperationEmailProviderSettingUpdate, s.operationTimeouts)
	defer cancel()
	candidate, err := normalizeEmailProviderCandidate(setting, expectedVersion)
	if err != nil {
		return app.EmailProviderSetting{}, storeError(ctx, OperationEmailProviderSettingUpdate, StoreErrorInvalid, err)
	}
	if err := s.connectorCommandGate.Acquire(ctx, 1); err != nil {
		return app.EmailProviderSetting{}, operationContextError(OperationEmailProviderSettingUpdate, ctx)
	}
	defer s.connectorCommandGate.Release(1)
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, connectorOwnerChannelAdvisoryKey(candidate.OwnerID, "browser_email")); err != nil {
		return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
	}
	current, err := scanEmailProviderSetting(tx.QueryRow(ctx, emailProviderSettingSelectSQL+` WHERE owner_id=$1 AND provider=$2`, candidate.OwnerID, candidate.Provider))
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
	}
	if exists {
		if err := validatePersistedEmailProviderSetting(current); err != nil {
			return app.EmailProviderSetting{}, storeError(ctx, OperationEmailProviderSettingUpdate, StoreErrorCorrupt, err)
		}
	}
	if (!exists && expectedVersion != 0) || (exists && current.Version != expectedVersion) {
		return app.EmailProviderSetting{}, storeError(ctx, OperationEmailProviderSettingUpdate, StoreErrorConflict, ErrEmailProviderSettingConflict)
	}
	key := emailProviderKey(candidate.OwnerID, candidate.Provider)
	at := nextRepositoryTime(s.connectorNow(), s.emailProviderWriteHighWater[key], current.UpdatedAt)
	if candidate.Default {
		rows, err := tx.Query(ctx, emailProviderSettingSelectSQL+` WHERE owner_id=$1 AND is_default=true AND provider<>$2 FOR UPDATE`, candidate.OwnerID, candidate.Provider)
		if err != nil {
			return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
		}
		demoted := make([]app.EmailProviderSetting, 0, 2)
		for rows.Next() {
			other, err := scanEmailProviderSetting(rows)
			if err != nil {
				rows.Close()
				return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
			}
			other.Default = false
			other.Version++
			other.UpdatedAt = at
			other.UpdatedBy = candidate.UpdatedBy
			demoted = append(demoted, other)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
		}
		rows.Close()
		for _, other := range demoted {
			if _, err := tx.Exec(ctx, `UPDATE email_provider_settings SET is_default=false, version=$3, updated_by=$4, updated_at=$5 WHERE owner_id=$1 AND provider=$2`, other.OwnerID, other.Provider, other.Version, other.UpdatedBy, other.UpdatedAt); err != nil {
				return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
			}
			s.emailProviderWriteHighWater[emailProviderKey(other.OwnerID, other.Provider)] = at
		}
	}
	candidate.Version = expectedVersion + 1
	candidate.UpdatedAt = at
	if _, err := tx.Exec(ctx, `INSERT INTO email_provider_settings
		(owner_id,provider,enabled,is_default,account,account_hint,state,last_checked_at,error_code,version,updated_by,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (owner_id,provider) DO UPDATE SET enabled=EXCLUDED.enabled,is_default=EXCLUDED.is_default,
		account=EXCLUDED.account,account_hint=EXCLUDED.account_hint,state=EXCLUDED.state,last_checked_at=EXCLUDED.last_checked_at,
		error_code=EXCLUDED.error_code,version=EXCLUDED.version,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at`,
		candidate.OwnerID, candidate.Provider, candidate.Enabled, candidate.Default, candidate.Account, candidate.AccountHint,
		candidate.State, candidate.LastCheckedAt, candidate.ErrorCode, candidate.Version, candidate.UpdatedBy, candidate.UpdatedAt); err != nil {
		return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
	}
	typ := "email.provider.updated"
	if !exists {
		typ = "email.provider.configured"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (id,happened_at,type,actor,summary,fields) VALUES ($1,$2,$3,$4,$5,$6)`,
		app.NewID("audit"), at, typ, candidate.UpdatedBy, candidate.Provider, optionalJSON(emailProviderAuditFields(candidate))); err != nil {
		return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (id,happened_at,type,payload) VALUES ($1,$2,$3,$4)`,
		app.NewID("evt"), at, typ, mustJSON(candidate)); err != nil {
		return app.EmailProviderSetting{}, classifyPostgresReadError(OperationEmailProviderSettingUpdate, ctx, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return candidate, storeError(ctx, OperationEmailProviderSettingUpdate, StoreErrorUnknownOutcome, err)
	}
	committed = true
	s.emailProviderWriteHighWater[key] = at
	return cloneEmailProviderSetting(candidate), nil
}

func scanEmailProviderSetting(row interface{ Scan(...any) error }) (app.EmailProviderSetting, error) {
	var setting app.EmailProviderSetting
	err := row.Scan(&setting.OwnerID, &setting.Provider, &setting.Enabled, &setting.Default, &setting.Account,
		&setting.AccountHint, &setting.State, &setting.LastCheckedAt, &setting.ErrorCode, &setting.Version,
		&setting.UpdatedBy, &setting.UpdatedAt)
	return setting, err
}
