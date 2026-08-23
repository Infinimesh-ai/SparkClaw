package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const browserAuthSelectSQL = `
	SELECT id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
		session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
	FROM browser_auth_records`

const browserLoginBlockSelectSQL = `
	SELECT id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
		workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
		last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
		owner_id, browser_profile_id, site_origin,
		site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
		transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
	FROM browser_login_blocks`

func (s *PostgresStore) SaveBrowserAuthRecord(ctx context.Context, record app.BrowserAuthRecord) (app.BrowserAuthRecord, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthSave, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	record.ID = strings.TrimSpace(record.ID)
	if record.ID == "" {
		record.ID = app.NewID("bauth")
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationBrowserAuthSave, s.browserStatePostgres)
	if err != nil {
		return app.BrowserAuthRecord{}, err
	}
	defer releasePostgresSession(session, release)
	current, err := scanBrowserAuthRecord(transaction.QueryRow(ctx, browserAuthSelectSQL+` WHERE id = $1 FOR UPDATE`, record.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		current = app.BrowserAuthRecord{}
	} else if err != nil {
		return app.BrowserAuthRecord{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserAuthSave, session, transaction, release, err)
	}
	record = normalizeBrowserAuthRecord(record, current)
	if _, err := transaction.Exec(ctx, `
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
		record.ExpiresAt, record.LastError, record.CreatedAt, record.UpdatedAt, record.RevokedAt); err != nil {
		return app.BrowserAuthRecord{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserAuthSave, session, transaction, release, err)
	}
	if err := appendBrowserStateLifecycle(transaction, ctx, "browser_auth.record_saved", "", "", "gateway", record.SiteOrigin, browserAuthAuditFields(record, nil), record); err != nil {
		return app.BrowserAuthRecord{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserAuthSave, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return record, storeError(ctx, OperationBrowserAuthSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneBrowserAuthRecord(record), nil
}

func (s *PostgresStore) GetBrowserAuthRecord(ctx context.Context, id string) (app.BrowserAuthRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthGet, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	record, err := scanBrowserAuthRecord(s.browserStatePostgres.QueryRow(ctx, browserAuthSelectSQL+` WHERE id = $1`, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.BrowserAuthRecord{}, false, nil
	}
	if err != nil {
		return app.BrowserAuthRecord{}, false, classifyBrowserStatePostgresError(OperationBrowserAuthGet, ctx, err)
	}
	return record, true, nil
}

func (s *PostgresStore) FindBrowserAuthRecord(ctx context.Context, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthFind, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	ownerID, browserProfileID, siteOrigin, siteRealm, accountHint = normalizeBrowserAuthLookup(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
	record, err := scanBrowserAuthRecord(s.browserStatePostgres.QueryRow(ctx, browserAuthSelectSQL+`
		WHERE owner_id = $1
		  AND browser_profile_id = $2
		  AND site_origin = $3
		  AND site_realm = $4
		  AND account_hint = $5
		  AND status = $6
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY updated_at DESC, id ASC
		LIMIT 1
	`, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint, app.BrowserAuthStatusActive))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.BrowserAuthRecord{}, false, nil
	}
	if err != nil {
		return app.BrowserAuthRecord{}, false, classifyBrowserStatePostgresError(OperationBrowserAuthFind, ctx, err)
	}
	return record, true, nil
}

func (s *PostgresStore) ListBrowserAuthRecords(ctx context.Context, ownerID, browserProfileID string) ([]app.BrowserAuthRecord, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthList, ctx); err != nil {
		return nil, err
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID != "" {
		ownerID = normalizeBrowserAuthOwnerID(ownerID)
	}
	browserProfileID = strings.TrimSpace(browserProfileID)
	if browserProfileID != "" {
		browserProfileID = normalizeBrowserProfileID(browserProfileID)
	}
	rows, err := s.browserStatePostgres.Query(ctx, browserAuthSelectSQL+`
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR browser_profile_id = $2)
		ORDER BY updated_at DESC, id ASC
	`, ownerID, browserProfileID)
	if err != nil {
		return nil, classifyBrowserStatePostgresError(OperationBrowserAuthList, ctx, err)
	}
	defer rows.Close()
	records := []app.BrowserAuthRecord{}
	for rows.Next() {
		record, err := scanBrowserAuthRecord(rows)
		if err != nil {
			return nil, classifyBrowserStatePostgresError(OperationBrowserAuthList, ctx, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyBrowserStatePostgresError(OperationBrowserAuthList, ctx, err)
	}
	return records, nil
}

func (s *PostgresStore) RevokeBrowserAuthRecord(ctx context.Context, id, reason string) (app.BrowserAuthRecord, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthRevoke, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationBrowserAuthRevoke, s.browserStatePostgres)
	if err != nil {
		return app.BrowserAuthRecord{}, err
	}
	defer releasePostgresSession(session, release)
	record, err := scanBrowserAuthRecord(transaction.QueryRow(ctx, browserAuthSelectSQL+` WHERE id = $1 FOR UPDATE`, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.BrowserAuthRecord{}, browserStateBusinessError(ctx, OperationBrowserAuthRevoke, StoreErrorNotFound, session, transaction, release, errors.New("browser auth record not found"))
	}
	if err != nil {
		return app.BrowserAuthRecord{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserAuthRevoke, session, transaction, release, err)
	}
	now := postgresTime(time.Now().UTC())
	record.Status = app.BrowserAuthStatusRevoked
	record.RevokedAt = &now
	record.UpdatedAt = now
	record.LastError = strings.TrimSpace(reason)
	if _, err := transaction.Exec(ctx, `
		UPDATE browser_auth_records
		SET status = $2, revoked_at = $3, updated_at = $4, last_error = $5
		WHERE id = $1
	`, record.ID, record.Status, record.RevokedAt, record.UpdatedAt, record.LastError); err != nil {
		return app.BrowserAuthRecord{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserAuthRevoke, session, transaction, release, err)
	}
	if err := appendBrowserStateLifecycle(transaction, ctx, "browser_auth.record_revoked", "", "", "owner", record.SiteOrigin, browserAuthAuditFields(record, map[string]any{"reason": record.LastError}), record); err != nil {
		return app.BrowserAuthRecord{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserAuthRevoke, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return record, storeError(ctx, OperationBrowserAuthRevoke, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneBrowserAuthRecord(record), nil
}

func (s *PostgresStore) SaveBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock) (app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockSave, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	block.ID = strings.TrimSpace(block.ID)
	if block.ID == "" {
		block.ID = app.NewID("blogin")
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationBrowserLoginBlockSave, s.browserStatePostgres)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	defer releasePostgresSession(session, release)
	current, err := scanBrowserLoginBlock(transaction.QueryRow(ctx, browserLoginBlockSelectSQL+` WHERE id = $1 FOR UPDATE`, block.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		current = app.BrowserLoginBlock{}
	} else if err != nil {
		return app.BrowserLoginBlock{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserLoginBlockSave, session, transaction, release, err)
	}
	block = normalizeBrowserLoginBlock(block, current)
	if _, err := transaction.Exec(ctx, `
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
		block.TransitionOwnerID, block.TransitionLeaseUntil, block.CreatedAt, block.UpdatedAt, block.ResolvedAt); err != nil {
		return app.BrowserLoginBlock{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserLoginBlockSave, session, transaction, release, err)
	}
	if err := appendBrowserStateLifecycle(transaction, ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil), block); err != nil {
		return app.BrowserLoginBlock{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserLoginBlockSave, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return block, storeError(ctx, OperationBrowserLoginBlockSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneBrowserLoginBlock(block), nil
}

func (s *PostgresStore) UpdateBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockUpdate, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationBrowserLoginBlockUpdate, s.browserStatePostgres)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	defer releasePostgresSession(session, release)
	current, err := scanBrowserLoginBlock(transaction.QueryRow(ctx, browserLoginBlockSelectSQL+` WHERE id = $1 FOR UPDATE`, strings.TrimSpace(block.ID)))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && current.Version != expectedVersion {
		return app.BrowserLoginBlock{}, browserStateBusinessError(ctx, OperationBrowserLoginBlockUpdate, StoreErrorConflict, session, transaction, release, ErrBrowserHandoffConflict)
	}
	if err != nil {
		return app.BrowserLoginBlock{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserLoginBlockUpdate, session, transaction, release, err)
	}
	block.Version = expectedVersion + 1
	block = normalizeBrowserLoginBlock(block, current)
	result, err := transaction.Exec(ctx, `
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
		return app.BrowserLoginBlock{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserLoginBlockUpdate, session, transaction, release, err)
	}
	if result.RowsAffected() != 1 {
		return app.BrowserLoginBlock{}, browserStateBusinessError(ctx, OperationBrowserLoginBlockUpdate, StoreErrorConflict, session, transaction, release, ErrBrowserHandoffConflict)
	}
	if err := appendBrowserStateLifecycle(transaction, ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil), block); err != nil {
		return app.BrowserLoginBlock{}, finishBrowserStatePostgresStatement(ctx, OperationBrowserLoginBlockUpdate, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return block, storeError(ctx, OperationBrowserLoginBlockUpdate, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneBrowserLoginBlock(block), nil
}

func (s *PostgresStore) GetBrowserLoginBlock(ctx context.Context, id string) (app.BrowserLoginBlock, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockGet, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	block, err := scanBrowserLoginBlock(s.browserStatePostgres.QueryRow(ctx, browserLoginBlockSelectSQL+` WHERE id = $1`, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.BrowserLoginBlock{}, false, nil
	}
	if err != nil {
		return app.BrowserLoginBlock{}, false, classifyBrowserStatePostgresError(OperationBrowserLoginBlockGet, ctx, err)
	}
	return block, true, nil
}

func (s *PostgresStore) FindActiveBrowserLoginBlock(ctx context.Context, sessionID string) (app.BrowserLoginBlock, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockFindActive, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockFindActive, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	block, err := scanBrowserLoginBlock(s.browserStatePostgres.QueryRow(ctx, browserLoginBlockSelectSQL+`
		WHERE session_id = $1 AND status = ANY($2)
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, strings.TrimSpace(sessionID), app.BrowserHandoffActiveStatuses()))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.BrowserLoginBlock{}, false, nil
	}
	if err != nil {
		return app.BrowserLoginBlock{}, false, classifyBrowserStatePostgresError(OperationBrowserLoginBlockFindActive, ctx, err)
	}
	return block, true, nil
}

func (s *PostgresStore) ListBrowserLoginBlocks(ctx context.Context, sessionID, status string) ([]app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.browserStatePostgres.Query(ctx, browserLoginBlockSelectSQL+`
		WHERE ($1 = '' OR session_id = $1) AND ($2 = '' OR status = $2)
		ORDER BY updated_at DESC, id DESC
	`, strings.TrimSpace(sessionID), strings.TrimSpace(status))
	if err != nil {
		return nil, classifyBrowserStatePostgresError(OperationBrowserLoginBlockList, ctx, err)
	}
	defer rows.Close()
	blocks := []app.BrowserLoginBlock{}
	for rows.Next() {
		block, err := scanBrowserLoginBlock(rows)
		if err != nil {
			return nil, classifyBrowserStatePostgresError(OperationBrowserLoginBlockList, ctx, err)
		}
		blocks = append(blocks, block)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyBrowserStatePostgresError(OperationBrowserLoginBlockList, ctx, err)
	}
	return blocks, nil
}

func appendBrowserStateLifecycle(transaction onboardingPostgresTx, ctx context.Context, typ, sessionID, runID, actor, summary string, fields map[string]any, payload any) error {
	at := postgresTime(time.Now().UTC())
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), at, typ, sessionID, runID, actor, summary, optionalJSON(fields)); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), at, typ, sessionID, runID, mustJSON(payload))
	return err
}

func finishBrowserStatePostgresStatement(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyBrowserStatePostgresError(operation, ctx, cause)
}

func browserStateBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(ctx, operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func classifyBrowserStatePostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errBrowserLoginBlockJSONDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return contextStoreError(operation, ctx, cause)
	}
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) {
		return storeError(ctx, operation, StoreErrorInternal, cause)
	}
	return storeError(ctx, operation, StoreErrorUnavailable, cause)
}
