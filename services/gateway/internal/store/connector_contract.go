package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var errNotificationBindingScopesDecode = errors.New("notification binding scopes decode failed")

func classifyConnectorBindingScanError(operation StoreOperation, ctx context.Context, err error) error {
	if errors.Is(err, errNotificationBindingScopesDecode) {
		return storeError(operation, StoreErrorCorrupt, err)
	}
	return classifyPostgresReadError(operation, ctx, err)
}

type NotificationBindingUpdateCommand struct {
	id       string
	expected [sha256.Size]byte
	valid    bool
	next     app.NotificationBinding
}

func NewNotificationBindingUpdate(previous, replacement app.NotificationBinding) NotificationBindingUpdateCommand {
	previous = cloneNotificationBinding(previous)
	replacement = cloneNotificationBinding(replacement)
	if err := validatePersistedNotificationBinding(previous); err != nil || !sameNotificationBindingIdentity(previous, replacement) {
		return NotificationBindingUpdateCommand{}
	}
	return NotificationBindingUpdateCommand{
		id: previous.ID, expected: notificationBindingDigest(previous), valid: true, next: replacement,
	}
}

func normalizeNotificationBindingUpdateCommand(command NotificationBindingUpdateCommand) (NotificationBindingUpdateCommand, error) {
	command.id = strings.TrimSpace(command.id)
	command.next = cloneNotificationBinding(command.next)
	if !command.valid || command.id == "" || command.next.ID != command.id {
		return NotificationBindingUpdateCommand{}, errors.New("notification binding update condition is invalid")
	}
	return command, nil
}

func normalizeConnectorSettingCandidate(setting app.ConnectorSetting, expectedVersion int64) (app.ConnectorSetting, error) {
	setting.OwnerID = normalizeConnectorOwner(setting.OwnerID)
	setting.Channel = normalizeConnectorChannel(setting.Channel)
	setting.UpdatedBy = strings.TrimSpace(setting.UpdatedBy)
	if setting.UpdatedBy == "" {
		setting.UpdatedBy = setting.OwnerID
	}
	setting.Version = 0
	setting.UpdatedAt = time.Time{}
	if setting.Channel == "" || expectedVersion < 0 {
		return app.ConnectorSetting{}, errors.New("connector channel and non-negative expected version are required")
	}
	return setting, nil
}

func validatePersistedConnectorSetting(setting app.ConnectorSetting) error {
	if setting.OwnerID == "" || setting.OwnerID != normalizeConnectorOwner(setting.OwnerID) ||
		setting.Channel == "" || setting.Channel != normalizeConnectorChannel(setting.Channel) ||
		setting.Version < 1 || setting.UpdatedAt.IsZero() {
		return errors.New("connector setting identity, version, and update time are invalid")
	}
	if strings.TrimSpace(setting.UpdatedBy) == "" {
		return errors.New("connector setting updater is required")
	}
	return nil
}

func normalizeAndValidatePersistedConnectorState(settings map[string]app.ConnectorSetting, bindings map[string]app.NotificationBinding) error {
	for key, setting := range settings {
		if strings.TrimSpace(setting.OwnerID) == "" {
			setting.OwnerID = app.DefaultOwnerID
		}
		if setting.Version == 0 {
			setting.Version = 1
		}
		if key != connectorSettingKey(setting.OwnerID, setting.Channel) {
			return fmt.Errorf("connector setting key %q does not match embedded identity", key)
		}
		if err := validatePersistedConnectorSetting(setting); err != nil {
			return fmt.Errorf("connector setting %q: %w", key, err)
		}
		settings[key] = setting
	}
	vaultOwners := map[string]string{}
	activeDefaults := map[string]string{}
	for id, binding := range bindings {
		if binding.Version == 0 {
			binding.Version = 1
		}
		if strings.TrimSpace(binding.ActorID) == "" {
			binding.ActorID = binding.OwnerID
		}
		if id == "" || binding.ID != id {
			return fmt.Errorf("notification binding key %q does not match embedded ID %q", id, binding.ID)
		}
		if err := validatePersistedNotificationBinding(binding); err != nil {
			return fmt.Errorf("notification binding %q: %w", id, err)
		}
		if err := claimBindingCredentialRef(vaultOwners, binding); err != nil {
			return err
		}
		if binding.Status == app.NotificationBindingActive && binding.DefaultForChannel {
			key := connectorSettingKey(binding.OwnerID, binding.Channel)
			if prior := activeDefaults[key]; prior != "" {
				return fmt.Errorf("notification bindings %q and %q are both active defaults", prior, id)
			}
			activeDefaults[key] = id
		}
		bindings[id] = cloneNotificationBinding(binding)
	}
	return nil
}

func normalizeNotificationBindingCreate(binding app.NotificationBinding) (app.NotificationBinding, error) {
	binding = cloneNotificationBinding(binding)
	if binding.Version != 0 || !binding.CreatedAt.IsZero() || !binding.UpdatedAt.IsZero() {
		return app.NotificationBinding{}, errors.New("starting notification binding carries repository lifecycle fields")
	}
	binding.ID = strings.TrimSpace(binding.ID)
	binding.OwnerID = strings.TrimSpace(binding.OwnerID)
	binding.ActorID = strings.TrimSpace(binding.ActorID)
	binding.Channel = normalizeConnectorChannel(binding.Channel)
	binding.Provider = strings.TrimSpace(binding.Provider)
	binding.CredentialKind = strings.TrimSpace(binding.CredentialKind)
	if binding.ID == "" || len(binding.ID) > 256 || binding.OwnerID == "" || binding.ActorID == "" ||
		binding.Channel == "" || binding.Provider == "" || binding.Status != app.NotificationBindingStarting {
		return app.NotificationBinding{}, errors.New("complete starting notification binding identity is required")
	}
	if binding.DisplayName != "" || binding.ExternalUserID != "" || binding.ExternalChatID != "" ||
		binding.ExternalThreadID != "" || binding.AccountID != "" || binding.CredentialRef != "" ||
		binding.BaseURL != "" || binding.ProviderSessionID != "" || binding.ProviderState != "" ||
		binding.ContextToken != "" || binding.ProviderCursor != "" || binding.QRCodeURL != "" ||
		binding.QRCodeImage != "" || binding.DefaultForChannel || binding.ExpiresAt != nil ||
		binding.RevokedAt != nil || binding.LastError != "" {
		return app.NotificationBinding{}, errors.New("starting notification binding carries provider or lifecycle state")
	}
	return binding, nil
}

func prepareNotificationBindingUpdate(previous app.NotificationBinding, replacement app.NotificationBinding, at time.Time) (app.NotificationBinding, error) {
	previous = cloneNotificationBinding(previous)
	replacement = cloneNotificationBinding(replacement)
	if err := validatePersistedNotificationBinding(previous); err != nil {
		return app.NotificationBinding{}, err
	}
	if !sameNotificationBindingIdentity(previous, replacement) {
		return app.NotificationBinding{}, errors.New("notification binding immutable identity changed")
	}
	replacement.ID = previous.ID
	replacement.OwnerID = previous.OwnerID
	replacement.ActorID = previous.ActorID
	replacement.Channel = previous.Channel
	replacement.Provider = strings.TrimSpace(replacement.Provider)
	if previous.Provider != "" && replacement.Provider != previous.Provider {
		return app.NotificationBinding{}, errors.New("notification binding provider changed")
	}
	replacement.CredentialRef = strings.TrimSpace(replacement.CredentialRef)
	replacement.CredentialKind = strings.TrimSpace(replacement.CredentialKind)
	replacement.CreatedAt = previous.CreatedAt
	replacement.Version = previous.Version + 1
	replacement.UpdatedAt = postgresTime(at)
	if !notificationBindingTransitionAllowed(previous.Status, replacement.Status) {
		return app.NotificationBinding{}, fmt.Errorf("notification binding transition %q to %q is not allowed", previous.Status, replacement.Status)
	}
	if previous.CredentialRef != replacement.CredentialRef {
		if previous.CredentialRef != "" || replacement.Status != app.NotificationBindingActive || replacement.CredentialRef == "" {
			return app.NotificationBinding{}, errors.New("notification binding credential ref changed outside activation")
		}
	}
	if previous.CredentialKind != "" && replacement.CredentialKind != previous.CredentialKind {
		return app.NotificationBinding{}, errors.New("notification binding credential kind changed")
	}
	if replacement.Status == app.NotificationBindingRevoked {
		revokedAt := replacement.UpdatedAt
		replacement.RevokedAt = &revokedAt
		replacement.DefaultForChannel = false
	} else if replacement.RevokedAt != nil {
		return app.NotificationBinding{}, errors.New("non-revoked notification binding carries revoke time")
	}
	if replacement.Status != app.NotificationBindingActive && replacement.Status != app.NotificationBindingRevoking && replacement.Status != app.NotificationBindingRevoked && replacement.CredentialRef != "" {
		return app.NotificationBinding{}, errors.New("non-active notification binding carries a credential ref")
	}
	if (previous.Status == app.NotificationBindingActive || previous.Status == app.NotificationBindingRevoking) && replacement.CredentialRef != previous.CredentialRef {
		return app.NotificationBinding{}, errors.New("notification binding lost credential ownership")
	}
	if replacement.Status == app.NotificationBindingCredentialPending && (replacement.CredentialKind == "" || replacement.CredentialRef != "") {
		return app.NotificationBinding{}, errors.New("credential-pending binding requires kind without ref")
	}
	if replacement.Status == app.NotificationBindingActive && strings.HasPrefix(replacement.CredentialRef, "cred_") && replacement.CredentialKind == "" && previous.CredentialRef == "" {
		return app.NotificationBinding{}, errors.New("new Vault credential requires a kind")
	}
	if err := validateNotificationBindingFieldMask(previous, replacement); err != nil {
		return app.NotificationBinding{}, err
	}
	if err := validatePersistedNotificationBinding(replacement); err != nil {
		return app.NotificationBinding{}, err
	}
	return replacement, nil
}

func validateNotificationBindingFieldMask(previous, replacement app.NotificationBinding) error {
	expected := cloneNotificationBinding(previous)
	expected.Status = replacement.Status
	expected.Version = replacement.Version
	expected.CreatedAt = replacement.CreatedAt
	expected.UpdatedAt = replacement.UpdatedAt
	expected.RevokedAt = cloneTimePointer(replacement.RevokedAt)
	switch {
	case isWaitingNotificationBinding(previous.Status) && isWaitingNotificationBinding(replacement.Status):
		expected.DisplayName = replacement.DisplayName
		expected.BaseURL = replacement.BaseURL
		expected.ProviderSessionID = replacement.ProviderSessionID
		expected.ProviderState = replacement.ProviderState
		expected.QRCodeURL = replacement.QRCodeURL
		expected.QRCodeImage = replacement.QRCodeImage
		expected.ExpiresAt = cloneTimePointer(replacement.ExpiresAt)
		expected.LastError = replacement.LastError
	case previous.Status == app.NotificationBindingActive && replacement.Status == app.NotificationBindingActive:
		expected.DisplayName = replacement.DisplayName
		expected.ExternalUserID = replacement.ExternalUserID
		expected.ExternalChatID = replacement.ExternalChatID
		expected.ExternalThreadID = replacement.ExternalThreadID
		expected.AccountID = replacement.AccountID
		expected.BaseURL = replacement.BaseURL
		expected.ContextToken = replacement.ContextToken
		expected.ProviderCursor = replacement.ProviderCursor
		expected.DefaultForChannel = replacement.DefaultForChannel
		expected.Scopes = append([]string(nil), replacement.Scopes...)
		expected.LastError = replacement.LastError
	default:
		return nil
	}
	if !notificationBindingsEqual(expected, replacement) {
		return errors.New("notification binding update changes fields outside its lifecycle mask")
	}
	return nil
}

func isWaitingNotificationBinding(status string) bool {
	return status == app.NotificationBindingWaitingScan || status == app.NotificationBindingWaitingConfirm
}

func validatePersistedNotificationBinding(binding app.NotificationBinding) error {
	if binding.ID == "" || len(binding.ID) > 256 || strings.TrimSpace(binding.ID) != binding.ID ||
		binding.OwnerID == "" || strings.TrimSpace(binding.OwnerID) != binding.OwnerID ||
		binding.ActorID == "" || strings.TrimSpace(binding.ActorID) != binding.ActorID ||
		binding.Channel == "" || normalizeConnectorChannel(binding.Channel) != binding.Channel ||
		strings.TrimSpace(binding.Provider) != binding.Provider || binding.Version < 1 ||
		binding.CreatedAt.IsZero() || binding.UpdatedAt.IsZero() {
		return errors.New("notification binding identity, version, and times are invalid")
	}
	if binding.ExpiresAt != nil && binding.ExpiresAt.IsZero() {
		return errors.New("notification binding expiry time is zero")
	}
	if binding.RevokedAt != nil && binding.RevokedAt.IsZero() {
		return errors.New("notification binding revoke time is zero")
	}
	if strings.TrimSpace(binding.CredentialRef) != binding.CredentialRef || strings.TrimSpace(binding.CredentialKind) != binding.CredentialKind {
		return errors.New("notification binding credential proof is not normalized")
	}
	for _, scope := range binding.Scopes {
		if strings.TrimSpace(scope) == "" || strings.TrimSpace(scope) != scope {
			return errors.New("notification binding scope is invalid")
		}
	}
	switch binding.Status {
	case app.NotificationBindingStarting, app.NotificationBindingWaitingScan, app.NotificationBindingWaitingConfirm:
		if binding.CredentialRef != "" || binding.RevokedAt != nil || binding.DefaultForChannel {
			return errors.New("pre-active notification binding carries active state")
		}
	case app.NotificationBindingCredentialPending:
		if binding.CredentialRef != "" || binding.CredentialKind == "" || binding.RevokedAt != nil || binding.DefaultForChannel {
			return errors.New("credential-pending notification binding is invalid")
		}
	case app.NotificationBindingActive:
		if binding.RevokedAt != nil {
			return errors.New("active notification binding is revoked")
		}
	case app.NotificationBindingRevoking:
		if binding.RevokedAt != nil || binding.DefaultForChannel {
			return errors.New("revoking notification binding is invalid")
		}
	case app.NotificationBindingRevoked:
		if binding.RevokedAt == nil || binding.DefaultForChannel {
			return errors.New("revoked notification binding is invalid")
		}
	case app.NotificationBindingFailed, app.NotificationBindingExpired:
		if binding.CredentialRef != "" || binding.RevokedAt != nil || binding.DefaultForChannel {
			return errors.New("terminal pre-active notification binding carries active state")
		}
	default:
		return fmt.Errorf("notification binding status %q is invalid", binding.Status)
	}
	return nil
}

func notificationBindingTransitionAllowed(previous, next string) bool {
	switch previous {
	case app.NotificationBindingStarting:
		return slices.Contains([]string{app.NotificationBindingWaitingScan, app.NotificationBindingWaitingConfirm, app.NotificationBindingActive, app.NotificationBindingFailed, app.NotificationBindingRevoking}, next)
	case app.NotificationBindingWaitingScan, app.NotificationBindingWaitingConfirm:
		return slices.Contains([]string{app.NotificationBindingWaitingScan, app.NotificationBindingWaitingConfirm, app.NotificationBindingCredentialPending, app.NotificationBindingActive, app.NotificationBindingExpired, app.NotificationBindingFailed, app.NotificationBindingRevoking}, next)
	case app.NotificationBindingCredentialPending:
		return slices.Contains([]string{app.NotificationBindingActive, app.NotificationBindingFailed, app.NotificationBindingRevoking}, next)
	case app.NotificationBindingActive:
		return next == app.NotificationBindingActive || next == app.NotificationBindingRevoking
	case app.NotificationBindingRevoking:
		return next == app.NotificationBindingRevoked
	default:
		return false
	}
}

func sameNotificationBindingIdentity(left, right app.NotificationBinding) bool {
	return strings.TrimSpace(left.ID) == strings.TrimSpace(right.ID) &&
		strings.TrimSpace(left.OwnerID) == strings.TrimSpace(right.OwnerID) &&
		strings.TrimSpace(left.ActorID) == strings.TrimSpace(right.ActorID) &&
		normalizeConnectorChannel(left.Channel) == normalizeConnectorChannel(right.Channel)
}

func cloneNotificationBinding(binding app.NotificationBinding) app.NotificationBinding {
	binding.Scopes = append([]string(nil), binding.Scopes...)
	binding.ExpiresAt = cloneTimePointer(binding.ExpiresAt)
	binding.RevokedAt = cloneTimePointer(binding.RevokedAt)
	return binding
}

func cloneNotificationBindingMap(values map[string]app.NotificationBinding) map[string]app.NotificationBinding {
	out := make(map[string]app.NotificationBinding, len(values))
	for id, binding := range values {
		out[id] = cloneNotificationBinding(binding)
	}
	return out
}

func notificationBindingsEqual(left, right app.NotificationBinding) bool {
	left = cloneNotificationBinding(left)
	right = cloneNotificationBinding(right)
	return notificationBindingDigest(left) == notificationBindingDigest(right)
}

func NotificationBindingsEqual(left, right app.NotificationBinding) bool {
	return notificationBindingsEqual(left, right)
}

func notificationBindingDigest(binding app.NotificationBinding) [sha256.Size]byte {
	digest := sha256.New()
	writeCredentialDigestBytes(digest, "sparkclaw-notification-binding-version-v1")
	for _, value := range []string{
		binding.ID, binding.OwnerID, binding.ActorID, binding.Channel, binding.Provider, binding.Status,
		binding.DisplayName, binding.ExternalUserID, binding.ExternalChatID, binding.ExternalThreadID,
		binding.AccountID, binding.CredentialRef, binding.CredentialKind, binding.BaseURL,
		binding.ProviderSessionID, binding.ProviderState, binding.ContextToken, binding.ProviderCursor,
		binding.QRCodeURL, binding.QRCodeImage, binding.LastError,
	} {
		writeCredentialDigestBytes(digest, value)
	}
	writeDigestBool(digest, binding.DefaultForChannel)
	writeDigestInt64(digest, binding.Version)
	writeCredentialDigestTime(digest, binding.CreatedAt)
	writeCredentialDigestTime(digest, binding.UpdatedAt)
	writeDigestTimePointer(digest, binding.ExpiresAt)
	writeDigestTimePointer(digest, binding.RevokedAt)
	writeDigestInt64(digest, int64(len(binding.Scopes)))
	for _, scope := range binding.Scopes {
		writeCredentialDigestBytes(digest, scope)
	}
	var out [sha256.Size]byte
	copy(out[:], digest.Sum(nil))
	return out
}

func writeDigestBool(digest hash.Hash, value bool) {
	encoded := byte(0)
	if value {
		encoded = 1
	}
	_, _ = digest.Write([]byte{encoded})
}

func writeDigestInt64(digest hash.Hash, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = digest.Write(encoded[:])
}

func writeDigestTimePointer(digest hash.Hash, value *time.Time) {
	writeDigestBool(digest, value != nil)
	if value != nil {
		writeCredentialDigestTime(digest, *value)
	}
}

func isVaultOwnedCredentialRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	return ref != "" && !strings.HasPrefix(ref, "config:")
}

func claimBindingCredentialRef(owners map[string]string, binding app.NotificationBinding) error {
	if !isVaultOwnedCredentialRef(binding.CredentialRef) {
		return nil
	}
	if prior := owners[binding.CredentialRef]; prior != "" && prior != binding.ID {
		return fmt.Errorf("notification bindings %q and %q share Vault credential ref", prior, binding.ID)
	}
	owners[binding.CredentialRef] = binding.ID
	return nil
}

func latestNotificationBindingTime(binding app.NotificationBinding) time.Time {
	latest := binding.CreatedAt
	for _, candidate := range []time.Time{binding.UpdatedAt, timePointerValue(binding.ExpiresAt), timePointerValue(binding.RevokedAt)} {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	return latest
}
