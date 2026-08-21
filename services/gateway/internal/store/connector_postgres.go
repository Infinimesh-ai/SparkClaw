package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const connectorSettingSelectSQL = `SELECT owner_id, channel, enabled, iscp_enabled, lan_access_enabled, version, updated_by, updated_at FROM connector_settings`

const notificationBindingSelectSQL = `SELECT id, owner_id, actor_id, channel, provider, status, display_name,
	external_user_id, external_chat_id, external_thread_id, account_id, credential_ref, base_url,
	provider_session_id, provider_state, context_token, provider_cursor, qr_code_url, qr_code_image,
	default_for_channel, scopes, created_at, updated_at, expires_at, revoked_at, last_error,
	version, credential_kind FROM notification_bindings`

func (s *PostgresStore) validateConnectorState(ctx context.Context) error {
	startupCtx, cancel := postgresMigrationStartupContext(ctx)
	defer cancel()
	settings := map[string]app.ConnectorSetting{}
	rows, err := s.connectorPostgres.Query(startupCtx, connectorSettingSelectSQL)
	if err != nil {
		return fmt.Errorf("validate connector settings: %w", err)
	}
	for rows.Next() {
		setting, err := scanConnectorSetting(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("validate connector settings: %w", err)
		}
		settings[connectorSettingKey(setting.OwnerID, setting.Channel)] = setting
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("validate connector settings: %w", err)
	}
	rows.Close()

	bindings := map[string]app.NotificationBinding{}
	rows, err = s.connectorPostgres.Query(startupCtx, notificationBindingSelectSQL)
	if err != nil {
		return fmt.Errorf("validate notification bindings: %w", err)
	}
	for rows.Next() {
		binding, err := scanNotificationBinding(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("validate notification bindings: %w", err)
		}
		bindings[binding.ID] = binding
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("validate notification bindings: %w", err)
	}
	rows.Close()
	if err := normalizeAndValidatePersistedConnectorState(settings, bindings); err != nil {
		return fmt.Errorf("validate connector state: %w", err)
	}
	for key, setting := range settings {
		s.connectorSettingWriteHighWater[key] = setting.UpdatedAt
	}
	for id, binding := range bindings {
		s.notificationBindingWriteHighWater[id] = latestNotificationBindingTime(binding)
	}
	return nil
}

func (s *PostgresStore) GetConnectorSetting(ctx context.Context, ownerID, channel string) (app.ConnectorSetting, bool, error) {
	ctx, cancel := operationContext(ctx, OperationConnectorSettingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConnectorSettingGet, ctx); err != nil {
		return app.ConnectorSetting{}, false, err
	}
	ownerID = normalizeConnectorOwner(ownerID)
	channel = normalizeConnectorChannel(channel)
	if channel == "" {
		return app.ConnectorSetting{}, false, nil
	}
	session, transaction, release, err := s.beginConnectorTransaction(ctx, OperationConnectorSettingGet, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return app.ConnectorSetting{}, false, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, connectorOwnerChannelAdvisoryKey(ownerID, channel)); err != nil {
		return app.ConnectorSetting{}, false, finishConnectorRead(ctx, OperationConnectorSettingGet, session, transaction, release, err)
	}
	setting, err := scanConnectorSetting(transaction.QueryRow(ctx, connectorSettingSelectSQL+` WHERE owner_id=$1 AND channel=$2`, ownerID, channel))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := commitConnectorRead(ctx, OperationConnectorSettingGet, session, transaction, release); err != nil {
			return app.ConnectorSetting{}, false, err
		}
		return app.ConnectorSetting{}, false, nil
	}
	if err != nil {
		return app.ConnectorSetting{}, false, finishConnectorRead(ctx, OperationConnectorSettingGet, session, transaction, release, err)
	}
	if err := validatePersistedConnectorSetting(setting); err != nil {
		return app.ConnectorSetting{}, false, connectorBusinessError(ctx, OperationConnectorSettingGet, StoreErrorCorrupt, session, transaction, release, err)
	}
	if err := commitConnectorRead(ctx, OperationConnectorSettingGet, session, transaction, release); err != nil {
		return app.ConnectorSetting{}, false, err
	}
	return setting, true, nil
}

func (s *PostgresStore) ListConnectorSettings(ctx context.Context, ownerID string) ([]app.ConnectorSetting, error) {
	return s.listConnectorSettings(ctx, OperationConnectorSettingList,
		connectorSettingSelectSQL+` WHERE owner_id=$1 ORDER BY channel ASC`, normalizeConnectorOwner(ownerID))
}

func (s *PostgresStore) ListAllConnectorSettings(ctx context.Context) ([]app.ConnectorSetting, error) {
	return s.listConnectorSettings(ctx, OperationConnectorSettingListAll,
		connectorSettingSelectSQL+` ORDER BY owner_id ASC, channel ASC`)
}

func (s *PostgresStore) listConnectorSettings(ctx context.Context, operation StoreOperation, query string, arguments ...any) ([]app.ConnectorSetting, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return nil, err
	}
	rows, err := s.connectorPostgres.Query(ctx, query, arguments...)
	if err != nil {
		return nil, classifyPostgresReadError(operation, ctx, err)
	}
	defer rows.Close()
	out := make([]app.ConnectorSetting, 0)
	for rows.Next() {
		setting, err := scanConnectorSetting(rows)
		if err != nil {
			return nil, classifyPostgresReadError(operation, ctx, err)
		}
		if err := validatePersistedConnectorSetting(setting); err != nil {
			return nil, storeError(operation, StoreErrorCorrupt, err)
		}
		out = append(out, setting)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(operation, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) UpdateConnectorSetting(ctx context.Context, setting app.ConnectorSetting, expectedVersion int64) (app.ConnectorSetting, error) {
	ctx, cancel := operationContext(ctx, OperationConnectorSettingUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConnectorSettingUpdate, ctx); err != nil {
		return app.ConnectorSetting{}, err
	}
	setting, err := normalizeConnectorSettingCandidate(setting, expectedVersion)
	if err != nil {
		return app.ConnectorSetting{}, storeError(OperationConnectorSettingUpdate, StoreErrorInvalid, err)
	}
	releaseCommand, err := s.acquireConnectorCommand(ctx, OperationConnectorSettingUpdate)
	if err != nil {
		return app.ConnectorSetting{}, err
	}
	defer releaseCommand()
	session, transaction, release, err := s.beginConnectorTransaction(ctx, OperationConnectorSettingUpdate, pgx.TxOptions{})
	if err != nil {
		return app.ConnectorSetting{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, connectorOwnerChannelAdvisoryKey(setting.OwnerID, setting.Channel)); err != nil {
		return app.ConnectorSetting{}, finishConnectorPreCandidate(ctx, OperationConnectorSettingUpdate, session, transaction, release, err)
	}
	current, err := scanConnectorSetting(transaction.QueryRow(ctx, connectorSettingSelectSQL+` WHERE owner_id=$1 AND channel=$2`, setting.OwnerID, setting.Channel))
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return app.ConnectorSetting{}, finishConnectorPreCandidate(ctx, OperationConnectorSettingUpdate, session, transaction, release, err)
	}
	if exists {
		if err := validatePersistedConnectorSetting(current); err != nil {
			return app.ConnectorSetting{}, connectorBusinessError(ctx, OperationConnectorSettingUpdate, StoreErrorCorrupt, session, transaction, release, err)
		}
	}
	if (!exists && expectedVersion != 0) || (exists && current.Version != expectedVersion) {
		return app.ConnectorSetting{}, connectorBusinessError(ctx, OperationConnectorSettingUpdate, StoreErrorConflict, session, transaction, release, ErrConnectorSettingConflict)
	}
	key := connectorSettingKey(setting.OwnerID, setting.Channel)
	commandAt := nextRepositoryTime(s.connectorNow(), s.connectorSettingWriteHighWater[key], current.UpdatedAt)
	setting.Version = expectedVersion + 1
	setting.UpdatedAt = commandAt
	s.connectorSettingWriteHighWater[key] = commandAt
	auditType := connectorSettingAuditType(exists, current.Enabled, current.ISCPEnabled, current.LANAccessEnabled, setting)
	mutationSQL := `INSERT INTO connector_settings (owner_id,channel,enabled,iscp_enabled,lan_access_enabled,version,updated_by,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	mutationArguments := []any{setting.OwnerID, setting.Channel, setting.Enabled, setting.ISCPEnabled, setting.LANAccessEnabled, setting.Version, setting.UpdatedBy, setting.UpdatedAt}
	if exists {
		mutationSQL = `UPDATE connector_settings SET enabled=$3,iscp_enabled=$4,lan_access_enabled=$5,version=$6,updated_by=$7,updated_at=$8 WHERE owner_id=$1 AND channel=$2 AND version=$9`
		mutationArguments = append(mutationArguments, expectedVersion)
	}
	statements := []connectorPostgresStatement{
		{sql: mutationSQL, args: mutationArguments, requireOne: true},
		{sql: `INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields) VALUES ($1,$2,$3,NULL,NULL,$4,$5,$6)`, args: []any{app.NewID("audit"), commandAt, auditType, setting.UpdatedBy, setting.Channel, optionalJSON(connectorSettingAuditFields(setting))}, requireOne: true},
		{sql: `INSERT INTO events (id,happened_at,type,session_id,run_id,payload) VALUES ($1,$2,$3,NULL,NULL,$4)`, args: []any{app.NewID("evt"), commandAt, auditType, mustJSON(setting)}, requireOne: true},
	}
	if err := executeConnectorStatements(ctx, OperationConnectorSettingUpdate, session, transaction, release, statements); err != nil {
		if StoreErrorCodeOf(err) == StoreErrorUnknownOutcome {
			return setting, err
		}
		return app.ConnectorSetting{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return setting, storeError(OperationConnectorSettingUpdate, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return setting, nil
}

func (s *PostgresStore) CreateNotificationBinding(ctx context.Context, binding app.NotificationBinding) (app.NotificationBinding, error) {
	ctx, cancel := operationContext(ctx, OperationNotificationBindingCreate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationNotificationBindingCreate, ctx); err != nil {
		return app.NotificationBinding{}, err
	}
	binding, err := normalizeNotificationBindingCreate(binding)
	if err != nil {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingCreate, StoreErrorInvalid, err)
	}
	releaseCommand, err := s.acquireConnectorCommand(ctx, OperationNotificationBindingCreate)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	defer releaseCommand()
	session, transaction, release, err := s.beginConnectorTransaction(ctx, OperationNotificationBindingCreate, pgx.TxOptions{})
	if err != nil {
		return app.NotificationBinding{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if err := acquireConnectorBindingBarriers(ctx, transaction, binding.OwnerID, binding.Channel, binding.ID, ""); err != nil {
		return app.NotificationBinding{}, finishConnectorPreCandidate(ctx, OperationNotificationBindingCreate, session, transaction, release, err)
	}
	_, err = scanNotificationBinding(transaction.QueryRow(ctx, notificationBindingSelectSQL+` WHERE id=$1`, binding.ID))
	if err == nil {
		return app.NotificationBinding{}, connectorBusinessError(ctx, OperationNotificationBindingCreate, StoreErrorConflict, session, transaction, release, errors.New("notification binding already exists"))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return app.NotificationBinding{}, finishConnectorPreCandidate(ctx, OperationNotificationBindingCreate, session, transaction, release, err)
	}
	commandAt := nextRepositoryTime(s.connectorNow(), s.notificationBindingWriteHighWater[binding.ID])
	binding.Version = 1
	binding.CreatedAt = commandAt
	binding.UpdatedAt = commandAt
	s.notificationBindingWriteHighWater[binding.ID] = commandAt
	statements := append(notificationBindingMutationStatements("INSERT", binding), notificationBindingLifecycleStatements(commandAt, binding)...)
	if err := executeConnectorStatements(ctx, OperationNotificationBindingCreate, session, transaction, release, statements); err != nil {
		if StoreErrorCodeOf(err) == StoreErrorUnknownOutcome {
			return cloneNotificationBinding(binding), err
		}
		return app.NotificationBinding{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return cloneNotificationBinding(binding), storeError(OperationNotificationBindingCreate, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneNotificationBinding(binding), nil
}

func (s *PostgresStore) GetNotificationBinding(ctx context.Context, id string) (app.NotificationBinding, bool, error) {
	ctx, cancel := operationContext(ctx, OperationNotificationBindingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationNotificationBindingGet, ctx); err != nil {
		return app.NotificationBinding{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.NotificationBinding{}, false, nil
	}
	session, transaction, release, err := s.beginConnectorTransaction(ctx, OperationNotificationBindingGet, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return app.NotificationBinding{}, false, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, notificationBindingAdvisoryKey(id)); err != nil {
		return app.NotificationBinding{}, false, finishConnectorRead(ctx, OperationNotificationBindingGet, session, transaction, release, err)
	}
	binding, err := scanNotificationBinding(transaction.QueryRow(ctx, notificationBindingSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := commitConnectorRead(ctx, OperationNotificationBindingGet, session, transaction, release); err != nil {
			return app.NotificationBinding{}, false, err
		}
		return app.NotificationBinding{}, false, nil
	}
	if err != nil {
		if errors.Is(err, errNotificationBindingScopesDecode) {
			return app.NotificationBinding{}, false, connectorBusinessError(ctx, OperationNotificationBindingGet, StoreErrorCorrupt, session, transaction, release, err)
		}
		return app.NotificationBinding{}, false, finishConnectorRead(ctx, OperationNotificationBindingGet, session, transaction, release, err)
	}
	if err := validatePersistedNotificationBinding(binding); err != nil {
		return app.NotificationBinding{}, false, connectorBusinessError(ctx, OperationNotificationBindingGet, StoreErrorCorrupt, session, transaction, release, err)
	}
	if err := commitConnectorRead(ctx, OperationNotificationBindingGet, session, transaction, release); err != nil {
		return app.NotificationBinding{}, false, err
	}
	return cloneNotificationBinding(binding), true, nil
}

func (s *PostgresStore) ListNotificationBindings(ctx context.Context, channel, status string) ([]app.NotificationBinding, error) {
	ctx, cancel := operationContext(ctx, OperationNotificationBindingList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationNotificationBindingList, ctx); err != nil {
		return nil, err
	}
	channel = normalizeConnectorChannel(channel)
	status = strings.TrimSpace(status)
	rows, err := s.connectorPostgres.Query(ctx, notificationBindingSelectSQL+` ORDER BY updated_at DESC, id ASC`)
	if err != nil {
		return nil, classifyPostgresReadError(OperationNotificationBindingList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.NotificationBinding, 0)
	vaultOwners := map[string]string{}
	activeDefaults := map[string]string{}
	for rows.Next() {
		binding, err := scanNotificationBinding(rows)
		if err != nil {
			return nil, classifyConnectorBindingScanError(OperationNotificationBindingList, ctx, err)
		}
		if err := validatePersistedNotificationBinding(binding); err != nil {
			return nil, storeError(OperationNotificationBindingList, StoreErrorCorrupt, err)
		}
		if err := claimBindingCredentialRef(vaultOwners, binding); err != nil {
			return nil, storeError(OperationNotificationBindingList, StoreErrorCorrupt, err)
		}
		if binding.Status == app.NotificationBindingActive && binding.DefaultForChannel {
			key := connectorSettingKey(binding.OwnerID, binding.Channel)
			if activeDefaults[key] != "" {
				return nil, storeError(OperationNotificationBindingList, StoreErrorCorrupt, errors.New("multiple active default bindings"))
			}
			activeDefaults[key] = binding.ID
		}
		if (channel == "" || binding.Channel == channel) && (status == "" || binding.Status == status) {
			out = append(out, cloneNotificationBinding(binding))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(OperationNotificationBindingList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) UpdateNotificationBinding(ctx context.Context, command NotificationBindingUpdateCommand) (app.NotificationBinding, error) {
	ctx, cancel := operationContext(ctx, OperationNotificationBindingUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationNotificationBindingUpdate, ctx); err != nil {
		return app.NotificationBinding{}, err
	}
	command, err := normalizeNotificationBindingUpdateCommand(command)
	if err != nil {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingUpdate, StoreErrorInvalid, err)
	}
	releaseCommand, err := s.acquireConnectorCommand(ctx, OperationNotificationBindingUpdate)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	defer releaseCommand()
	session, transaction, release, err := s.beginConnectorTransaction(ctx, OperationNotificationBindingUpdate, pgx.TxOptions{})
	if err != nil {
		return app.NotificationBinding{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	ownerID := strings.TrimSpace(command.next.OwnerID)
	channel := normalizeConnectorChannel(command.next.Channel)
	if err := acquireConnectorBindingBarriers(ctx, transaction, ownerID, channel, command.id, ""); err != nil {
		return app.NotificationBinding{}, finishConnectorPreCandidate(ctx, OperationNotificationBindingUpdate, session, transaction, release, err)
	}
	previous, err := scanNotificationBinding(transaction.QueryRow(ctx, notificationBindingSelectSQL+` WHERE id=$1`, command.id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.NotificationBinding{}, connectorBusinessError(ctx, OperationNotificationBindingUpdate, StoreErrorNotFound, session, transaction, release, errors.New("notification binding not found"))
	}
	if err != nil {
		return app.NotificationBinding{}, finishConnectorPreCandidate(ctx, OperationNotificationBindingUpdate, session, transaction, release, err)
	}
	if err := validatePersistedNotificationBinding(previous); err != nil {
		return app.NotificationBinding{}, connectorBusinessError(ctx, OperationNotificationBindingUpdate, StoreErrorCorrupt, session, transaction, release, err)
	}
	if notificationBindingDigest(previous) != command.expected {
		return app.NotificationBinding{}, connectorBusinessError(ctx, OperationNotificationBindingUpdate, StoreErrorConflict, session, transaction, release, errors.New("notification binding changed"))
	}
	refs := connectorVaultRefs(previous.CredentialRef, command.next.CredentialRef)
	for _, ref := range refs {
		if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, credentialAdvisoryKey(ref)); err != nil {
			return app.NotificationBinding{}, finishConnectorPreCandidate(ctx, OperationNotificationBindingUpdate, session, transaction, release, err)
		}
	}
	demoted, err := s.listDefaultBindingsForUpdate(ctx, transaction, previous)
	if err != nil {
		return app.NotificationBinding{}, finishConnectorPreCandidate(ctx, OperationNotificationBindingUpdate, session, transaction, release, err)
	}
	highWater := []time.Time{s.notificationBindingWriteHighWater[previous.ID], latestNotificationBindingTime(previous)}
	for _, binding := range demoted {
		highWater = append(highWater, s.notificationBindingWriteHighWater[binding.ID], latestNotificationBindingTime(binding))
	}
	commandAt := nextRepositoryTime(s.connectorNow(), highWater...)
	candidate, err := prepareNotificationBindingUpdate(previous, command.next, commandAt)
	if err != nil {
		return app.NotificationBinding{}, connectorBusinessError(ctx, OperationNotificationBindingUpdate, StoreErrorInvalid, session, transaction, release, err)
	}
	if isVaultOwnedCredentialRef(candidate.CredentialRef) {
		var existingID string
		err := transaction.QueryRow(ctx, `SELECT id FROM notification_bindings WHERE credential_ref=$1 AND id<>$2 ORDER BY id LIMIT 1`, candidate.CredentialRef, candidate.ID).Scan(&existingID)
		if err == nil {
			return app.NotificationBinding{}, connectorBusinessError(ctx, OperationNotificationBindingUpdate, StoreErrorConflict, session, transaction, release, fmt.Errorf("Vault credential ref is retained by notification binding %q", existingID))
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return app.NotificationBinding{}, finishConnectorPreCandidate(ctx, OperationNotificationBindingUpdate, session, transaction, release, err)
		}
	}
	statements := make([]connectorPostgresStatement, 0, len(demoted)*3+3)
	if candidate.Status == app.NotificationBindingActive && candidate.DefaultForChannel {
		for _, existing := range demoted {
			existing.DefaultForChannel = false
			existing.Version++
			existing.UpdatedAt = commandAt
			s.notificationBindingWriteHighWater[existing.ID] = commandAt
			statements = append(statements, notificationBindingMutationStatements("UPDATE", existing)...)
			statements = append(statements, notificationBindingTypedStatements(commandAt, "notification_binding.default_demoted", "system", existing)...)
		}
	}
	s.notificationBindingWriteHighWater[candidate.ID] = commandAt
	statements = append(statements, notificationBindingMutationStatements("UPDATE", candidate)...)
	statements = append(statements, notificationBindingLifecycleStatements(commandAt, candidate)...)
	if err := executeConnectorStatements(ctx, OperationNotificationBindingUpdate, session, transaction, release, statements); err != nil {
		if StoreErrorCodeOf(err) == StoreErrorUnknownOutcome {
			return cloneNotificationBinding(candidate), err
		}
		return app.NotificationBinding{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return cloneNotificationBinding(candidate), storeError(OperationNotificationBindingUpdate, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneNotificationBinding(candidate), nil
}

func (s *PostgresStore) listDefaultBindingsForUpdate(ctx context.Context, transaction onboardingPostgresTx, previous app.NotificationBinding) ([]app.NotificationBinding, error) {
	rows, err := transaction.Query(ctx, notificationBindingSelectSQL+` WHERE owner_id=$1 AND channel=$2 AND status='active' AND default_for_channel AND id<>$3 ORDER BY id FOR UPDATE`, previous.OwnerID, previous.Channel, previous.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]app.NotificationBinding, 0, 1)
	for rows.Next() {
		binding, err := scanNotificationBinding(rows)
		if err != nil {
			return nil, err
		}
		if err := validatePersistedNotificationBinding(binding); err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type connectorPostgresStatement struct {
	sql        string
	args       []any
	requireOne bool
}

func notificationBindingMutationStatements(mode string, binding app.NotificationBinding) []connectorPostgresStatement {
	arguments := notificationBindingSQLArguments(binding)
	if mode == "INSERT" {
		return []connectorPostgresStatement{{
			sql:  `INSERT INTO notification_bindings (id,owner_id,actor_id,channel,provider,status,display_name,external_user_id,external_chat_id,external_thread_id,account_id,credential_ref,base_url,provider_session_id,provider_state,context_token,provider_cursor,qr_code_url,qr_code_image,default_for_channel,scopes,created_at,updated_at,expires_at,revoked_at,last_error,version,credential_kind) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)`,
			args: arguments, requireOne: true,
		}}
	}
	arguments = append(arguments, binding.Version-1)
	return []connectorPostgresStatement{{
		sql:  `UPDATE notification_bindings SET owner_id=$2,actor_id=$3,channel=$4,provider=$5,status=$6,display_name=$7,external_user_id=$8,external_chat_id=$9,external_thread_id=$10,account_id=$11,credential_ref=$12,base_url=$13,provider_session_id=$14,provider_state=$15,context_token=$16,provider_cursor=$17,qr_code_url=$18,qr_code_image=$19,default_for_channel=$20,scopes=$21,created_at=$22,updated_at=$23,expires_at=$24,revoked_at=$25,last_error=$26,version=$27,credential_kind=$28 WHERE id=$1 AND version=$29`,
		args: arguments, requireOne: true,
	}}
}

func notificationBindingSQLArguments(binding app.NotificationBinding) []any {
	return []any{
		binding.ID, binding.OwnerID, binding.ActorID, binding.Channel, binding.Provider, binding.Status,
		binding.DisplayName, binding.ExternalUserID, binding.ExternalChatID, binding.ExternalThreadID,
		binding.AccountID, binding.CredentialRef, binding.BaseURL, binding.ProviderSessionID,
		binding.ProviderState, binding.ContextToken, binding.ProviderCursor, binding.QRCodeURL,
		binding.QRCodeImage, binding.DefaultForChannel, mustJSON(binding.Scopes), binding.CreatedAt,
		binding.UpdatedAt, binding.ExpiresAt, binding.RevokedAt, binding.LastError, binding.Version,
		binding.CredentialKind,
	}
}

func notificationBindingLifecycleStatements(at time.Time, binding app.NotificationBinding) []connectorPostgresStatement {
	return notificationBindingTypedStatements(at, "notification_binding."+binding.Status, "owner", binding)
}

func notificationBindingTypedStatements(at time.Time, typ, actor string, binding app.NotificationBinding) []connectorPostgresStatement {
	return []connectorPostgresStatement{
		{sql: `INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields) VALUES ($1,$2,$3,NULL,NULL,$4,$5,$6)`, args: []any{app.NewID("audit"), at, typ, actor, binding.Channel, optionalJSON(notificationBindingAuditFields(binding))}, requireOne: true},
		{sql: `INSERT INTO events (id,happened_at,type,session_id,run_id,payload) VALUES ($1,$2,$3,NULL,NULL,$4)`, args: []any{app.NewID("evt"), at, typ, mustJSON(binding)}, requireOne: true},
	}
}

func executeConnectorStatements(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, statements []connectorPostgresStatement) error {
	for _, statement := range statements {
		tag, err := transaction.Exec(ctx, statement.sql, statement.args...)
		if err != nil {
			_, resultErr := finishConnectorStatement(ctx, operation, session, transaction, release, err)
			return resultErr
		}
		if statement.requireOne && tag.RowsAffected() != 1 {
			return connectorBusinessError(ctx, operation, StoreErrorConflict, session, transaction, release, errors.New("connector record changed"))
		}
	}
	return nil
}

func acquireConnectorBindingBarriers(ctx context.Context, transaction onboardingPostgresTx, ownerID, channel, id, ref string) error {
	for _, key := range []int64{connectorOwnerChannelAdvisoryKey(ownerID, channel), notificationBindingAdvisoryKey(id)} {
		if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
			return err
		}
	}
	if isVaultOwnedCredentialRef(ref) {
		_, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, credentialAdvisoryKey(ref))
		return err
	}
	return nil
}

func connectorVaultRefs(values ...string) []string {
	unique := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if isVaultOwnedCredentialRef(value) {
			unique[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(unique))
	for value := range unique {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (s *PostgresStore) acquireConnectorCommand(ctx context.Context, operation StoreOperation) (func(), error) {
	if err := s.connectorCommandGate.Acquire(ctx, 1); err != nil {
		if contextErr := operationContextError(operation, ctx); contextErr != nil {
			return nil, contextErr
		}
		return nil, storeError(operation, StoreErrorUnavailable, err)
	}
	if err := operationContextError(operation, ctx); err != nil {
		s.connectorCommandGate.Release(1)
		return nil, err
	}
	return func() { s.connectorCommandGate.Release(1) }, nil
}

func (s *PostgresStore) beginConnectorTransaction(ctx context.Context, operation StoreOperation, options pgx.TxOptions) (onboardingPostgresSession, onboardingPostgresTx, *bool, error) {
	session, err := s.connectorPostgres.Acquire(ctx)
	if err != nil {
		return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	transaction, err := session.Begin(ctx, options)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) || pgconn.SafeToRetry(err) {
			session.Release()
			if postgresError != nil {
				return nil, nil, nil, storeError(operation, StoreErrorInternal, err)
			}
			return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
		}
		return nil, nil, nil, storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return session, transaction, &release, nil
}

func commitConnectorRead(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool) error {
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return nil
}

func finishConnectorRead(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
		if postgresError != nil {
			return storeError(operation, StoreErrorInternal, cause)
		}
		return classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func finishConnectorPreCandidate(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return finishConnectorRead(ctx, operation, session, transaction, release, cause)
}

func finishConnectorStatement(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (bool, error) {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
		if postgresError != nil && postgresError.Code == "23505" {
			return false, storeError(operation, StoreErrorConflict, cause)
		}
		if postgresError != nil {
			return false, storeError(operation, StoreErrorInternal, cause)
		}
		return false, classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return true, storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func connectorBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(operation, code, rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause))
}

func connectorOwnerChannelAdvisoryKey(ownerID, channel string) int64 {
	return connectorAdvisoryKey("owner-channel", normalizeConnectorOwner(ownerID)+"\x00"+normalizeConnectorChannel(channel))
}

func notificationBindingAdvisoryKey(id string) int64 {
	return connectorAdvisoryKey("binding", strings.TrimSpace(id))
}

func connectorAdvisoryKey(domain, value string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/connector/v1\x00" + domain + "\x00" + value))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}
