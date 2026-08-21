package store

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func normalizeConnectorOwner(ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return app.DefaultOwnerID
	}
	return ownerID
}

func normalizeConnectorChannel(channel string) string {
	return strings.ToLower(strings.TrimSpace(channel))
}

func connectorSettingKey(ownerID, channel string) string {
	return normalizeConnectorOwner(ownerID) + "\x1f" + normalizeConnectorChannel(channel)
}

func externalChatSessionTitle(channel string) string {
	if strings.EqualFold(strings.TrimSpace(channel), "telegram") {
		return "Telegram 会话"
	}
	return "微信会话"
}

func deriveTitle(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "New SparkClaw Session"
	}
	runes := []rune(content)
	if len(runes) > 42 {
		return string(runes[:42]) + "..."
	}
	return content
}

func summarizeReminderText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "Reminder"
	}
	runes := []rune(content)
	if len(runes) > 80 {
		return string(runes[:80]) + "..."
	}
	return content
}

func redactExternalID(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 6 {
		return value
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-2:])
}
