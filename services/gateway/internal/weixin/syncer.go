package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

const (
	// defaultDispatchWorkers bounds how many bindings can run agent turns at
	// the same time; messages within one binding are always handled in order.
	defaultDispatchWorkers = 4
	// maxDispatchAttempts is how many polls a failing inbound message is
	// retried for before the cursor is advanced past it.
	maxDispatchAttempts = 3
	syncInterval        = 15 * time.Second
)

type Syncer struct {
	store       SyncerRepository
	dispatcher  *Dispatcher
	client      *http.Client
	cfg         config.Config
	media       *MediaAdapter
	credentials credential.CredentialVault

	slots chan struct{}
	wg    sync.WaitGroup

	mu   sync.Mutex
	busy map[string]bool
}

type SyncerRepository interface {
	store.ConnectorRepository
	store.SessionRepository
	store.ArtifactMetadataRepository
	store.AuditRepository
	store.DeliveryRecordRepository
	store.ExternalChatRepository
	store.MCPRepository
}

type updateTextItem struct {
	Text string `json:"text"`
}

type updateItem struct {
	Type      int            `json:"type"`
	TextItem  updateTextItem `json:"text_item"`
	ImageItem imageItem      `json:"image_item"`
	FileItem  fileItem       `json:"file_item"`
}

func NewSyncer(st SyncerRepository) *Syncer {
	return &Syncer{
		store:  st,
		client: &http.Client{Timeout: 40 * time.Second},
		slots:  make(chan struct{}, defaultDispatchWorkers),
		busy:   map[string]bool{},
	}
}

func (s *Syncer) WithDispatcher(dispatcher *Dispatcher) *Syncer {
	s.dispatcher = dispatcher
	return s
}

func (s *Syncer) WithCredentialVault(vault credential.CredentialVault) *Syncer {
	s.credentials = vault
	return s
}

func (s *Syncer) WithConfig(cfg config.Config) *Syncer {
	s.cfg = cfg
	s.media = NewMediaAdapter(cfg, s.store)
	return s
}

func (s *Syncer) Run(ctx context.Context, scope connectorruntime.RuntimeScope) error {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		s.tick(ctx, scope.WorkContext(ctx), scope)
		select {
		case <-ctx.Done():
			s.Wait()
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Syncer) Tick(ctx context.Context, scope connectorruntime.RuntimeScope) {
	s.tick(ctx, ctx, scope)
}

func (s *Syncer) tick(ctx, workCtx context.Context, scope connectorruntime.RuntimeScope) {
	bindings, err := s.store.ListNotificationBindings(ctx, "weixin", "active")
	if err != nil {
		slog.Warn("weixin bindings unavailable", "code", store.StoreErrorCodeOf(err))
		return
	}
	for _, binding := range bindings {
		if !scope.AllowsOwner(binding.OwnerID) {
			continue
		}
		if !strings.Contains(strings.ToLower(binding.Provider), "openclaw-weixin") {
			continue
		}
		// While a binding's previous batch is still being dispatched its
		// cursor has not advanced, so polling again would only requeue the
		// same messages.
		if s.isBusy(binding.ID) {
			continue
		}
		if err := s.syncBinding(ctx, workCtx, scope, binding); err != nil {
			replacement := binding
			replacement.LastError = stableWeixinSyncError(err)
			if _, updateErr := s.store.UpdateNotificationBinding(ctx, store.NewNotificationBindingUpdate(binding, replacement)); updateErr != nil {
				slog.Warn("weixin binding error state could not be persisted", "binding_id", binding.ID, "code", store.StoreErrorCodeOf(updateErr))
			}
			slog.Warn("weixin context sync failed", "binding_id", binding.ID, "error", err)
		}
	}
}

func stableWeixinSyncError(err error) string {
	var credentialErr *credential.Error
	if errors.As(err, &credentialErr) {
		return credentialErr.Error()
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		if code := strings.TrimSpace(coded.ErrorCode()); code != "" {
			return code
		}
	}
	return "weixin_sync_failed"
}

// Wait blocks until every in-flight dispatch batch has finished.
func (s *Syncer) Wait() {
	s.wg.Wait()
}

func (s *Syncer) isBusy(bindingID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy[bindingID]
}

// inboundEnvelope is one provider message queued for dispatch.
type inboundEnvelope struct {
	FromUserID   string
	ContextToken string
	ExternalID   string
	Items        []updateItem
	CreatedAt    time.Time
}

// inboundBatch is the dispatchable result of one getupdates poll for a
// binding. Cursor is only persisted once every message dispatched.
type inboundBatch struct {
	Binding app.NotificationBinding
	Cursor  string
	Msgs    []inboundEnvelope
}

func (s *Syncer) syncBinding(ctx, workCtx context.Context, scope connectorruntime.RuntimeScope, binding app.NotificationBinding) error {
	previous := binding
	baseURL := strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
	if baseURL == "" {
		return nil
	}
	if s.credentials == nil {
		return &credential.Error{Code: credential.CodeKeyUnavailable}
	}
	secret, err := s.credentials.Open(workCtx, strings.TrimSpace(binding.CredentialRef))
	if err != nil {
		return err
	}
	defer clearSecret(secret)
	payload := map[string]any{
		"get_updates_buf": binding.ProviderCursor,
		"base_info": map[string]any{
			"channel_version": weixinproto.ChannelVersion,
			"bot_agent":       "SparkClaw",
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ilink/bot/getupdates", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	weixinproto.SetHeaders(req, strings.TrimSpace(string(secret)))
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("getupdates returned HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Ret           int    `json:"ret"`
		ErrCode       int    `json:"errcode"`
		ErrMsg        string `json:"errmsg"`
		GetUpdatesBuf string `json:"get_updates_buf"`
		Msgs          []struct {
			ID           string       `json:"id"`
			MsgID        string       `json:"msg_id"`
			ClientID     string       `json:"client_id"`
			FromUserID   string       `json:"from_user_id"`
			ContextToken string       `json:"context_token"`
			CreateTime   int64        `json:"create_time"`
			ItemList     []updateItem `json:"item_list"`
		} `json:"msgs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	if decoded.Ret != 0 || decoded.ErrCode != 0 {
		message := decoded.ErrMsg
		if message == "" {
			message = fmt.Sprintf("getupdates ret=%d errcode=%d", decoded.Ret, decoded.ErrCode)
		}
		return fmt.Errorf("%s", message)
	}
	changed := false
	envelopes := []inboundEnvelope{}
	for _, msg := range decoded.Msgs {
		contextToken := strings.TrimSpace(msg.ContextToken)
		if contextToken == "" {
			continue
		}
		if binding.ContextToken != contextToken {
			binding.ContextToken = contextToken
			binding.LastError = ""
			changed = true
		}
		if s.dispatcher != nil {
			envelopes = append(envelopes, inboundEnvelope{
				FromUserID:   strings.TrimSpace(msg.FromUserID),
				ContextToken: contextToken,
				ExternalID:   firstNonEmpty(msg.ID, msg.MsgID, msg.ClientID),
				Items:        msg.ItemList,
				CreatedAt:    unixTime(msg.CreateTime),
			})
		}
	}
	if len(envelopes) == 0 {
		// Nothing to dispatch: safe to advance the cursor right away.
		if strings.TrimSpace(decoded.GetUpdatesBuf) != "" && decoded.GetUpdatesBuf != binding.ProviderCursor {
			binding.ProviderCursor = decoded.GetUpdatesBuf
			changed = true
		}
		if changed {
			if _, err := s.store.UpdateNotificationBinding(ctx, store.NewNotificationBindingUpdate(previous, binding)); err != nil {
				return err
			}
			slog.Info("weixin context synced", "binding_id", binding.ID, "has_context_token", binding.ContextToken != "")
		}
		return nil
	}
	if changed {
		binding, err = s.store.UpdateNotificationBinding(ctx, store.NewNotificationBindingUpdate(previous, binding))
		if err != nil {
			return err
		}
		slog.Info("weixin context synced", "binding_id", binding.ID, "has_context_token", binding.ContextToken != "")
	}
	s.enqueueBatch(workCtx, scope, inboundBatch{Binding: binding, Cursor: decoded.GetUpdatesBuf, Msgs: envelopes})
	return nil
}

func clearSecret(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

// enqueueBatch hands a binding's inbound messages to the bounded dispatch
// pool. One batch per binding runs at a time (Tick skips busy bindings), so
// message order within a binding is preserved while distinct bindings are
// handled in parallel.
func (s *Syncer) enqueueBatch(ctx context.Context, scope connectorruntime.RuntimeScope, batch inboundBatch) {
	s.mu.Lock()
	if s.busy[batch.Binding.ID] {
		s.mu.Unlock()
		return
	}
	s.busy[batch.Binding.ID] = true
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.busy, batch.Binding.ID)
			s.mu.Unlock()
		}()
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			return
		}
		s.processBatch(ctx, scope, batch)
	}()
}

func (s *Syncer) processBatch(ctx context.Context, scope connectorruntime.RuntimeScope, batch inboundBatch) {
	binding := batch.Binding
	for _, msg := range batch.Msgs {
		if ctx.Err() != nil {
			return
		}
		if !scope.AllowsOwner(binding.OwnerID) {
			return
		}
		inbound := InboundMessage{
			Binding:        binding,
			FromUserID:     msg.FromUserID,
			ContextToken:   msg.ContextToken,
			ExternalID:     msg.ExternalID,
			ProviderCursor: batch.Cursor,
			CreatedAt:      msg.CreatedAt,
		}
		if strings.TrimSpace(inbound.ExternalID) == "" {
			inbound.ExternalID = stableInboundID(inbound)
		}
		chatSession, err := s.dispatcher.ensureChatSession(ctx, inbound)
		if err != nil {
			slog.Warn("weixin owner profile unavailable; will retry", "binding_id", binding.ID, "external_id", msg.ExternalID, "error", err)
			return
		}
		// Admitted-source resolution: the poll loop only runs while the
		// connector is enabled, and a disable mid-batch must not strand
		// already-fetched messages.
		endpoint, endpointErr := messagecontrol.NewEndpointRegistry(s.store).GetAdmittedSource(ctx, app.EndpointID(chatSession.ID))
		if endpointErr != nil {
			slog.Warn("weixin inbound source endpoint rejected", "binding_id", binding.ID, "code", messagecontrol.CodeBindingUnavailable)
			continue
		}
		receives := messagecontrol.NewReceiveLifecycle(s.store)
		receive, fresh, receiveErr := receives.Begin(ctx, endpoint, inbound.ExternalID)
		if receiveErr != nil {
			slog.Warn("weixin receive state unavailable; will retry", "binding_id", binding.ID, "external_id", msg.ExternalID, "code", store.StoreErrorCodeOf(receiveErr))
			return
		}
		if !fresh {
			existing, ok, err := s.store.FindExternalChatMessageByExternalID(ctx, chatSession.ID, inbound.ExternalID)
			if err != nil {
				slog.Warn("weixin external chat state unavailable; will retry", "binding_id", binding.ID, "external_id", msg.ExternalID, "code", store.StoreErrorCodeOf(err))
				return
			}
			if !ok || (existing.Status != "failed" && existing.Status != "delivery_failed") {
				continue
			}
		}
		inbound.ReceiveRecord, receiveErr = receives.Advance(ctx, receive, "authorized", "", "")
		if receiveErr != nil {
			slog.Warn("weixin receive state unavailable; will retry", "binding_id", binding.ID, "external_id", msg.ExternalID, "code", store.StoreErrorCodeOf(receiveErr))
			return
		}
		inbound.Text = extractInboundText(msg.Items)
		inbound.Attachments = s.downloadInboundAttachments(ctx, binding, msg.Items, chatSession.LinkedSessionID, msg.ExternalID)
		if inbound.Text == "" && len(inbound.Attachments) == 0 {
			continue
		}
		if err := s.dispatcher.HandleInbound(ctx, inbound); err != nil {
			if delivery.IsBlocked(err) {
				// Retrying cannot succeed until an operator intervenes and
				// the dispatcher already recorded the reason on the message,
				// so skip it instead of holding the binding's cursor back.
				slog.Warn("weixin inbound delivery blocked; advancing past message",
					"binding_id", binding.ID, "external_id", msg.ExternalID, "error", err)
				continue
			}
			attempts, persistErr := s.recordDispatchAttempt(ctx, chatSession.ID, binding.ID, inbound.ExternalID)
			if persistErr != nil {
				slog.Warn("weixin dispatch retry state unavailable; will retry", "binding_id", binding.ID, "external_id", msg.ExternalID, "code", store.StoreErrorCodeOf(persistErr))
				return
			}
			if attempts < maxDispatchAttempts {
				// Keep the old cursor so the provider redelivers this
				// message (and everything after it) on the next poll.
				slog.Warn("weixin inbound dispatch failed; will retry",
					"binding_id", binding.ID, "external_id", msg.ExternalID, "attempts", attempts, "error", err)
				return
			}
			slog.Warn("weixin inbound dropped after repeated dispatch failures",
				"binding_id", binding.ID, "external_id", msg.ExternalID, "attempts", attempts, "error", err)
		}
	}
	s.advanceCursor(ctx, binding.ID, batch.Cursor)
}

func (s *Syncer) advanceCursor(ctx context.Context, bindingID, cursor string) {
	if strings.TrimSpace(cursor) == "" {
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		binding, ok, err := s.store.GetNotificationBinding(ctx, bindingID)
		if err != nil {
			slog.Warn("weixin binding cursor read failed", "binding_id", bindingID, "code", store.StoreErrorCodeOf(err))
			return
		}
		if !ok || binding.ProviderCursor == cursor {
			return
		}
		replacement := binding
		replacement.ProviderCursor = cursor
		if _, err := s.store.UpdateNotificationBinding(ctx, store.NewNotificationBindingUpdate(binding, replacement)); err == nil {
			return
		} else if store.StoreErrorCodeOf(err) != store.StoreErrorConflict {
			slog.Warn("weixin binding cursor update failed", "binding_id", bindingID, "code", store.StoreErrorCodeOf(err))
			return
		}
	}
	slog.Warn("weixin binding cursor update conflicted repeatedly", "binding_id", bindingID)
}

// recordDispatchAttempt persists a failed dispatch on the inbound message
// record: the retry budget survives gateway restarts and lives and dies with
// the message itself, so nothing lingers in syncer memory for bindings that
// are later revoked. A successful dispatch resets the count when the
// dispatcher finalizes the record.
func (s *Syncer) recordDispatchAttempt(ctx context.Context, chatSessionID, bindingID, externalID string) (int, error) {
	record, ok, err := s.store.FindExternalChatMessageByExternalID(ctx, chatSessionID, externalID)
	if err != nil {
		return 0, err
	}
	if !ok {
		// The dispatch failed before anything was persisted, so there is no
		// durable place for a budget; treat the message as exhausted rather
		// than blocking the binding's cursor indefinitely.
		slog.Warn("weixin inbound failed without a persisted record; dropping",
			"binding_id", bindingID, "external_id", externalID)
		return maxDispatchAttempts, nil
	}
	record.DispatchAttempts++
	record, err = s.store.SaveExternalChatMessage(ctx, record)
	if err != nil {
		return 0, err
	}
	return record.DispatchAttempts, nil
}

func (s *Syncer) downloadInboundAttachments(ctx context.Context, binding app.NotificationBinding, items []updateItem, sessionID, nameSeed string) []app.MessageAttachment {
	if s.media == nil {
		return nil
	}
	attachments := []app.MessageAttachment{}
	for idx, item := range items {
		seed := nameSeed
		if seed == "" {
			seed = app.NewID("wxmsg")
		}
		if idx > 0 {
			seed = fmt.Sprintf("%s-%d", seed, idx)
		}
		var (
			attachment app.MessageAttachment
			err        error
		)
		switch item.Type {
		case 2:
			attachment, err = s.media.DownloadInboundImage(ctx, binding, item.ImageItem, sessionID, seed)
		case 4:
			attachment, err = s.media.DownloadInboundFile(ctx, binding, item.FileItem, sessionID, seed)
		default:
			continue
		}
		if err != nil {
			slog.Warn("weixin inbound attachment download failed", "binding_id", binding.ID, "item_type", item.Type, "error", err)
			continue
		}
		attachments = append(attachments, attachment)
		if len(attachments) >= 5 {
			break
		}
	}
	return attachments
}

func extractInboundText(items []updateItem) string {
	for _, item := range items {
		if item.Type != 0 && item.Type != 1 {
			continue
		}
		if text := strings.TrimSpace(item.TextItem.Text); text != "" {
			return text
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}
