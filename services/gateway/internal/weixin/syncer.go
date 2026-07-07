package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Syncer struct {
	store      store.Store
	dispatcher *Dispatcher
	client     *http.Client
	cfg        config.Config
	media      *MediaAdapter
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

func (s *Syncer) Tick(ctx context.Context) {
	for _, binding := range s.store.ListNotificationBindings("weixin", "active") {
		if !strings.Contains(strings.ToLower(binding.Provider), "openclaw-weixin") {
			continue
		}
		if err := s.syncBinding(ctx, binding); err != nil {
			binding.LastError = err.Error()
			binding.UpdatedAt = time.Now().UTC()
			s.store.SaveNotificationBinding(binding)
			slog.Warn("weixin context sync failed", "binding_id", binding.ID, "error", err)
		}
	}
}

func (s *Syncer) syncBinding(ctx context.Context, binding app.NotificationBinding) error {
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
			"channel_version": "2.4.6",
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
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(secret.Value))
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", "132102")
	req.Header.Set("X-WECHAT-UIN", randomWechatUIN())
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
	if strings.TrimSpace(decoded.GetUpdatesBuf) != "" && decoded.GetUpdatesBuf != binding.ProviderCursor {
		binding.ProviderCursor = decoded.GetUpdatesBuf
		changed = true
	}
	for _, msg := range decoded.Msgs {
		contextToken := strings.TrimSpace(msg.ContextToken)
		if contextToken == "" {
			continue
		}
		from := strings.TrimSpace(msg.FromUserID)
		if binding.ContextToken != contextToken {
			binding.ContextToken = contextToken
			binding.LastError = ""
			changed = true
		}
		if s.dispatcher != nil {
			text := extractInboundText(msg.ItemList)
			externalID := firstNonEmpty(msg.ID, msg.MsgID, msg.ClientID)
			chatSession := s.dispatcher.ensureChatSession(InboundMessage{
				Binding:        binding,
				FromUserID:     from,
				ContextToken:   contextToken,
				ExternalID:     externalID,
				ProviderCursor: decoded.GetUpdatesBuf,
				CreatedAt:      unixTime(msg.CreateTime),
			})
			attachments := s.downloadInboundAttachments(ctx, binding, msg.ItemList, chatSession.LinkedSessionID, externalID)
			if text != "" || len(attachments) > 0 {
				if err := s.dispatcher.HandleInbound(ctx, InboundMessage{
					Binding:        binding,
					FromUserID:     from,
					ContextToken:   contextToken,
					Text:           text,
					Attachments:    attachments,
					ExternalID:     externalID,
					ProviderCursor: decoded.GetUpdatesBuf,
					CreatedAt:      unixTime(msg.CreateTime),
				}); err != nil {
					slog.Warn("weixin inbound dispatch failed", "binding_id", binding.ID, "error", err)
				}
			}
		}
	}
	if changed {
		binding.UpdatedAt = time.Now().UTC()
		s.store.SaveNotificationBinding(binding)
		slog.Info("weixin context synced", "binding_id", binding.ID, "has_context_token", binding.ContextToken != "")
	}
	return nil
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

func randomWechatUIN() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return base64.StdEncoding.EncodeToString([]byte("0"))
	}
	value := int(raw[0])<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
	if value < 0 {
		value = -value
	}
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", value)))
}
