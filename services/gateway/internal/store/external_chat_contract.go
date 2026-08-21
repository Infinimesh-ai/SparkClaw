package store

import (
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func prepareExternalChatSession(session app.ExternalChatSession, now time.Time) app.ExternalChatSession {
	now = normalizeExternalChatTime(now)
	if session.ID == "" {
		session.ID = app.NewID("extchat")
	}
	session.Channel = strings.ToLower(strings.TrimSpace(session.Channel))
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
	session.Status = strings.TrimSpace(session.Status)
	if session.Status == "" {
		session.Status = "active"
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	} else {
		session.CreatedAt = normalizeExternalChatTime(session.CreatedAt)
	}
	session.UpdatedAt = now
	return session
}

func prepareExternalChatMessage(message app.ExternalChatMessage, channel string, now time.Time) app.ExternalChatMessage {
	now = normalizeExternalChatTime(now)
	if message.ID == "" {
		message.ID = app.NewID("extmsg")
	}
	if message.Channel == "" {
		message.Channel = channel
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	} else {
		message.CreatedAt = normalizeExternalChatTime(message.CreatedAt)
	}
	message.UpdatedAt = now
	return message
}

func normalizeExternalChatSession(session app.ExternalChatSession) app.ExternalChatSession {
	if !session.CreatedAt.IsZero() {
		session.CreatedAt = normalizeExternalChatTime(session.CreatedAt)
	}
	if !session.UpdatedAt.IsZero() {
		session.UpdatedAt = normalizeExternalChatTime(session.UpdatedAt)
	}
	return session
}

func normalizeExternalChatMessage(message app.ExternalChatMessage) app.ExternalChatMessage {
	if !message.CreatedAt.IsZero() {
		message.CreatedAt = normalizeExternalChatTime(message.CreatedAt)
	}
	if !message.UpdatedAt.IsZero() {
		message.UpdatedAt = normalizeExternalChatTime(message.UpdatedAt)
	}
	return message
}

func normalizeExternalChatTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}
