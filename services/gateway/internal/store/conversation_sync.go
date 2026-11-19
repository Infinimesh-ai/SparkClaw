package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var (
	_ ConversationSyncRepository = (*MemoryStore)(nil)
	_ ConversationSyncRepository = (*FileStore)(nil)
	_ ConversationSyncRepository = (*PostgresStore)(nil)
)

// PreviewRunes bounds the list preview.
const PreviewRunes = 200

func sessionPreview(content string) string {
	content = strings.TrimSpace(content)
	if utf8.RuneCountInString(content) <= PreviewRunes {
		return content
	}
	r := []rune(content)
	return string(r[:PreviewRunes])
}

func (s *MemoryStore) bumpSessionRevisionLocked(session *app.Session) {
	s.sessionRevision++
	session.Revision = s.sessionRevision
}

// backfillConversationSyncLocked assigns sequence numbers and revisions to
// state written before conversation sync existed.
func (s *MemoryStore) backfillConversationSyncLocked() {
	if s.sessionTombstones == nil {
		s.sessionTombstones = map[string]app.SessionChange{}
	}
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		if c := s.sessions[a].UpdatedAt.Compare(s.sessions[b].UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	for _, id := range ids {
		session := s.sessions[id]
		messages := s.messages[id]
		changed := false
		var max int64
		for _, m := range messages {
			if m.Seq > max {
				max = m.Seq
			}
		}
		for i := range messages {
			if messages[i].Seq == 0 {
				max++
				messages[i].Seq = max
				changed = true
			}
		}
		if session.LastSeq < max {
			session.LastSeq = max
			changed = true
		}
		if session.Preview == "" && len(messages) > 0 {
			latest := messages[0]
			for _, m := range messages {
				if m.Seq > latest.Seq {
					latest = m
				}
			}
			session.Preview = sessionPreview(latest.Content)
			changed = true
		}
		if session.Revision == 0 || changed {
			s.bumpSessionRevisionLocked(&session)
		}
		s.sessions[id] = session
		s.messages[id] = messages
	}
}

func (s *MemoryStore) SessionChanges(ctx context.Context, ownerID string, cursor int64, limit int) ([]app.SessionChange, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.SessionChange, 0)
	for _, session := range s.sessions {
		if session.Hidden || session.OwnerID != ownerID || session.Revision <= cursor {
			continue
		}
		out = append(out, app.SessionChange{Session: session})
	}
	for _, tomb := range s.sessionTombstones {
		if tomb.Session.OwnerID != ownerID || tomb.Session.Revision <= cursor {
			continue
		}
		out = append(out, tomb)
	}
	slices.SortFunc(out, func(a, b app.SessionChange) int {
		switch {
		case a.Session.Revision < b.Session.Revision:
			return -1
		case a.Session.Revision > b.Session.Revision:
			return 1
		}
		return 0
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) SessionHeader(ctx context.Context, id string) (app.Session, bool, error) {
	return s.GetSession(ctx, id)
}

func (s *MemoryStore) ListMessagePage(ctx context.Context, sessionID string, beforeSeq, afterSeq int64, limit int) ([]app.Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := cloneMessages(s.messages[sessionID])
	slices.SortFunc(all, func(a, b app.Message) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		}
		return 0
	})
	if afterSeq > 0 {
		out := make([]app.Message, 0, limit)
		for _, m := range all {
			if m.Seq > afterSeq {
				out = append(out, m)
				if len(out) == limit {
					break
				}
			}
		}
		more := len(all) > 0 && len(out) > 0 && all[0].Seq < out[0].Seq
		return out, more, nil
	}
	end := len(all)
	if beforeSeq > 0 {
		end = 0
		for i, m := range all {
			if m.Seq < beforeSeq {
				end = i + 1
			}
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	return all[start:end], start > 0, nil
}

func (s *MemoryStore) SetSessionArchived(ctx context.Context, id string, archived bool) (app.Session, error) {
	if err := ctx.Err(); err != nil {
		return app.Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return app.Session{}, storeError(ctx, OperationConversationSyncArchive, StoreErrorNotFound, errors.New("session not found"))
	}
	if session.Archived == archived {
		return session, nil
	}
	session.Archived = archived
	s.bumpSessionRevisionLocked(&session)
	s.sessions[id] = session
	s.appendEventLocked("session.updated", id, "", session)
	return session, nil
}

func (s *MemoryStore) FindMessageByClientID(ctx context.Context, sessionID, clientMessageID string) (app.Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return app.Message{}, false, err
	}
	if strings.TrimSpace(clientMessageID) == "" {
		return app.Message{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.messages[sessionID] {
		if m.ClientMessageID == clientMessageID {
			return cloneMessage(m), true, nil
		}
	}
	return app.Message{}, false, nil
}

// FileStore delegates reads to the in-memory state and persists writes.

func (s *FileStore) SessionChanges(ctx context.Context, ownerID string, cursor int64, limit int) ([]app.SessionChange, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationSyncChanges, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.SessionChanges(ctx, ownerID, cursor, limit)
}

func (s *FileStore) SessionHeader(ctx context.Context, id string) (app.Session, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationSyncHeader, 1)
	if err != nil {
		return app.Session{}, false, err
	}
	defer release()
	return s.inner.SessionHeader(ctx, id)
}

func (s *FileStore) ListMessagePage(ctx context.Context, sessionID string, beforeSeq, afterSeq int64, limit int) ([]app.Message, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationSyncPage, 1)
	if err != nil {
		return nil, false, err
	}
	defer release()
	return s.inner.ListMessagePage(ctx, sessionID, beforeSeq, afterSeq, limit)
}

func (s *FileStore) SetSessionArchived(ctx context.Context, id string, archived bool) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationSyncArchive, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationConversationSyncArchive, func(ctx context.Context) (app.Session, error) {
		return s.inner.SetSessionArchived(ctx, id, archived)
	})
}

func (s *FileStore) FindMessageByClientID(ctx context.Context, sessionID, clientMessageID string) (app.Message, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationSyncFindClient, 1)
	if err != nil {
		return app.Message{}, false, err
	}
	defer release()
	return s.inner.FindMessageByClientID(ctx, sessionID, clientMessageID)
}
