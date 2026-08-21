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
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/semaphore"
)

type PostgresStore struct {
	db                                *pgxpool.Pool
	operationTimeouts                 OperationTimeouts
	sessionPostgres                   ownerPostgresOps
	sessionCommandGate                *semaphore.Weighted
	sessionWriteHighWater             map[string]time.Time
	sessionNow                        func() time.Time
	onboardingPostgres                onboardingPostgresOps
	ownerPostgres                     ownerPostgresOps
	clientPostgres                    ownerPostgresOps
	ownerMu                           sync.Mutex
	ownerWriteHighWater               map[string]time.Time
	ownerNow                          func() time.Time
	clientCommandGate                 *semaphore.Weighted
	clientWriteHighWater              map[string]time.Time
	pairingWriteHighWater             map[string]time.Time
	clientNow                         func() time.Time
	credentialPostgres                ownerPostgresOps
	credentialCommandGate             *semaphore.Weighted
	credentialWriteHighWater          map[string]time.Time
	credentialNow                     func() time.Time
	connectorPostgres                 ownerPostgresOps
	connectorCommandGate              *semaphore.Weighted
	connectorSettingWriteHighWater    map[string]time.Time
	notificationBindingWriteHighWater map[string]time.Time
	connectorNow                      func() time.Time
	conversationPostgres              ownerPostgresOps
	runPostgres                       ownerPostgresOps
	documentPostgres                  ownerPostgresOps
	// passiveNotificationRevs mirrors the memory backend's per-owner change
	// counter for SSE pollers. Process-local by design: the gateway is the
	// only writer of passive notifications, and callers only compare values
	// for equality, so a restart resetting it is harmless.
	passiveRevMu            sync.Mutex
	passiveNotificationRevs map[string]uint64
}

type ownerPostgresOps interface {
	Acquire(context.Context) (onboardingPostgresSession, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (onboardingPostgresRows, error)
	QueryRow(context.Context, string, ...any) onboardingPostgresRow
}

type pgxOwnerPostgresOps struct {
	pool *pgxpool.Pool
}

func (o pgxOwnerPostgresOps) Acquire(ctx context.Context) (onboardingPostgresSession, error) {
	return pgxOnboardingPostgresOps{pool: o.pool}.Acquire(ctx)
}

func (o pgxOwnerPostgresOps) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return o.pool.Exec(ctx, sql, arguments...)
}

func (o pgxOwnerPostgresOps) Query(ctx context.Context, sql string, arguments ...any) (onboardingPostgresRows, error) {
	return o.pool.Query(ctx, sql, arguments...)
}

func (o pgxOwnerPostgresOps) QueryRow(ctx context.Context, sql string, arguments ...any) onboardingPostgresRow {
	return o.pool.QueryRow(ctx, sql, arguments...)
}

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	return NewPostgresStoreWithOptions(ctx, dsn, defaultOperationTimeouts)
}

func NewPostgresStoreWithOptions(ctx context.Context, dsn string, timeouts OperationTimeouts) (*PostgresStore, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres state backend requires SPARKCLAW_STATE_DSN or SPARKCLAW_POSTGRES_DSN")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	st := &PostgresStore{
		db: pool, operationTimeouts: normalizeOperationTimeouts(timeouts),
		sessionPostgres:                   pgxOwnerPostgresOps{pool: pool},
		sessionCommandGate:                semaphore.NewWeighted(1),
		sessionWriteHighWater:             map[string]time.Time{},
		sessionNow:                        time.Now,
		onboardingPostgres:                pgxOnboardingPostgresOps{pool: pool},
		ownerPostgres:                     pgxOwnerPostgresOps{pool: pool},
		clientPostgres:                    pgxOwnerPostgresOps{pool: pool},
		clientCommandGate:                 semaphore.NewWeighted(1),
		ownerWriteHighWater:               map[string]time.Time{},
		ownerNow:                          time.Now,
		clientWriteHighWater:              map[string]time.Time{},
		pairingWriteHighWater:             map[string]time.Time{},
		clientNow:                         time.Now,
		credentialPostgres:                pgxOwnerPostgresOps{pool: pool},
		credentialCommandGate:             semaphore.NewWeighted(1),
		credentialWriteHighWater:          map[string]time.Time{},
		credentialNow:                     time.Now,
		connectorPostgres:                 pgxOwnerPostgresOps{pool: pool},
		connectorCommandGate:              semaphore.NewWeighted(1),
		connectorSettingWriteHighWater:    map[string]time.Time{},
		notificationBindingWriteHighWater: map[string]time.Time{},
		connectorNow:                      time.Now,
		conversationPostgres:              pgxOwnerPostgresOps{pool: pool},
		runPostgres:                       pgxOwnerPostgresOps{pool: pool},
		documentPostgres:                  pgxOwnerPostgresOps{pool: pool},
		passiveNotificationRevs:           map[string]uint64{},
	}
	if err := st.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := st.validateSessionState(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := st.validateClientState(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := st.validateCredentialState(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := st.validateConnectorState(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := st.seedDefaultOwner(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return st, nil
}

func (s *PostgresStore) Close() {
	s.db.Close()
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	startupCtx, cancel := postgresMigrationStartupContext(ctx)
	defer cancel()
	if err := runPostgresMigrations(startupCtx, s.db); err != nil {
		return fmt.Errorf("migrate postgres store: %w", err)
	}
	if err := s.normalizeMCPBindingSessions(startupCtx); err != nil {
		return fmt.Errorf("normalize MCP binding sessions: %w", err)
	}
	return nil
}

func (s *PostgresStore) normalizeMCPBindingSessions(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT payload FROM mcp_bindings`)
	if err != nil {
		return err
	}
	bindings := make([]app.MCPBinding, 0)
	for rows.Next() {
		var raw []byte
		var binding app.MCPBinding
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &binding); err != nil {
			rows.Close()
			return err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, binding := range bindings {
		if strings.TrimSpace(binding.LinkedSessionID) == "" {
			continue
		}
		createdAt := normalizeSessionTime(firstNonZeroTime(binding.CreatedAt, time.Now().UTC()))
		updatedAt := normalizeSessionTime(firstNonZeroTime(binding.UpdatedAt, createdAt))
		if _, err := s.db.Exec(ctx, `
			INSERT INTO sessions (id, owner_id, title, source, hidden, created_at, updated_at)
			VALUES ($1, $2, $3, 'mcp', false, $4, $5)
			ON CONFLICT (id) DO UPDATE SET owner_id=EXCLUDED.owner_id, title=EXCLUDED.title, source='mcp', hidden=false
		`, binding.LinkedSessionID, binding.OwnerID, mcpSessionTitle(binding.RequesterDeviceID), createdAt, updatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) seedDefaultOwner(ctx context.Context) error {
	startupCtx, cancel := postgresMigrationStartupContext(ctx)
	defer cancel()
	profile := app.DefaultOwnerProfile()
	profile.CreatedAt = profile.CreatedAt.UTC().Truncate(time.Microsecond)
	profile.UpdatedAt = profile.CreatedAt
	if _, err := s.ownerPostgres.Exec(startupCtx, `
		INSERT INTO owners (id, source, external_ref, workspace_root, default_channel, default_binding_id,
			display_name, email, preferences, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO NOTHING
	`, profile.ID, profile.Source, profile.ExternalRef, profile.WorkspaceRoot, profile.DefaultChannel,
		profile.DefaultBindingID, profile.DisplayName, profile.Email, mustJSON(profile.Preferences),
		profile.CreatedAt, profile.UpdatedAt); err != nil {
		return fmt.Errorf("seed default owner: %w", err)
	}
	confirmed, err := scanOwnerProfile(s.ownerPostgres.QueryRow(startupCtx, ownerProfileSelectSQL+` WHERE id=$1`, app.DefaultOwnerID))
	if err != nil {
		return fmt.Errorf("confirm default owner: %w", err)
	}
	if confirmed.ID != app.DefaultOwnerID {
		return errors.New("confirm default owner: invalid owner identity")
	}
	s.ownerWriteHighWater[confirmed.ID] = confirmed.UpdatedAt
	return nil
}

const ownerProfileSelectSQL = `SELECT id, source, external_ref, workspace_root, default_channel, default_binding_id,
	display_name, email, preferences, created_at, updated_at FROM owners`

func (s *PostgresStore) GetOwnerProfile(ctx context.Context) (app.OwnerProfile, error) {
	profile, found, err := s.getOwnerProfileByID(ctx, OperationOwnerProfileGet, app.DefaultOwnerID)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	if !found {
		return app.OwnerProfile{}, storeError(OperationOwnerProfileGet, StoreErrorCorrupt, errors.New("default owner profile is missing"))
	}
	return profile, nil
}

func (s *PostgresStore) UpdateOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	profile.ID = app.DefaultOwnerID
	return s.saveOwnerProfile(ctx, OperationOwnerProfileUpdate, profile)
}

func (s *PostgresStore) GetOwnerProfileByID(ctx context.Context, id string) (app.OwnerProfile, bool, error) {
	return s.getOwnerProfileByID(ctx, OperationOwnerProfileGetByID, id)
}

func (s *PostgresStore) getOwnerProfileByID(ctx context.Context, operation StoreOperation, id string) (app.OwnerProfile, bool, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	id = normalizeOwnerProfileID(id)
	session, err := s.ownerPostgres.Acquire(ctx)
	if err != nil {
		return app.OwnerProfile{}, false, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return app.OwnerProfile{}, false, classifyPostgresPreTransaction(operation, ctx, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerAdvisoryKey(id)); err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return app.OwnerProfile{}, false, classifyPostgresReadError(operation, ctx, err)
	}
	profile, err := scanOwnerProfile(transaction.QueryRow(ctx, ownerProfileSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := transaction.Commit(ctx); err != nil {
			release = false
			return app.OwnerProfile{}, false, classifyPostgresReadError(operation, ctx, errors.Join(err, session.Terminate(ctx)))
		}
		return app.OwnerProfile{}, false, nil
	}
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return app.OwnerProfile{}, false, classifyPostgresOwnerReadError(operation, ctx, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		return app.OwnerProfile{}, false, classifyPostgresReadError(operation, ctx, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneOwnerProfile(profile), true, nil
}

func (s *PostgresStore) SaveOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	return s.saveOwnerProfile(ctx, OperationOwnerProfileSave, profile)
}

func (s *PostgresStore) saveOwnerProfile(ctx context.Context, operation StoreOperation, profile app.OwnerProfile) (app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	profile.ID = normalizeOwnerProfileID(profile.ID)
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	session, err := s.ownerPostgres.Acquire(ctx)
	if err != nil {
		return app.OwnerProfile{}, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err != nil {
		return app.OwnerProfile{}, classifyPostgresPreTransaction(operation, ctx, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerAdvisoryKey(profile.ID)); err != nil {
		return app.OwnerProfile{}, finishPostgresOwnerPreCandidate(ctx, operation, session, transaction, &release, err)
	}
	current, err := scanOwnerProfile(transaction.QueryRow(ctx, ownerProfileSelectSQL+` WHERE id=$1`, profile.ID))
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return app.OwnerProfile{}, finishPostgresOwnerPreCandidate(ctx, operation, session, transaction, &release, err)
	}
	candidate := prepareOwnerProfile(profile, current, exists, s.ownerNow(), s.ownerWriteHighWater[profile.ID])
	s.ownerWriteHighWater[candidate.ID] = candidate.UpdatedAt
	if _, err := transaction.Exec(ctx, `
		INSERT INTO owners (id,source,external_ref,workspace_root,default_channel,default_binding_id,
			display_name,email,preferences,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO UPDATE SET source=EXCLUDED.source, external_ref=EXCLUDED.external_ref,
			workspace_root=EXCLUDED.workspace_root, default_channel=EXCLUDED.default_channel,
			default_binding_id=EXCLUDED.default_binding_id, display_name=EXCLUDED.display_name,
			email=EXCLUDED.email, preferences=EXCLUDED.preferences, updated_at=EXCLUDED.updated_at
	`, candidate.ID, candidate.Source, candidate.ExternalRef, candidate.WorkspaceRoot,
		candidate.DefaultChannel, candidate.DefaultBindingID, candidate.DisplayName, candidate.Email,
		mustJSON(candidate.Preferences), candidate.CreatedAt, candidate.UpdatedAt); err != nil {
		return finishPostgresOwnerStatement(ctx, operation, candidate, session, transaction, &release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields)
		VALUES ($1,$2,'owner_profile.updated',NULL,NULL,'owner',$3,$4)
	`, app.NewID("audit"), candidate.UpdatedAt, candidate.DisplayName, optionalJSON(ownerProfileAuditFields(candidate))); err != nil {
		return finishPostgresOwnerStatement(ctx, operation, candidate, session, transaction, &release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO events (id,happened_at,type,session_id,run_id,payload)
		VALUES ($1,$2,'owner_profile.updated',NULL,NULL,$3)
	`, app.NewID("evt"), candidate.UpdatedAt, mustJSON(candidate)); err != nil {
		return finishPostgresOwnerStatement(ctx, operation, candidate, session, transaction, &release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		return candidate, storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneOwnerProfile(candidate), nil
}

func finishPostgresOwnerPreCandidate(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	var postgresError *pgconn.PgError
	definite := errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) || errors.Is(cause, errOwnerPreferencesDecode)
	if !definite {
		*release = false
		return storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
	}
	cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
	if errors.Is(cause, errOwnerPreferencesDecode) {
		return storeError(operation, StoreErrorCorrupt, cause)
	}
	if postgresError != nil {
		return storeError(operation, StoreErrorInternal, cause)
	}
	return classifyPostgresPreTransaction(operation, ctx, cause)
}

func finishPostgresOwnerStatement(ctx context.Context, operation StoreOperation, candidate app.OwnerProfile, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (app.OwnerProfile, error) {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		rollbackErr := transaction.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			*release = false
			rollbackErr = errors.Join(rollbackErr, session.Terminate(ctx))
		}
		joined := errors.Join(cause, rollbackErr)
		if postgresError != nil {
			return app.OwnerProfile{}, storeError(operation, StoreErrorInternal, joined)
		}
		return app.OwnerProfile{}, classifyPostgresPreTransaction(operation, ctx, joined)
	}
	*release = false
	return candidate, storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func (s *PostgresStore) ListOwnerProfiles(ctx context.Context) ([]app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileList, s.operationTimeouts)
	defer cancel()
	rows, err := s.ownerPostgres.Query(ctx, ownerProfileSelectSQL+` ORDER BY updated_at DESC, id ASC`)
	if err != nil {
		return nil, classifyPostgresReadError(OperationOwnerProfileList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.OwnerProfile, 0)
	for rows.Next() {
		profile, err := scanOwnerProfile(rows)
		if err != nil {
			return nil, classifyPostgresOwnerReadError(OperationOwnerProfileList, ctx, err)
		}
		out = append(out, cloneOwnerProfile(profile))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(OperationOwnerProfileList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) FindOwnerProfileByExternalRef(ctx context.Context, source, externalRef string) (app.OwnerProfile, bool, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileFindExternalRef, s.operationTimeouts)
	defer cancel()
	source = strings.TrimSpace(source)
	externalRef = strings.TrimSpace(externalRef)
	if source == "" || externalRef == "" {
		return app.OwnerProfile{}, false, nil
	}
	profile, err := scanOwnerProfile(s.ownerPostgres.QueryRow(ctx, ownerProfileSelectSQL+`
		WHERE source=$1 AND external_ref=$2 ORDER BY updated_at DESC, id ASC LIMIT 1`, source, externalRef))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.OwnerProfile{}, false, nil
	}
	if err != nil {
		return app.OwnerProfile{}, false, classifyPostgresOwnerReadError(OperationOwnerProfileFindExternalRef, ctx, err)
	}
	return cloneOwnerProfile(profile), true, nil
}

func ownerAdvisoryKey(id string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/owner/v1\x00" + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func (s *PostgresStore) AddMessage(ctx context.Context, message app.Message) (app.Message, error) {
	ctx, cancel := operationContext(ctx, OperationConversationAddMessage, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationAddMessage, ctx); err != nil {
		return app.Message{}, err
	}
	candidate, err := prepareMessage(message, time.Now())
	if err != nil {
		return app.Message{}, storeError(OperationConversationAddMessage, StoreErrorInvalid, err)
	}
	if existing, err := scanMessage(s.conversationPostgres.QueryRow(ctx, `
		SELECT id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at
		FROM messages WHERE id = $1
	`, candidate.ID)); err == nil {
		return cloneMessage(existing), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return app.Message{}, classifyConversationPostgresError(OperationConversationAddMessage, ctx, err)
	}

	session, transaction, release, err := beginPostgresTransaction(ctx, OperationConversationAddMessage, s.conversationPostgres)
	if err != nil {
		return app.Message{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	current, err := scanSession(transaction.QueryRow(ctx, sessionSelectSQL+` WHERE id=$1 FOR UPDATE`, candidate.SessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Message{}, conversationBusinessError(ctx, OperationConversationAddMessage, StoreErrorNotFound, session, transaction, release, errors.New("message session not found"))
	}
	if err != nil {
		return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, candidate, session, transaction, release, err)
	}
	if err := validatePersistedSession(candidate.SessionID, current); err != nil {
		return app.Message{}, conversationBusinessError(ctx, OperationConversationAddMessage, StoreErrorCorrupt, session, transaction, release, err)
	}
	stored, err := scanMessage(transaction.QueryRow(ctx, `
		INSERT INTO messages (id, session_id, run_id, role, content, attachments, requested_media, created_at)
		VALUES ($1, $2, nullif($3, ''), $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING
		RETURNING id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at
	`, candidate.ID, candidate.SessionID, candidate.RunID, candidate.Role, candidate.Content,
		mustJSON(candidate.Attachments), mustJSON(candidate.RequestedMedia), candidate.CreatedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, readErr := scanMessage(transaction.QueryRow(ctx, `
			SELECT id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at
			FROM messages WHERE id = $1
		`, candidate.ID))
		if readErr != nil {
			return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, candidate, session, transaction, release, readErr)
		}
		if rollbackErr := rollbackPostgresTransaction(ctx, session, transaction, release, nil); rollbackErr != nil {
			return app.Message{}, classifyConversationPostgresError(OperationConversationAddMessage, ctx, rollbackErr)
		}
		return cloneMessage(existing), nil
	}
	if err != nil {
		return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, candidate, session, transaction, release, err)
	}
	current.UpdatedAt = nextSessionTime(stored.CreatedAt, current.UpdatedAt)
	if !current.Hidden && (current.Title == "" || current.Title == "New SparkClaw Session") {
		current.Title = deriveTitle(stored.Content)
	}
	if _, err := transaction.Exec(ctx, `UPDATE sessions SET title=$2, updated_at=$3 WHERE id=$1`, current.ID, current.Title, current.UpdatedAt); err != nil {
		return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, stored, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, 'message.created', $3, nullif($4, ''), $5)
	`, app.NewID("evt"), normalizeSessionTime(time.Now()), stored.SessionID, stored.RunID, mustJSON(stored)); err != nil {
		return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, stored, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return cloneMessage(stored), storeError(OperationConversationAddMessage, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneMessage(stored), nil
}

func (s *PostgresStore) ListMessages(ctx context.Context, sessionID string) ([]app.Message, error) {
	ctx, cancel := operationContext(ctx, OperationConversationListMessages, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationListMessages, ctx); err != nil {
		return nil, err
	}
	rows, err := s.conversationPostgres.Query(ctx, `
		SELECT id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at
		FROM messages
		WHERE session_id = $1
		ORDER BY created_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyConversationPostgresError(OperationConversationListMessages, ctx, err)
	}
	defer rows.Close()
	out := make([]app.Message, 0)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, classifyConversationPostgresError(OperationConversationListMessages, ctx, err)
		}
		out = append(out, cloneMessage(message))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyConversationPostgresError(OperationConversationListMessages, ctx, err)
	}
	return out, nil
}

func beginPostgresTransaction(ctx context.Context, operation StoreOperation, backend ownerPostgresOps) (onboardingPostgresSession, onboardingPostgresTx, *bool, error) {
	session, err := backend.Acquire(ctx)
	if err != nil {
		return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err == nil {
		return session, transaction, &release, nil
	}
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

func rollbackPostgresTransaction(ctx context.Context, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	if rollbackErr := transaction.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
		*release = false
		return errors.Join(cause, rollbackErr, session.Terminate(ctx))
	}
	return cause
}

func finishConversationStatement(ctx context.Context, operation StoreOperation, _ app.Message, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyConversationPostgresError(operation, ctx, cause)
}

func conversationBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func classifyConversationPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errMessageJSONDecode) {
		return storeError(operation, StoreErrorCorrupt, cause)
	}
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return contextStoreError(operation, ctx, cause)
	}
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) {
		return storeError(operation, StoreErrorInternal, cause)
	}
	return storeError(operation, StoreErrorUnavailable, cause)
}

func (s *PostgresStore) SaveRunFeedback(ctx context.Context, feedback app.RunFeedback) (app.RunFeedback, error) {
	ctx, cancel := operationContext(ctx, OperationRunFeedbackSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunFeedbackSave, ctx); err != nil {
		return app.RunFeedback{}, err
	}
	feedback, err := prepareRunFeedback(feedback, nil, time.Now().UTC())
	if err != nil {
		return app.RunFeedback{}, storeError(OperationRunFeedbackSave, StoreErrorInvalid, err)
	}
	session, err := s.runPostgres.Acquire(ctx)
	if err != nil {
		return app.RunFeedback{}, classifyPostgresPreTransaction(OperationRunFeedbackSave, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err != nil {
		return app.RunFeedback{}, classifyPostgresPreTransaction(OperationRunFeedbackSave, ctx, err)
	}
	lockID := feedback.ID
	if feedback.MessageID != "" {
		lockID = feedback.RunID + "\x00message\x00" + feedback.MessageID
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, runAdvisoryKey("run_feedback", lockID)); err != nil {
		return app.RunFeedback{}, finishRunPostgresPreCandidate(ctx, OperationRunFeedbackSave, session, transaction, &release, err)
	}
	var existing *app.RunFeedback
	current, err := scanRunFeedback(transaction.QueryRow(ctx, `
		SELECT id, session_id, run_id, coalesce(message_id, ''), rating, note, correction, created_at, updated_at
		FROM run_feedback
		WHERE id = $1 OR (run_id = $2 AND $3 <> '' AND message_id = $3)
		ORDER BY CASE WHEN id = $1 THEN 0 ELSE 1 END
		LIMIT 1
	`, feedback.ID, feedback.RunID, feedback.MessageID))
	if err == nil {
		existing = &current
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return app.RunFeedback{}, finishRunPostgresPreCandidate(ctx, OperationRunFeedbackSave, session, transaction, &release, err)
	}
	feedback, err = prepareRunFeedback(feedback, existing, time.Now().UTC())
	if err != nil {
		cause := rollbackPostgresTransaction(ctx, session, transaction, &release, err)
		return app.RunFeedback{}, storeError(OperationRunFeedbackSave, StoreErrorInvalid, cause)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO run_feedback (id, session_id, run_id, message_id, rating, note, correction, created_at, updated_at)
		VALUES ($1, $2, $3, nullif($4, ''), $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET session_id=EXCLUDED.session_id, run_id=EXCLUDED.run_id,
			message_id=EXCLUDED.message_id, rating=EXCLUDED.rating, note=EXCLUDED.note,
			correction=EXCLUDED.correction, updated_at=EXCLUDED.updated_at
	`, feedback.ID, feedback.SessionID, feedback.RunID, feedback.MessageID, feedback.Rating, feedback.Note, feedback.Correction, feedback.CreatedAt, feedback.UpdatedAt); err != nil {
		return finishRunPostgresStatement(ctx, OperationRunFeedbackSave, feedback, session, transaction, &release, err)
	}
	if err := appendRunLifecycle(transaction, ctx, "run_feedback.saved", feedback.SessionID, feedback.RunID, "owner", feedback.Rating, map[string]any{
		"feedback_id": feedback.ID, "message_id": feedback.MessageID,
		"has_note": feedback.Note != "", "has_correction": feedback.Correction != "",
	}, feedback); err != nil {
		return finishRunPostgresStatement(ctx, OperationRunFeedbackSave, feedback, session, transaction, &release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		return feedback, storeError(OperationRunFeedbackSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return feedback, nil
}

func (s *PostgresStore) ListRunFeedback(ctx context.Context, runID string) ([]app.RunFeedback, error) {
	ctx, cancel := operationContext(ctx, OperationRunFeedbackList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunFeedbackList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, session_id, run_id, coalesce(message_id, ''), rating, note, correction, created_at, updated_at
		FROM run_feedback
		WHERE $1 = '' OR run_id = $1
		ORDER BY updated_at DESC, id ASC
	`, runID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationRunFeedbackList, ctx, err)
	}
	defer rows.Close()
	out := []app.RunFeedback{}
	for rows.Next() {
		feedback, err := scanRunFeedback(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationRunFeedbackList, ctx, err)
		}
		out = append(out, feedback)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationRunFeedbackList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SaveRun(ctx context.Context, run app.AgentRun) (app.AgentRun, error) {
	run, err := prepareRun(run, time.Now().UTC())
	if err != nil {
		return app.AgentRun{}, storeError(OperationRunSave, StoreErrorInvalid, err)
	}
	workflowState := optionalJSON(run.Workflow)
	messageContext := optionalJSON(run.MessageContext)
	return runPostgresWrite(s, ctx, OperationRunSave, "run", run.ID, run, func(transaction onboardingPostgresTx, commandCtx context.Context) error {
		if _, err := transaction.Exec(commandCtx, `
			INSERT INTO agent_runs (id, session_id, state, model_lane, risk_level, summary, workflow_state, message_context, started_at, completed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO UPDATE SET state=EXCLUDED.state, model_lane=EXCLUDED.model_lane,
				risk_level=EXCLUDED.risk_level, summary=EXCLUDED.summary, workflow_state=EXCLUDED.workflow_state,
				message_context=EXCLUDED.message_context, started_at=EXCLUDED.started_at, completed_at=EXCLUDED.completed_at
		`, run.ID, run.SessionID, run.State, run.ModelLane, string(run.Risk), run.Summary, workflowState, messageContext, run.StartedAt, run.CompletedAt); err != nil {
			return err
		}
		return appendRunEvent(transaction, commandCtx, "run."+run.State, run.SessionID, run.ID, run)
	})
}

func (s *PostgresStore) GetRun(ctx context.Context, id string) (app.AgentRun, bool, error) {
	ctx, cancel := operationContext(ctx, OperationRunGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunGet, ctx); err != nil {
		return app.AgentRun{}, false, err
	}
	row := s.runPostgres.QueryRow(ctx, `
		SELECT id, session_id, state, model_lane, risk_level, started_at, completed_at, coalesce(summary, ''), workflow_state, message_context
		FROM agent_runs
		WHERE id = $1
	`, id)
	run, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.AgentRun{}, false, nil
	}
	if err != nil {
		return app.AgentRun{}, false, classifyRunPostgresReadError(OperationRunGet, ctx, err)
	}
	return run, true, nil
}

func (s *PostgresStore) ListRuns(ctx context.Context, sessionID string) ([]app.AgentRun, error) {
	ctx, cancel := operationContext(ctx, OperationRunList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, session_id, state, model_lane, risk_level, started_at, completed_at, coalesce(summary, ''), workflow_state, message_context
		FROM agent_runs
		WHERE $1 = '' OR session_id = $1
		ORDER BY started_at DESC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationRunList, ctx, err)
	}
	defer rows.Close()
	out := []app.AgentRun{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationRunList, ctx, err)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationRunList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SaveModelCall(ctx context.Context, call app.ModelCall) (app.ModelCall, error) {
	call, err := prepareModelCall(call, time.Now().UTC())
	if err != nil {
		return app.ModelCall{}, storeError(OperationModelCallSave, StoreErrorInvalid, err)
	}
	return runPostgresWrite(s, ctx, OperationModelCallSave, "model_call", call.ID, call, func(transaction onboardingPostgresTx, commandCtx context.Context) error {
		if _, err := transaction.Exec(commandCtx, `
			INSERT INTO model_calls (id, session_id, run_id, lane, profile, model, operation, mock, fallback, status,
				prompt_tokens, response_tokens, total_tokens, latency_ms, error, started_at, completed_at)
			VALUES ($1, nullif($2, ''), nullif($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, nullif($15, ''), $16, $17)
			ON CONFLICT (id) DO UPDATE SET session_id=EXCLUDED.session_id, run_id=EXCLUDED.run_id,
				lane=EXCLUDED.lane, profile=EXCLUDED.profile, model=EXCLUDED.model, operation=EXCLUDED.operation,
				mock=EXCLUDED.mock, fallback=EXCLUDED.fallback, status=EXCLUDED.status,
				prompt_tokens=EXCLUDED.prompt_tokens, response_tokens=EXCLUDED.response_tokens,
				total_tokens=EXCLUDED.total_tokens, latency_ms=EXCLUDED.latency_ms, error=EXCLUDED.error,
				started_at=EXCLUDED.started_at, completed_at=EXCLUDED.completed_at
		`, call.ID, call.SessionID, call.RunID, call.Lane, call.Profile, call.Model, call.Operation, call.Mock, call.Fallback, call.Status,
			call.PromptTokens, call.ResponseTokens, call.TotalTokens, call.LatencyMS, call.Error, call.StartedAt, call.CompletedAt); err != nil {
			return err
		}
		return appendRunLifecycle(transaction, commandCtx, "model_call."+call.Status, call.SessionID, call.RunID, "model-router", call.Model, map[string]any{
			"lane": call.Lane, "profile": call.Profile, "operation": call.Operation, "latency_ms": call.LatencyMS,
		}, call)
	})
}

func (s *PostgresStore) ListModelCalls(ctx context.Context, sessionID, runID string) ([]app.ModelCall, error) {
	ctx, cancel := operationContext(ctx, OperationModelCallList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationModelCallList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), lane, profile, model, operation, mock, fallback,
			status, prompt_tokens, response_tokens, total_tokens, latency_ms, coalesce(error, ''), started_at, completed_at
		FROM model_calls
		WHERE ($1 = '' OR session_id = $1) AND ($2 = '' OR run_id = $2)
		ORDER BY started_at ASC, id ASC
	`, sessionID, runID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationModelCallList, ctx, err)
	}
	defer rows.Close()
	out := []app.ModelCall{}
	for rows.Next() {
		call, err := scanModelCall(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationModelCallList, ctx, err)
		}
		out = append(out, call)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationModelCallList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SaveToolCall(ctx context.Context, call app.ToolCall) (app.ToolCall, error) {
	call, err := prepareToolCall(call, time.Now().UTC())
	if err != nil {
		return app.ToolCall{}, storeError(OperationToolCallSave, StoreErrorInvalid, err)
	}
	args := mustJSON(call.Arguments)
	result := optionalJSON(call.Result)
	policyContext := optionalJSON(call.PolicyContext)
	return runPostgresWrite(s, ctx, OperationToolCallSave, "tool_call", call.ID, call, func(transaction onboardingPostgresTx, commandCtx context.Context) error {
		if _, err := transaction.Exec(commandCtx, `
			INSERT INTO tool_calls (id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
				tool, risk_level, status, arguments, result, error, error_code, approval_id, observation_ref,
				observation_summary, started_at, completed_at, policy_context)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, nullif($13, ''), nullif($14, ''), nullif($15, ''), nullif($16, ''), $17, $18, $19, $20)
			ON CONFLICT (id) DO UPDATE SET workflow_id=EXCLUDED.workflow_id, workflow_node_id=EXCLUDED.workflow_node_id,
				scope_revision=EXCLUDED.scope_revision, capability=EXCLUDED.capability, risk_level=EXCLUDED.risk_level,
				status=EXCLUDED.status, arguments=EXCLUDED.arguments, result=EXCLUDED.result, error=EXCLUDED.error,
				error_code=EXCLUDED.error_code, approval_id=EXCLUDED.approval_id,
				observation_ref=EXCLUDED.observation_ref, observation_summary=EXCLUDED.observation_summary,
				started_at=EXCLUDED.started_at, completed_at=EXCLUDED.completed_at, policy_context=EXCLUDED.policy_context
		`, call.ID, call.SessionID, call.RunID, string(call.WorkflowID), string(call.WorkflowNodeID), call.ScopeRevision, call.Capability,
			call.Tool, string(call.Risk), call.Status, args, result, call.Error, call.ErrorCode, call.ApprovalID, call.ObservationRef, call.ObservationSummary, call.StartedAt, call.CompletedAt, policyContext); err != nil {
			return err
		}
		return appendRunLifecycle(transaction, commandCtx, "tool_call."+call.Status, call.SessionID, call.RunID, "agent", call.Tool, map[string]any{
			"risk": call.Risk, "id": call.ID,
		}, call)
	})
}

func (s *PostgresStore) GetToolCall(ctx context.Context, id string) (app.ToolCall, bool, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallGet, ctx); err != nil {
		return app.ToolCall{}, false, err
	}
	row := s.runPostgres.QueryRow(ctx, `
		SELECT id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
			tool, risk_level, status, arguments, result, coalesce(error, ''), coalesce(error_code, ''),
			coalesce(approval_id, ''), started_at, completed_at, coalesce(observation_ref, ''), coalesce(observation_summary, ''), policy_context
		FROM tool_calls
		WHERE id = $1
	`, id)
	call, err := scanToolCall(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ToolCall{}, false, nil
	}
	if err != nil {
		return app.ToolCall{}, false, classifyRunPostgresReadError(OperationToolCallGet, ctx, err)
	}
	return call, true, nil
}

func (s *PostgresStore) ListToolCalls(ctx context.Context, sessionID string) ([]app.ToolCall, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
			tool, risk_level, status, arguments, result, coalesce(error, ''), coalesce(error_code, ''),
			coalesce(approval_id, ''), started_at, completed_at, coalesce(observation_ref, ''), coalesce(observation_summary, ''), policy_context
		FROM tool_calls
		WHERE $1 = '' OR session_id = $1
		ORDER BY started_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationToolCallList, ctx, err)
	}
	defer rows.Close()
	out := []app.ToolCall{}
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationToolCallList, ctx, err)
		}
		out = append(out, call)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationToolCallList, ctx, err)
	}
	return out, nil
}

func runPostgresWrite[T any](s *PostgresStore, parent context.Context, operation StoreOperation, kind, id string, candidate T, command func(onboardingPostgresTx, context.Context) error) (T, error) {
	var zero T
	ctx, cancel := operationContext(parent, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return zero, err
	}
	session, err := s.runPostgres.Acquire(ctx)
	if err != nil {
		return zero, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err != nil {
		return zero, classifyPostgresPreTransaction(operation, ctx, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, runAdvisoryKey(kind, id)); err != nil {
		return zero, finishRunPostgresPreCandidate(ctx, operation, session, transaction, &release, err)
	}
	if err := command(transaction, ctx); err != nil {
		return finishRunPostgresStatement(ctx, operation, candidate, session, transaction, &release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		return candidate, storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func finishRunPostgresPreCandidate(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
		if postgresError != nil {
			return storeError(operation, StoreErrorInternal, cause)
		}
		return classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func finishRunPostgresStatement[T any](ctx context.Context, operation StoreOperation, candidate T, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (T, error) {
	var zero T
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
		if postgresError != nil {
			return zero, storeError(operation, StoreErrorInternal, cause)
		}
		return zero, classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return candidate, storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func appendRunLifecycle(transaction onboardingPostgresTx, ctx context.Context, typ, sessionID, runID, actor, summary string, fields map[string]any, payload any) error {
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), time.Now().UTC(), typ, sessionID, runID, actor, summary, optionalJSON(fields)); err != nil {
		return err
	}
	return appendRunEvent(transaction, ctx, typ, sessionID, runID, payload)
}

func appendRunEvent(transaction onboardingPostgresTx, ctx context.Context, typ, sessionID, runID string, payload any) error {
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), time.Now().UTC(), typ, sessionID, runID, mustJSON(payload))
	return err
}

func classifyRunPostgresReadError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errRunJSONDecode) {
		return storeError(operation, StoreErrorCorrupt, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}

func runAdvisoryKey(kind, id string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/run/v1\x00" + kind + "\x00" + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func (s *PostgresStore) SaveDocumentRecord(ctx context.Context, record app.DocumentRecord) (app.DocumentRecord, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordSave, ctx); err != nil {
		return app.DocumentRecord{}, err
	}
	if record.ID == "" {
		record.ID = app.NewID("doc")
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationDocumentRecordSave, s.documentPostgres)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()

	record = prepareDocumentRecord(record, nil, time.Now())
	var persistedCreatedAt time.Time
	if err := transaction.QueryRow(ctx, `
		INSERT INTO document_records (
			id, owner_id, session_id, governed_path, name, content_type, format,
			size_bytes, sha256, status, source, source_message_id, source_run_id,
			source_tool_call_id, parent_document_id, last_activity, last_activity_id,
			last_activity_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19, $20
		)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			session_id = EXCLUDED.session_id,
			governed_path = EXCLUDED.governed_path,
			name = EXCLUDED.name,
			content_type = EXCLUDED.content_type,
			format = EXCLUDED.format,
			size_bytes = EXCLUDED.size_bytes,
			sha256 = EXCLUDED.sha256,
			status = EXCLUDED.status,
			source = EXCLUDED.source,
			source_message_id = EXCLUDED.source_message_id,
			source_run_id = EXCLUDED.source_run_id,
			source_tool_call_id = EXCLUDED.source_tool_call_id,
			parent_document_id = EXCLUDED.parent_document_id,
			last_activity = EXCLUDED.last_activity,
			last_activity_id = EXCLUDED.last_activity_id,
			last_activity_at = EXCLUDED.last_activity_at,
			updated_at = EXCLUDED.updated_at
		RETURNING created_at
	`, record.ID, record.OwnerID, record.SessionID, record.GovernedPath, record.Name,
		record.ContentType, record.Format, record.SizeBytes, record.SHA256, record.Status,
		record.Source, record.SourceMessageID, record.SourceRunID, record.SourceToolCallID,
		record.ParentDocumentID, record.LastActivity, record.LastActivityID,
		record.LastActivityAt, record.CreatedAt, record.UpdatedAt).Scan(&persistedCreatedAt); err != nil {
		return app.DocumentRecord{}, finishDocumentPostgresStatement(ctx, session, transaction, release, err)
	}
	record.CreatedAt = normalizeDocumentTime(persistedCreatedAt)
	if err := appendDocumentLifecycle(transaction, ctx, record); err != nil {
		return app.DocumentRecord{}, finishDocumentPostgresStatement(ctx, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return record, storeError(OperationDocumentRecordSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return record, nil
}

func appendDocumentLifecycle(transaction onboardingPostgresTx, ctx context.Context, record app.DocumentRecord) error {
	fields := map[string]any{
		"document_id": record.ID,
		"path":        record.GovernedPath,
		"activity_id": record.LastActivityID,
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, 'document.saved', nullif($3, ''), nullif($4, ''), 'document_registry', $5, $6)
	`, app.NewID("audit"), normalizeDocumentTime(time.Now()), record.SessionID, record.SourceRunID, record.LastActivity, optionalJSON(fields)); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, 'document.saved', nullif($3, ''), nullif($4, ''), $5)
	`, app.NewID("evt"), normalizeDocumentTime(time.Now()), record.SessionID, record.SourceRunID, mustJSON(record))
	return err
}

func (s *PostgresStore) GetDocumentRecord(ctx context.Context, id string) (app.DocumentRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordGet, ctx); err != nil {
		return app.DocumentRecord{}, false, err
	}
	row := s.documentPostgres.QueryRow(ctx, `
		SELECT id, owner_id, session_id, governed_path, name, content_type, format,
			size_bytes, sha256, status, source, source_message_id, source_run_id,
			source_tool_call_id, parent_document_id, last_activity, last_activity_id,
			last_activity_at, created_at, updated_at
		FROM document_records
		WHERE id = $1
	`, id)
	record, err := scanDocumentRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.DocumentRecord{}, false, nil
	}
	if err != nil {
		return app.DocumentRecord{}, false, classifyDocumentPostgresError(OperationDocumentRecordGet, ctx, err)
	}
	return record, true, nil
}

func (s *PostgresStore) ListDocumentRecords(ctx context.Context, ownerID, sessionID string, limit int) ([]app.DocumentRecord, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordList, ctx); err != nil {
		return nil, err
	}
	limit = normalizeDocumentRecordLimit(limit)
	rows, err := s.documentPostgres.Query(ctx, `
		SELECT id, owner_id, session_id, governed_path, name, content_type, format,
			size_bytes, sha256, status, source, source_message_id, source_run_id,
			source_tool_call_id, parent_document_id, last_activity, last_activity_id,
			last_activity_at, created_at, updated_at
		FROM document_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR session_id = $2)
		ORDER BY last_activity_at DESC, updated_at DESC, id ASC
		LIMIT $3
	`, ownerID, sessionID, limit)
	if err != nil {
		return nil, classifyDocumentPostgresError(OperationDocumentRecordList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.DocumentRecord, 0)
	for rows.Next() {
		record, err := scanDocumentRecord(rows)
		if err != nil {
			return nil, classifyDocumentPostgresError(OperationDocumentRecordList, ctx, err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDocumentPostgresError(OperationDocumentRecordList, ctx, err)
	}
	return out, nil
}

func finishDocumentPostgresStatement(ctx context.Context, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyDocumentPostgresError(OperationDocumentRecordSave, ctx, cause)
}

func classifyDocumentPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return contextStoreError(operation, ctx, cause)
	}
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) {
		return storeError(operation, StoreErrorInternal, cause)
	}
	return storeError(operation, StoreErrorUnavailable, cause)
}

func (s *PostgresStore) SaveApproval(approval app.Approval) {
	ctx := context.Background()
	approval = normalizeApproval(approval)
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO approvals (
			id, source, external_id, external_context, session_id, run_id, tool_call_id,
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, resolution_note, policy_context
		)
		VALUES ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''), $8,
			$9, $10, $11, $12, $13, $14, $15, $16, nullif($17, ''), $18)
		ON CONFLICT (id) DO UPDATE SET
			source = EXCLUDED.source,
			external_id = EXCLUDED.external_id,
			external_context = EXCLUDED.external_context,
			status = EXCLUDED.status,
			summary = EXCLUDED.summary,
			reason = EXCLUDED.reason,
			resources = EXCLUDED.resources,
			arguments = EXCLUDED.arguments,
			resolved_at = EXCLUDED.resolved_at,
			resolution_note = EXCLUDED.resolution_note,
			policy_context = EXCLUDED.policy_context
	`, approval.ID, string(approval.Source), approval.ExternalID, mustJSON(approval.ExternalContext), approval.SessionID, approval.RunID, approval.ToolCallID, approval.Tool, string(approval.Risk), approval.Status, approval.Summary, approval.Reason, mustJSON(approval.Resources), mustJSON(approval.Arguments), approval.CreatedAt, approval.ResolvedAt, approval.ResolutionNote, optionalJSON(approval.PolicyContext))
	actor := "policy"
	if approval.Source != app.ApprovalSourceTool {
		actor = "integration"
	}
	s.appendAudit(ctx, "approval."+approval.Status, approval.SessionID, approval.RunID, actor, approval.Summary, map[string]any{
		"tool": approval.Tool,
		"risk": approval.Risk,
	})
	s.appendEvent(ctx, "approval."+approval.Status, approval.SessionID, approval.RunID, approval)
}

func (s *PostgresStore) GetApproval(id string) (app.Approval, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, source, external_id, external_context,
			coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, coalesce(resolution_note, ''), policy_context
		FROM approvals
		WHERE id = $1
	`, id)
	approval, err := scanApproval(row)
	return approval, err == nil
}

func (s *PostgresStore) FindApprovalByExternalRef(source app.ApprovalSource, externalID string) (app.Approval, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, source, external_id, external_context,
			coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, coalesce(resolution_note, ''), policy_context
		FROM approvals
		WHERE source = $1 AND external_id = $2
	`, source, externalID)
	approval, err := scanApproval(row)
	return approval, err == nil
}

func (s *PostgresStore) UpdatePendingApproval(approval app.Approval) (app.Approval, error) {
	approval = normalizeApproval(approval)
	approval.Status = "pending"
	approval.ResolvedAt = nil
	approval.ResolutionNote = ""
	command, err := s.db.Exec(context.Background(), `
		UPDATE approvals SET
			source = $2, external_id = $3, external_context = $4, summary = $5,
			reason = $6, resources = $7, arguments = $8
		WHERE id = $1 AND status = 'pending'
	`, approval.ID, string(approval.Source), approval.ExternalID, mustJSON(approval.ExternalContext),
		approval.Summary, approval.Reason, mustJSON(approval.Resources), mustJSON(approval.Arguments))
	if err != nil {
		return app.Approval{}, err
	}
	if command.RowsAffected() == 0 {
		if _, ok := s.GetApproval(approval.ID); !ok {
			return app.Approval{}, errors.New("approval not found")
		}
		return app.Approval{}, errors.New("approval already resolved")
	}
	s.appendEvent(context.Background(), "approval.pending", approval.SessionID, approval.RunID, approval)
	return approval, nil
}

func (s *PostgresStore) ResolveApproval(id, status, note string) (app.Approval, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.Approval{}, err
	}
	defer rollbackTx(ctx, tx)
	row := tx.QueryRow(ctx, `
		SELECT id, source, external_id, external_context,
			coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, coalesce(resolution_note, ''), policy_context
		FROM approvals
		WHERE id = $1
		FOR UPDATE
	`, id)
	approval, err := scanApproval(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Approval{}, errors.New("approval not found")
		}
		return app.Approval{}, err
	}
	if approval.Status != "pending" {
		return app.Approval{}, errors.New("approval already resolved")
	}
	now := time.Now().UTC()
	approval.Status = status
	approval.ResolvedAt = &now
	approval.ResolutionNote = note
	if _, err := tx.Exec(ctx, `
		UPDATE approvals
		SET status = $2, resolved_at = $3, resolution_note = nullif($4, '')
		WHERE id = $1
	`, approval.ID, approval.Status, approval.ResolvedAt, approval.ResolutionNote); err != nil {
		return app.Approval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Approval{}, err
	}
	actor := "owner"
	if status == "resolved_elsewhere" {
		actor = "integration"
	}
	s.appendAudit(ctx, "approval."+status, approval.SessionID, approval.RunID, actor, approval.Summary, map[string]any{"note": note})
	s.appendEvent(ctx, "approval."+status, approval.SessionID, approval.RunID, approval)
	return approval, nil
}

func (s *PostgresStore) ListApprovals(status string) []app.Approval {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, source, external_id, external_context,
			coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, coalesce(resolution_note, ''), policy_context
		FROM approvals
		WHERE $1 = '' OR status = $1
		ORDER BY created_at DESC
	`, status)
	if err != nil {
		return []app.Approval{}
	}
	defer rows.Close()
	return collectRows(rows, scanApproval)
}

func (s *PostgresStore) SaveReminder(reminder app.Reminder) app.Reminder {
	now := time.Now().UTC()
	if reminder.ID == "" {
		reminder.ID = app.NewID("rem")
	}
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = now
	}
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = now
	}
	if reminder.Status == "" {
		reminder.Status = "pending"
	}
	if reminder.TextSummary == "" {
		reminder.TextSummary = summarizeReminderText(reminder.Text)
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO reminders (
			id, session_id, run_id, text, text_summary, due_time, timezone, channel, recipient,
			recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status,
			last_delivery_id, last_error, created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		)
		VALUES ($1, nullif($2, ''), nullif($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		ON CONFLICT (id) DO UPDATE SET
			text = EXCLUDED.text,
			text_summary = EXCLUDED.text_summary,
			due_time = EXCLUDED.due_time,
			timezone = EXCLUDED.timezone,
			channel = EXCLUDED.channel,
			recipient = EXCLUDED.recipient,
			recipient_binding = EXCLUDED.recipient_binding,
			binding_id = EXCLUDED.binding_id,
			credential_ref = EXCLUDED.credential_ref,
			base_url = EXCLUDED.base_url,
			recurrence = EXCLUDED.recurrence,
			dedupe_key = EXCLUDED.dedupe_key,
			status = EXCLUDED.status,
			last_delivery_id = EXCLUDED.last_delivery_id,
			last_error = EXCLUDED.last_error,
			updated_at = EXCLUDED.updated_at,
			sent_at = EXCLUDED.sent_at,
			canceled_at = EXCLUDED.canceled_at,
			delivery_attempt = EXCLUDED.delivery_attempt,
			schedule_spec = EXCLUDED.schedule_spec
	`, reminder.ID, reminder.SessionID, reminder.RunID, reminder.Text, reminder.TextSummary, reminder.DueTime, reminder.Timezone, reminder.Channel, reminder.Recipient,
		reminder.RecipientBinding, reminder.BindingID, reminder.CredentialRef, reminder.BaseURL, reminder.Recurrence, reminder.DedupeKey, reminder.Status, reminder.LastDeliveryID, reminder.LastError, reminder.CreatedAt, reminder.UpdatedAt,
		reminder.SentAt, reminder.CanceledAt, reminder.DeliveryAttempt, mustJSON(reminder.ScheduleSpec))
	s.appendAudit(ctx, "reminder."+reminder.Status, reminder.SessionID, reminder.RunID, "toolhub", reminder.TextSummary, map[string]any{
		"reminder_id": reminder.ID,
		"due_time":    reminder.DueTime.UTC().Format(time.RFC3339),
		"channel":     reminder.Channel,
	})
	s.appendEvent(ctx, "reminder."+reminder.Status, reminder.SessionID, reminder.RunID, reminder)
	return reminder
}

func (s *PostgresStore) UpdatePendingReminder(reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = time.Now().UTC()
	}
	if reminder.TextSummary == "" {
		reminder.TextSummary = summarizeReminderText(reminder.Text)
	}
	ctx := context.Background()
	row := s.db.QueryRow(ctx, `
		UPDATE reminders SET
			session_id = nullif($2, ''), run_id = nullif($3, ''), text = $4, text_summary = $5,
			due_time = $6, timezone = $7, channel = $8, recipient = $9,
			recipient_binding = $10, binding_id = $11, credential_ref = $12, base_url = $13,
			recurrence = $14, dedupe_key = $15, status = $16, last_delivery_id = $17,
			last_error = $18, updated_at = $19, sent_at = $20, canceled_at = $21,
			delivery_attempt = $22, schedule_spec = $23
		WHERE id = $1 AND status = 'pending' AND updated_at = $24
		RETURNING id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
	`, reminder.ID, reminder.SessionID, reminder.RunID, reminder.Text, reminder.TextSummary,
		reminder.DueTime, reminder.Timezone, reminder.Channel, reminder.Recipient,
		reminder.RecipientBinding, reminder.BindingID, reminder.CredentialRef, reminder.BaseURL,
		reminder.Recurrence, reminder.DedupeKey, reminder.Status, reminder.LastDeliveryID,
		reminder.LastError, reminder.UpdatedAt, reminder.SentAt, reminder.CanceledAt,
		reminder.DeliveryAttempt, mustJSON(reminder.ScheduleSpec), expectedUpdatedAt.UTC())
	updated, err := scanReminder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Reminder{}, ErrReminderConflict
	}
	if err != nil {
		return app.Reminder{}, err
	}
	s.appendAudit(ctx, "reminder."+updated.Status, updated.SessionID, updated.RunID, "toolhub", updated.TextSummary, map[string]any{
		"reminder_id": updated.ID,
		"due_time":    updated.DueTime.UTC().Format(time.RFC3339),
		"channel":     updated.Channel,
	})
	s.appendEvent(ctx, "reminder."+updated.Status, updated.SessionID, updated.RunID, updated)
	return updated, nil
}

func (s *PostgresStore) GetReminder(id string) (app.Reminder, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		FROM reminders
		WHERE id = $1
	`, id)
	reminder, err := scanReminder(row)
	return reminder, err == nil
}

func (s *PostgresStore) ListReminders(filter app.ReminderFilter) []app.Reminder {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	var from, to any
	if filter.From != nil {
		from = *filter.From
	}
	if filter.To != nil {
		to = *filter.To
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		FROM reminders
		WHERE ($1 = '' OR status = $1)
			AND ($2::timestamptz IS NULL OR due_time >= $2::timestamptz)
			AND ($3::timestamptz IS NULL OR due_time <= $3::timestamptz)
		ORDER BY due_time ASC
		LIMIT $4
	`, filter.Status, from, to, limit)
	if err != nil {
		return []app.Reminder{}
	}
	defer rows.Close()
	return collectRows(rows, scanReminder)
}

func (s *PostgresStore) ClaimDueReminders(now, staleBefore time.Time, limit int) []app.Reminder {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(context.Background(), `
		UPDATE reminders
		SET status = 'sending', updated_at = $1
		WHERE id IN (
			SELECT id FROM reminders
			WHERE (status = 'pending' AND due_time <= $1)
				OR (status = 'sending' AND updated_at <= $2)
			ORDER BY due_time ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
	`, now.UTC(), staleBefore.UTC(), limit)
	if err != nil {
		return []app.Reminder{}
	}
	defer rows.Close()
	return collectRows(rows, scanReminder)
}

func (s *PostgresStore) SaveReminderDelivery(delivery app.ReminderDelivery) app.ReminderDelivery {
	now := time.Now().UTC()
	if delivery.ID == "" {
		delivery.ID = app.NewID("rdel")
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO reminder_deliveries (
			id, reminder_id, channel, provider, recipient, status, provider_status, error,
			retry_state, attempt, sent_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			channel = EXCLUDED.channel,
			provider = EXCLUDED.provider,
			recipient = EXCLUDED.recipient,
			status = EXCLUDED.status,
			provider_status = EXCLUDED.provider_status,
			error = EXCLUDED.error,
			retry_state = EXCLUDED.retry_state,
			attempt = EXCLUDED.attempt,
			sent_at = EXCLUDED.sent_at,
			created_at = EXCLUDED.created_at
	`, delivery.ID, delivery.ReminderID, delivery.Channel, delivery.Provider, delivery.Recipient, delivery.Status, delivery.ProviderStatus, delivery.Error,
		delivery.RetryState, delivery.Attempt, zeroTimeToNil(delivery.SentAt), delivery.CreatedAt)
	_, _ = s.db.Exec(ctx, `
		UPDATE reminders
		SET last_delivery_id = $1,
			last_error = $2,
			status = CASE WHEN $3 = 'sent' THEN 'sent' WHEN $3 = 'failed' THEN 'failed' ELSE status END,
			sent_at = CASE WHEN $3 = 'sent' THEN $4 ELSE sent_at END,
			delivery_attempt = $5,
			updated_at = $6
		WHERE id = $7
	`, delivery.ID, delivery.Error, delivery.Status, zeroTimeToNil(delivery.SentAt), delivery.Attempt, now, delivery.ReminderID)
	s.appendAudit(ctx, "reminder_delivery."+delivery.Status, "", delivery.ReminderID, "scheduler", delivery.ProviderStatus, map[string]any{
		"delivery_id": delivery.ID,
		"reminder_id": delivery.ReminderID,
		"channel":     delivery.Channel,
		"provider":    delivery.Provider,
		"attempt":     delivery.Attempt,
	})
	s.appendEvent(ctx, "reminder_delivery."+delivery.Status, "", delivery.ReminderID, delivery)
	return delivery
}

func (s *PostgresStore) ListReminderDeliveries(reminderID string) []app.ReminderDelivery {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, reminder_id, channel, provider, recipient, status, provider_status, error,
			retry_state, attempt, sent_at, created_at
		FROM reminder_deliveries
		WHERE $1 = '' OR reminder_id = $1
		ORDER BY created_at ASC
	`, reminderID)
	if err != nil {
		return []app.ReminderDelivery{}
	}
	defer rows.Close()
	return collectRows(rows, scanReminderDelivery)
}

func (s *PostgresStore) CreatePassiveNotification(notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	notification.OwnerID = strings.TrimSpace(notification.OwnerID)
	notification.EndpointID = strings.TrimSpace(notification.EndpointID)
	notification.IdempotencyKey = strings.TrimSpace(notification.IdempotencyKey)
	if notification.OwnerID == "" || notification.EndpointID == "" || notification.IdempotencyKey == "" || strings.TrimSpace(notification.Fingerprint) == "" {
		return app.PassiveNotification{}, false, errors.New("notification owner, endpoint, idempotency key, and fingerprint are required")
	}
	now := time.Now().UTC()
	if notification.ID == "" {
		notification.ID = app.NewID("notification")
	}
	if notification.CreatedAt.IsZero() {
		notification.CreatedAt = now
	}
	notification.UpdatedAt = now
	row := s.db.QueryRow(context.Background(), `
		INSERT INTO passive_notifications (
			id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT DO NOTHING
		RETURNING id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		          source, kind, deep_link, occurred_at, read_at, created_at, updated_at
	`, notification.ID, notification.OwnerID, notification.EndpointID, notification.IdempotencyKey,
		notification.Fingerprint, notification.NotificationID, notification.Source, notification.Kind,
		notification.DeepLink, notification.OccurredAt, notification.ReadAt, notification.CreatedAt, notification.UpdatedAt)
	inserted, err := scanPassiveNotification(row)
	if err == nil {
		s.bumpPassiveNotificationRev(notification.OwnerID)
		s.appendAudit(context.Background(), "notification.received", "", "", notification.OwnerID, notification.Source, map[string]any{
			"notification_id": notification.ID,
			"endpoint_id":     notification.EndpointID,
			"kind":            notification.Kind,
		})
		return inserted, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return app.PassiveNotification{}, false, err
	}
	existingRow := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		FROM passive_notifications WHERE endpoint_id = $1 AND idempotency_key = $2
	`, notification.EndpointID, notification.IdempotencyKey)
	existing, err := scanPassiveNotification(existingRow)
	if err != nil {
		return app.PassiveNotification{}, false, err
	}
	if existing.OwnerID != notification.OwnerID || existing.Fingerprint != notification.Fingerprint {
		return app.PassiveNotification{}, false, ErrPassiveNotificationConflict
	}
	return existing, false, nil
}

func (s *PostgresStore) GetPassiveNotification(ownerID, id string) (app.PassiveNotification, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		FROM passive_notifications WHERE owner_id = $1 AND id = $2
	`, ownerID, id)
	notification, err := scanPassiveNotification(row)
	return notification, err == nil
}

func (s *PostgresStore) ListPassiveNotifications(ownerID, after string, limit int) []app.PassiveNotification {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	ctx := context.Background()
	var rows pgx.Rows
	var err error
	if after == "" {
		rows, err = s.db.Query(ctx, `
			SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
			FROM passive_notifications WHERE owner_id = $1
			ORDER BY created_at DESC, id DESC LIMIT $2
		`, ownerID, limit)
	} else {
		cursor, ok := s.GetPassiveNotification(ownerID, after)
		if !ok {
			return []app.PassiveNotification{}
		}
		rows, err = s.db.Query(ctx, `
			SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
			FROM passive_notifications
			WHERE owner_id = $1 AND (created_at > $2 OR (created_at = $2 AND id > $3))
			ORDER BY created_at ASC, id ASC LIMIT $4
		`, ownerID, cursor.CreatedAt, cursor.ID, limit)
	}
	if err != nil {
		return []app.PassiveNotification{}
	}
	defer rows.Close()
	return collectRows(rows, scanPassiveNotification)
}

func (s *PostgresStore) CountUnreadPassiveNotifications(ownerID string) int {
	var count int
	if err := s.db.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM passive_notifications WHERE owner_id = $1 AND read_at IS NULL
	`, ownerID).Scan(&count); err != nil {
		return 0
	}
	return count
}

func (s *PostgresStore) MarkPassiveNotificationRead(ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = readAt.UTC()
	}
	row := s.db.QueryRow(context.Background(), `
		UPDATE passive_notifications SET read_at = COALESCE(read_at, $3), updated_at = CASE WHEN read_at IS NULL THEN $3 ELSE updated_at END
		WHERE owner_id = $1 AND id = $2
		RETURNING id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		          source, kind, deep_link, occurred_at, read_at, created_at, updated_at
	`, ownerID, id, readAt)
	notification, err := scanPassiveNotification(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.PassiveNotification{}, ErrPassiveNotificationNotFound
	}
	if err == nil {
		s.bumpPassiveNotificationRev(ownerID)
	}
	return notification, err
}

func (s *PostgresStore) MarkAllPassiveNotificationsRead(ownerID string, readAt time.Time) (int, error) {
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = readAt.UTC()
	}
	result, err := s.db.Exec(context.Background(), `
		UPDATE passive_notifications SET read_at = $2, updated_at = $2
		WHERE owner_id = $1 AND read_at IS NULL
	`, ownerID, readAt)
	if err != nil {
		return 0, err
	}
	if result.RowsAffected() > 0 {
		s.bumpPassiveNotificationRev(ownerID)
	}
	return int(result.RowsAffected()), nil
}

func (s *PostgresStore) PrunePassiveNotifications(cutoff time.Time, maxPerOwner int) int {
	ctx := context.Background()
	removedByOwner := map[string]int{}
	if !cutoff.IsZero() {
		rows, err := s.db.Query(ctx, `
			DELETE FROM passive_notifications WHERE created_at < $1 RETURNING owner_id
		`, cutoff)
		if err == nil {
			for rows.Next() {
				var ownerID string
				if rows.Scan(&ownerID) == nil {
					removedByOwner[ownerID]++
				}
			}
			rows.Close()
		}
	}
	if maxPerOwner > 0 {
		type ownerExcess struct {
			ownerID string
			excess  int
		}
		var over []ownerExcess
		ownerRows, err := s.db.Query(ctx, `
			SELECT owner_id, COUNT(*) FROM passive_notifications
			GROUP BY owner_id HAVING COUNT(*) > $1
		`, maxPerOwner)
		if err == nil {
			for ownerRows.Next() {
				var ownerID string
				var count int
				if ownerRows.Scan(&ownerID, &count) == nil {
					over = append(over, ownerExcess{ownerID: ownerID, excess: count - maxPerOwner})
				}
			}
			ownerRows.Close()
		}
		for _, entry := range over {
			// Evict read notifications oldest-first before unread ones so an
			// over-cap inbox keeps the newest unread records.
			result, err := s.db.Exec(ctx, `
				DELETE FROM passive_notifications WHERE id IN (
					SELECT id FROM passive_notifications WHERE owner_id = $1
					ORDER BY (read_at IS NOT NULL) DESC, created_at ASC, id ASC
					LIMIT $2
				)
			`, entry.ownerID, entry.excess)
			if err == nil && result.RowsAffected() > 0 {
				removedByOwner[entry.ownerID] += int(result.RowsAffected())
			}
		}
	}
	removed := 0
	for ownerID, count := range removedByOwner {
		removed += count
		s.bumpPassiveNotificationRev(ownerID)
		s.appendAudit(ctx, "notification.pruned", "", "", "notification-retention", ownerID, map[string]any{
			"removed":       count,
			"max_per_owner": maxPerOwner,
			"cutoff":        cutoff.UTC().Format(time.RFC3339),
		})
	}
	return removed
}

func (s *PostgresStore) PassiveNotificationRevision(ownerID string) uint64 {
	s.passiveRevMu.Lock()
	defer s.passiveRevMu.Unlock()
	return s.passiveNotificationRevs[ownerID]
}

func (s *PostgresStore) bumpPassiveNotificationRev(ownerID string) {
	s.passiveRevMu.Lock()
	defer s.passiveRevMu.Unlock()
	s.passiveNotificationRevs[ownerID]++
}

func (s *PostgresStore) SaveExternalChatSession(session app.ExternalChatSession) app.ExternalChatSession {
	now := time.Now().UTC()
	if session.ID == "" {
		session.ID = app.NewID("extchat")
	}
	if session.Channel == "" {
		session.Channel = "weixin"
	}
	if session.ExternalChatID == "" {
		session.ExternalChatID = session.ExternalUserID
	}
	if strings.TrimSpace(session.AuthorizedOwnerID) == "" {
		session.AuthorizedOwnerID = session.OwnerID
	}
	if strings.TrimSpace(session.AuthorizedActorID) == "" {
		session.AuthorizedActorID = session.AuthorizedOwnerID
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO external_chat_sessions (
			id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
			external_chat_id, external_thread_id, display_name, linked_session_id, status,
			provider_cursor, last_context_token, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			authorized_owner_id = EXCLUDED.authorized_owner_id,
			authorized_actor_id = EXCLUDED.authorized_actor_id,
			workspace_root = EXCLUDED.workspace_root,
			binding_id = EXCLUDED.binding_id,
			channel = EXCLUDED.channel,
			provider = EXCLUDED.provider,
			external_user_id = EXCLUDED.external_user_id,
			external_chat_id = EXCLUDED.external_chat_id,
			external_thread_id = EXCLUDED.external_thread_id,
			display_name = EXCLUDED.display_name,
			linked_session_id = EXCLUDED.linked_session_id,
			status = EXCLUDED.status,
			provider_cursor = EXCLUDED.provider_cursor,
			last_context_token = EXCLUDED.last_context_token,
			updated_at = EXCLUDED.updated_at
	`, session.ID, session.OwnerID, session.AuthorizedOwnerID, session.AuthorizedActorID, session.WorkspaceRoot, session.BindingID, session.Channel, session.Provider,
		session.ExternalUserID, session.ExternalChatID, session.ExternalThreadID, session.DisplayName,
		session.LinkedSessionID, session.Status, session.ProviderCursor, session.LastContextToken,
		session.CreatedAt, session.UpdatedAt)
	if strings.TrimSpace(session.LinkedSessionID) != "" {
		sessionUpdatedAt := normalizeSessionTime(now)
		_, _ = s.db.Exec(context.Background(), `
			UPDATE sessions
			SET source = $5,
			    hidden = true,
			    owner_id = CASE WHEN $3 <> '' THEN $3 ELSE owner_id END,
			    workspace_root = CASE WHEN $4 <> '' THEN $4 ELSE workspace_root END,
			    title = CASE WHEN title = '' OR title = 'New SparkClaw Session' OR title = '微信会话' THEN $6 ELSE title END,
			    updated_at = $2
			WHERE id = $1
		`, session.LinkedSessionID, sessionUpdatedAt, session.OwnerID, session.WorkspaceRoot, session.Channel, externalChatSessionTitle(session.Channel))
	}
	s.appendAudit(context.Background(), "external_chat_session."+session.Status, session.LinkedSessionID, "", "gateway", redactPostgresExternalID(session.ExternalUserID), map[string]any{
		"chat_session_id": session.ID,
		"binding_id":      session.BindingID,
		"channel":         session.Channel,
		"provider":        session.Provider,
	})
	s.appendEvent(context.Background(), "external_chat_session."+session.Status, session.LinkedSessionID, "", session)
	return session
}

func (s *PostgresStore) GetExternalChatSession(id string) (app.ExternalChatSession, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE id = $1
	`, id)
	session, err := scanExternalChatSession(row)
	return session, err == nil
}

func (s *PostgresStore) ListExternalChatSessions(channel, status string) []app.ExternalChatSession {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE ($1 = '' OR channel = $1) AND ($2 = '' OR status = $2)
		ORDER BY updated_at DESC
	`, channel, status)
	if err != nil {
		return []app.ExternalChatSession{}
	}
	defer rows.Close()
	return collectRows(rows, scanExternalChatSession)
}

func (s *PostgresStore) FindExternalChatSession(bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE binding_id = $1 AND external_chat_id = $2 AND external_thread_id = $3
		ORDER BY updated_at DESC
		LIMIT 1
	`, bindingID, externalChatID, externalThreadID)
	session, err := scanExternalChatSession(row)
	return session, err == nil
}

func (s *PostgresStore) FindExternalChatSessionByLinkedSessionID(sessionID string) (app.ExternalChatSession, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE linked_session_id = $1
		ORDER BY updated_at DESC
		LIMIT 1
	`, sessionID)
	session, err := scanExternalChatSession(row)
	return session, err == nil
}

func (s *PostgresStore) SaveExternalChatMessage(message app.ExternalChatMessage) app.ExternalChatMessage {
	now := time.Now().UTC()
	if message.ID == "" {
		message.ID = app.NewID("extmsg")
	}
	if message.Channel == "" {
		if session, ok := s.GetExternalChatSession(message.ChatSessionID); ok {
			message.Channel = session.Channel
		}
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	message.UpdatedAt = now
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO external_chat_messages (
			id, chat_session_id, binding_id, channel, direction, role, external_message_id,
			content, context_token, linked_run_id, status, error,
			pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (id) DO UPDATE SET
			chat_session_id = EXCLUDED.chat_session_id,
			binding_id = EXCLUDED.binding_id,
			channel = EXCLUDED.channel,
			direction = EXCLUDED.direction,
			role = EXCLUDED.role,
			external_message_id = EXCLUDED.external_message_id,
			content = EXCLUDED.content,
			context_token = EXCLUDED.context_token,
			linked_run_id = EXCLUDED.linked_run_id,
			status = EXCLUDED.status,
			error = EXCLUDED.error,
			pending_reply_kind = EXCLUDED.pending_reply_kind,
			pending_reply = EXCLUDED.pending_reply,
			dispatch_attempts = EXCLUDED.dispatch_attempts,
			updated_at = EXCLUDED.updated_at
	`, message.ID, message.ChatSessionID, message.BindingID, message.Channel, message.Direction, message.Role,
		message.ExternalMessageID, message.Content, message.ContextToken, message.LinkedRunID,
		message.Status, message.Error, message.PendingReplyKind, message.PendingReply, message.DispatchAttempts,
		message.CreatedAt, message.UpdatedAt)
	s.appendAudit(context.Background(), "external_chat_message."+message.Status, "", message.LinkedRunID, "gateway", message.Direction, map[string]any{
		"message_id":      message.ID,
		"chat_session_id": message.ChatSessionID,
		"binding_id":      message.BindingID,
		"channel":         message.Channel,
		"direction":       message.Direction,
		"role":            message.Role,
	})
	s.appendEvent(context.Background(), "external_chat_message."+message.Status, "", message.LinkedRunID, message)
	return message
}

func (s *PostgresStore) GetExternalChatMessage(id string) (app.ExternalChatMessage, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE id = $1
	`, id)
	message, err := scanExternalChatMessage(row)
	return message, err == nil
}

func (s *PostgresStore) FindExternalChatMessageByExternalID(chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool) {
	if strings.TrimSpace(externalMessageID) == "" {
		return app.ExternalChatMessage{}, false
	}
	row := s.db.QueryRow(context.Background(), `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE chat_session_id = $1 AND external_message_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, chatSessionID, externalMessageID)
	message, err := scanExternalChatMessage(row)
	return message, err == nil
}

func (s *PostgresStore) ListExternalChatMessages(chatSessionID string, limit int) []app.ExternalChatMessage {
	query := `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE ($1 = '' OR chat_session_id = $1)
		ORDER BY created_at ASC
	`
	args := []any{chatSessionID}
	if limit > 0 {
		query = `
			SELECT * FROM (
				SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
				       content, context_token, linked_run_id, status, error,
				       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
				FROM external_chat_messages
				WHERE ($1 = '' OR chat_session_id = $1)
				ORDER BY created_at DESC
				LIMIT $2
			) recent
			ORDER BY created_at ASC
		`
		args = append(args, limit)
	}
	rows, err := s.db.Query(context.Background(), query, args...)
	if err != nil {
		return []app.ExternalChatMessage{}
	}
	defer rows.Close()
	return collectRows(rows, scanExternalChatMessage)
}

func (s *PostgresStore) SaveMessageReceive(record app.MessageReceiveRecord) app.MessageReceiveRecord {
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = app.NewID("recv")
	}
	if record.Direction == "" {
		record.Direction = app.MessageDirectionReceive
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if len(record.Transitions) == 0 || record.Transitions[len(record.Transitions)-1].Status != record.Status {
		record.Transitions = append(record.Transitions, app.MessageLifecycleTransition{Status: record.Status, At: now})
	}
	if existing, ok := s.FindMessageReceive(record.SourceEndpointID, record.NativeMessageID); ok && existing.ID != record.ID {
		record.ID = existing.ID
		record.CreatedAt = existing.CreatedAt
		record.Transitions = append(existing.Transitions, app.MessageLifecycleTransition{Status: record.Status, At: now})
	}
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO message_receive_records (id, owner_id, actor_id, source_endpoint_id, native_message_id, status, record, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			actor_id = EXCLUDED.actor_id,
			source_endpoint_id = EXCLUDED.source_endpoint_id,
			native_message_id = EXCLUDED.native_message_id,
			status = EXCLUDED.status,
			record = EXCLUDED.record,
			updated_at = EXCLUDED.updated_at
	`, record.ID, record.OwnerID, record.ActorID, record.SourceEndpointID, record.NativeMessageID, record.Status, mustJSON(record), record.UpdatedAt)
	return record
}

func (s *PostgresStore) GetMessageReceive(id string) (app.MessageReceiveRecord, bool) {
	return s.queryMessageReceive(`SELECT record FROM message_receive_records WHERE id = $1`, id)
}

func (s *PostgresStore) FindMessageReceive(sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool) {
	return s.queryMessageReceive(`SELECT record FROM message_receive_records WHERE source_endpoint_id = $1 AND native_message_id = $2`, sourceEndpointID, nativeMessageID)
}

func (s *PostgresStore) queryMessageReceive(query string, args ...any) (app.MessageReceiveRecord, bool) {
	var raw []byte
	if err := s.db.QueryRow(context.Background(), query, args...).Scan(&raw); err != nil {
		return app.MessageReceiveRecord{}, false
	}
	var record app.MessageReceiveRecord
	if json.Unmarshal(raw, &record) != nil {
		return app.MessageReceiveRecord{}, false
	}
	return record, true
}

func (s *PostgresStore) ListMessageReceives(ownerID, actorID string, limit int) []app.MessageReceiveRecord {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT record FROM message_receive_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR actor_id = $2)
		ORDER BY updated_at DESC LIMIT $3
	`, ownerID, actorID, limit)
	if err != nil {
		return []app.MessageReceiveRecord{}
	}
	defer rows.Close()
	out := []app.MessageReceiveRecord{}
	for rows.Next() {
		var raw []byte
		var record app.MessageReceiveRecord
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &record) == nil {
			out = append(out, record)
		}
	}
	return out
}

func (s *PostgresStore) SaveMessageDelivery(record app.MessageDeliveryRecord) app.MessageDeliveryRecord {
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = app.DeliveryID(app.NewID("del"))
	}
	if record.Direction == "" {
		record.Direction = app.MessageDirectionSend
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO message_delivery_records (id, owner_id, actor_id, idempotency_key, content_digest, status, record, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			record = EXCLUDED.record,
			updated_at = EXCLUDED.updated_at
	`, record.ID, record.OwnerID, record.ActorID, record.Request.IdempotencyKey, record.ContentDigest, record.Status, mustJSON(record), record.UpdatedAt)
	return record
}

func (s *PostgresStore) GetMessageDelivery(id app.DeliveryID) (app.MessageDeliveryRecord, bool) {
	return s.queryMessageDelivery(`SELECT record FROM message_delivery_records WHERE id = $1`, id)
}

func (s *PostgresStore) FindMessageDeliveryByIdempotency(ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool) {
	return s.queryMessageDelivery(`SELECT record FROM message_delivery_records WHERE owner_id = $1 AND actor_id = $2 AND idempotency_key = $3`, ownerID, actorID, idempotencyKey)
}

func (s *PostgresStore) queryMessageDelivery(query string, args ...any) (app.MessageDeliveryRecord, bool) {
	var raw []byte
	if err := s.db.QueryRow(context.Background(), query, args...).Scan(&raw); err != nil {
		return app.MessageDeliveryRecord{}, false
	}
	var record app.MessageDeliveryRecord
	if json.Unmarshal(raw, &record) != nil {
		return app.MessageDeliveryRecord{}, false
	}
	return record, true
}

func (s *PostgresStore) ListMessageDeliveries(ownerID, actorID string, limit int) []app.MessageDeliveryRecord {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT record FROM message_delivery_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR actor_id = $2)
		ORDER BY updated_at DESC LIMIT $3
	`, ownerID, actorID, limit)
	if err != nil {
		return []app.MessageDeliveryRecord{}
	}
	defer rows.Close()
	out := []app.MessageDeliveryRecord{}
	for rows.Next() {
		var raw []byte
		var record app.MessageDeliveryRecord
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &record) == nil {
			out = append(out, record)
		}
	}
	return out
}

func (s *PostgresStore) SaveChannelInboxUpdate(update app.ChannelInboxUpdate) app.ChannelInboxUpdate {
	now := time.Now().UTC()
	if update.ID == "" {
		if existing, ok := s.FindChannelInboxUpdate(update.BindingID, update.ExternalID); ok {
			return existing
		}
	}
	if update.ID == "" {
		update.ID = app.NewID("inbox")
	}
	if update.Status == "" {
		update.Status = "pending"
	}
	if update.AvailableAt.IsZero() {
		update.AvailableAt = now
	}
	if update.CreatedAt.IsZero() {
		update.CreatedAt = now
	}
	update.UpdatedAt = now
	_, err := s.db.Exec(context.Background(), `
		INSERT INTO channel_inbox_updates (
			id, binding_id, channel, external_id, chat_key, payload, status, attempts,
			available_at, last_error, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (binding_id, external_id) DO UPDATE SET
			chat_key = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.chat_key ELSE channel_inbox_updates.chat_key END,
			payload = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.payload ELSE channel_inbox_updates.payload END,
			status = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.status ELSE channel_inbox_updates.status END,
			attempts = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.attempts ELSE channel_inbox_updates.attempts END,
			available_at = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.available_at ELSE channel_inbox_updates.available_at END,
			last_error = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.last_error ELSE channel_inbox_updates.last_error END,
			updated_at = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.updated_at ELSE channel_inbox_updates.updated_at END
	`, update.ID, update.BindingID, update.Channel, update.ExternalID, update.ChatKey,
		mustJSONRaw(update.Payload), update.Status, update.Attempts, update.AvailableAt,
		update.LastError, update.CreatedAt, update.UpdatedAt)
	if err == nil {
		if saved, ok := s.FindChannelInboxUpdate(update.BindingID, update.ExternalID); ok {
			return saved
		}
	}
	return update
}

func (s *PostgresStore) GetChannelInboxUpdate(id string) (app.ChannelInboxUpdate, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates WHERE id = $1
	`, id)
	update, err := scanChannelInboxUpdate(row)
	return update, err == nil
}

func (s *PostgresStore) FindChannelInboxUpdate(bindingID, externalID string) (app.ChannelInboxUpdate, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates WHERE binding_id = $1 AND external_id = $2
	`, bindingID, externalID)
	update, err := scanChannelInboxUpdate(row)
	return update, err == nil
}

func (s *PostgresStore) ListChannelInboxUpdates(channel, status string, readyBefore time.Time, limit int) []app.ChannelInboxUpdate {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates
		WHERE ($1 = '' OR channel = $1)
		  AND ($2 = '' OR status = $2)
	`
	args := []any{channel, status}
	if !readyBefore.IsZero() {
		query += ` AND available_at <= $3 ORDER BY created_at ASC LIMIT $4`
		args = append(args, readyBefore, limit)
	} else {
		query += ` ORDER BY created_at ASC LIMIT $3`
		args = append(args, limit)
	}
	rows, err := s.db.Query(context.Background(), query, args...)
	if err != nil {
		return []app.ChannelInboxUpdate{}
	}
	defer rows.Close()
	return collectRows(rows, scanChannelInboxUpdate)
}

func (s *PostgresStore) SaveBrowserAuthRecord(record app.BrowserAuthRecord) app.BrowserAuthRecord {
	current, _ := s.GetBrowserAuthRecord(record.ID)
	record = normalizeBrowserAuthRecord(record, current)
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO browser_auth_records (
			id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
			session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			browser_profile_id = EXCLUDED.browser_profile_id,
			site_origin = EXCLUDED.site_origin,
			site_realm = EXCLUDED.site_realm,
			account_hint = EXCLUDED.account_hint,
			auth_strategy = EXCLUDED.auth_strategy,
			status = EXCLUDED.status,
			session_ref = EXCLUDED.session_ref,
			credential_ref = EXCLUDED.credential_ref,
			cookie_jar_ref = EXCLUDED.cookie_jar_ref,
			last_verified_at = EXCLUDED.last_verified_at,
			expires_at = EXCLUDED.expires_at,
			last_error = EXCLUDED.last_error,
			updated_at = EXCLUDED.updated_at,
			revoked_at = EXCLUDED.revoked_at
	`, record.ID, record.OwnerID, record.BrowserProfileID, record.SiteOrigin, record.SiteRealm, record.AccountHint, record.AuthStrategy,
		record.Status, record.SessionRef, record.CredentialRef, record.CookieJarRef, zeroTimeToNil(record.LastVerifiedAt),
		record.ExpiresAt, record.LastError, record.CreatedAt, record.UpdatedAt, record.RevokedAt)
	s.appendAudit(ctx, "browser_auth.record_saved", "", "", "gateway", record.SiteOrigin, browserAuthAuditFields(record, nil))
	s.appendEvent(ctx, "browser_auth.record_saved", "", "", record)
	return record
}

func (s *PostgresStore) GetBrowserAuthRecord(id string) (app.BrowserAuthRecord, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
			session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
		FROM browser_auth_records
		WHERE id = $1
	`, strings.TrimSpace(id))
	record, err := scanBrowserAuthRecord(row)
	return record, err == nil
}

func (s *PostgresStore) FindBrowserAuthRecord(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool) {
	ownerID, browserProfileID, siteOrigin, siteRealm, accountHint = normalizeBrowserAuthLookup(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
			session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
		FROM browser_auth_records
		WHERE owner_id = $1
		  AND browser_profile_id = $2
		  AND site_origin = $3
		  AND site_realm = $4
		  AND account_hint = $5
		  AND status = $6
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY updated_at DESC
		LIMIT 1
	`, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint, app.BrowserAuthStatusActive)
	record, err := scanBrowserAuthRecord(row)
	return record, err == nil
}

func (s *PostgresStore) ListBrowserAuthRecords(ownerID, browserProfileID string) []app.BrowserAuthRecord {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID != "" {
		ownerID = normalizeBrowserAuthOwnerID(ownerID)
	}
	browserProfileID = strings.TrimSpace(browserProfileID)
	if browserProfileID != "" {
		browserProfileID = normalizeBrowserProfileID(browserProfileID)
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
			session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
		FROM browser_auth_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR browser_profile_id = $2)
		ORDER BY updated_at DESC
	`, ownerID, browserProfileID)
	if err != nil {
		return []app.BrowserAuthRecord{}
	}
	defer rows.Close()
	return collectRows(rows, scanBrowserAuthRecord)
}

func (s *PostgresStore) RevokeBrowserAuthRecord(id, reason string) (app.BrowserAuthRecord, error) {
	record, ok := s.GetBrowserAuthRecord(id)
	if !ok {
		return app.BrowserAuthRecord{}, errors.New("browser auth record not found")
	}
	now := time.Now().UTC()
	record.Status = app.BrowserAuthStatusRevoked
	record.RevokedAt = &now
	record.UpdatedAt = now
	record.LastError = strings.TrimSpace(reason)
	record = s.SaveBrowserAuthRecord(record)
	s.appendAudit(context.Background(), "browser_auth.record_revoked", "", "", "owner", record.SiteOrigin, browserAuthAuditFields(record, map[string]any{"reason": record.LastError}))
	s.appendEvent(context.Background(), "browser_auth.record_revoked", "", "", record)
	return record, nil
}

func (s *PostgresStore) SaveBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock {
	current, _ := s.GetBrowserLoginBlock(block.ID)
	block = normalizeBrowserLoginBlock(block, current)
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO browser_login_blocks (
			id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
			workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
			last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
			owner_id, browser_profile_id, site_origin,
			site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
			transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32)
		ON CONFLICT (id) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			run_id = EXCLUDED.run_id,
			schema_version = EXCLUDED.schema_version,
			version = EXCLUDED.version,
			workflow_id = EXCLUDED.workflow_id,
			workflow_revision = EXCLUDED.workflow_revision,
			workflow_node_id = EXCLUDED.workflow_node_id,
			session_generation = EXCLUDED.session_generation,
			status = EXCLUDED.status,
			original_goal = EXCLUDED.original_goal,
			resume_tool = EXCLUDED.resume_tool,
			resume_args = EXCLUDED.resume_args,
			last_tool_call_id = EXCLUDED.last_tool_call_id,
			login_handoff_url = EXCLUDED.login_handoff_url,
			login_handoff_page_id = EXCLUDED.login_handoff_page_id,
			last_visible_page_id = EXCLUDED.last_visible_page_id,
			owner_id = EXCLUDED.owner_id,
			browser_profile_id = EXCLUDED.browser_profile_id,
			site_origin = EXCLUDED.site_origin,
			site_realm = EXCLUDED.site_realm,
			account_hint = EXCLUDED.account_hint,
			browser_auth_status = EXCLUDED.browser_auth_status,
			target = EXCLUDED.target,
			visible_evidence = EXCLUDED.visible_evidence,
			last_user_reply = EXCLUDED.last_user_reply,
			last_error = EXCLUDED.last_error,
			transition_owner_id = EXCLUDED.transition_owner_id,
			transition_lease_until = EXCLUDED.transition_lease_until,
			updated_at = EXCLUDED.updated_at,
			resolved_at = EXCLUDED.resolved_at
	`, block.ID, block.SessionID, block.RunID, block.SchemaVersion, block.Version, block.WorkflowID, block.WorkflowRevision,
		block.WorkflowNodeID, block.SessionGeneration, block.Status, block.OriginalGoal, block.ResumeTool, mustJSON(block.ResumeArgs),
		block.LastToolCallID, block.LoginHandoffURL, block.LoginHandoffPageID, block.LastVisiblePageID,
		block.OwnerID, block.BrowserProfileID, block.SiteOrigin,
		block.SiteRealm, block.AccountHint, block.BrowserAuthStatus, mustJSON(block.Target), mustJSON(block.VisibleEvidence), block.LastUserReply, block.LastError,
		block.TransitionOwnerID, block.TransitionLeaseUntil, block.CreatedAt, block.UpdatedAt, block.ResolvedAt)
	s.appendAudit(ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEvent(ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return block
}

func (s *PostgresStore) UpdateBrowserLoginBlock(block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	current, ok := s.GetBrowserLoginBlock(block.ID)
	if !ok || current.Version != expectedVersion {
		return app.BrowserLoginBlock{}, ErrBrowserHandoffConflict
	}
	block.Version = expectedVersion + 1
	block = normalizeBrowserLoginBlock(block, current)
	ctx := context.Background()
	result, err := s.db.Exec(ctx, `
		UPDATE browser_login_blocks SET
			session_id = $2, run_id = $3, schema_version = $4, version = $5,
			workflow_id = $6, workflow_revision = $7, workflow_node_id = $8,
			session_generation = $9, status = $10, original_goal = $11,
			resume_tool = $12, resume_args = $13, last_tool_call_id = $14,
			login_handoff_url = $15, login_handoff_page_id = $16,
			last_visible_page_id = $17, owner_id = $18, browser_profile_id = $19,
			site_origin = $20, site_realm = $21, account_hint = $22,
			browser_auth_status = $23, target = $24, visible_evidence = $25,
			last_user_reply = $26, last_error = $27, transition_owner_id = $28,
			transition_lease_until = $29, created_at = $30,
			updated_at = $31, resolved_at = $32
		WHERE id = $1 AND version = $33
	`, block.ID, block.SessionID, block.RunID, block.SchemaVersion, block.Version,
		block.WorkflowID, block.WorkflowRevision, block.WorkflowNodeID, block.SessionGeneration,
		block.Status, block.OriginalGoal, block.ResumeTool, mustJSON(block.ResumeArgs),
		block.LastToolCallID, block.LoginHandoffURL, block.LoginHandoffPageID, block.LastVisiblePageID,
		block.OwnerID, block.BrowserProfileID, block.SiteOrigin, block.SiteRealm, block.AccountHint,
		block.BrowserAuthStatus, mustJSON(block.Target), mustJSON(block.VisibleEvidence),
		block.LastUserReply, block.LastError, block.TransitionOwnerID, block.TransitionLeaseUntil,
		block.CreatedAt, block.UpdatedAt, block.ResolvedAt,
		expectedVersion)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	affected := result.RowsAffected()
	if affected != 1 {
		return app.BrowserLoginBlock{}, ErrBrowserHandoffConflict
	}
	s.appendAudit(ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEvent(ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return block, nil
}

func (s *PostgresStore) GetBrowserLoginBlock(id string) (app.BrowserLoginBlock, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
			workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
			last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
			owner_id, browser_profile_id, site_origin,
			site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
			transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
		FROM browser_login_blocks
		WHERE id = $1
	`, strings.TrimSpace(id))
	block, err := scanBrowserLoginBlock(row)
	return block, err == nil
}

func (s *PostgresStore) FindActiveBrowserLoginBlock(sessionID string) (app.BrowserLoginBlock, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
			workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
			last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
			owner_id, browser_profile_id, site_origin,
			site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
			transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
		FROM browser_login_blocks
		WHERE session_id = $1 AND status = ANY($2)
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, strings.TrimSpace(sessionID), app.BrowserHandoffActiveStatuses())
	block, err := scanBrowserLoginBlock(row)
	return block, err == nil
}

func (s *PostgresStore) ListBrowserLoginBlocks(sessionID, status string) []app.BrowserLoginBlock {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
			workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
			last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
			owner_id, browser_profile_id, site_origin,
			site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
			transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
		FROM browser_login_blocks
		WHERE ($1 = '' OR session_id = $1) AND ($2 = '' OR status = $2)
		ORDER BY updated_at DESC, id DESC
	`, strings.TrimSpace(sessionID), strings.TrimSpace(status))
	if err != nil {
		return []app.BrowserLoginBlock{}
	}
	defer rows.Close()
	return collectRows(rows, scanBrowserLoginBlock)
}

func (s *PostgresStore) AddMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate {
	if candidate.ID == "" {
		candidate.ID = app.NewID("mc")
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now().UTC()
	}
	if candidate.Status == "" {
		candidate.Status = "pending"
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO memory_candidates (
			id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			kind = EXCLUDED.kind,
			content = EXCLUDED.content,
			sensitivity = EXCLUDED.sensitivity,
			status = EXCLUDED.status,
			reason = EXCLUDED.reason,
			resolved_at = EXCLUDED.resolved_at
	`, candidate.ID, candidate.SessionID, candidate.RunID, candidate.Kind, candidate.Content, candidate.Sensitivity, candidate.Status, candidate.Reason, candidate.CreatedAt, candidate.ResolvedAt)
	s.appendAudit(ctx, "memory_candidate.created", candidate.SessionID, candidate.RunID, "agent", candidate.Content, map[string]any{"kind": candidate.Kind})
	s.appendEvent(ctx, "memory_candidate.created", candidate.SessionID, candidate.RunID, candidate)
	return candidate
}

func (s *PostgresStore) ResolveMemoryCandidate(id, status string) (app.MemoryCandidate, *app.Memory, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	defer rollbackTx(ctx, tx)
	row := tx.QueryRow(ctx, `
		SELECT id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		FROM memory_candidates
		WHERE id = $1
		FOR UPDATE
	`, id)
	candidate, err := scanMemoryCandidate(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.MemoryCandidate{}, nil, errors.New("memory candidate not found")
		}
		return app.MemoryCandidate{}, nil, err
	}
	if candidate.Status != "pending" {
		return app.MemoryCandidate{}, nil, errors.New("memory candidate already resolved")
	}
	now := time.Now().UTC()
	candidate.Status = status
	candidate.ResolvedAt = &now
	if _, err := tx.Exec(ctx, `
		UPDATE memory_candidates
		SET status = $2, resolved_at = $3
		WHERE id = $1
	`, candidate.ID, candidate.Status, candidate.ResolvedAt); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	var memory *app.Memory
	if status == "accepted" {
		m := app.Memory{
			ID:        app.NewID("mem"),
			Kind:      candidate.Kind,
			Content:   candidate.Content,
			SourceID:  candidate.RunID,
			CreatedAt: now,
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO memories (id, kind, content, source_run_id, created_at)
			VALUES ($1, $2, $3, $4, $5)
		`, m.ID, m.Kind, m.Content, m.SourceID, m.CreatedAt); err != nil {
			return app.MemoryCandidate{}, nil, err
		}
		memory = &m
	}
	if err := tx.Commit(ctx); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	s.appendAudit(ctx, "memory_candidate."+status, candidate.SessionID, candidate.RunID, "owner", candidate.Content, nil)
	s.appendEvent(ctx, "memory_candidate."+status, candidate.SessionID, candidate.RunID, candidate)
	return candidate, memory, nil
}

func (s *PostgresStore) ListMemoryCandidates(status string) []app.MemoryCandidate {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		FROM memory_candidates
		WHERE $1 = '' OR status = $1
		ORDER BY created_at DESC
	`, status)
	if err != nil {
		return []app.MemoryCandidate{}
	}
	defer rows.Close()
	return collectRows(rows, scanMemoryCandidate)
}

func (s *PostgresStore) SearchMemories(query string) []app.Memory {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, kind, content, source_run_id, created_at
		FROM memories
		WHERE $1 = ''
			OR lower(content) LIKE '%' || lower($1) || '%'
			OR lower(kind) LIKE '%' || lower($1) || '%'
		ORDER BY created_at DESC
	`, query)
	if err != nil {
		return []app.Memory{}
	}
	defer rows.Close()
	return collectRows(rows, scanMemory)
}

func (s *PostgresStore) UpdateMemory(id, kind, content string) (app.Memory, error) {
	ctx := context.Background()
	row := s.db.QueryRow(ctx, `
		UPDATE memories AS memory
		SET kind = $2, content = $3
		FROM agent_runs AS run
		WHERE memory.id = $1 AND run.id = memory.source_run_id
		RETURNING memory.id, memory.kind, memory.content, memory.source_run_id, memory.created_at, run.session_id
	`, id, kind, content)
	memory, sessionID, err := scanMemoryWithSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Memory{}, errors.New("memory not found")
		}
		return app.Memory{}, err
	}
	s.appendAudit(ctx, "memory.updated", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEvent(ctx, "memory.updated", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *PostgresStore) DeleteMemory(id string) (app.Memory, error) {
	ctx := context.Background()
	row := s.db.QueryRow(ctx, `
		DELETE FROM memories AS memory
		USING agent_runs AS run
		WHERE memory.id = $1 AND run.id = memory.source_run_id
		RETURNING memory.id, memory.kind, memory.content, memory.source_run_id, memory.created_at, run.session_id
	`, id)
	memory, sessionID, err := scanMemoryWithSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Memory{}, errors.New("memory not found")
		}
		return app.Memory{}, err
	}
	s.appendAudit(ctx, "memory.deleted", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEvent(ctx, "memory.deleted", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *PostgresStore) PruneMemories(cutoff time.Time) []app.Memory {
	if cutoff.IsZero() {
		return []app.Memory{}
	}
	ctx := context.Background()
	rows, err := s.db.Query(ctx, `
		DELETE FROM memories AS memory
		USING agent_runs AS run
		WHERE memory.source_run_id = run.id
			AND memory.created_at < $1
		RETURNING memory.id, memory.kind, memory.content, memory.source_run_id, memory.created_at, run.session_id
	`, cutoff)
	if err != nil {
		return []app.Memory{}
	}
	defer rows.Close()
	type prunedMemory struct {
		memory    app.Memory
		sessionID string
	}
	pruned := []prunedMemory{}
	for rows.Next() {
		memory, sessionID, err := scanMemoryWithSession(rows)
		if err != nil {
			continue
		}
		pruned = append(pruned, prunedMemory{memory: memory, sessionID: sessionID})
	}
	out := make([]app.Memory, 0, len(pruned))
	for _, item := range pruned {
		out = append(out, item.memory)
		s.appendAudit(ctx, "memory.pruned", item.sessionID, item.memory.SourceID, "memory-retention", item.memory.Kind, map[string]any{
			"memory_id": item.memory.ID,
			"cutoff":    cutoff.UTC().Format(time.RFC3339),
		})
		s.appendEvent(ctx, "memory.pruned", item.sessionID, item.memory.SourceID, item.memory)
	}
	slices.SortFunc(out, func(a, b app.Memory) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *PostgresStore) AddAudit(event app.AuditEvent) {
	if event.ID == "" {
		event.ID = app.NewID("audit")
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, event.ID, event.Time, event.Type, event.SessionID, event.RunID, event.Actor, event.Summary, optionalJSON(event.Fields))
}

func (s *PostgresStore) ListAudit(sessionID string) []app.AuditEvent {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), actor, summary, fields
		FROM audit_events
		WHERE $1 = '' OR session_id = $1
		ORDER BY happened_at DESC
	`, sessionID)
	if err != nil {
		return []app.AuditEvent{}
	}
	defer rows.Close()
	return collectRows(rows, scanAuditEvent)
}

func (s *PostgresStore) EventsAfter(sessionID, after string) []app.Event {
	var afterSeq int64
	if after != "" {
		_ = s.db.QueryRow(context.Background(), `SELECT seq FROM events WHERE id = $1`, after).Scan(&afterSeq)
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), payload
		FROM events
		WHERE seq > $1 AND ($2 = '' OR session_id = $2)
		ORDER BY seq ASC
	`, afterSeq, sessionID)
	if err != nil {
		return []app.Event{}
	}
	defer rows.Close()
	return collectRows(rows, scanEvent)
}

func (s *PostgresStore) MessageEventHead(ctx context.Context, sessionID string) (string, error) {
	ctx, cancel := operationContext(ctx, OperationConversationMessageHead, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationMessageHead, ctx); err != nil {
		return "", err
	}
	var cursor string
	err := s.conversationPostgres.QueryRow(ctx, `
		SELECT id
		FROM events
		WHERE session_id = $1 AND type = 'message.created'
		ORDER BY seq DESC
		LIMIT 1
	`, sessionID).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", classifyConversationPostgresError(OperationConversationMessageHead, ctx, err)
	}
	return cursor, nil
}

func (s *PostgresStore) MessageEventsAfter(ctx context.Context, sessionID, after string, limit int) (MessageEventPage, error) {
	ctx, cancel := operationContext(ctx, OperationConversationMessagesAfter, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationMessagesAfter, ctx); err != nil {
		return MessageEventPage{}, err
	}
	if limit <= 0 || limit > MessageEventPageLimit {
		limit = MessageEventPageLimit
	}
	var afterSeq int64
	if after != "" {
		var cursorSessionID, cursorType string
		err := s.conversationPostgres.QueryRow(ctx, `
			SELECT seq, coalesce(session_id, ''), type
			FROM events
			WHERE id = $1
		`, after).Scan(&afterSeq, &cursorSessionID, &cursorType)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (cursorSessionID != sessionID || cursorType != "message.created") {
			return MessageEventPage{}, storeError(OperationConversationMessagesAfter, StoreErrorInvalid, ErrMessageEventCursorInvalid)
		}
		if err != nil {
			return MessageEventPage{}, classifyConversationPostgresError(OperationConversationMessagesAfter, ctx, err)
		}
	}

	rows, err := s.conversationPostgres.Query(ctx, `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), payload
		FROM events
		WHERE seq > $1 AND session_id = $2 AND type = 'message.created'
		ORDER BY seq ASC
		LIMIT $3
	`, afterSeq, sessionID, limit+1)
	if err != nil {
		return MessageEventPage{}, classifyConversationPostgresError(OperationConversationMessagesAfter, ctx, err)
	}
	defer rows.Close()
	events := make([]app.Event, 0, limit+1)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return MessageEventPage{}, classifyConversationPostgresError(OperationConversationMessagesAfter, ctx, err)
		}
		events = append(events, cloneClientLifecycleEvent(event))
	}
	if err := rows.Err(); err != nil {
		return MessageEventPage{}, classifyConversationPostgresError(OperationConversationMessagesAfter, ctx, err)
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	next := after
	if len(events) > 0 {
		next = events[len(events)-1].ID
	}
	return MessageEventPage{Events: events, NextCursor: next, HasMore: hasMore}, nil
}

func (s *PostgresStore) SaveEvalRun(run app.EvalRun) {
	if run.ID == "" {
		run.ID = app.NewID("eval")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO eval_runs (id, profile, status, summary, cases, failure_archives, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			profile = EXCLUDED.profile,
			status = EXCLUDED.status,
			summary = EXCLUDED.summary,
			cases = EXCLUDED.cases,
			failure_archives = EXCLUDED.failure_archives,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at
	`, run.ID, run.Profile, run.Status, run.Summary, mustJSON(run.Cases), mustJSON(run.FailureArchives), run.StartedAt, run.CompletedAt)
	s.appendAudit(ctx, "eval."+run.Status, "", "", "evaluator", run.Summary, map[string]any{
		"profile":          run.Profile,
		"id":               run.ID,
		"failure_archives": len(run.FailureArchives),
	})
	s.appendEvent(ctx, "eval."+run.Status, "", run.ID, run)
}

func (s *PostgresStore) GetEvalRun(id string) (app.EvalRun, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, profile, status, summary, cases, failure_archives, started_at, completed_at
		FROM eval_runs
		WHERE id = $1
	`, id)
	run, err := scanEvalRun(row)
	return run, err == nil
}

func (s *PostgresStore) ListEvalRuns() []app.EvalRun {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, profile, status, summary, cases, failure_archives, started_at, completed_at
		FROM eval_runs
		ORDER BY started_at DESC
	`)
	if err != nil {
		return []app.EvalRun{}
	}
	defer rows.Close()
	return collectRows(rows, scanEvalRun)
}

func (s *PostgresStore) SaveArtifactObject(object app.ArtifactObject) {
	if object.ID == "" {
		object.ID = app.NewID("obj")
	}
	if object.CreatedAt.IsZero() {
		object.CreatedAt = time.Now().UTC()
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO artifact_objects (
			id, kind, run_id, eval_id, session_id, backend, bucket, object_key, uri, path,
			content_type, bytes, created_at
		)
		VALUES ($1, $2, nullif($3, ''), nullif($4, ''), nullif($5, ''), $6, nullif($7, ''), $8, $9, nullif($10, ''), $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			kind = EXCLUDED.kind,
			run_id = EXCLUDED.run_id,
			eval_id = EXCLUDED.eval_id,
			session_id = EXCLUDED.session_id,
			backend = EXCLUDED.backend,
			bucket = EXCLUDED.bucket,
			object_key = EXCLUDED.object_key,
			uri = EXCLUDED.uri,
			path = EXCLUDED.path,
			content_type = EXCLUDED.content_type,
			bytes = EXCLUDED.bytes,
			created_at = EXCLUDED.created_at
	`, object.ID, object.Kind, object.RunID, object.EvalID, object.SessionID, object.Backend, object.Bucket, object.Key, object.URI, object.Path, object.ContentType, object.Bytes, object.CreatedAt)
	s.appendAudit(ctx, "artifact.saved", object.SessionID, object.RunID, "artifact-store", object.URI, map[string]any{
		"kind":    object.Kind,
		"backend": object.Backend,
		"key":     object.Key,
		"bytes":   object.Bytes,
		"eval_id": object.EvalID,
	})
	s.appendEvent(ctx, "artifact.saved", object.SessionID, object.RunID, object)
}

func (s *PostgresStore) ListArtifactObjects(limit int) []app.ArtifactObject {
	query := `
		SELECT id, kind, coalesce(run_id, ''), coalesce(eval_id, ''), coalesce(session_id, ''),
			backend, coalesce(bucket, ''), object_key, uri, coalesce(path, ''), content_type, bytes, created_at
		FROM artifact_objects
		ORDER BY created_at DESC
	`
	var rows pgx.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(context.Background(), query+` LIMIT $1`, limit)
	} else {
		rows, err = s.db.Query(context.Background(), query)
	}
	if err != nil {
		return []app.ArtifactObject{}
	}
	defer rows.Close()
	return collectRows(rows, scanArtifactObject)
}

func (s *PostgresStore) FindArtifactObjectByURI(uri, sessionID, runID string) (app.ArtifactObject, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, kind, coalesce(run_id, ''), coalesce(eval_id, ''), coalesce(session_id, ''),
			backend, coalesce(bucket, ''), object_key, uri, coalesce(path, ''), content_type, bytes, created_at
		FROM artifact_objects
		WHERE uri = $1
		  AND ($2 = '' OR session_id = $2)
		  AND ($3 = '' OR run_id = $3)
		ORDER BY created_at DESC
		LIMIT 1
	`, uri, sessionID, runID)
	object, err := scanArtifactObject(row)
	return object, err == nil
}

func (s *PostgresStore) SaveEpisodeSummary(ctx context.Context, summary app.EpisodeSummary) (app.EpisodeSummary, error) {
	summary, err := prepareEpisodeSummary(summary, time.Now().UTC())
	if err != nil {
		return app.EpisodeSummary{}, storeError(OperationEpisodeSummarySave, StoreErrorInvalid, err)
	}
	return runPostgresWrite(s, ctx, OperationEpisodeSummarySave, "episode_summary", summary.ID, summary, func(transaction onboardingPostgresTx, commandCtx context.Context) error {
		if _, err := transaction.Exec(commandCtx, `
			INSERT INTO episode_summaries (id, session_id, run_id, goal, outcome, risk_level, model_lane, tools, approvals,
				failures, repair_performed, summary, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (id) DO UPDATE SET goal=EXCLUDED.goal, outcome=EXCLUDED.outcome,
				risk_level=EXCLUDED.risk_level, model_lane=EXCLUDED.model_lane, tools=EXCLUDED.tools,
				approvals=EXCLUDED.approvals, failures=EXCLUDED.failures,
				repair_performed=EXCLUDED.repair_performed, summary=EXCLUDED.summary, created_at=EXCLUDED.created_at
		`, summary.ID, summary.SessionID, summary.RunID, summary.Goal, summary.Outcome, string(summary.Risk), summary.ModelLane,
			mustJSON(summary.Tools), mustJSON(summary.Approvals), mustJSON(summary.Failures), summary.RepairPerformed, summary.Summary, summary.CreatedAt); err != nil {
			return err
		}
		return appendRunLifecycle(transaction, commandCtx, "episode_summary.saved", summary.SessionID, summary.RunID, "runtime", summary.Outcome, map[string]any{
			"tools": summary.Tools, "repair_performed": summary.RepairPerformed,
		}, summary)
	})
}

func (s *PostgresStore) ListEpisodeSummaries(ctx context.Context, sessionID string) ([]app.EpisodeSummary, error) {
	ctx, cancel := operationContext(ctx, OperationEpisodeSummaryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEpisodeSummaryList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, session_id, run_id, goal, outcome, risk_level, model_lane, tools, approvals,
			failures, repair_performed, summary, created_at
		FROM episode_summaries
		WHERE $1 = '' OR session_id = $1
		ORDER BY created_at DESC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationEpisodeSummaryList, ctx, err)
	}
	defer rows.Close()
	out := []app.EpisodeSummary{}
	for rows.Next() {
		summary, err := scanEpisodeSummary(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationEpisodeSummaryList, ctx, err)
		}
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationEpisodeSummaryList, ctx, err)
	}
	return out, nil
}

func mapValues[K comparable, V any](values map[K]V) []V {
	out := make([]V, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func (s *PostgresStore) appendAudit(ctx context.Context, typ, sessionID, runID, actor, summary string, fields map[string]any) {
	_, _ = s.db.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), time.Now().UTC(), typ, sessionID, runID, actor, summary, optionalJSON(fields))
}

func (s *PostgresStore) appendEvent(ctx context.Context, typ, sessionID, runID string, payload any) {
	_, _ = s.db.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), time.Now().UTC(), typ, sessionID, runID, mustJSON(payload))
}

type scanner interface {
	Scan(dest ...any) error
}

func collectRows[T any](rows pgx.Rows, scan func(scanner) (T, error)) []T {
	out := []T{}
	for rows.Next() {
		value, err := scan(rows)
		if err == nil {
			out = append(out, value)
		}
	}
	return out
}

func scanSession(row scanner) (app.Session, error) {
	var session app.Session
	err := row.Scan(&session.ID, &session.OwnerID, &session.WorkspaceRoot, &session.Title, &session.Source, &session.Hidden, &session.CreatedAt, &session.UpdatedAt)
	return session, err
}

func scanClient(row scanner) (app.Client, error) {
	var client app.Client
	err := row.Scan(&client.ID, &client.OwnerID, &client.ActorID, &client.Name, &client.TokenHash, &client.CreatedAt, &client.LastSeenAt, &client.RevokedAt)
	return client, err
}

func scanOwnerProfile(row scanner) (app.OwnerProfile, error) {
	var profile app.OwnerProfile
	var preferences []byte
	err := row.Scan(&profile.ID, &profile.Source, &profile.ExternalRef, &profile.WorkspaceRoot,
		&profile.DefaultChannel, &profile.DefaultBindingID, &profile.DisplayName, &profile.Email,
		&preferences, &profile.CreatedAt, &profile.UpdatedAt)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	profile.Preferences = map[string]string{}
	if err := json.Unmarshal(preferences, &profile.Preferences); err != nil {
		return app.OwnerProfile{}, errors.Join(errOwnerPreferencesDecode, err)
	}
	if profile.Preferences == nil {
		profile.Preferences = map[string]string{}
	}
	return profile, nil
}

var errOwnerPreferencesDecode = errors.New("owner preferences decode failed")

func classifyPostgresOwnerReadError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errOwnerPreferencesDecode) {
		return storeError(operation, StoreErrorCorrupt, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}

func scanPairingCode(row scanner) (app.PairingCode, error) {
	var code app.PairingCode
	err := row.Scan(&code.ID, &code.CodeHash, &code.Status, &code.ExpiresAt, &code.CreatedAt, &code.ClaimedAt, &code.ClientID)
	return code, err
}

func scanMessage(row scanner) (app.Message, error) {
	var message app.Message
	var attachments, requestedMedia []byte
	err := row.Scan(&message.ID, &message.SessionID, &message.RunID, &message.Role, &message.Content, &attachments, &requestedMedia, &message.CreatedAt)
	if err != nil {
		return app.Message{}, err
	}
	if len(attachments) > 0 {
		if err := json.Unmarshal(attachments, &message.Attachments); err != nil {
			return app.Message{}, fmt.Errorf("%w: attachments: %v", errMessageJSONDecode, err)
		}
	}
	if len(requestedMedia) > 0 {
		if err := json.Unmarshal(requestedMedia, &message.RequestedMedia); err != nil {
			return app.Message{}, fmt.Errorf("%w: requested media: %v", errMessageJSONDecode, err)
		}
	}
	return cloneMessage(message), nil
}

func scanExternalChatSession(row scanner) (app.ExternalChatSession, error) {
	var session app.ExternalChatSession
	err := row.Scan(
		&session.ID,
		&session.OwnerID,
		&session.AuthorizedOwnerID,
		&session.AuthorizedActorID,
		&session.WorkspaceRoot,
		&session.BindingID,
		&session.Channel,
		&session.Provider,
		&session.ExternalUserID,
		&session.ExternalChatID,
		&session.ExternalThreadID,
		&session.DisplayName,
		&session.LinkedSessionID,
		&session.Status,
		&session.ProviderCursor,
		&session.LastContextToken,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	return session, err
}

func scanExternalChatMessage(row scanner) (app.ExternalChatMessage, error) {
	var message app.ExternalChatMessage
	err := row.Scan(
		&message.ID,
		&message.ChatSessionID,
		&message.BindingID,
		&message.Channel,
		&message.Direction,
		&message.Role,
		&message.ExternalMessageID,
		&message.Content,
		&message.ContextToken,
		&message.LinkedRunID,
		&message.Status,
		&message.Error,
		&message.PendingReplyKind,
		&message.PendingReply,
		&message.DispatchAttempts,
		&message.CreatedAt,
		&message.UpdatedAt,
	)
	return message, err
}

func scanChannelInboxUpdate(row scanner) (app.ChannelInboxUpdate, error) {
	var update app.ChannelInboxUpdate
	var payload []byte
	err := row.Scan(
		&update.ID,
		&update.BindingID,
		&update.Channel,
		&update.ExternalID,
		&update.ChatKey,
		&payload,
		&update.Status,
		&update.Attempts,
		&update.AvailableAt,
		&update.LastError,
		&update.CreatedAt,
		&update.UpdatedAt,
	)
	update.Payload = append([]byte(nil), payload...)
	return update, err
}

func scanPassiveNotification(row scanner) (app.PassiveNotification, error) {
	var notification app.PassiveNotification
	err := row.Scan(
		&notification.ID,
		&notification.OwnerID,
		&notification.EndpointID,
		&notification.IdempotencyKey,
		&notification.Fingerprint,
		&notification.NotificationID,
		&notification.Source,
		&notification.Kind,
		&notification.DeepLink,
		&notification.OccurredAt,
		&notification.ReadAt,
		&notification.CreatedAt,
		&notification.UpdatedAt,
	)
	return notification, err
}

func scanRunFeedback(row scanner) (app.RunFeedback, error) {
	var feedback app.RunFeedback
	err := row.Scan(
		&feedback.ID,
		&feedback.SessionID,
		&feedback.RunID,
		&feedback.MessageID,
		&feedback.Rating,
		&feedback.Note,
		&feedback.Correction,
		&feedback.CreatedAt,
		&feedback.UpdatedAt,
	)
	return feedback, err
}

func scanRun(row scanner) (app.AgentRun, error) {
	var run app.AgentRun
	var risk string
	var workflowState []byte
	var messageContext []byte
	err := row.Scan(&run.ID, &run.SessionID, &run.State, &run.ModelLane, &risk, &run.StartedAt, &run.CompletedAt, &run.Summary, &workflowState, &messageContext)
	if err != nil {
		return app.AgentRun{}, err
	}
	run.Risk = app.RiskLevel(risk)
	if len(workflowState) > 0 {
		var workflow app.WorkflowState
		if err := json.Unmarshal(workflowState, &workflow); err != nil {
			return app.AgentRun{}, fmt.Errorf("%w: workflow state: %v", errRunJSONDecode, err)
		}
		run.Workflow = &workflow
	}
	if len(messageContext) > 0 {
		var context app.MessageRunContext
		if err := json.Unmarshal(messageContext, &context); err != nil {
			return app.AgentRun{}, fmt.Errorf("%w: message context: %v", errRunJSONDecode, err)
		}
		run.MessageContext = &context
	}
	return run, nil
}

func scanModelCall(row scanner) (app.ModelCall, error) {
	var call app.ModelCall
	err := row.Scan(
		&call.ID,
		&call.SessionID,
		&call.RunID,
		&call.Lane,
		&call.Profile,
		&call.Model,
		&call.Operation,
		&call.Mock,
		&call.Fallback,
		&call.Status,
		&call.PromptTokens,
		&call.ResponseTokens,
		&call.TotalTokens,
		&call.LatencyMS,
		&call.Error,
		&call.StartedAt,
		&call.CompletedAt,
	)
	return call, err
}

func scanToolCall(row scanner) (app.ToolCall, error) {
	var call app.ToolCall
	var risk string
	var args []byte
	var result []byte
	var policyContext []byte
	err := row.Scan(&call.ID, &call.SessionID, &call.RunID, &call.WorkflowID, &call.WorkflowNodeID, &call.ScopeRevision, &call.Capability,
		&call.Tool, &risk, &call.Status, &args, &result, &call.Error, &call.ErrorCode, &call.ApprovalID, &call.StartedAt, &call.CompletedAt, &call.ObservationRef, &call.ObservationSummary, &policyContext)
	if err != nil {
		return app.ToolCall{}, err
	}
	call.Risk = app.RiskLevel(risk)
	call.Arguments = map[string]any{}
	if err := json.Unmarshal(args, &call.Arguments); err != nil {
		return app.ToolCall{}, fmt.Errorf("%w: tool arguments: %v", errRunJSONDecode, err)
	}
	if len(result) > 0 && string(result) != "null" {
		if err := json.Unmarshal(result, &call.Result); err != nil {
			return app.ToolCall{}, fmt.Errorf("%w: tool result: %v", errRunJSONDecode, err)
		}
	}
	if len(policyContext) > 0 && string(policyContext) != "null" {
		call.PolicyContext = &app.PolicyExecutionContext{}
		if err := json.Unmarshal(policyContext, call.PolicyContext); err != nil {
			return app.ToolCall{}, fmt.Errorf("%w: tool policy context: %v", errRunJSONDecode, err)
		}
	}
	return call, nil
}

func scanDocumentRecord(row scanner) (app.DocumentRecord, error) {
	var record app.DocumentRecord
	err := row.Scan(
		&record.ID,
		&record.OwnerID,
		&record.SessionID,
		&record.GovernedPath,
		&record.Name,
		&record.ContentType,
		&record.Format,
		&record.SizeBytes,
		&record.SHA256,
		&record.Status,
		&record.Source,
		&record.SourceMessageID,
		&record.SourceRunID,
		&record.SourceToolCallID,
		&record.ParentDocumentID,
		&record.LastActivity,
		&record.LastActivityID,
		&record.LastActivityAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	return normalizePersistedDocumentRecord(record), nil
}

func scanApproval(row scanner) (app.Approval, error) {
	var approval app.Approval
	var source string
	var risk string
	var externalContext []byte
	var resources []byte
	var args []byte
	var policyContext []byte
	err := row.Scan(&approval.ID, &source, &approval.ExternalID, &externalContext,
		&approval.SessionID, &approval.RunID, &approval.ToolCallID, &approval.Tool, &risk,
		&approval.Status, &approval.Summary, &approval.Reason, &resources, &args,
		&approval.CreatedAt, &approval.ResolvedAt, &approval.ResolutionNote, &policyContext)
	if err != nil {
		return app.Approval{}, err
	}
	approval.Source = app.ApprovalSource(source)
	approval.Risk = app.RiskLevel(risk)
	if len(externalContext) > 0 && string(externalContext) != "null" {
		approval.ExternalContext = &app.ExternalApprovalContext{}
		_ = json.Unmarshal(externalContext, approval.ExternalContext)
	}
	approval.Resources = []string{}
	_ = json.Unmarshal(resources, &approval.Resources)
	approval.Arguments = map[string]any{}
	_ = json.Unmarshal(args, &approval.Arguments)
	if len(policyContext) > 0 && string(policyContext) != "null" {
		approval.PolicyContext = &app.PolicyExecutionContext{}
		_ = json.Unmarshal(policyContext, approval.PolicyContext)
	}
	return normalizeApproval(approval), nil
}

func scanReminder(row scanner) (app.Reminder, error) {
	var reminder app.Reminder
	var scheduleSpec []byte
	err := row.Scan(&reminder.ID, &reminder.SessionID, &reminder.RunID, &reminder.Text, &reminder.TextSummary,
		&reminder.DueTime, &reminder.Timezone, &reminder.Channel, &reminder.Recipient, &reminder.RecipientBinding,
		&reminder.BindingID, &reminder.CredentialRef, &reminder.BaseURL, &reminder.Recurrence,
		&reminder.DedupeKey, &reminder.Status, &reminder.LastDeliveryID, &reminder.LastError,
		&reminder.CreatedAt, &reminder.UpdatedAt, &reminder.SentAt, &reminder.CanceledAt, &reminder.DeliveryAttempt, &scheduleSpec)
	if err == nil && len(scheduleSpec) > 0 && string(scheduleSpec) != "null" {
		var spec app.ScheduleSpec
		if json.Unmarshal(scheduleSpec, &spec) == nil && spec.SchemaVersion != 0 {
			reminder.ScheduleSpec = &spec
		}
	}
	return reminder, err
}

func scanReminderDelivery(row scanner) (app.ReminderDelivery, error) {
	var delivery app.ReminderDelivery
	err := row.Scan(&delivery.ID, &delivery.ReminderID, &delivery.Channel, &delivery.Provider, &delivery.Recipient,
		&delivery.Status, &delivery.ProviderStatus, &delivery.Error, &delivery.RetryState, &delivery.Attempt,
		&delivery.SentAt, &delivery.CreatedAt)
	return delivery, err
}

func scanConnectorSetting(row scanner) (app.ConnectorSetting, error) {
	var setting app.ConnectorSetting
	err := row.Scan(&setting.OwnerID, &setting.Channel, &setting.Enabled, &setting.ISCPEnabled, &setting.LANAccessEnabled, &setting.Version, &setting.UpdatedBy, &setting.UpdatedAt)
	return setting, err
}

func scanNotificationBinding(row scanner) (app.NotificationBinding, error) {
	var binding app.NotificationBinding
	var scopes []byte
	err := row.Scan(&binding.ID, &binding.OwnerID, &binding.ActorID, &binding.Channel, &binding.Provider, &binding.Status,
		&binding.DisplayName, &binding.ExternalUserID, &binding.ExternalChatID, &binding.ExternalThreadID, &binding.AccountID, &binding.CredentialRef,
		&binding.BaseURL, &binding.ProviderSessionID, &binding.ProviderState, &binding.ContextToken,
		&binding.ProviderCursor, &binding.QRCodeURL, &binding.QRCodeImage, &binding.DefaultForChannel,
		&scopes, &binding.CreatedAt, &binding.UpdatedAt, &binding.ExpiresAt, &binding.RevokedAt,
		&binding.LastError, &binding.Version, &binding.CredentialKind)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	binding.Scopes = []string{}
	if err := json.Unmarshal(scopes, &binding.Scopes); err != nil {
		return app.NotificationBinding{}, errors.Join(errNotificationBindingScopesDecode, err)
	}
	return binding, nil
}

func scanCredentialSecret(row scanner) (app.CredentialSecret, error) {
	var secret app.CredentialSecret
	err := row.Scan(&secret.Ref, &secret.Kind, &secret.Value, &secret.CreatedAt, &secret.UpdatedAt)
	return secret, err
}

func scanBrowserAuthRecord(row scanner) (app.BrowserAuthRecord, error) {
	var record app.BrowserAuthRecord
	var lastVerifiedAt *time.Time
	err := row.Scan(
		&record.ID,
		&record.OwnerID,
		&record.BrowserProfileID,
		&record.SiteOrigin,
		&record.SiteRealm,
		&record.AccountHint,
		&record.AuthStrategy,
		&record.Status,
		&record.SessionRef,
		&record.CredentialRef,
		&record.CookieJarRef,
		&lastVerifiedAt,
		&record.ExpiresAt,
		&record.LastError,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.RevokedAt,
	)
	if lastVerifiedAt != nil {
		record.LastVerifiedAt = *lastVerifiedAt
	}
	return record, err
}

func scanBrowserLoginBlock(row scanner) (app.BrowserLoginBlock, error) {
	var block app.BrowserLoginBlock
	var args, target, visibleEvidence []byte
	err := row.Scan(
		&block.ID,
		&block.SessionID,
		&block.RunID,
		&block.SchemaVersion,
		&block.Version,
		&block.WorkflowID,
		&block.WorkflowRevision,
		&block.WorkflowNodeID,
		&block.SessionGeneration,
		&block.Status,
		&block.OriginalGoal,
		&block.ResumeTool,
		&args,
		&block.LastToolCallID,
		&block.LoginHandoffURL,
		&block.LoginHandoffPageID,
		&block.LastVisiblePageID,
		&block.OwnerID,
		&block.BrowserProfileID,
		&block.SiteOrigin,
		&block.SiteRealm,
		&block.AccountHint,
		&block.BrowserAuthStatus,
		&target,
		&visibleEvidence,
		&block.LastUserReply,
		&block.LastError,
		&block.TransitionOwnerID,
		&block.TransitionLeaseUntil,
		&block.CreatedAt,
		&block.UpdatedAt,
		&block.ResolvedAt,
	)
	if len(args) > 0 {
		_ = json.Unmarshal(args, &block.ResumeArgs)
	}
	if len(target) > 0 {
		_ = json.Unmarshal(target, &block.Target)
	}
	if len(visibleEvidence) > 0 && string(visibleEvidence) != "null" {
		_ = json.Unmarshal(visibleEvidence, &block.VisibleEvidence)
	}
	if block.ResumeArgs == nil {
		block.ResumeArgs = map[string]any{}
	}
	return block, err
}

func scanMemoryCandidate(row scanner) (app.MemoryCandidate, error) {
	var candidate app.MemoryCandidate
	err := row.Scan(&candidate.ID, &candidate.SessionID, &candidate.RunID, &candidate.Kind, &candidate.Content, &candidate.Sensitivity, &candidate.Status, &candidate.Reason, &candidate.CreatedAt, &candidate.ResolvedAt)
	return candidate, err
}

func scanMemory(row scanner) (app.Memory, error) {
	var memory app.Memory
	err := row.Scan(&memory.ID, &memory.Kind, &memory.Content, &memory.SourceID, &memory.CreatedAt)
	return memory, err
}

func scanMemoryWithSession(row scanner) (app.Memory, string, error) {
	var memory app.Memory
	var sessionID string
	err := row.Scan(&memory.ID, &memory.Kind, &memory.Content, &memory.SourceID, &memory.CreatedAt, &sessionID)
	return memory, sessionID, err
}

func scanAuditEvent(row scanner) (app.AuditEvent, error) {
	var event app.AuditEvent
	var fields []byte
	err := row.Scan(&event.ID, &event.Time, &event.Type, &event.SessionID, &event.RunID, &event.Actor, &event.Summary, &fields)
	if err != nil {
		return app.AuditEvent{}, err
	}
	if len(fields) > 0 {
		event.Fields = map[string]any{}
		_ = json.Unmarshal(fields, &event.Fields)
	}
	return event, nil
}

func scanEvent(row scanner) (app.Event, error) {
	var event app.Event
	var payload []byte
	err := row.Scan(&event.ID, &event.Time, &event.Type, &event.SessionID, &event.RunID, &payload)
	if err != nil {
		return app.Event{}, err
	}
	event.Payload = decodeJSON(payload)
	return event, nil
}

func scanEvalRun(row scanner) (app.EvalRun, error) {
	var run app.EvalRun
	var cases []byte
	var failureArchives []byte
	err := row.Scan(&run.ID, &run.Profile, &run.Status, &run.Summary, &cases, &failureArchives, &run.StartedAt, &run.CompletedAt)
	if err != nil {
		return app.EvalRun{}, err
	}
	run.Cases = []app.EvalCase{}
	_ = json.Unmarshal(cases, &run.Cases)
	run.FailureArchives = []app.EvalArtifact{}
	_ = json.Unmarshal(failureArchives, &run.FailureArchives)
	return run, nil
}

func scanArtifactObject(row scanner) (app.ArtifactObject, error) {
	var object app.ArtifactObject
	err := row.Scan(
		&object.ID,
		&object.Kind,
		&object.RunID,
		&object.EvalID,
		&object.SessionID,
		&object.Backend,
		&object.Bucket,
		&object.Key,
		&object.URI,
		&object.Path,
		&object.ContentType,
		&object.Bytes,
		&object.CreatedAt,
	)
	return object, err
}

func scanEpisodeSummary(row scanner) (app.EpisodeSummary, error) {
	var summary app.EpisodeSummary
	var risk string
	var tools []byte
	var approvals []byte
	var failures []byte
	err := row.Scan(&summary.ID, &summary.SessionID, &summary.RunID, &summary.Goal, &summary.Outcome, &risk, &summary.ModelLane, &tools, &approvals, &failures, &summary.RepairPerformed, &summary.Summary, &summary.CreatedAt)
	if err != nil {
		return app.EpisodeSummary{}, err
	}
	summary.Risk = app.RiskLevel(risk)
	if err := json.Unmarshal(tools, &summary.Tools); err != nil {
		return app.EpisodeSummary{}, fmt.Errorf("%w: episode tools: %v", errRunJSONDecode, err)
	}
	if err := json.Unmarshal(approvals, &summary.Approvals); err != nil {
		return app.EpisodeSummary{}, fmt.Errorf("%w: episode approvals: %v", errRunJSONDecode, err)
	}
	if err := json.Unmarshal(failures, &summary.Failures); err != nil {
		return app.EpisodeSummary{}, fmt.Errorf("%w: episode failures: %v", errRunJSONDecode, err)
	}
	return summary, nil
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(`null`)
	}
	return raw
}

func mustJSONRaw(value json.RawMessage) []byte {
	if len(value) == 0 || !json.Valid(value) {
		return []byte(`{}`)
	}
	return append([]byte(nil), value...)
}

func optionalJSON(value any) []byte {
	if value == nil {
		return nil
	}
	return mustJSON(value)
}

func decodeJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func zeroTimeToNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func redactPostgresExternalID(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 6 {
		return value
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-2:])
}

func rollbackTx(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}
