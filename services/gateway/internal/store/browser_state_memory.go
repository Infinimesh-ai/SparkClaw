package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveBrowserAuthRecord(ctx context.Context, record app.BrowserAuthRecord) (app.BrowserAuthRecord, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthSave, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationBrowserAuthSave, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	record.ID = strings.TrimSpace(record.ID)
	record = normalizeBrowserAuthRecord(record, s.browserAuthRecords[record.ID])
	s.browserAuthRecords[record.ID] = cloneBrowserAuthRecord(record)
	s.appendAuditLocked("browser_auth.record_saved", "", "", "gateway", record.SiteOrigin, browserAuthAuditFields(record, nil))
	s.appendEventLocked("browser_auth.record_saved", "", "", record)
	return cloneBrowserAuthRecord(record), nil
}

func (s *MemoryStore) GetBrowserAuthRecord(ctx context.Context, id string) (app.BrowserAuthRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthGet, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserAuthGet, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	record, ok := s.browserAuthRecords[strings.TrimSpace(id)]
	if !ok {
		return app.BrowserAuthRecord{}, false, nil
	}
	return cloneBrowserAuthRecord(record), true, nil
}

func (s *MemoryStore) FindBrowserAuthRecord(ctx context.Context, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthFind, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	ownerID, browserProfileID, siteOrigin, siteRealm, accountHint = normalizeBrowserAuthLookup(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserAuthFind, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	matches := []app.BrowserAuthRecord{}
	now := time.Now().UTC()
	for _, record := range s.browserAuthRecords {
		if record.OwnerID != ownerID || record.BrowserProfileID != browserProfileID || record.SiteOrigin != siteOrigin || record.SiteRealm != siteRealm || record.AccountHint != accountHint {
			continue
		}
		if record.Status != app.BrowserAuthStatusActive || record.RevokedAt != nil {
			continue
		}
		if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
			continue
		}
		matches = append(matches, record)
	}
	if len(matches) == 0 {
		return app.BrowserAuthRecord{}, false, nil
	}
	slices.SortFunc(matches, func(a, b app.BrowserAuthRecord) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return cloneBrowserAuthRecord(matches[0]), true, nil
}

func (s *MemoryStore) ListBrowserAuthRecords(ctx context.Context, ownerID, browserProfileID string) ([]app.BrowserAuthRecord, error) {
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserAuthList, ctx); err != nil {
		return nil, err
	}
	out := []app.BrowserAuthRecord{}
	for _, record := range s.browserAuthRecords {
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if browserProfileID != "" && record.BrowserProfileID != browserProfileID {
			continue
		}
		out = append(out, cloneBrowserAuthRecord(record))
	}
	slices.SortFunc(out, func(a, b app.BrowserAuthRecord) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) RevokeBrowserAuthRecord(ctx context.Context, id, reason string) (app.BrowserAuthRecord, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthRevoke, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationBrowserAuthRevoke, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	id = strings.TrimSpace(id)
	record, ok := s.browserAuthRecords[id]
	if !ok {
		return app.BrowserAuthRecord{}, storeError(ctx, OperationBrowserAuthRevoke, StoreErrorNotFound, errors.New("browser auth record not found"))
	}
	now := postgresTime(time.Now().UTC())
	record.Status = app.BrowserAuthStatusRevoked
	record.RevokedAt = &now
	record.UpdatedAt = now
	record.LastError = strings.TrimSpace(reason)
	s.browserAuthRecords[id] = cloneBrowserAuthRecord(record)
	s.appendAuditLocked("browser_auth.record_revoked", "", "", "owner", record.SiteOrigin, browserAuthAuditFields(record, map[string]any{"reason": record.LastError}))
	s.appendEventLocked("browser_auth.record_revoked", "", "", record)
	return cloneBrowserAuthRecord(record), nil
}

func (s *MemoryStore) SaveBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock) (app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockSave, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationBrowserLoginBlockSave, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	block.ID = strings.TrimSpace(block.ID)
	block = normalizeBrowserLoginBlock(block, s.browserLoginBlocks[block.ID])
	s.browserLoginBlocks[block.ID] = cloneBrowserLoginBlock(block)
	s.appendAuditLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEventLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return cloneBrowserLoginBlock(block), nil
}

func (s *MemoryStore) UpdateBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockUpdate, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationBrowserLoginBlockUpdate, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	current, ok := s.browserLoginBlocks[strings.TrimSpace(block.ID)]
	if !ok || current.Version != expectedVersion {
		return app.BrowserLoginBlock{}, storeError(ctx, OperationBrowserLoginBlockUpdate, StoreErrorConflict, ErrBrowserHandoffConflict)
	}
	block.Version = expectedVersion + 1
	block = normalizeBrowserLoginBlock(block, current)
	s.browserLoginBlocks[block.ID] = cloneBrowserLoginBlock(block)
	s.appendAuditLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEventLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return cloneBrowserLoginBlock(block), nil
}

func (s *MemoryStore) GetBrowserLoginBlock(ctx context.Context, id string) (app.BrowserLoginBlock, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockGet, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserLoginBlockGet, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	block, ok := s.browserLoginBlocks[strings.TrimSpace(id)]
	if !ok {
		return app.BrowserLoginBlock{}, false, nil
	}
	return cloneBrowserLoginBlock(block), true, nil
}

func (s *MemoryStore) FindActiveBrowserLoginBlock(ctx context.Context, sessionID string) (app.BrowserLoginBlock, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockFindActive, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockFindActive, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserLoginBlockFindActive, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	blocks := s.listBrowserLoginBlocksLocked(sessionID, "")
	for _, block := range blocks {
		if app.BrowserHandoffStatusActive(block.Status) {
			return block, true, nil
		}
	}
	return app.BrowserLoginBlock{}, false, nil
}

func (s *MemoryStore) ListBrowserLoginBlocks(ctx context.Context, sessionID, status string) ([]app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserLoginBlockList, ctx); err != nil {
		return nil, err
	}
	return s.listBrowserLoginBlocksLocked(sessionID, status), nil
}

func (s *MemoryStore) listBrowserLoginBlocksLocked(sessionID, status string) []app.BrowserLoginBlock {
	sessionID = strings.TrimSpace(sessionID)
	status = strings.TrimSpace(status)
	out := []app.BrowserLoginBlock{}
	// Read path: return stored values verbatim. Normalization that stamps
	// SchemaVersion/Version/UpdatedAt happens only on write (and once at
	// snapshot load), otherwise reads would destroy migration evidence,
	// degrade UpdatedAt ordering, and break CAS against the stored Version.
	for _, block := range s.browserLoginBlocks {
		if sessionID != "" && block.SessionID != sessionID {
			continue
		}
		if status != "" && block.Status != status {
			continue
		}
		out = append(out, cloneBrowserLoginBlock(block))
	}
	slices.SortFunc(out, func(a, b app.BrowserLoginBlock) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out
}

func normalizeBrowserAuthRecord(record app.BrowserAuthRecord, current app.BrowserAuthRecord) app.BrowserAuthRecord {
	now := postgresTime(time.Now().UTC())
	record = migrateLegacyBrowserAuthRecord(record)
	record.ID = strings.TrimSpace(record.ID)
	if record.ID == "" {
		record.ID = app.NewID("bauth")
	}
	if !current.CreatedAt.IsZero() {
		record.CreatedAt = current.CreatedAt
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.CreatedAt = postgresTime(record.CreatedAt)
	record.UpdatedAt = now
	return cloneBrowserAuthRecord(record)
}

func migrateLegacyBrowserAuthRecord(record app.BrowserAuthRecord) app.BrowserAuthRecord {
	record.ID = strings.TrimSpace(record.ID)
	record.OwnerID = normalizeBrowserAuthOwnerID(record.OwnerID)
	record.BrowserProfileID = normalizeBrowserProfileID(record.BrowserProfileID)
	record.SiteOrigin = normalizeSiteOrigin(record.SiteOrigin)
	record.SiteRealm = strings.TrimSpace(record.SiteRealm)
	record.AccountHint = strings.ToLower(strings.TrimSpace(record.AccountHint))
	record.AuthStrategy = strings.TrimSpace(record.AuthStrategy)
	if record.AuthStrategy == "" {
		record.AuthStrategy = "session_restore"
	}
	record.Status = strings.TrimSpace(record.Status)
	if record.Status == "" {
		record.Status = app.BrowserAuthStatusActive
	}
	if !record.LastVerifiedAt.IsZero() {
		record.LastVerifiedAt = postgresTime(record.LastVerifiedAt)
	}
	if !record.CreatedAt.IsZero() {
		record.CreatedAt = postgresTime(record.CreatedAt)
	}
	if !record.UpdatedAt.IsZero() {
		record.UpdatedAt = postgresTime(record.UpdatedAt)
	}
	record.ExpiresAt = normalizeBrowserTimePointer(record.ExpiresAt)
	record.RevokedAt = normalizeBrowserTimePointer(record.RevokedAt)
	return cloneBrowserAuthRecord(record)
}

// Legacy schema-v1 browser login block status strings, kept only so
// previously persisted snapshots can be migrated at load time.
const (
	legacyBrowserHandoffStatusWaiting  = "waiting"
	legacyBrowserHandoffStatusResuming = "resuming"
)

// migrateLegacyBrowserLoginBlock upgrades a schema-v1 block persisted by an
// older build to the v2 shape. It runs once at snapshot load — never on read
// paths — and preserves the stored time points while canonicalizing their UTC
// microsecond representation. The postgres schema performs the same status
// mapping in SQL; keep the two in sync.
func migrateLegacyBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock {
	block = cloneBrowserLoginBlock(block)
	switch strings.TrimSpace(block.Status) {
	case legacyBrowserHandoffStatusWaiting:
		block.Status = app.BrowserHandoffStatusWaitingOwner
	case legacyBrowserHandoffStatusResuming:
		block.Status = app.BrowserHandoffStatusValidatingVisible
	}
	if block.SchemaVersion <= 0 {
		block.SchemaVersion = app.BrowserHandoffSchemaVersion
	}
	if block.Version <= 0 {
		block.Version = 1
	}
	// Read paths no longer normalize, so the resume defaults formerly
	// injected on read must be materialized here for legacy rows.
	if strings.TrimSpace(block.ResumeTool) == "" {
		block.ResumeTool = "browser.read"
	}
	if block.ResumeArgs == nil {
		block.ResumeArgs = map[string]any{}
	}
	if !block.CreatedAt.IsZero() {
		block.CreatedAt = postgresTime(block.CreatedAt)
	}
	if !block.UpdatedAt.IsZero() {
		block.UpdatedAt = postgresTime(block.UpdatedAt)
	}
	block.TransitionLeaseUntil = normalizeBrowserTimePointer(block.TransitionLeaseUntil)
	block.ResolvedAt = normalizeBrowserTimePointer(block.ResolvedAt)
	return cloneBrowserLoginBlock(block)
}

// normalizeBrowserLoginBlock is a WRITE-path helper (Save/Update in every
// backend): it stamps SchemaVersion, bumps Version past current, and sets
// UpdatedAt to now. It must never run on read paths — see
// migrateLegacyBrowserLoginBlock for the one-time snapshot-load fix-up.
func normalizeBrowserLoginBlock(block app.BrowserLoginBlock, current app.BrowserLoginBlock) app.BrowserLoginBlock {
	now := postgresTime(time.Now().UTC())
	block = cloneBrowserLoginBlock(block)
	if block.SchemaVersion <= 0 {
		block.SchemaVersion = app.BrowserHandoffSchemaVersion
	}
	if block.Version <= current.Version {
		block.Version = current.Version + 1
	}
	if block.Version <= 0 {
		block.Version = 1
	}
	block.SessionID = strings.TrimSpace(block.SessionID)
	block.RunID = strings.TrimSpace(block.RunID)
	block.TransitionOwnerID = strings.TrimSpace(block.TransitionOwnerID)
	block.Status = strings.TrimSpace(block.Status)
	if block.Status == "" {
		block.Status = app.BrowserLoginBlockStatusWaiting
	}
	block.OriginalGoal = strings.TrimSpace(block.OriginalGoal)
	block.ResumeTool = strings.TrimSpace(block.ResumeTool)
	if block.ResumeTool == "" {
		block.ResumeTool = "browser.read"
	}
	if block.ResumeArgs == nil {
		block.ResumeArgs = map[string]any{}
	}
	block.LastToolCallID = strings.TrimSpace(block.LastToolCallID)
	block.LoginHandoffURL = strings.TrimSpace(block.LoginHandoffURL)
	block.LoginHandoffPageID = strings.TrimSpace(block.LoginHandoffPageID)
	block.LastVisiblePageID = strings.TrimSpace(block.LastVisiblePageID)
	block.OwnerID = normalizeBrowserAuthOwnerID(block.OwnerID)
	block.BrowserProfileID = normalizeBrowserProfileID(block.BrowserProfileID)
	block.SiteOrigin = normalizeSiteOrigin(block.SiteOrigin)
	block.SiteRealm = strings.TrimSpace(block.SiteRealm)
	block.AccountHint = strings.ToLower(strings.TrimSpace(block.AccountHint))
	block.BrowserAuthStatus = strings.TrimSpace(block.BrowserAuthStatus)
	block.LastUserReply = strings.TrimSpace(block.LastUserReply)
	block.LastError = strings.TrimSpace(block.LastError)
	if block.Status == app.BrowserHandoffStatusWaitingOwner || !app.BrowserHandoffStatusActive(block.Status) {
		block.TransitionOwnerID = ""
		block.TransitionLeaseUntil = nil
	}
	block.ID = strings.TrimSpace(block.ID)
	if block.ID == "" {
		block.ID = app.NewID("blogin")
	}
	if !current.CreatedAt.IsZero() {
		block.CreatedAt = current.CreatedAt
	}
	if block.CreatedAt.IsZero() {
		block.CreatedAt = now
	}
	block.CreatedAt = postgresTime(block.CreatedAt)
	block.TransitionLeaseUntil = normalizeBrowserTimePointer(block.TransitionLeaseUntil)
	block.ResolvedAt = normalizeBrowserTimePointer(block.ResolvedAt)
	if !app.BrowserHandoffStatusActive(block.Status) && block.ResolvedAt == nil {
		block.ResolvedAt = normalizeBrowserTimePointer(current.ResolvedAt)
	}
	block.UpdatedAt = now
	return cloneBrowserLoginBlock(block)
}

func normalizeBrowserTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := postgresTime(*value)
	return &normalized
}

func normalizeBrowserAuthLookup(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (string, string, string, string, string) {
	return normalizeBrowserAuthOwnerID(ownerID),
		normalizeBrowserProfileID(browserProfileID),
		normalizeSiteOrigin(siteOrigin),
		strings.TrimSpace(siteRealm),
		strings.ToLower(strings.TrimSpace(accountHint))
}

func normalizeBrowserAuthOwnerID(ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return app.DefaultOwnerID
	}
	return ownerID
}

func normalizeBrowserProfileID(profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return "default"
	}
	return profileID
}

func normalizeSiteOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	origin = strings.TrimRight(origin, "/")
	return strings.ToLower(origin)
}

func browserAuthAuditFields(record app.BrowserAuthRecord, extra map[string]any) map[string]any {
	fields := map[string]any{
		"record_id":          record.ID,
		"owner_id":           record.OwnerID,
		"browser_profile_id": record.BrowserProfileID,
		"site_origin":        record.SiteOrigin,
		"site_realm":         record.SiteRealm,
		"account_hint":       record.AccountHint,
		"auth_strategy":      record.AuthStrategy,
		"status":             record.Status,
		"credential_ref_set": strings.TrimSpace(record.CredentialRef) != "",
		"cookie_jar_ref_set": strings.TrimSpace(record.CookieJarRef) != "",
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

func browserLoginBlockAuditFields(block app.BrowserLoginBlock, extra map[string]any) map[string]any {
	fields := map[string]any{
		"block_id":              block.ID,
		"run_id":                block.RunID,
		"status":                block.Status,
		"resume_tool":           block.ResumeTool,
		"last_tool_call_id":     block.LastToolCallID,
		"login_handoff_page_id": block.LoginHandoffPageID,
		"last_visible_page_id":  block.LastVisiblePageID,
		"owner_id":              block.OwnerID,
		"browser_profile_id":    block.BrowserProfileID,
		"site_origin":           block.SiteOrigin,
		"site_realm":            block.SiteRealm,
		"account_hint":          block.AccountHint,
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

func cloneStringMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
