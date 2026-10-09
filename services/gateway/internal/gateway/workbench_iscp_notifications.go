package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type iscpNotificationWatermark struct {
	Owner        string    `json:"owner"`
	Client       string    `json:"client"`
	Installation string    `json:"installation"`
	ExpiresAt    time.Time `json:"expires_at"`
	IDs          []string  `json:"ids"`
}

func (a *iscpDomainAdapter) notificationWatermark(principal requestPrincipal, installation string, records []app.PassiveNotification) (string, error) {
	token := iscpNotificationWatermark{Owner: principal.OwnerID, Client: principal.ClientID, Installation: installation, ExpiresAt: domainNow().Add(10 * time.Minute), IDs: []string{}}
	for _, record := range records {
		token.IDs = append(token.IDs, record.ID)
	}
	raw, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.receipts.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := a.receipts.aead.Seal(nonce, nonce, raw, []byte("notification-watermark-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}
func (a *iscpDomainAdapter) notifications(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	repo := a.server.store
	if request.Operation == iscpworkbench.OperationNotificationsList {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		limit := 100
		if value := request.Params["limit"]; value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 100 {
				return domainError(400, "invalid_input")
			}
			limit = parsed
		}
		after := request.Params["after"]
		if after != "" {
			if _, found, err := repo.GetPassiveNotification(ctx, principal.OwnerID, after); err != nil {
				return domainError(503, "notifications_unavailable")
			} else if !found {
				return domainError(409, "cursor_expired")
			}
		}
		// Use the domain repository's newest-first bounded collection; its existing
		// `after` cursor is a change feed, not backwards pagination.
		all, err := repo.ListPassiveNotifications(ctx, principal.OwnerID, "", 500)
		if err != nil {
			return domainError(503, "notifications_unavailable")
		}
		start := 0
		if after != "" {
			found := false
			for i, record := range all {
				if record.ID == after {
					start = i + 1
					found = true
					break
				}
			}
			if !found {
				return domainError(409, "cursor_expired")
			}
		}
		end := start + limit
		if end > len(all) {
			end = len(all)
		}
		records := all[start:end]
		token, err := a.notificationWatermark(principal, request.InstallationID, all)
		if err != nil {
			return domainError(503, "notifications_unavailable")
		}
		unread, err := repo.CountUnreadPassiveNotifications(ctx, principal.OwnerID)
		if err != nil {
			return domainError(503, "notifications_unavailable")
		}
		revision, err := repo.PassiveNotificationRevision(ctx, principal.OwnerID)
		if err != nil {
			return domainError(503, "notifications_unavailable")
		}
		views := make([]passiveNotificationView, 0, len(records))
		for _, record := range records {
			views = append(views, publicPassiveNotification(record))
		}
		next := ""
		if len(records) == limit {
			next = records[len(records)-1].ID
		}
		return domainJSON(200, map[string]any{"revision": strconv.FormatUint(revision, 10), "value": map[string]any{"notifications": views, "unread_count": unread, "watermark": token, "next_cursor": next, "watermark_limit": 500}})
	}
	var ids []string
	switch request.Operation {
	case iscpworkbench.OperationNotificationsRead:
		if !domainEmpty(request.Body) || request.Params["id"] == "" {
			return domainError(400, "invalid_input")
		}
		ids = []string{request.Params["id"]}
	case iscpworkbench.OperationNotificationsReadAll:
		var input struct {
			Watermark string `json:"watermark"`
		}
		if domainDecode(request.Body, &input) != nil || len(input.Watermark) > 48<<10 {
			return domainError(400, "invalid_watermark")
		}
		sealed, err := base64.RawURLEncoding.DecodeString(input.Watermark)
		n := a.receipts.aead.NonceSize()
		if err != nil || len(sealed) <= n {
			return domainError(400, "invalid_watermark")
		}
		raw, err := a.receipts.aead.Open(nil, sealed[:n], sealed[n:], []byte("notification-watermark-v1"))
		var token iscpNotificationWatermark
		if err != nil || json.Unmarshal(raw, &token) != nil || token.Owner != principal.OwnerID || token.Client != principal.ClientID || token.Installation != request.InstallationID || len(token.IDs) > 500 {
			return domainError(403, "invalid_watermark")
		}
		if !token.ExpiresAt.After(domainNow()) {
			return domainError(409, "watermark_expired")
		}
		ids = token.IDs
	}
	updated := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return domainError(409, "operation_outcome_unknown")
		}
		record, found, err := repo.GetPassiveNotification(ctx, principal.OwnerID, id)
		if err != nil {
			return domainError(503, "notifications_outcome_unknown")
		}
		if !found {
			if request.Operation == iscpworkbench.OperationNotificationsRead {
				return domainError(404, "notification_not_found")
			}
			continue
		}
		if record.ReadAt != nil {
			continue
		}
		_, err = repo.MarkPassiveNotificationRead(ctx, principal.OwnerID, id, domainNow())
		if errors.Is(err, store.ErrPassiveNotificationNotFound) {
			continue
		}
		if err != nil {
			return domainError(503, "notifications_outcome_unknown")
		}
		updated++
	}
	unread, err := repo.CountUnreadPassiveNotifications(ctx, principal.OwnerID)
	if err != nil {
		return domainError(503, "notifications_unavailable")
	}
	revision, err := repo.PassiveNotificationRevision(ctx, principal.OwnerID)
	if err != nil {
		return domainError(503, "notifications_unavailable")
	}
	return domainJSON(200, map[string]any{"revision": strconv.FormatUint(revision, 10), "value": map[string]any{"updated": updated, "unread_count": unread}})
}
