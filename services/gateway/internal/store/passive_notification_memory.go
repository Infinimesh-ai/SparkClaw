package store

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) CreatePassiveNotification(ctx context.Context, notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationCreate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationCreate, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	var err error
	notification, err = preparePassiveNotification(notification, time.Now().UTC())
	if err != nil {
		return app.PassiveNotification{}, false, storeError(ctx, OperationPassiveNotificationCreate, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPassiveNotificationCreate, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	if existingID, ok := s.passiveNotificationIDsByKey[passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey)]; ok {
		existing := s.passiveNotifications[existingID]
		if !passiveNotificationsEqualForReplay(existing, notification) {
			return app.PassiveNotification{}, false, storeError(ctx, OperationPassiveNotificationCreate, StoreErrorConflict, ErrPassiveNotificationConflict)
		}
		return clonePassiveNotification(existing), false, nil
	}
	if _, exists := s.passiveNotifications[notification.ID]; exists {
		return app.PassiveNotification{}, false, storeError(ctx, OperationPassiveNotificationCreate, StoreErrorConflict, ErrPassiveNotificationConflict)
	}
	s.passiveNotifications[notification.ID] = clonePassiveNotification(notification)
	s.passiveNotificationIDsByKey[passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey)] = notification.ID
	s.passiveNotificationRevs[notification.OwnerID]++
	s.appendAuditLocked("notification.received", "", "", notification.OwnerID, notification.Source, map[string]any{
		"notification_id": notification.ID,
		"endpoint_id":     notification.EndpointID,
		"kind":            notification.Kind,
	})
	return clonePassiveNotification(notification), true, nil
}

func (s *MemoryStore) GetPassiveNotification(ctx context.Context, ownerID, id string) (app.PassiveNotification, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationGet, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPassiveNotificationGet, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	notification, ok := s.passiveNotifications[id]
	if !ok || notification.OwnerID != ownerID {
		return app.PassiveNotification{}, false, nil
	}
	return clonePassiveNotification(notification), true, nil
}

func (s *MemoryStore) ListPassiveNotifications(ctx context.Context, ownerID, after string, limit int) ([]app.PassiveNotification, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPassiveNotificationList, ctx); err != nil {
		return nil, err
	}
	limit = normalizePassiveNotificationLimit(limit)
	var cursor app.PassiveNotification
	if after != "" {
		var ok bool
		cursor, ok = s.passiveNotifications[after]
		if !ok || cursor.OwnerID != ownerID {
			return []app.PassiveNotification{}, nil
		}
	}
	out := make([]app.PassiveNotification, 0)
	for _, notification := range s.passiveNotifications {
		if notification.OwnerID != ownerID {
			continue
		}
		if after != "" && (notification.CreatedAt.Before(cursor.CreatedAt) || (notification.CreatedAt.Equal(cursor.CreatedAt) && notification.ID <= cursor.ID)) {
			continue
		}
		out = append(out, clonePassiveNotification(notification))
	}
	slices.SortFunc(out, func(a, b app.PassiveNotification) int {
		order := a.CreatedAt.Compare(b.CreatedAt)
		if order == 0 {
			order = strings.Compare(a.ID, b.ID)
		}
		if after == "" {
			return -order
		}
		return order
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) CountUnreadPassiveNotifications(ctx context.Context, ownerID string) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationCount, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationCount, ctx); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPassiveNotificationCount, ctx); err != nil {
		return 0, err
	}
	count := 0
	for _, notification := range s.passiveNotifications {
		if notification.OwnerID == ownerID && notification.ReadAt == nil {
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) MarkPassiveNotificationRead(ctx context.Context, ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationMarkRead, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationMarkRead, ctx); err != nil {
		return app.PassiveNotification{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPassiveNotificationMarkRead, ctx); err != nil {
		return app.PassiveNotification{}, err
	}
	notification, ok := s.passiveNotifications[id]
	if !ok || notification.OwnerID != ownerID {
		return app.PassiveNotification{}, storeError(ctx, OperationPassiveNotificationMarkRead, StoreErrorNotFound, ErrPassiveNotificationNotFound)
	}
	if notification.ReadAt != nil {
		return clonePassiveNotification(notification), nil
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = postgresTime(readAt)
	}
	readAt = postgresTime(readAt)
	notification.ReadAt = &readAt
	notification.UpdatedAt = readAt
	s.passiveNotifications[id] = notification
	s.passiveNotificationRevs[notification.OwnerID]++
	return clonePassiveNotification(notification), nil
}

func (s *MemoryStore) MarkAllPassiveNotificationsRead(ctx context.Context, ownerID string, readAt time.Time) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationMarkAll, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationMarkAll, ctx); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPassiveNotificationMarkAll, ctx); err != nil {
		return 0, err
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = postgresTime(readAt)
	}
	readAt = postgresTime(readAt)
	count := 0
	for id, notification := range s.passiveNotifications {
		if notification.OwnerID != ownerID || notification.ReadAt != nil {
			continue
		}
		notification.ReadAt = &readAt
		notification.UpdatedAt = readAt
		s.passiveNotifications[id] = notification
		count++
	}
	if count > 0 {
		s.passiveNotificationRevs[ownerID]++
	}
	return count, nil
}

func (s *MemoryStore) PrunePassiveNotifications(ctx context.Context, cutoff time.Time, maxPerOwner int) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationPrune, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationPrune, ctx); err != nil {
		return 0, err
	}
	cutoff = postgresTime(cutoff)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPassiveNotificationPrune, ctx); err != nil {
		return 0, err
	}
	removedByOwner := map[string]int{}
	if !cutoff.IsZero() {
		for id, notification := range s.passiveNotifications {
			if notification.CreatedAt.Before(cutoff) {
				s.removePassiveNotificationLocked(id, notification)
				removedByOwner[notification.OwnerID]++
			}
		}
	}
	if maxPerOwner > 0 {
		byOwner := map[string][]app.PassiveNotification{}
		for _, notification := range s.passiveNotifications {
			byOwner[notification.OwnerID] = append(byOwner[notification.OwnerID], notification)
		}
		for ownerID, notifications := range byOwner {
			excess := len(notifications) - maxPerOwner
			if excess <= 0 {
				continue
			}
			slices.SortFunc(notifications, passiveNotificationEvictionOrder)
			for _, notification := range notifications[:excess] {
				s.removePassiveNotificationLocked(notification.ID, notification)
				removedByOwner[ownerID]++
			}
		}
	}
	removed := 0
	for ownerID, count := range removedByOwner {
		removed += count
		s.appendAuditLocked("notification.pruned", "", "", "notification-retention", ownerID, map[string]any{
			"removed":       count,
			"max_per_owner": maxPerOwner,
			"cutoff":        cutoff.UTC().Format(time.RFC3339),
		})
	}
	return removed, nil
}

func (s *MemoryStore) removePassiveNotificationLocked(id string, notification app.PassiveNotification) {
	delete(s.passiveNotifications, id)
	delete(s.passiveNotificationIDsByKey, passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey))
	s.passiveNotificationRevs[notification.OwnerID]++
}

func (s *MemoryStore) PassiveNotificationRevision(ctx context.Context, ownerID string) (uint64, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationRevision, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationRevision, ctx); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPassiveNotificationRevision, ctx); err != nil {
		return 0, err
	}
	return s.passiveNotificationRevs[ownerID], nil
}

// passiveNotificationEvictionOrder ranks cap evictions: read notifications go
// first (oldest first), then unread oldest-first, so an over-cap inbox keeps
// the newest unread records.
func passiveNotificationEvictionOrder(a, b app.PassiveNotification) int {
	aRead, bRead := a.ReadAt != nil, b.ReadAt != nil
	if aRead != bRead {
		if aRead {
			return -1
		}
		return 1
	}
	if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
		return order
	}
	return strings.Compare(a.ID, b.ID)
}

func passiveNotificationKey(endpointID, idempotencyKey string) string {
	return endpointID + "\x00" + idempotencyKey
}
