package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
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
	store      store.Store
	dispatcher *Dispatcher
	client     *http.Client
	cfg        config.Config
	media      *MediaAdapter

	slots chan struct{}
	wg    sync.WaitGroup

	mu   sync.Mutex
	busy map[string]bool
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

func NewSyncer(st store.Store) *Syncer {
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
	for _, binding := range s.store.ListNotificationBindings("weixin", "active") {
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
			binding.LastError = err.Error()
			binding.UpdatedAt = time.Now().UTC()
			s.store.SaveNotificationBinding(binding)
			slog.Warn("weixin context sync failed", "binding_id", binding.ID, "error", err)
		}
	}
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
	baseURL := strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
	if baseURL == "" {
		return nil
	}
	secret, ok := s.store.GetCredentialSecret(strings.TrimSpace(binding.CredentialRef))
	if !ok || strings.TrimSpace(secret.Value) == "" {
		return nil
	}
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
	weixinproto.SetHeaders(req, secret.Value)
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
			binding.UpdatedAt = time.Now().UTC()
			s.store.SaveNotificationBinding(binding)
			slog.Info("weixin context synced", "binding_id", binding.ID, "has_context_token", binding.ContextToken != "")
		}
		return nil
	}
	if changed {
		binding.UpdatedAt = time.Now().UTC()
		binding = s.store.SaveNotificationBinding(binding)
		slog.Info("weixin context synced", "binding_id", binding.ID, "has_context_token", binding.ContextToken != "")
	}
	s.enqueueBatch(workCtx, scope, inboundBatch{Binding: binding, Cursor: decoded.GetUpdatesBuf, Msgs: envelopes})
	return nil
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
		chatSession := s.dispatcher.ensureChatSession(inbound)
		endpoint, endpointErr := messagecontrol.NewEndpointRegistry(s.store).Get(ctx, app.EndpointID(chatSession.ID))
		if endpointErr != nil {
			slog.Warn("weixin inbound source endpoint rejected", "binding_id", binding.ID, "code", messagecontrol.CodeBindingUnavailable)
			continue
		}
		receives := messagecontrol.NewReceiveLifecycle(s.store)
		receive, fresh := receives.Begin(endpoint, inbound.ExternalID)
		if !fresh {
			existing, ok := s.store.FindExternalChatMessageByExternalID(chatSession.ID, inbound.ExternalID)
			if !ok || (existing.Status != "failed" && existing.Status != "delivery_failed") {
				continue
			}
		}
		inbound.ReceiveRecord = receives.Advance(receive, "authorized", "", "")
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
			attempts := s.recordDispatchAttempt(chatSession.ID, binding.ID, inbound.ExternalID)
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
	s.advanceCursor(binding.ID, batch.Cursor)
}

func (s *Syncer) advanceCursor(bindingID, cursor string) {
	if strings.TrimSpace(cursor) == "" {
		return
	}
	// Reload so we don't clobber concurrent updates to the binding record.
	binding, ok := s.store.GetNotificationBinding(bindingID)
	if !ok || binding.ProviderCursor == cursor {
		return
	}
	binding.ProviderCursor = cursor
	binding.UpdatedAt = time.Now().UTC()
	s.store.SaveNotificationBinding(binding)
}

// recordDispatchAttempt persists a failed dispatch on the inbound message
// record: the retry budget survives gateway restarts and lives and dies with
// the message itself, so nothing lingers in syncer memory for bindings that
// are later revoked. A successful dispatch resets the count when the
// dispatcher finalizes the record.
func (s *Syncer) recordDispatchAttempt(chatSessionID, bindingID, externalID string) int {
	record, ok := s.store.FindExternalChatMessageByExternalID(chatSessionID, externalID)
	if !ok {
		// The dispatch failed before anything was persisted, so there is no
		// durable place for a budget; treat the message as exhausted rather
		// than blocking the binding's cursor indefinitely.
		slog.Warn("weixin inbound failed without a persisted record; dropping",
			"binding_id", bindingID, "external_id", externalID)
		return maxDispatchAttempts
	}
	record.DispatchAttempts++
	record = s.store.SaveExternalChatMessage(record)
	return record.DispatchAttempts
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
