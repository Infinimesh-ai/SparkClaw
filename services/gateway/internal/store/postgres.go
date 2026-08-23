package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	approvalPostgres                  ownerPostgresOps
	auditPostgres                     ownerPostgresOps
	evaluationPostgres                ownerPostgresOps
	artifactMetadataPostgres          ownerPostgresOps
	browserStatePostgres              ownerPostgresOps
	memoryPostgres                    ownerPostgresOps
	schedulePostgres                  ownerPostgresOps
	passiveNotificationPostgres       ownerPostgresOps
	deliveryRecordPostgres            ownerPostgresOps
	externalChatPostgres              ownerPostgresOps
	mcpPostgres                       ownerPostgresOps
	approvalCommandGate               *semaphore.Weighted
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
		approvalPostgres:                  pgxOwnerPostgresOps{pool: pool},
		auditPostgres:                     pgxOwnerPostgresOps{pool: pool},
		evaluationPostgres:                pgxOwnerPostgresOps{pool: pool},
		artifactMetadataPostgres:          pgxOwnerPostgresOps{pool: pool},
		browserStatePostgres:              pgxOwnerPostgresOps{pool: pool},
		memoryPostgres:                    pgxOwnerPostgresOps{pool: pool},
		schedulePostgres:                  pgxOwnerPostgresOps{pool: pool},
		passiveNotificationPostgres:       pgxOwnerPostgresOps{pool: pool},
		deliveryRecordPostgres:            pgxOwnerPostgresOps{pool: pool},
		externalChatPostgres:              pgxOwnerPostgresOps{pool: pool},
		mcpPostgres:                       pgxOwnerPostgresOps{pool: pool},
		approvalCommandGate:               semaphore.NewWeighted(1),
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

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(`null`)
	}
	return raw
}

func mustJSONRaw(value json.RawMessage) []byte {
	if len(value) == 0 {
		return []byte(`null`)
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
