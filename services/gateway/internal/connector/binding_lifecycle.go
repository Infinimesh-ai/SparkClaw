package connector

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

func (r *Registry) StartNotificationBinding(ctx context.Context, requested app.NotificationBinding, options binding.StartOptions) (app.NotificationBinding, error) {
	if r == nil || r.store == nil {
		return app.NotificationBinding{}, errors.New("connector binding store is unavailable")
	}
	channel := normalizeChannel(requested.Channel)
	registration, ok := r.registrations[channel]
	if !ok || registration.Binding == nil {
		return app.NotificationBinding{}, &binding.BindingError{Code: binding.CodeConnectorUnavailable}
	}
	if !r.Enabled(requested.OwnerID, channel) {
		return app.NotificationBinding{}, &binding.BindingError{Code: binding.CodeUserDisabled}
	}
	if err := registration.Binding.Availability(); err != nil {
		return app.NotificationBinding{}, err
	}
	if requested.ID == "" {
		requested.ID = app.NewID("bind")
	}
	unlock := r.lockBinding(requested.ID)
	defer unlock()
	provider := strings.TrimSpace(registration.BindingProvider)
	if provider == "" {
		provider = strings.TrimSpace(r.cfg.Tools.Notifications.Channels[channel].Provider)
	}
	starting := app.NotificationBinding{
		ID: requested.ID, OwnerID: strings.TrimSpace(requested.OwnerID), ActorID: strings.TrimSpace(requested.ActorID),
		Channel: channel, Provider: provider, Status: app.NotificationBindingStarting,
		Scopes: append([]string(nil), requested.Scopes...), CredentialKind: strings.TrimSpace(registration.CredentialKind),
	}
	created, err := r.store.CreateNotificationBinding(ctx, starting)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	started, err := registration.Binding.Start(ctx, created, options)
	if err != nil {
		return app.NotificationBinding{}, r.failPreActiveBinding(ctx, created, err)
	}
	started.ID, started.OwnerID, started.ActorID = created.ID, created.OwnerID, created.ActorID
	started.Channel, started.Provider = created.Channel, created.Provider
	if strings.TrimSpace(started.CredentialKind) == "" {
		started.CredentialKind = created.CredentialKind
	}
	if started.Status == app.NotificationBindingActive {
		started.DefaultForChannel, err = r.shouldDefaultBinding(ctx, started)
		if err != nil {
			return app.NotificationBinding{}, r.failPreActiveBinding(ctx, created, err)
		}
	}
	committed, prior, updateErr := r.updateBindingAndResolve(ctx, created, started)
	if updateErr == nil {
		return committed, nil
	}
	if prior {
		if created.CredentialKind != "" {
			if cleanupErr := r.abortSeal(ctx, created); cleanupErr != nil {
				return app.NotificationBinding{}, cleanupErr
			}
		}
		if failureErr := r.markBindingFailed(ctx, created, updateErr); failureErr != nil {
			return app.NotificationBinding{}, errors.Join(updateErr, failureErr)
		}
	}
	return app.NotificationBinding{}, updateErr
}

func (r *Registry) PollNotificationBinding(ctx context.Context, id string) (app.NotificationBinding, error) {
	id = strings.TrimSpace(id)
	unlock := r.lockBinding(id)
	defer unlock()
	current, found, err := r.store.GetNotificationBinding(ctx, id)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	if !found {
		return app.NotificationBinding{}, storeErrorNotFound("notification binding not found")
	}
	if current.Status != app.NotificationBindingWaitingScan && current.Status != app.NotificationBindingWaitingConfirm {
		return current, nil
	}
	registration, ok := r.registrations[normalizeChannel(current.Channel)]
	if !ok || registration.Binding == nil {
		return app.NotificationBinding{}, &binding.BindingError{Code: binding.CodeConnectorUnavailable}
	}
	poll, pollErr := registration.Binding.Poll(ctx, current)
	if pollErr != nil {
		return app.NotificationBinding{}, r.failPreActiveBinding(ctx, current, pollErr)
	}
	replacement := applyPollResult(current, poll)
	if replacement.Status == app.NotificationBindingActive && strings.TrimSpace(poll.CredentialSecret) != "" {
		kind := strings.TrimSpace(poll.CredentialKind)
		if kind == "" {
			kind = strings.TrimSpace(registration.CredentialKind)
		}
		pending := replacement
		pending.Status = app.NotificationBindingCredentialPending
		pending.CredentialKind = kind
		pending.CredentialRef = ""
		pending.DefaultForChannel = false
		committedPending, prior, err := r.updateBindingAndResolve(ctx, current, pending)
		if err != nil {
			return app.NotificationBinding{}, err
		}
		if prior {
			return app.NotificationBinding{}, connectorUnavailable(errors.New("credential-pending transition did not commit"))
		}
		pending = committedPending
		secret := []byte(poll.CredentialSecret)
		ref, sealErr := r.seal(ctx, pending.ID, kind, secret)
		clearSecret(secret)
		poll.CredentialSecret = ""
		if sealErr != nil {
			if cleanupErr := r.abortSeal(ctx, pending); cleanupErr != nil {
				return app.NotificationBinding{}, cleanupErr
			}
			return app.NotificationBinding{}, r.markBindingFailed(ctx, pending, sealErr)
		}
		active := replacement
		active.ID, active.OwnerID, active.ActorID = pending.ID, pending.OwnerID, pending.ActorID
		active.Channel, active.Provider = pending.Channel, pending.Provider
		active.CredentialKind, active.CredentialRef = kind, ref
		active.DefaultForChannel, err = r.shouldDefaultBinding(ctx, active)
		if err != nil {
			return app.NotificationBinding{}, err
		}
		committed, prior, updateErr := r.updateBindingAndResolve(ctx, pending, active)
		if updateErr == nil {
			return committed, nil
		}
		if prior {
			if cleanupErr := r.abortSeal(ctx, pending); cleanupErr != nil {
				return app.NotificationBinding{}, cleanupErr
			}
			_ = r.markBindingFailed(ctx, pending, updateErr)
		}
		return app.NotificationBinding{}, updateErr
	}
	if replacement.Status == app.NotificationBindingActive {
		replacement.CredentialKind = strings.TrimSpace(poll.CredentialKind)
		replacement.DefaultForChannel, err = r.shouldDefaultBinding(ctx, replacement)
		if err != nil {
			return app.NotificationBinding{}, err
		}
	}
	committed, _, err := r.updateBindingAndResolve(ctx, current, replacement)
	return committed, err
}

func (r *Registry) RevokeNotificationBinding(ctx context.Context, id string) (app.NotificationBinding, error) {
	id = strings.TrimSpace(id)
	unlock := r.lockBinding(id)
	defer unlock()
	current, found, err := r.store.GetNotificationBinding(ctx, id)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	if !found {
		return app.NotificationBinding{}, storeErrorNotFound("notification binding not found")
	}
	if current.Status == app.NotificationBindingRevoked {
		return current, nil
	}
	if current.Status == app.NotificationBindingFailed || current.Status == app.NotificationBindingExpired {
		return current, storeErrorConflict("terminal notification binding cannot be revoked")
	}
	if current.Status != app.NotificationBindingRevoking {
		replacement := current
		replacement.Status = app.NotificationBindingRevoking
		replacement.DefaultForChannel = false
		current, _, err = r.updateBindingAndResolve(ctx, current, replacement)
		if err != nil {
			return app.NotificationBinding{}, err
		}
	}
	if registration, ok := r.registrations[normalizeChannel(current.Channel)]; ok {
		if registration.Binding != nil {
			if err := registration.Binding.Cancel(ctx, current); err != nil {
				return app.NotificationBinding{}, connectorUnavailable(err)
			}
		}
		if registration.CancelBinding != nil {
			registration.CancelBinding(current)
		}
	}
	if err := r.cleanupRevokingBinding(ctx, current); err != nil {
		return app.NotificationBinding{}, err
	}
	revoked := current
	revoked.Status = app.NotificationBindingRevoked
	revoked.DefaultForChannel = false
	committed, _, err := r.updateBindingAndResolve(ctx, current, revoked)
	return committed, err
}

func (r *Registry) recoverNotificationBindings(ctx context.Context) error {
	if r == nil || r.store == nil {
		return nil
	}
	bindings, err := r.store.ListNotificationBindings(ctx, "", "")
	if err != nil {
		return err
	}
	for _, snapshot := range bindings {
		switch snapshot.Status {
		case app.NotificationBindingStarting, app.NotificationBindingCredentialPending, app.NotificationBindingRevoking:
		default:
			continue
		}
		unlock := r.lockBinding(snapshot.ID)
		current, found, readErr := r.store.GetNotificationBinding(ctx, snapshot.ID)
		if readErr != nil {
			unlock()
			return readErr
		}
		if !found || current.Status != snapshot.Status || current.Version != snapshot.Version {
			unlock()
			continue
		}
		if current.Status == app.NotificationBindingRevoking {
			if err := r.cleanupRevokingBinding(ctx, current); err != nil {
				unlock()
				return err
			}
			replacement := current
			replacement.Status = app.NotificationBindingRevoked
			_, _, err = r.updateBindingAndResolve(ctx, current, replacement)
		} else {
			if current.CredentialKind != "" {
				err = r.abortSeal(ctx, current)
			}
			if err == nil {
				replacement := current
				replacement.Status = app.NotificationBindingFailed
				replacement.LastError = "connector_setup_interrupted"
				_, _, err = r.updateBindingAndResolve(ctx, current, replacement)
			}
		}
		unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func applyPollResult(current app.NotificationBinding, poll binding.PollResult) app.NotificationBinding {
	replacement := current
	if strings.TrimSpace(poll.Status) != "" {
		replacement.Status = strings.TrimSpace(poll.Status)
	}
	replacement.DisplayName = strings.TrimSpace(poll.DisplayName)
	replacement.ExternalUserID = strings.TrimSpace(poll.ExternalUserID)
	replacement.AccountID = strings.TrimSpace(poll.AccountID)
	replacement.BaseURL = strings.TrimSpace(poll.BaseURL)
	replacement.LastError = stableConnectorErrorText(poll.LastError)
	if replacement.Status == app.NotificationBindingActive {
		replacement.CredentialRef = strings.TrimSpace(poll.CredentialRef)
		replacement.QRCodeURL = ""
		replacement.QRCodeImage = ""
		replacement.ExpiresAt = nil
	}
	return replacement
}

func (r *Registry) failPreActiveBinding(ctx context.Context, current app.NotificationBinding, cause error) error {
	proven, found, err := r.store.GetNotificationBinding(ctx, current.ID)
	if err != nil {
		return connectorUnavailable(errors.Join(cause, err))
	}
	if !found || !store.NotificationBindingsEqual(proven, current) {
		return connectorUnavailable(cause)
	}
	if proven.CredentialKind != "" {
		if err := r.abortSeal(ctx, proven); err != nil {
			return err
		}
	}
	if err := r.markBindingFailed(ctx, proven, cause); err != nil {
		return err
	}
	var coded interface{ ErrorCode() string }
	if errors.As(cause, &coded) {
		return cause
	}
	return connectorUnavailable(cause)
}

func (r *Registry) markBindingFailed(ctx context.Context, current app.NotificationBinding, cause error) error {
	replacement := current
	replacement.Status = app.NotificationBindingFailed
	replacement.DefaultForChannel = false
	replacement.CredentialRef = ""
	replacement.LastError = stableConnectorError(cause)
	_, _, err := r.updateBindingAndResolve(ctx, current, replacement)
	return err
}

func (r *Registry) cleanupRevokingBinding(ctx context.Context, current app.NotificationBinding) error {
	ref := strings.TrimSpace(current.CredentialRef)
	legacyWeixinPrefix := "provider:" + weixinproto.QRProvider + ":"
	if strings.HasPrefix(ref, legacyWeixinPrefix) && ref != legacyWeixinPrefix+current.ID {
		return connectorUnavailable(errors.New("legacy credential reference does not match binding identity"))
	}
	switch {
	case ref != "" && !strings.HasPrefix(ref, "config:"):
		if r.credentials == nil {
			return connectorUnavailable(errors.New("credential lifecycle is unavailable"))
		}
		if err := r.credentials.Delete(ctx, ref); err != nil {
			return err
		}
	case ref == "" && current.CredentialKind != "":
		return r.abortSeal(ctx, current)
	}
	return nil
}

func (r *Registry) abortSeal(ctx context.Context, current app.NotificationBinding) error {
	if r.credentials == nil {
		return connectorUnavailable(errors.New("credential lifecycle is unavailable"))
	}
	return r.credentials.AbortSeal(ctx, current.ID, current.CredentialKind)
}

func (r *Registry) seal(ctx context.Context, id, kind string, secret []byte) (string, error) {
	if r.credentials == nil {
		return "", connectorUnavailable(errors.New("credential lifecycle is unavailable"))
	}
	return r.credentials.Seal(ctx, id, kind, secret)
}

func (r *Registry) shouldDefaultBinding(ctx context.Context, candidate app.NotificationBinding) (bool, error) {
	if candidate.DefaultForChannel {
		return true, nil
	}
	bindings, err := r.store.ListNotificationBindings(ctx, candidate.Channel, app.NotificationBindingActive)
	if err != nil {
		return false, err
	}
	for _, existing := range bindings {
		if existing.ID != candidate.ID && existing.DefaultForChannel {
			return false, nil
		}
	}
	return true, nil
}

func (r *Registry) updateBindingAndResolve(ctx context.Context, previous, replacement app.NotificationBinding) (app.NotificationBinding, bool, error) {
	candidate, err := r.store.UpdateNotificationBinding(ctx, store.NewNotificationBindingUpdate(previous, replacement))
	if err == nil {
		return candidate, false, nil
	}
	current, found, readErr := r.store.GetNotificationBinding(ctx, previous.ID)
	if readErr != nil {
		return app.NotificationBinding{}, false, errors.Join(err, readErr)
	}
	if found && candidate.ID != "" && store.NotificationBindingsEqual(current, candidate) {
		return current, false, nil
	}
	if found && store.NotificationBindingsEqual(current, previous) {
		return app.NotificationBinding{}, true, err
	}
	return app.NotificationBinding{}, false, err
}

func (r *Registry) lockBinding(id string) func() {
	value, _ := r.bindingLocks.LoadOrStore(strings.TrimSpace(id), &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func stableConnectorError(err error) string {
	if err == nil {
		return ""
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) && strings.TrimSpace(coded.ErrorCode()) != "" {
		return strings.TrimSpace(coded.ErrorCode())
	}
	return "connector_operation_failed"
}

func stableConnectorErrorText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "connector_provider_pending"
}

func connectorUnavailable(cause error) error {
	return &lifecycleUnavailableError{cause: cause}
}

type lifecycleUnavailableError struct {
	cause error
}

func (e *lifecycleUnavailableError) Error() string {
	return "connector lifecycle is unavailable"
}

func (e *lifecycleUnavailableError) Unwrap() error {
	return e.cause
}

func (e *lifecycleUnavailableError) ErrorCode() string {
	return binding.CodeConnectorUnavailable
}

func (e *lifecycleUnavailableError) Retryable() bool {
	return true
}

func storeErrorNotFound(message string) error {
	return &store.StoreError{Code: store.StoreErrorNotFound, Operation: store.OperationNotificationBindingGet, Err: errors.New(message)}
}

func storeErrorConflict(message string) error {
	return &store.StoreError{Code: store.StoreErrorConflict, Operation: store.OperationNotificationBindingUpdate, Err: errors.New(message)}
}

func clearSecret(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ CredentialLifecycle = (credential.CredentialVault)(nil)
