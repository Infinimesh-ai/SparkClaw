package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
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
	busyChats     map[string]bool
	activeCancels map[string]map[string]context.CancelFunc
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

func (s *Service) Run(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}
	if s.store == nil || s.dispatcher == nil || s.clientFactory == nil {
		return errors.New("Telegram service dependencies are incomplete")
	}
	s.migrateLegacyDirectBindings()
	s.recoverExpiredLeases(time.Now().UTC())
	go s.workerLoop(ctx)
	s.pollLoop(ctx)
	return nil
}

func (s *Service) pollLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if s.inboxDepth() >= s.cfg.MaxPending {
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		bindings := s.pollingBindings()
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
				if s.inboxDepth() >= s.cfg.MaxPending {
					persisted = false
					break
				}
				if err := s.persistUpdate(binding, update); err != nil {
					persisted = false
					break
				}
				if update.UpdateID >= nextOffset {
					nextOffset = update.UpdateID + 1
				}
			}
			if persisted && nextOffset > offset {
				s.saveBindingCursor(binding, nextOffset)
			}
			s.signalWorker()
		}
	}
}

func (s *Service) persistUpdate(binding app.NotificationBinding, update Update) error {
	externalID := strconv.FormatInt(update.UpdateID, 10)
	if _, ok := s.store.FindChannelInboxUpdate(binding.ID, externalID); ok {
		return nil
	}
	raw, err := json.Marshal(update)
	if err != nil {
		return err
	}
	chatID, threadID := updateChat(update)
	saved := s.store.SaveChannelInboxUpdate(app.ChannelInboxUpdate{
		BindingID:  binding.ID,
		Channel:    "telegram",
		ExternalID: externalID,
		ChatKey:    binding.ID + ":" + strconv.FormatInt(chatID, 10) + ":" + strconv.FormatInt(threadID, 10),
		Payload:    raw,
		Status:     "pending",
	})
	verified, ok := s.store.FindChannelInboxUpdate(binding.ID, externalID)
	if !ok || verified.ID != saved.ID {
		return errors.New("Telegram inbox update could not be persisted")
	}
	return nil
}

func (s *Service) workerLoop(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.dispatchPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

func (s *Service) dispatchPending(ctx context.Context) {
	now := time.Now().UTC()
	s.recoverExpiredLeases(now)
	ready := append(s.store.ListChannelInboxUpdates("telegram", "pending", now, s.cfg.MaxPending),
		s.store.ListChannelInboxUpdates("telegram", "retry_wait", now, s.cfg.MaxPending)...)
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].CreatedAt.Before(ready[j].CreatedAt) })
	for _, inbox := range ready {
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
		inbox = s.store.SaveChannelInboxUpdate(inbox)
		go s.runReservedInbox(ctx, inbox)
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
	binding, ok := s.store.GetNotificationBinding(inbox.BindingID)
	if !ok || binding.Status == "revoked" || binding.Status == "expired" || binding.Status == "failed" {
		inbox.Status = "canceled"
		inbox.LastError = CodeBindingUnavailable
		inbox.Payload = nil
		s.store.SaveChannelInboxUpdate(inbox)
		return
	}
	var update Update
	if err := json.Unmarshal(inbox.Payload, &update); err != nil {
		s.failInbox(inbox, NewConnectorError("invalid_persisted_update", false, err))
		return
	}
	client, err := s.clientFactory(ctx, binding)
	if err == nil && binding.Status == "active" {
		claimedNow := false
		authorized := s.authorized(binding, update)
		if binding.ExternalUserID == "" || binding.ExternalChatID == "" {
			binding, authorized, claimedNow = s.claimBinding(binding.ID, update)
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
			if current, ok := s.store.GetChannelInboxUpdate(inbox.ID); ok && current.Status == "canceled" {
				return
			}
			// Preserve processing state and payload during service shutdown. The
			// lease recovery path will make the update runnable after restart.
			return
		}
		s.failInbox(inbox, err)
		return
	}
	if current, ok := s.store.GetChannelInboxUpdate(inbox.ID); ok && current.Status == "canceled" {
		return
	}
	if currentBinding, ok := s.store.GetNotificationBinding(inbox.BindingID); !ok || currentBinding.Status == "revoked" {
		s.cancelInbox(inbox)
		return
	}
	inbox.Status = "completed"
	inbox.LastError = ""
	inbox.Payload = nil
	inbox.AvailableAt = time.Now().UTC()
	s.store.SaveChannelInboxUpdate(inbox)
}

// CancelBinding stops active work and makes queued Telegram updates
// non-replayable before the associated credential is deleted.
func (s *Service) CancelBinding(bindingID string) {
	bindingID = strings.TrimSpace(bindingID)
	if bindingID == "" {
		return
	}
	for _, status := range []string{"pending", "processing", "retry_wait"} {
		for _, inbox := range s.store.ListChannelInboxUpdates("telegram", status, time.Time{}, s.cfg.MaxPending+1) {
			if inbox.BindingID == bindingID {
				s.cancelInbox(inbox)
			}
		}
	}
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.activeCancels[bindingID]))
	for _, cancel := range s.activeCancels[bindingID] {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Service) cancelInbox(inbox app.ChannelInboxUpdate) {
	inbox.Status = "canceled"
	inbox.LastError = CodeBindingUnavailable
	inbox.Payload = nil
	inbox.AvailableAt = time.Now().UTC()
	s.store.SaveChannelInboxUpdate(inbox)
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

func (s *Service) failInbox(inbox app.ChannelInboxUpdate, err error) {
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
	s.store.SaveChannelInboxUpdate(inbox)
}

func (s *Service) claimBinding(bindingID string, update Update) (app.NotificationBinding, bool, bool) {
	message := update.Message
	if message == nil || message.From == nil || message.Chat.Type != "private" {
		return app.NotificationBinding{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.store.GetNotificationBinding(bindingID)
	if !ok || binding.Status != "active" {
		return app.NotificationBinding{}, false, false
	}
	if binding.ExternalUserID != "" || binding.ExternalChatID != "" {
		return binding, s.authorized(binding, update), false
	}
	if message.Date > 0 && !binding.CreatedAt.IsZero() && time.Unix(message.Date, 0).Before(binding.CreatedAt.Add(-30*time.Second)) {
		return binding, false, false
	}
	now := time.Now().UTC()
	binding.Status = "active"
	binding.ExternalUserID = strconv.FormatInt(message.From.ID, 10)
	binding.ExternalChatID = strconv.FormatInt(message.Chat.ID, 10)
	binding.ExternalThreadID = threadIDString(message.MessageThreadID)
	binding.ContextToken = binding.ExternalChatID
	binding.ProviderState = ""
	binding.QRCodeURL = ""
	binding.QRCodeImage = ""
	binding.ExpiresAt = nil
	binding.LastError = ""
	binding.UpdatedAt = now
	if !hasDefaultActiveBinding(s.store, binding.ID) {
		binding.DefaultForChannel = true
	}
	binding = s.store.SaveNotificationBinding(binding)
	s.store.AddAudit(app.AuditEvent{
		Actor:   "telegram",
		Type:    "telegram.binding.claimed",
		Summary: "Claimed Telegram binding from a private chat",
		Fields:  map[string]any{"binding_id": binding.ID},
	})
	return binding, true, true
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

func (s *Service) pollingBindings() []app.NotificationBinding {
	return s.store.ListNotificationBindings("telegram", "active")
}

func (s *Service) migrateLegacyDirectBindings() {
	now := time.Now().UTC()
	for _, status := range []string{"waiting_confirm", "expired"} {
		for _, binding := range s.store.ListNotificationBindings("telegram", status) {
			if strings.TrimSpace(binding.CredentialRef) == "" {
				continue
			}
			binding.Status = "active"
			binding.ProviderState = ""
			binding.QRCodeURL = ""
			binding.QRCodeImage = ""
			binding.ExpiresAt = nil
			binding.LastError = ""
			if binding.ExternalUserID == "" || binding.ExternalChatID == "" {
				binding.DefaultForChannel = false
			}
			binding.UpdatedAt = now
			s.store.SaveNotificationBinding(binding)
			s.store.AddAudit(app.AuditEvent{
				Actor:   "system",
				Type:    "telegram.binding.direct_mode_migrated",
				Summary: "Migrated Telegram binding to direct chat claim mode",
				Fields:  map[string]any{"binding_id": binding.ID, "previous_status": status},
			})
		}
	}
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

func (s *Service) recoverExpiredLeases(now time.Time) {
	for _, update := range s.store.ListChannelInboxUpdates("telegram", "processing", now, s.cfg.MaxPending) {
		update.Status = "pending"
		update.AvailableAt = now
		update.LastError = ""
		s.store.SaveChannelInboxUpdate(update)
	}
}

func (s *Service) inboxDepth() int {
	depth := 0
	for _, status := range []string{"pending", "processing", "retry_wait"} {
		depth += len(s.store.ListChannelInboxUpdates("telegram", status, time.Time{}, s.cfg.MaxPending+1))
	}
	return depth
}

func (s *Service) saveBindingCursor(binding app.NotificationBinding, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.store.GetNotificationBinding(binding.ID)
	if !ok || bindingCursor(current) >= offset {
		return
	}
	current.ProviderCursor = strconv.FormatInt(offset, 10)
	current.UpdatedAt = time.Now().UTC()
	s.store.SaveNotificationBinding(current)
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

func hasDefaultActiveBinding(st store.Store, exceptID string) bool {
	for _, binding := range st.ListNotificationBindings("telegram", "active") {
		if binding.ID != exceptID && binding.DefaultForChannel {
			return true
		}
	}
	return false
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
