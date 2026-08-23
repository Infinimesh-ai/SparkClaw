package store

import (
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	defaultPassiveNotificationLimit = 100
	maxPassiveNotificationLimit     = 500
)

func preparePassiveNotification(notification app.PassiveNotification, now time.Time) (app.PassiveNotification, error) {
	notification.OwnerID = strings.TrimSpace(notification.OwnerID)
	notification.EndpointID = strings.TrimSpace(notification.EndpointID)
	notification.IdempotencyKey = strings.TrimSpace(notification.IdempotencyKey)
	if notification.OwnerID == "" || notification.EndpointID == "" || notification.IdempotencyKey == "" || strings.TrimSpace(notification.Fingerprint) == "" {
		return app.PassiveNotification{}, errors.New("notification owner, endpoint, idempotency key, and fingerprint are required")
	}
	if notification.ID == "" {
		notification.ID = app.NewID("notification")
	}
	if notification.CreatedAt.IsZero() {
		notification.CreatedAt = now
	}
	notification.UpdatedAt = now
	return normalizePassiveNotification(notification), nil
}

func normalizePassiveNotification(notification app.PassiveNotification) app.PassiveNotification {
	notification.OccurredAt = postgresTime(notification.OccurredAt)
	notification.ReadAt = normalizeScheduleTimePointer(notification.ReadAt)
	notification.CreatedAt = postgresTime(notification.CreatedAt)
	notification.UpdatedAt = postgresTime(notification.UpdatedAt)
	return notification
}

func clonePassiveNotification(notification app.PassiveNotification) app.PassiveNotification {
	notification.ReadAt = cloneTimePointer(notification.ReadAt)
	return notification
}

func clonePassiveNotificationMap(values map[string]app.PassiveNotification) map[string]app.PassiveNotification {
	out := make(map[string]app.PassiveNotification, len(values))
	for id, notification := range values {
		out[id] = clonePassiveNotification(notification)
	}
	return out
}

func normalizePassiveNotificationLimit(limit int) int {
	if limit <= 0 || limit > maxPassiveNotificationLimit {
		return defaultPassiveNotificationLimit
	}
	return limit
}

func passiveNotificationsEqualForReplay(existing, candidate app.PassiveNotification) bool {
	return existing.OwnerID == candidate.OwnerID && existing.EndpointID == candidate.EndpointID &&
		existing.IdempotencyKey == candidate.IdempotencyKey && existing.Fingerprint == candidate.Fingerprint
}
