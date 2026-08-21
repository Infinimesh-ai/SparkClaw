package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var (
	_ SessionRepository = (*MemoryStore)(nil)
	_ SessionRepository = (*FileStore)(nil)
	_ SessionRepository = (*PostgresStore)(nil)
)

func prepareSession(title, ownerID, workspaceRoot, source string, hidden bool, now time.Time) (app.Session, error) {
	if strings.TrimSpace(title) == "" {
		title = "New SparkClaw Session"
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "webchat"
	}
	now = normalizeSessionTime(now)
	session := app.Session{
		ID: app.NewID("s"), OwnerID: ownerID, WorkspaceRoot: strings.TrimSpace(workspaceRoot),
		Title: title, Source: source, Hidden: hidden, CreatedAt: now, UpdatedAt: now,
	}
	if err := validatePersistedSession(session.ID, session); err != nil {
		return app.Session{}, err
	}
	return session, nil
}

func validatePersistedSession(key string, session app.Session) error {
	if strings.TrimSpace(key) == "" || key != session.ID {
		return fmt.Errorf("session key %q does not match embedded ID %q", key, session.ID)
	}
	if len(session.ID) > 256 {
		return errors.New("session ID exceeds 256 bytes")
	}
	if strings.TrimSpace(session.OwnerID) == "" || strings.TrimSpace(session.Title) == "" || strings.TrimSpace(session.Source) == "" {
		return errors.New("session owner, title, and source are required")
	}
	if session.OwnerID != strings.TrimSpace(session.OwnerID) ||
		session.Source != strings.TrimSpace(session.Source) ||
		session.WorkspaceRoot != strings.TrimSpace(session.WorkspaceRoot) {
		return errors.New("session owner, source, and workspace must be normalized")
	}
	if !validSessionTime(session.CreatedAt) || !validSessionTime(session.UpdatedAt) {
		return errors.New("session timestamps must be nonzero UTC microseconds")
	}
	if session.UpdatedAt.Before(session.CreatedAt) {
		return errors.New("session update time precedes creation time")
	}
	return nil
}

func validSessionTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Nanosecond()%1000 == 0
}

func normalizeSessionTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func nextSessionTime(now time.Time, floors ...time.Time) time.Time {
	next := normalizeSessionTime(now)
	for _, floor := range floors {
		floor = normalizeSessionTime(floor)
		if !next.After(floor) {
			next = floor.Add(time.Microsecond)
		}
	}
	return next
}

func sessionsEqual(left, right app.Session) bool {
	return left.ID == right.ID && left.OwnerID == right.OwnerID && left.WorkspaceRoot == right.WorkspaceRoot &&
		left.Title == right.Title && left.Source == right.Source && left.Hidden == right.Hidden &&
		left.CreatedAt.Equal(right.CreatedAt) && left.UpdatedAt.Equal(right.UpdatedAt)
}

func (s *MemoryStore) validateSessionState() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, session := range s.sessions {
		if err := validatePersistedSession(id, session); err != nil {
			return err
		}
	}
	return nil
}

func ReconcileSessionWrite(ctx context.Context, repository SessionRepository, candidate app.Session, writeErr error) (app.Session, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.Session{}, writeErr
	}
	stored, found, err := repository.GetSession(ctx, candidate.ID)
	if err != nil {
		return app.Session{}, errors.Join(writeErr, err)
	}
	if found && sessionsEqual(stored, candidate) {
		return stored, nil
	}
	return app.Session{}, writeErr
}

func ReconcileSessionDelete(ctx context.Context, repository SessionRepository, candidate app.Session, deleteErr error) (app.Session, error) {
	if deleteErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(deleteErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.Session{}, deleteErr
	}
	_, found, err := repository.GetSession(ctx, candidate.ID)
	if err != nil {
		return app.Session{}, errors.Join(deleteErr, err)
	}
	if !found {
		return candidate, nil
	}
	return app.Session{}, deleteErr
}
