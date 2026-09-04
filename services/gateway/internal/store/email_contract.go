package store

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var emailErrorCodePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

func emailProviderKey(ownerID, provider string) string {
	return normalizeConnectorOwner(ownerID) + "\x00" + strings.ToLower(strings.TrimSpace(provider))
}

func supportedEmailProvider(provider string) bool {
	switch provider {
	case app.EmailProviderQQMail, app.EmailProviderOutlook, app.EmailProviderGmail:
		return true
	default:
		return false
	}
}

func supportedEmailState(state string) bool {
	switch state {
	case app.EmailStateNotConfigured, app.EmailStateLoginRequired, app.EmailStateReady,
		app.EmailStateNeedsAttention, app.EmailStateTemporarilyUnavailable:
		return true
	default:
		return false
	}
}

func normalizeEmailProviderCandidate(setting app.EmailProviderSetting, expectedVersion int64) (app.EmailProviderSetting, error) {
	setting.OwnerID = normalizeConnectorOwner(setting.OwnerID)
	setting.Provider = strings.ToLower(strings.TrimSpace(setting.Provider))
	setting.Account = strings.ToLower(strings.TrimSpace(setting.Account))
	if setting.Account == "" {
		setting.Account = app.EmailAccountDefault
	}
	setting.AccountHint = strings.TrimSpace(setting.AccountHint)
	setting.State = strings.ToLower(strings.TrimSpace(setting.State))
	if setting.State == "" {
		setting.State = app.EmailStateNotConfigured
	}
	setting.ErrorCode = strings.ToLower(strings.TrimSpace(setting.ErrorCode))
	setting.UpdatedBy = strings.TrimSpace(setting.UpdatedBy)
	if setting.UpdatedBy == "" {
		setting.UpdatedBy = setting.OwnerID
	}
	setting.LastCheckedAt = cloneEmailTime(setting.LastCheckedAt)
	setting.Version = 0
	setting.UpdatedAt = time.Time{}
	if expectedVersion < 0 {
		return app.EmailProviderSetting{}, errors.New("non-negative expected version is required")
	}
	if err := validateEmailProviderBusinessFields(setting); err != nil {
		return app.EmailProviderSetting{}, err
	}
	return setting, nil
}

func validateEmailProviderBusinessFields(setting app.EmailProviderSetting) error {
	if !supportedEmailProvider(setting.Provider) {
		return errors.New("email provider is not registered")
	}
	if setting.Account != app.EmailAccountDefault {
		return errors.New("only the default email account is supported")
	}
	if !supportedEmailState(setting.State) {
		return errors.New("email provider state is invalid")
	}
	if setting.Default && !setting.Enabled {
		return errors.New("disabled email provider cannot be the default")
	}
	if err := validateMaskedEmailAccountHint(setting.AccountHint); err != nil {
		return err
	}
	if setting.ErrorCode != "" && !emailErrorCodePattern.MatchString(setting.ErrorCode) {
		return errors.New("email provider error code is invalid")
	}
	if setting.State == app.EmailStateReady && setting.LastCheckedAt == nil {
		return errors.New("ready email provider requires a login check time")
	}
	return nil
}

func validateMaskedEmailAccountHint(hint string) error {
	if hint == "" {
		return nil
	}
	if utf8.RuneCountInString(hint) > 64 || strings.ContainsAny(hint, "\r\n\x00") {
		return errors.New("email account hint is invalid")
	}
	separator := strings.LastIndex(hint, "***@")
	if separator <= 0 || strings.Count(hint, "***@") != 1 {
		return errors.New("email account hint must be irreversibly masked")
	}
	prefix := hint[:separator]
	domain := hint[separator+4:]
	if utf8.RuneCountInString(prefix) > 2 || strings.TrimSpace(prefix) != prefix || domain == "" ||
		domain != strings.ToLower(domain) || strings.ContainsAny(domain, " @/\\") || !strings.Contains(domain, ".") {
		return errors.New("email account hint must contain at most two local-part characters and a lowercase domain")
	}
	return nil
}

func validatePersistedEmailProviderSetting(setting app.EmailProviderSetting) error {
	if setting.OwnerID == "" || setting.OwnerID != normalizeConnectorOwner(setting.OwnerID) ||
		setting.Provider == "" || setting.Provider != strings.ToLower(strings.TrimSpace(setting.Provider)) ||
		setting.Version < 1 || setting.UpdatedAt.IsZero() || strings.TrimSpace(setting.UpdatedBy) == "" {
		return errors.New("email provider identity, version, updater, and update time are invalid")
	}
	return validateEmailProviderBusinessFields(setting)
}

func normalizeAndValidatePersistedEmailProviderState(settings map[string]app.EmailProviderSetting) error {
	defaults := map[string]string{}
	for key, setting := range settings {
		if key != emailProviderKey(setting.OwnerID, setting.Provider) {
			return fmt.Errorf("email provider setting key %q does not match embedded identity", key)
		}
		if err := validatePersistedEmailProviderSetting(setting); err != nil {
			return fmt.Errorf("email provider setting %q: %w", key, err)
		}
		if setting.Default {
			if prior := defaults[setting.OwnerID]; prior != "" {
				return fmt.Errorf("email providers %q and %q are both defaults for owner %q", prior, setting.Provider, setting.OwnerID)
			}
			defaults[setting.OwnerID] = setting.Provider
		}
		settings[key] = cloneEmailProviderSetting(setting)
	}
	return nil
}

func cloneEmailProviderSetting(setting app.EmailProviderSetting) app.EmailProviderSetting {
	setting.LastCheckedAt = cloneEmailTime(setting.LastCheckedAt)
	return setting
}

func cloneEmailProviderSettingMap(input map[string]app.EmailProviderSetting) map[string]app.EmailProviderSetting {
	output := make(map[string]app.EmailProviderSetting, len(input))
	for key, setting := range input {
		output[key] = cloneEmailProviderSetting(setting)
	}
	return output
}

func cloneEmailTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := postgresTime(*value)
	return &normalized
}

func compareEmailProviderSettings(left, right app.EmailProviderSetting) int {
	return strings.Compare(left.Provider, right.Provider)
}

func emailProviderAuditFields(setting app.EmailProviderSetting) map[string]any {
	return map[string]any{
		"provider": setting.Provider, "enabled": setting.Enabled, "default": setting.Default,
		"account": setting.Account, "account_hint": setting.AccountHint, "state": setting.State,
		"last_checked_at": setting.LastCheckedAt, "error_code": setting.ErrorCode, "version": setting.Version,
	}
}

func sortedEmailProviderSettings(settings map[string]app.EmailProviderSetting, ownerID string) []app.EmailProviderSetting {
	ownerID = normalizeConnectorOwner(ownerID)
	out := make([]app.EmailProviderSetting, 0, len(settings))
	for _, setting := range settings {
		if setting.OwnerID == ownerID {
			out = append(out, cloneEmailProviderSetting(setting))
		}
	}
	slices.SortFunc(out, compareEmailProviderSettings)
	return out
}
