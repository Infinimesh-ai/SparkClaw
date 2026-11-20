package gateway

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// The notifications domain over the link layer: the desktop's passive
// notification inbox. Marking all as read is bound to a sealed watermark
// that names exactly the notifications the device listed, so one that
// arrives later is never marked unseen. Results keep the domain's
// {revision, value} shape.

const notificationWatermarkLimit = 500

type notificationWatermark struct {
	Owner     string    `json:"owner"`
	Device    string    `json:"device"`
	ExpiresAt time.Time `json:"expires_at"`
	IDs       []string  `json:"ids"`
}

var (
	notificationSealOnce sync.Once
	notificationSeal     cipher.AEAD
)

// notificationAEAD seals watermarks for this process's lifetime; a
// watermark lives ten minutes, and one from before a restart is refused.
func notificationAEAD() cipher.AEAD {
	notificationSealOnce.Do(func() {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic(err)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			panic(err)
		}
		if notificationSeal, err = cipher.NewGCM(block); err != nil {
			panic(err)
		}
	})
	return notificationSeal
}

func sealNotificationWatermark(token notificationWatermark) (string, error) {
	raw, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	aead := notificationAEAD()
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, raw, []byte("notification-watermark-v2"))), nil
}

func openNotificationWatermark(value string) (notificationWatermark, error) {
	var token notificationWatermark
	if len(value) > 48<<10 {
		return token, errNotificationWatermarkInvalid
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	aead := notificationAEAD()
	n := aead.NonceSize()
	if err != nil || len(sealed) <= n {
		return token, errNotificationWatermarkInvalid
	}
	raw, err := aead.Open(nil, sealed[:n], sealed[n:], []byte("notification-watermark-v2"))
	if err != nil || json.Unmarshal(raw, &token) != nil || len(token.IDs) > notificationWatermarkLimit {
		return token, errNotificationWatermarkInvalid
	}
	return token, nil
}

var (
	errNotificationInvalid          = errors.New("invalid_input")
	errNotificationCursorExpired    = errors.New("cursor_expired")
	errNotificationWatermarkInvalid = errors.New("invalid_watermark")
	errNotificationWatermarkOwner   = errors.New("watermark_not_owned")
	errNotificationWatermarkExpired = errors.New("watermark_expired")
	errNotificationNotFound         = errors.New("notification_not_found")
	errNotificationsUnavailable     = errors.New("notifications_unavailable")
	errNotificationsOutcomeUnknown  = errors.New("notifications_outcome_unknown")
)

type notificationListArgs struct {
	Limit int    `json:"limit,omitempty"`
	After string `json:"after,omitempty"`
}

// listDeviceNotifications pages the newest-first inbox and seals a
// watermark over everything the device can mark.
func (s *Server) listDeviceNotifications(ctx context.Context, owner, device string, args notificationListArgs) (any, error) {
	limit := args.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 100 {
		return nil, errNotificationInvalid
	}
	repo := s.store
	if args.After != "" {
		if _, found, err := repo.GetPassiveNotification(ctx, owner, args.After); err != nil {
			return nil, errNotificationsUnavailable
		} else if !found {
			return nil, errNotificationCursorExpired
		}
	}
	all, err := repo.ListPassiveNotifications(ctx, owner, "", notificationWatermarkLimit)
	if err != nil {
		return nil, errNotificationsUnavailable
	}
	start := 0
	if args.After != "" {
		found := false
		for i, record := range all {
			if record.ID == args.After {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return nil, errNotificationCursorExpired
		}
	}
	end := min(start+limit, len(all))
	records := all[start:end]
	token := notificationWatermark{Owner: owner, Device: device, ExpiresAt: time.Now().UTC().Add(10 * time.Minute), IDs: []string{}}
	for _, record := range all {
		token.IDs = append(token.IDs, record.ID)
	}
	watermark, err := sealNotificationWatermark(token)
	if err != nil {
		return nil, errNotificationsUnavailable
	}
	unread, err := repo.CountUnreadPassiveNotifications(ctx, owner)
	if err != nil {
		return nil, errNotificationsUnavailable
	}
	revision, err := repo.PassiveNotificationRevision(ctx, owner)
	if err != nil {
		return nil, errNotificationsUnavailable
	}
	views := make([]passiveNotificationView, 0, len(records))
	for _, record := range records {
		views = append(views, publicPassiveNotification(record))
	}
	next := ""
	if len(records) == limit {
		next = records[len(records)-1].ID
	}
	return map[string]any{"revision": strconv.FormatUint(revision, 10), "value": map[string]any{"notifications": views, "unread_count": unread, "watermark": watermark, "next_cursor": next, "watermark_limit": notificationWatermarkLimit}}, nil
}

// markDeviceNotifications marks one notification (id) or every
// notification a watermark names as read.
func (s *Server) markDeviceNotifications(ctx context.Context, owner, device, id, watermark string) (any, error) {
	repo := s.store
	var ids []string
	single := id != ""
	if single {
		ids = []string{id}
	} else {
		token, err := openNotificationWatermark(watermark)
		if err != nil {
			return nil, err
		}
		if token.Owner != owner || token.Device != device {
			return nil, errNotificationWatermarkOwner
		}
		if !token.ExpiresAt.After(time.Now()) {
			return nil, errNotificationWatermarkExpired
		}
		ids = token.IDs
	}
	updated := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return nil, errNotificationsOutcomeUnknown
		}
		record, found, err := repo.GetPassiveNotification(ctx, owner, id)
		if err != nil {
			return nil, errNotificationsOutcomeUnknown
		}
		if !found {
			if single {
				return nil, errNotificationNotFound
			}
			continue
		}
		if record.ReadAt != nil {
			continue
		}
		_, err = repo.MarkPassiveNotificationRead(ctx, owner, id, time.Now().UTC())
		if errors.Is(err, store.ErrPassiveNotificationNotFound) {
			continue
		}
		if err != nil {
			return nil, errNotificationsOutcomeUnknown
		}
		updated++
	}
	unread, err := repo.CountUnreadPassiveNotifications(ctx, owner)
	if err != nil {
		return nil, errNotificationsUnavailable
	}
	revision, err := repo.PassiveNotificationRevision(ctx, owner)
	if err != nil {
		return nil, errNotificationsUnavailable
	}
	return map[string]any{"revision": strconv.FormatUint(revision, 10), "value": map[string]any{"updated": updated, "unread_count": unread}}, nil
}

func linkNotificationError(err error) error {
	if err == nil {
		return nil
	}
	code := contract.CodeBackendUnavailable
	switch {
	case errors.Is(err, errNotificationInvalid), errors.Is(err, errNotificationWatermarkInvalid):
		code = contract.CodeInvalidRequest
	case errors.Is(err, errNotificationCursorExpired), errors.Is(err, errNotificationWatermarkExpired):
		code = contract.CodeConflict
	case errors.Is(err, errNotificationWatermarkOwner):
		code = contract.CodeUnauthorized
	case errors.Is(err, errNotificationNotFound):
		code = contract.CodeNotFound
	case errors.Is(err, errNotificationsOutcomeUnknown):
		code = contract.CodeOutcomeUnknown
	}
	return linkDomainError(code, err.Error(), err.Error(), code == contract.CodeBackendUnavailable)
}

type notificationReadArgs struct {
	ID string `json:"id"`
}

type notificationReadAllArgs struct {
	Watermark string `json:"watermark"`
}

func init() {
	registerLinkDomain("notifications", map[string]linkDomainMethod{
		"notifications.list": {Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[notificationListArgs](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.listDeviceNotifications(ctx, req.Principal.OwnerID, req.Principal.DeviceID, args)
			return out, linkNotificationError(err)
		}},
		"notifications.read": {Mutation: true, Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[notificationReadArgs](raw)
			if err != nil || args.ID == "" {
				return nil, linkNotificationError(errNotificationInvalid)
			}
			out, err := s.markDeviceNotifications(ctx, req.Principal.OwnerID, req.Principal.DeviceID, args.ID, "")
			return out, linkNotificationError(err)
		}},
		"notifications.read_all": {Mutation: true, Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[notificationReadAllArgs](raw)
			if err != nil {
				return nil, linkNotificationError(errNotificationWatermarkInvalid)
			}
			out, err := s.markDeviceNotifications(ctx, req.Principal.OwnerID, req.Principal.DeviceID, "", args.Watermark)
			return out, linkNotificationError(err)
		}},
	})
}
