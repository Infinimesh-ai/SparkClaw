package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	inboxLeaseDuration = 2 * time.Minute
	maxInboxAttempts   = 3
)

type ClientFactory func(context.Context, app.NotificationBinding) (BotAPI, error)

type Service struct {
	store         store.Store
	cfg           config.NotificationChannelConfig
	vault         credential.CredentialVault
	dispatcher    *Dispatcher
	clientFactory ClientFactory
	wake          chan struct{}
	sem           chan struct{}
	mu            sync.Mutex
	workers       sync.WaitGroup
	busyChats     map[string]bool
	activeCancels map[string]map[string]context.CancelFunc
	lifecycleCtx  context.Context
}

func NewService(st store.Store, cfg config.NotificationChannelConfig, vault credential.CredentialVault, dispatcher *Dispatcher) *Service {
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 1
	}
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = 32
	}
	service := &Service{
		store:         st,
		cfg:           cfg,
		vault:         vault,
		dispatcher:    dispatcher,
		wake:          make(chan struct{}, 1),
		sem:           make(chan struct{}, cfg.MaxConcurrency),
		busyChats:     map[string]bool{},
		activeCancels: map[string]map[string]context.CancelFunc{},
	}
	service.clientFactory = service.vaultClient
	return service
}

func (s *Service) WithClientFactory(factory ClientFactory) *Service {
	if factory != nil {
		s.clientFactory = factory
	}
	return s
}

func (s *Service) Run(ctx context.Context, scope connectorruntime.RuntimeScope) error {
	if s.store == nil || s.dispatcher == nil || s.clientFactory == nil {
		return errors.New("Telegram service dependencies are incomplete")
	}
	s.mu.Lock()
	s.lifecycleCtx = ctx
	s.mu.Unlock()
	if err := s.recoverExpiredLeases(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("recover Telegram inbox leases: %w", err)
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		s.workerLoop(ctx, scope.WorkContext(ctx), scope)
	}()
	s.pollLoop(ctx, scope)
	<-workerDone
	s.workers.Wait()
	return nil
}

func (s *Service) pollLoop(ctx context.Context, scope connectorruntime.RuntimeScope) {
	backoff := time.Second
	for ctx.Err() == nil {
		depth, depthErr := s.inboxDepth(ctx)
		if depthErr != nil {
			slog.Warn("Telegram inbox unavailable", "code", store.StoreErrorCodeOf(depthErr))
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		if depth >= s.cfg.MaxPending {
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		bindings, err := s.pollingBindings(ctx, scope)
		if err != nil {
			slog.Warn("Telegram bindings unavailable", "code", store.StoreErrorCodeOf(err))
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		if len(bindings) == 0 {
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		for _, binding := range bindings {
			client, err := s.clientFactory(ctx, binding)
			if err != nil {
				slog.Warn("Telegram credential unavailable", "binding_id", binding.ID, "code", credential.ErrorCode(err))
				continue
			}
			offset := bindingCursor(binding)
			updates, err := client.GetUpdates(ctx, offset, s.cfg.PollTimeoutSeconds)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				var apiErr *APIError
				if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
					backoff = apiErr.RetryAfter
				}
				slog.Warn("Telegram getUpdates failed", "binding_id", binding.ID, "code", connectorErrorCode(err), "retry_in", backoff)
				if !waitContext(ctx, backoff) {
					return
				}
				if backoff < 30*time.Second {
					backoff *= 2
				}
				continue
			}
			backoff = time.Second
			nextOffset := offset
			persisted := true
			for _, update := range updates {
				depth, depthErr := s.inboxDepth(ctx)
				if depthErr != nil || depth >= s.cfg.MaxPending {
					persisted = false
					break
				}
				if err := s.persistUpdate(ctx, binding, update); err != nil {
					persisted = false
					break
				}
				if update.UpdateID >= nextOffset {
					nextOffset = update.UpdateID + 1
				}
			}
			if persisted && nextOffset > offset {
				s.saveBindingCursor(ctx, binding, nextOffset)
			}
			s.signalWorker()
		}
	}
}

func (s *Service) persistUpdate(ctx context.Context, binding app.NotificationBinding, update Update) error {
	externalID := strconv.FormatInt(update.UpdateID, 10)
	if _, ok, err := s.store.FindChannelInboxUpdate(ctx, binding.ID, externalID); err != nil {
		return err
	} else if ok {
		return nil
	}
	raw, err := json.Marshal(update)
	if err != nil {
		return err
	}
	chatID, threadID := updateChat(update)
	saved, err := s.saveInbox(ctx, app.ChannelInboxUpdate{
		BindingID:  binding.ID,
		Channel:    "telegram",
		ExternalID: externalID,
		ChatKey:    binding.ID + ":" + strconv.FormatInt(chatID, 10) + ":" + strconv.FormatInt(threadID, 10),
		Payload:    raw,
		Status:     "pending",
	})
	if err != nil {
		return err
	}
	verified, ok, err := s.store.FindChannelInboxUpdate(ctx, binding.ID, externalID)
	if err != nil {
		return err
	}
	if !ok || verified.ID != saved.ID {
		return errors.New("Telegram inbox update could not be persisted")
	}
	return nil
}

func (s *Service) workerLoop(ctx, workCtx context.Context, scope connectorruntime.RuntimeScope) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s.dispatchPending(workCtx, scope)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

func (s *Service) dispatchPending(ctx context.Context, scope connectorruntime.RuntimeScope) {
	now := time.Now().UTC()
	if err := s.recoverExpiredLeases(ctx, now); err != nil {
		slog.Warn("Telegram inbox lease recovery failed", "code", store.StoreErrorCodeOf(err))
		return
	}
	pending, err := s.store.ListChannelInboxUpdates(ctx, "telegram", "pending", now, s.cfg.MaxPending)
	if err != nil {
		slog.Warn("Telegram pending inbox unavailable", "code", store.StoreErrorCodeOf(err))
		return
	}
	retrying, err := s.store.ListChannelInboxUpdates(ctx, "telegram", "retry_wait", now, s.cfg.MaxPending)
	if err != nil {
		slog.Warn("Telegram retry inbox unavailable", "code", store.StoreErrorCodeOf(err))
		return
	}
	ready := append(pending, retrying...)
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].CreatedAt.Before(ready[j].CreatedAt) })
	for _, inbox := range ready {
		binding, ok, err := s.store.GetNotificationBinding(ctx, inbox.BindingID)
		if err != nil {
			slog.Warn("Telegram binding unavailable while dispatching", "binding_id", inbox.BindingID, "code", store.StoreErrorCodeOf(err))
			return
		}
		if ok && !scope.AllowsOwner(binding.OwnerID) {
			continue
		}
		if ctx.Err() != nil || !s.reserveChat(inbox.ChatKey) {
			continue
		}
		select {
		case s.sem <- struct{}{}:
		default:
			s.releaseChat(inbox.ChatKey)
			return
		}
		inbox.Status = "processing"
		inbox.LastError = ""
		inbox.AvailableAt = now.Add(inboxLeaseDuration)
		inbox, err = s.saveInbox(ctx, inbox)
		if err != nil {
			<-s.sem
			s.releaseChat(inbox.ChatKey)
			slog.Warn("Telegram inbox lease could not be persisted", "inbox_id", inbox.ID, "code", store.StoreErrorCodeOf(err))
			return
		}
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			s.runReservedInbox(ctx, inbox)
		}()
	}
}

func (s *Service) runReservedInbox(ctx context.Context, inbox app.ChannelInboxUpdate) {
	defer func() {
		<-s.sem
		s.releaseChat(inbox.ChatKey)
		s.signalWorker()
	}()
	s.processInbox(ctx, inbox)
}

func (s *Service) processInbox(ctx context.Context, inbox app.ChannelInboxUpdate) {
	ctx, cancel := context.WithCancel(ctx)
	s.registerActive(inbox.BindingID, inbox.ID, cancel)
	defer func() {
		cancel()
		s.unregisterActive(inbox.BindingID, inbox.ID)
	}()
	binding, ok, bindingErr := s.store.GetNotificationBinding(ctx, inbox.BindingID)
	if bindingErr != nil {
		s.failInbox(ctx, inbox, NewConnectorError("binding_store_unavailable", true, bindingErr))
		return
	}
	if !ok || binding.Status == "revoked" || binding.Status == "expired" || binding.Status == "failed" {
		inbox.Status = "canceled"
		inbox.LastError = CodeBindingUnavailable
		inbox.Payload = nil
		if _, err := s.saveInbox(ctx, inbox); err != nil {
			slog.Warn("Telegram canceled inbox could not be persisted", "inbox_id", inbox.ID, "code", store.StoreErrorCodeOf(err))
		}
		return
	}
	var update Update
	if err := json.Unmarshal(inbox.Payload, &update); err != nil {
		s.failInbox(ctx, inbox, NewConnectorError("invalid_persisted_update", false, err))
		return
	}
	client, err := s.clientFactory(ctx, binding)
	if err == nil && binding.Status == "active" {
		claimedNow := false
		authorized := s.authorized(binding, update)
		if binding.ExternalUserID == "" || binding.ExternalChatID == "" {
			var claimErr error
			binding, authorized, claimedNow, claimErr = s.claimBinding(ctx, binding.ID, update)
			if claimErr != nil {
				err = claimErr
			}
		}
		if authorized {
			if claimedNow && isStartCommand(update) {
				message := update.Message
				_, err = client.SendMessage(ctx, message.Chat.ID, message.MessageThreadID, bindingConnectedMessage(message.From.LanguageCode), nil)
			} else {
				err = s.dispatcher.WithClient(client).HandleUpdate(ctx, binding, update)
			}
		}
	} else if err == nil {
		err = NewConnectorError(CodeBindingUnavailable, false, nil)
	}
	if err != nil {
		if ctx.Err() != nil {
			if current, ok, readErr := s.store.GetChannelInboxUpdate(ctx, inbox.ID); readErr == nil && ok && current.Status == "canceled" {
				return
			}
			// Preserve processing state and payload during service shutdown. The
			// lease recovery path will make the update runnable after restart.
			return
		}
		s.failInbox(ctx, inbox, err)
		return
	}
	if current, ok, readErr := s.store.GetChannelInboxUpdate(ctx, inbox.ID); readErr != nil {
		s.failInbox(ctx, inbox, NewConnectorError("inbox_store_unavailable", true, readErr))
		return
	} else if ok && current.Status == "canceled" {
		return
	}
	if currentBinding, ok, bindingErr := s.store.GetNotificationBinding(ctx, inbox.BindingID); bindingErr != nil {
		s.failInbox(ctx, inbox, NewConnectorError("binding_store_unavailable", true, bindingErr))
		return
	} else if !ok || currentBinding.Status == "revoked" {
		s.cancelInbox(ctx, inbox)
		return
	}
	inbox.Status = "completed"
	inbox.LastError = ""
	inbox.Payload = nil
	inbox.AvailableAt = time.Now().UTC()
	if _, err := s.saveInbox(ctx, inbox); err != nil {
		slog.Warn("Telegram completed inbox could not be persisted", "inbox_id", inbox.ID, "code", store.StoreErrorCodeOf(err))
	}
}

// CancelBinding stops active work and makes queued Telegram updates
// non-replayable before the associated credential is deleted.
func (s *Service) CancelBinding(bindingID string) {
	bindingID = strings.TrimSpace(bindingID)
	if bindingID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.currentLifecycleContext()), 30*time.Second)
	defer cancel()
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.activeCancels[bindingID]))
	for _, activeCancel := range s.activeCancels[bindingID] {
		cancels = append(cancels, activeCancel)
	}
	s.mu.Unlock()
	defer func() {
		for _, activeCancel := range cancels {
			activeCancel()
		}
	}()
	for _, status := range []string{"pending", "processing", "retry_wait"} {
		inboxes, err := s.store.ListChannelInboxUpdates(ctx, "telegram", status, time.Time{}, s.cfg.MaxPending+1)
		if err != nil {
			slog.Warn("Telegram inbox unavailable while canceling binding", "binding_id", bindingID, "code", store.StoreErrorCodeOf(err))
			return
		}
		for _, inbox := range inboxes {
			if inbox.BindingID == bindingID {
				s.cancelInbox(ctx, inbox)
			}
		}
	}
}

func (s *Service) cancelInbox(ctx context.Context, inbox app.ChannelInboxUpdate) {
	inbox.Status = "canceled"
	inbox.LastError = CodeBindingUnavailable
	inbox.Payload = nil
	inbox.AvailableAt = time.Now().UTC()
	if _, err := s.saveInbox(ctx, inbox); err != nil {
		slog.Warn("Telegram inbox cancellation could not be persisted", "inbox_id", inbox.ID, "code", store.StoreErrorCodeOf(err))
	}
}

func (s *Service) registerActive(bindingID, inboxID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeCancels[bindingID] == nil {
		s.activeCancels[bindingID] = map[string]context.CancelFunc{}
	}
	s.activeCancels[bindingID][inboxID] = cancel
}

func (s *Service) unregisterActive(bindingID, inboxID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.activeCancels[bindingID], inboxID)
	if len(s.activeCancels[bindingID]) == 0 {
		delete(s.activeCancels, bindingID)
	}
}

func (s *Service) failInbox(ctx context.Context, inbox app.ChannelInboxUpdate, err error) {
	inbox.Attempts++
	inbox.LastError = connectorErrorCode(err)
	if isRetryable(err) && inbox.Attempts < maxInboxAttempts {
		inbox.Status = "retry_wait"
		inbox.AvailableAt = time.Now().UTC().Add(time.Duration(1<<inbox.Attempts) * time.Second)
	} else {
		inbox.Status = "failed"
		if isRetryable(err) {
			inbox.LastError = CodeRetryExhausted
		}
	}
	if _, persistErr := s.saveInbox(ctx, inbox); persistErr != nil {
		slog.Warn("Telegram inbox failure could not be persisted", "inbox_id", inbox.ID, "code", store.StoreErrorCodeOf(persistErr))
	}
}

func (s *Service) claimBinding(ctx context.Context, bindingID string, update Update) (app.NotificationBinding, bool, bool, error) {
	message := update.Message
	if message == nil || message.From == nil || message.Chat.Type != "private" {
		return app.NotificationBinding{}, false, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok, err := s.store.GetNotificationBinding(ctx, bindingID)
	if err != nil {
		return app.NotificationBinding{}, false, false, err
	}
	if !ok || binding.Status != "active" {
		return app.NotificationBinding{}, false, false, nil
	}
	if binding.ExternalUserID != "" || binding.ExternalChatID != "" {
		return binding, s.authorized(binding, update), false, nil
	}
	if message.Date > 0 && !binding.CreatedAt.IsZero() && time.Unix(message.Date, 0).Before(binding.CreatedAt.Add(-30*time.Second)) {
		return binding, false, false, nil
	}
	replacement := binding
	replacement.Status = "active"
	replacement.ExternalUserID = strconv.FormatInt(message.From.ID, 10)
	replacement.ExternalChatID = strconv.FormatInt(message.Chat.ID, 10)
	replacement.ExternalThreadID = threadIDString(message.MessageThreadID)
	replacement.ContextToken = replacement.ExternalChatID
	replacement.ProviderState = ""
	replacement.QRCodeURL = ""
	replacement.QRCodeImage = ""
	replacement.ExpiresAt = nil
	replacement.LastError = ""
	hasDefault, err := hasDefaultActiveBinding(ctx, s.store, binding.ID)
	if err != nil {
		return app.NotificationBinding{}, false, false, err
	}
	if !hasDefault {
		replacement.DefaultForChannel = true
	}
	binding, err = s.store.UpdateNotificationBinding(ctx, store.NewNotificationBindingUpdate(binding, replacement))
	if err != nil {
		return app.NotificationBinding{}, false, false, err
	}
	recordAudit(ctx, s.store, app.AuditEvent{
		Actor:   "telegram",
		Type:    "telegram.binding.claimed",
		Summary: "Claimed Telegram binding from a private chat",
		Fields:  map[string]any{"binding_id": binding.ID},
	})
	return binding, true, true, nil
}

func bindingConnectedMessage(languageCode string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(languageCode)), "zh") {
		return "Telegram Bot 已连接到 SparkClaw。"
	}
	return "Telegram Bot is connected to SparkClaw."
}

func (s *Service) authorized(binding app.NotificationBinding, update Update) bool {
	chatID, threadID := updateChat(update)
	userID := updateUserID(update)
	if chatID == 0 || userID == 0 {
		return false
	}
	if message := update.Message; message != nil && message.Chat.Type != "private" {
		return false
	}
	if query := update.CallbackQuery; query != nil && query.Message != nil && query.Message.Chat.Type != "private" {
		return false
	}
	return binding.ExternalUserID == strconv.FormatInt(userID, 10) &&
		binding.ExternalChatID == strconv.FormatInt(chatID, 10) &&
		binding.ExternalThreadID == threadIDString(threadID)
}

func (s *Service) pollingBindings(ctx context.Context, scope connectorruntime.RuntimeScope) ([]app.NotificationBinding, error) {
	bindings, err := s.store.ListNotificationBindings(ctx, "telegram", "active")
	if err != nil {
		return nil, err
	}
	eligible := make([]app.NotificationBinding, 0, len(bindings))
	for _, binding := range bindings {
		if scope.AllowsOwner(binding.OwnerID) {
			eligible = append(eligible, binding)
		}
	}
	return eligible, nil
}

func (s *Service) vaultClient(ctx context.Context, binding app.NotificationBinding) (BotAPI, error) {
	if s.vault == nil {
		return nil, &credential.Error{Code: credential.CodeKeyUnavailable}
	}
	token, err := s.vault.Open(ctx, binding.CredentialRef)
	if err != nil {
		return nil, err
	}
	defer clear(token)
	baseURL := strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(s.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return nil, errors.New("Telegram base URL is unavailable")
	}
	return NewClient(baseURL, string(token), nil), nil
}

func (s *Service) recoverExpiredLeases(ctx context.Context, now time.Time) error {
	updates, err := s.store.ListChannelInboxUpdates(ctx, "telegram", "processing", now, s.cfg.MaxPending)
	if err != nil {
		return err
	}
	for _, update := range updates {
		update.Status = "pending"
		update.AvailableAt = now
		update.LastError = ""
		if _, err := s.saveInbox(ctx, update); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) inboxDepth(ctx context.Context) (int, error) {
	depth := 0
	for _, status := range []string{"pending", "processing", "retry_wait"} {
		updates, err := s.store.ListChannelInboxUpdates(ctx, "telegram", status, time.Time{}, s.cfg.MaxPending+1)
		if err != nil {
			return 0, err
		}
		depth += len(updates)
	}
	return depth, nil
}

func (s *Service) saveInbox(ctx context.Context, update app.ChannelInboxUpdate) (app.ChannelInboxUpdate, error) {
	saved, err := s.store.SaveChannelInboxUpdate(ctx, update)
	return store.ReconcileChannelInboxUpdateWrite(ctx, s.store, saved, err)
}

func (s *Service) currentLifecycleContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lifecycleCtx != nil {
		return s.lifecycleCtx
	}
	return context.TODO()
}

func (s *Service) saveBindingCursor(ctx context.Context, binding app.NotificationBinding, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		current, ok, err := s.store.GetNotificationBinding(ctx, binding.ID)
		if err != nil {
			slog.Warn("Telegram binding cursor read failed", "binding_id", binding.ID, "code", store.StoreErrorCodeOf(err))
			return
		}
		if !ok || bindingCursor(current) >= offset {
			return
		}
		replacement := current
		replacement.ProviderCursor = strconv.FormatInt(offset, 10)
		if _, err := s.store.UpdateNotificationBinding(ctx, store.NewNotificationBindingUpdate(current, replacement)); err == nil {
			return
		} else if store.StoreErrorCodeOf(err) != store.StoreErrorConflict {
			slog.Warn("Telegram binding cursor update failed", "binding_id", binding.ID, "code", store.StoreErrorCodeOf(err))
			return
		}
	}
	slog.Warn("Telegram binding cursor update conflicted repeatedly", "binding_id", binding.ID)
}

func (s *Service) signalWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) reserveChat(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busyChats[key] {
		return false
	}
	s.busyChats[key] = true
	return true
}

func (s *Service) releaseChat(key string) {
	s.mu.Lock()
	delete(s.busyChats, key)
	s.mu.Unlock()
}

func bindingCursor(binding app.NotificationBinding) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(binding.ProviderCursor), 10, 64)
	return value
}

func hasDefaultActiveBinding(ctx context.Context, st store.ConnectorRepository, exceptID string) (bool, error) {
	bindings, err := st.ListNotificationBindings(ctx, "telegram", "active")
	if err != nil {
		return false, err
	}
	for _, binding := range bindings {
		if binding.ID != exceptID && binding.DefaultForChannel {
			return true, nil
		}
	}
	return false, nil
}

func isStartCommand(update Update) bool {
	if update.Message == nil {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(update.Message.Text))
	return len(fields) > 0 && strings.ToLower(strings.SplitN(fields[0], "@", 2)[0]) == "/start"
}

func updateChat(update Update) (int64, int64) {
	if update.Message != nil {
		return update.Message.Chat.ID, update.Message.MessageThreadID
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message != nil {
		return update.CallbackQuery.Message.Chat.ID, update.CallbackQuery.Message.MessageThreadID
	}
	return 0, 0
}

func updateUserID(update Update) int64 {
	if update.Message != nil && update.Message.From != nil {
		return update.Message.From.ID
	}
	if update.CallbackQuery != nil {
		return update.CallbackQuery.From.ID
	}
	return 0
}

func threadIDString(threadID int64) string {
	if threadID == 0 {
		return ""
	}
	return strconv.FormatInt(threadID, 10)
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
