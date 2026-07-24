package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type FileStore struct {
	inner      *MemoryStore
	path       string
	encryption *fileEncryption
	mu         sync.Mutex
}

type FileStoreOptions struct {
	Path              string
	EncryptAtRest     bool
	EncryptionKey     string
	EncryptionKeyFile string
}

type Snapshot struct {
	Sessions             map[string]app.Session               `json:"sessions"`
	Clients              map[string]app.Client                `json:"clients"`
	OwnerProfile         app.OwnerProfile                     `json:"owner_profile"`
	OwnerProfiles        map[string]app.OwnerProfile          `json:"owner_profiles,omitempty"`
	PairingCodes         map[string]app.PairingCode           `json:"pairing_codes"`
	Messages             map[string][]app.Message             `json:"messages"`
	RunFeedback          map[string][]app.RunFeedback         `json:"run_feedback"`
	Runs                 map[string]app.AgentRun              `json:"runs"`
	ModelCalls           map[string]app.ModelCall             `json:"model_calls"`
	ToolCalls            map[string]app.ToolCall              `json:"tool_calls"`
	Approvals            map[string]app.Approval              `json:"approvals"`
	Reminders            map[string]app.Reminder              `json:"reminders"`
	ReminderDelivery     map[string]app.ReminderDelivery      `json:"reminder_delivery"`
	NotificationBindings map[string]app.NotificationBinding   `json:"notification_bindings"`
	ExternalChatSessions map[string]app.ExternalChatSession   `json:"external_chat_sessions,omitempty"`
	ExternalChatMessages map[string]app.ExternalChatMessage   `json:"external_chat_messages,omitempty"`
	MessageReceives      map[string]app.MessageReceiveRecord  `json:"message_receives,omitempty"`
	MessageDeliveries    map[string]app.MessageDeliveryRecord `json:"message_deliveries,omitempty"`
	ChannelInboxUpdates  map[string]app.ChannelInboxUpdate    `json:"channel_inbox_updates,omitempty"`
	WeixinChatSessions   map[string]app.WeixinChatSession     `json:"weixin_chat_sessions,omitempty"`
	WeixinChatMessages   map[string]app.WeixinChatMessage     `json:"weixin_chat_messages,omitempty"`
	CredentialSecrets    map[string]app.CredentialSecret      `json:"credential_secrets"`
	BrowserAuthRecords   map[string]app.BrowserAuthRecord     `json:"browser_auth_records,omitempty"`
	BrowserLoginBlocks   map[string]app.BrowserLoginBlock     `json:"browser_login_blocks,omitempty"`
	Memories             map[string]app.Memory                `json:"memories"`
	MemoryCandidates     map[string]app.MemoryCandidate       `json:"memory_candidates"`
	AuditEvents          []app.AuditEvent                     `json:"audit_events"`
	Events               []app.Event                          `json:"events"`
	EvalRuns             map[string]app.EvalRun               `json:"eval_runs"`
	ArtifactObjects      map[string]app.ArtifactObject        `json:"artifact_objects"`
	EpisodeSummaries     map[string]app.EpisodeSummary        `json:"episode_summaries"`
}

func NewFileStore(path string) (*FileStore, error) {
	return NewFileStoreWithOptions(FileStoreOptions{Path: path})
}

func NewFileStoreWithOptions(opts FileStoreOptions) (*FileStore, error) {
	inner := NewMemoryStore()
	path := opts.Path
	encryption, err := newFileEncryption(opts)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return &FileStore{inner: inner, encryption: encryption}, nil
	}
	if raw, err := os.ReadFile(path); err == nil {
		if encryption != nil {
			decrypted, err := encryption.decrypt(raw)
			if err != nil {
				return nil, err
			}
			raw = decrypted
		}
		var snapshot Snapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return nil, err
		}
		inner.loadSnapshot(snapshot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &FileStore{inner: inner, path: path, encryption: encryption}, nil
}

func (s *FileStore) CreateSession(title string) app.Session {
	out := s.inner.CreateSession(title)
	s.persist()
	return out
}

func (s *FileStore) CreateSessionWithScope(title, ownerID, workspaceRoot, source string, hidden bool) app.Session {
	out := s.inner.CreateSessionWithScope(title, ownerID, workspaceRoot, source, hidden)
	s.persist()
	return out
}

func (s *FileStore) ListSessions() []app.Session {
	return s.inner.ListSessions()
}

func (s *FileStore) GetSession(id string) (app.Session, bool) {
	return s.inner.GetSession(id)
}

func (s *FileStore) UpdateSessionTitle(id, title string) (app.Session, error) {
	out, err := s.inner.UpdateSessionTitle(id, title)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) DeleteSession(id string) (app.Session, error) {
	out, err := s.inner.DeleteSession(id)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) SaveClient(client app.Client) {
	s.inner.SaveClient(client)
	s.persist()
}

func (s *FileStore) GetClient(id string) (app.Client, bool) {
	return s.inner.GetClient(id)
}

func (s *FileStore) ListClients() []app.Client {
	return s.inner.ListClients()
}

func (s *FileStore) RevokeClient(id string) (app.Client, error) {
	out, err := s.inner.RevokeClient(id)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) FindClientByTokenHash(tokenHash string) (app.Client, bool) {
	return s.inner.FindClientByTokenHash(tokenHash)
}

func (s *FileStore) TouchClient(id string) {
	s.inner.TouchClient(id)
	s.persist()
}

func (s *FileStore) GetOwnerProfile() app.OwnerProfile {
	return s.inner.GetOwnerProfile()
}

func (s *FileStore) UpdateOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	out := s.inner.UpdateOwnerProfile(profile)
	s.persist()
	return out
}

func (s *FileStore) GetOwnerProfileByID(id string) (app.OwnerProfile, bool) {
	return s.inner.GetOwnerProfileByID(id)
}

func (s *FileStore) SaveOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	out := s.inner.SaveOwnerProfile(profile)
	s.persist()
	return out
}

func (s *FileStore) ListOwnerProfiles() []app.OwnerProfile {
	return s.inner.ListOwnerProfiles()
}

func (s *FileStore) FindOwnerProfileByExternalRef(source, externalRef string) (app.OwnerProfile, bool) {
	return s.inner.FindOwnerProfileByExternalRef(source, externalRef)
}

func (s *FileStore) SavePairingCode(code app.PairingCode) {
	s.inner.SavePairingCode(code)
	s.persist()
}

func (s *FileStore) GetPairingCode(id string) (app.PairingCode, bool) {
	return s.inner.GetPairingCode(id)
}

func (s *FileStore) ClaimPairingCode(id, clientID string) (app.PairingCode, error) {
	out, err := s.inner.ClaimPairingCode(id, clientID)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) AddMessage(message app.Message) app.Message {
	out := s.inner.AddMessage(message)
	s.persist()
	return out
}

func (s *FileStore) ListMessages(sessionID string) []app.Message {
	return s.inner.ListMessages(sessionID)
}

func (s *FileStore) SaveRunFeedback(feedback app.RunFeedback) app.RunFeedback {
	out := s.inner.SaveRunFeedback(feedback)
	s.persist()
	return out
}

func (s *FileStore) ListRunFeedback(runID string) []app.RunFeedback {
	return s.inner.ListRunFeedback(runID)
}

func (s *FileStore) SaveRun(run app.AgentRun) {
	s.inner.SaveRun(run)
	s.persist()
}

func (s *FileStore) GetRun(id string) (app.AgentRun, bool) {
	return s.inner.GetRun(id)
}

func (s *FileStore) ListRuns(sessionID string) []app.AgentRun {
	return s.inner.ListRuns(sessionID)
}

func (s *FileStore) SaveModelCall(call app.ModelCall) {
	s.inner.SaveModelCall(call)
	s.persist()
}

func (s *FileStore) ListModelCalls(sessionID, runID string) []app.ModelCall {
	return s.inner.ListModelCalls(sessionID, runID)
}

func (s *FileStore) SaveToolCall(call app.ToolCall) {
	s.inner.SaveToolCall(call)
	s.persist()
}

func (s *FileStore) GetToolCall(id string) (app.ToolCall, bool) {
	return s.inner.GetToolCall(id)
}

func (s *FileStore) ListToolCalls(sessionID string) []app.ToolCall {
	return s.inner.ListToolCalls(sessionID)
}

func (s *FileStore) SaveApproval(approval app.Approval) {
	s.inner.SaveApproval(approval)
	s.persist()
}

func (s *FileStore) ResolveApproval(id, status, note string) (app.Approval, error) {
	out, err := s.inner.ResolveApproval(id, status, note)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) ListApprovals(status string) []app.Approval {
	return s.inner.ListApprovals(status)
}

func (s *FileStore) SaveReminder(reminder app.Reminder) app.Reminder {
	out := s.inner.SaveReminder(reminder)
	s.persist()
	return out
}

func (s *FileStore) UpdatePendingReminder(reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	out, err := s.inner.UpdatePendingReminder(reminder, expectedUpdatedAt)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) GetReminder(id string) (app.Reminder, bool) {
	return s.inner.GetReminder(id)
}

func (s *FileStore) ListReminders(filter app.ReminderFilter) []app.Reminder {
	return s.inner.ListReminders(filter)
}

func (s *FileStore) ClaimDueReminders(now, staleBefore time.Time, limit int) []app.Reminder {
	out := s.inner.ClaimDueReminders(now, staleBefore, limit)
	if len(out) > 0 {
		s.persist()
	}
	return out
}

func (s *FileStore) SaveReminderDelivery(delivery app.ReminderDelivery) app.ReminderDelivery {
	out := s.inner.SaveReminderDelivery(delivery)
	s.persist()
	return out
}

func (s *FileStore) ListReminderDeliveries(reminderID string) []app.ReminderDelivery {
	return s.inner.ListReminderDeliveries(reminderID)
}

func (s *FileStore) SaveNotificationBinding(binding app.NotificationBinding) app.NotificationBinding {
	out := s.inner.SaveNotificationBinding(binding)
	s.persist()
	return out
}

func (s *FileStore) GetNotificationBinding(id string) (app.NotificationBinding, bool) {
	return s.inner.GetNotificationBinding(id)
}

func (s *FileStore) ListNotificationBindings(channel, status string) []app.NotificationBinding {
	return s.inner.ListNotificationBindings(channel, status)
}

func (s *FileStore) RevokeNotificationBinding(id string) (app.NotificationBinding, error) {
	out, err := s.inner.RevokeNotificationBinding(id)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) SaveExternalChatSession(session app.ExternalChatSession) app.ExternalChatSession {
	out := s.inner.SaveExternalChatSession(session)
	s.persist()
	return out
}

func (s *FileStore) GetExternalChatSession(id string) (app.ExternalChatSession, bool) {
	return s.inner.GetExternalChatSession(id)
}

func (s *FileStore) ListExternalChatSessions(channel, status string) []app.ExternalChatSession {
	return s.inner.ListExternalChatSessions(channel, status)
}

func (s *FileStore) FindExternalChatSession(bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool) {
	return s.inner.FindExternalChatSession(bindingID, externalChatID, externalThreadID)
}

func (s *FileStore) FindExternalChatSessionByLinkedSessionID(sessionID string) (app.ExternalChatSession, bool) {
	return s.inner.FindExternalChatSessionByLinkedSessionID(sessionID)
}

func (s *FileStore) SaveExternalChatMessage(message app.ExternalChatMessage) app.ExternalChatMessage {
	out := s.inner.SaveExternalChatMessage(message)
	s.persist()
	return out
}

func (s *FileStore) GetExternalChatMessage(id string) (app.ExternalChatMessage, bool) {
	return s.inner.GetExternalChatMessage(id)
}

func (s *FileStore) FindExternalChatMessageByExternalID(chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool) {
	return s.inner.FindExternalChatMessageByExternalID(chatSessionID, externalMessageID)
}

func (s *FileStore) ListExternalChatMessages(chatSessionID string, limit int) []app.ExternalChatMessage {
	return s.inner.ListExternalChatMessages(chatSessionID, limit)
}

func (s *FileStore) SaveMessageReceive(record app.MessageReceiveRecord) app.MessageReceiveRecord {
	out := s.inner.SaveMessageReceive(record)
	s.persist()
	return out
}

func (s *FileStore) GetMessageReceive(id string) (app.MessageReceiveRecord, bool) {
	return s.inner.GetMessageReceive(id)
}

func (s *FileStore) FindMessageReceive(sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool) {
	return s.inner.FindMessageReceive(sourceEndpointID, nativeMessageID)
}

func (s *FileStore) ListMessageReceives(ownerID, actorID string, limit int) []app.MessageReceiveRecord {
	return s.inner.ListMessageReceives(ownerID, actorID, limit)
}

func (s *FileStore) SaveMessageDelivery(record app.MessageDeliveryRecord) app.MessageDeliveryRecord {
	out := s.inner.SaveMessageDelivery(record)
	s.persist()
	return out
}

func (s *FileStore) GetMessageDelivery(id app.DeliveryID) (app.MessageDeliveryRecord, bool) {
	return s.inner.GetMessageDelivery(id)
}

func (s *FileStore) FindMessageDeliveryByIdempotency(ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool) {
	return s.inner.FindMessageDeliveryByIdempotency(ownerID, actorID, idempotencyKey)
}

func (s *FileStore) ListMessageDeliveries(ownerID, actorID string, limit int) []app.MessageDeliveryRecord {
	return s.inner.ListMessageDeliveries(ownerID, actorID, limit)
}

func (s *FileStore) SaveChannelInboxUpdate(update app.ChannelInboxUpdate) app.ChannelInboxUpdate {
	out := s.inner.SaveChannelInboxUpdate(update)
	s.persist()
	return out
}

func (s *FileStore) GetChannelInboxUpdate(id string) (app.ChannelInboxUpdate, bool) {
	return s.inner.GetChannelInboxUpdate(id)
}

func (s *FileStore) FindChannelInboxUpdate(bindingID, externalID string) (app.ChannelInboxUpdate, bool) {
	return s.inner.FindChannelInboxUpdate(bindingID, externalID)
}

func (s *FileStore) ListChannelInboxUpdates(channel, status string, readyBefore time.Time, limit int) []app.ChannelInboxUpdate {
	return s.inner.ListChannelInboxUpdates(channel, status, readyBefore, limit)
}

func (s *FileStore) SaveWeixinChatSession(session app.WeixinChatSession) app.WeixinChatSession {
	return s.SaveExternalChatSession(session)
}

func (s *FileStore) GetWeixinChatSession(id string) (app.WeixinChatSession, bool) {
	return s.GetExternalChatSession(id)
}

func (s *FileStore) FindWeixinChatSession(bindingID, externalUserID string) (app.WeixinChatSession, bool) {
	return s.FindExternalChatSession(bindingID, externalUserID, "")
}

func (s *FileStore) FindWeixinChatSessionByLinkedSessionID(sessionID string) (app.WeixinChatSession, bool) {
	return s.FindExternalChatSessionByLinkedSessionID(sessionID)
}

func (s *FileStore) SaveWeixinChatMessage(message app.WeixinChatMessage) app.WeixinChatMessage {
	return s.SaveExternalChatMessage(message)
}

func (s *FileStore) GetWeixinChatMessage(id string) (app.WeixinChatMessage, bool) {
	return s.GetExternalChatMessage(id)
}

func (s *FileStore) FindWeixinChatMessageByExternalID(chatSessionID, externalMessageID string) (app.WeixinChatMessage, bool) {
	return s.FindExternalChatMessageByExternalID(chatSessionID, externalMessageID)
}

func (s *FileStore) ListWeixinChatMessages(chatSessionID string, limit int) []app.WeixinChatMessage {
	return s.ListExternalChatMessages(chatSessionID, limit)
}

func (s *FileStore) SaveCredentialSecret(secret app.CredentialSecret) app.CredentialSecret {
	out := s.inner.SaveCredentialSecret(secret)
	s.persist()
	return out
}

func (s *FileStore) GetCredentialSecret(ref string) (app.CredentialSecret, bool) {
	return s.inner.GetCredentialSecret(ref)
}

func (s *FileStore) DeleteCredentialSecret(ref string) error {
	err := s.inner.DeleteCredentialSecret(ref)
	if err == nil {
		s.persist()
	}
	return err
}

func (s *FileStore) SaveBrowserAuthRecord(record app.BrowserAuthRecord) app.BrowserAuthRecord {
	out := s.inner.SaveBrowserAuthRecord(record)
	s.persist()
	return out
}

func (s *FileStore) GetBrowserAuthRecord(id string) (app.BrowserAuthRecord, bool) {
	return s.inner.GetBrowserAuthRecord(id)
}

func (s *FileStore) FindBrowserAuthRecord(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool) {
	return s.inner.FindBrowserAuthRecord(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
}

func (s *FileStore) ListBrowserAuthRecords(ownerID, browserProfileID string) []app.BrowserAuthRecord {
	return s.inner.ListBrowserAuthRecords(ownerID, browserProfileID)
}

func (s *FileStore) RevokeBrowserAuthRecord(id, reason string) (app.BrowserAuthRecord, error) {
	out, err := s.inner.RevokeBrowserAuthRecord(id, reason)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) SaveBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock {
	out := s.inner.SaveBrowserLoginBlock(block)
	s.persist()
	return out
}

func (s *FileStore) GetBrowserLoginBlock(id string) (app.BrowserLoginBlock, bool) {
	return s.inner.GetBrowserLoginBlock(id)
}

func (s *FileStore) FindActiveBrowserLoginBlock(sessionID string) (app.BrowserLoginBlock, bool) {
	return s.inner.FindActiveBrowserLoginBlock(sessionID)
}

func (s *FileStore) ListBrowserLoginBlocks(sessionID, status string) []app.BrowserLoginBlock {
	return s.inner.ListBrowserLoginBlocks(sessionID, status)
}

func (s *FileStore) AddMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate {
	out := s.inner.AddMemoryCandidate(candidate)
	s.persist()
	return out
}

func (s *FileStore) ResolveMemoryCandidate(id, status string) (app.MemoryCandidate, *app.Memory, error) {
	candidate, memory, err := s.inner.ResolveMemoryCandidate(id, status)
	if err == nil {
		s.persist()
	}
	return candidate, memory, err
}

func (s *FileStore) ListMemoryCandidates(status string) []app.MemoryCandidate {
	return s.inner.ListMemoryCandidates(status)
}

func (s *FileStore) SearchMemories(query string) []app.Memory {
	return s.inner.SearchMemories(query)
}

func (s *FileStore) UpdateMemory(id, kind, content string) (app.Memory, error) {
	out, err := s.inner.UpdateMemory(id, kind, content)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) DeleteMemory(id string) (app.Memory, error) {
	out, err := s.inner.DeleteMemory(id)
	if err == nil {
		s.persist()
	}
	return out, err
}

func (s *FileStore) PruneMemories(cutoff time.Time) []app.Memory {
	out := s.inner.PruneMemories(cutoff)
	if len(out) > 0 {
		s.persist()
	}
	return out
}

func (s *FileStore) AddAudit(event app.AuditEvent) {
	s.inner.AddAudit(event)
	s.persist()
}

func (s *FileStore) ListAudit(sessionID string) []app.AuditEvent {
	return s.inner.ListAudit(sessionID)
}

func (s *FileStore) EventsAfter(sessionID, after string) []app.Event {
	return s.inner.EventsAfter(sessionID, after)
}

func (s *FileStore) SaveEvalRun(run app.EvalRun) {
	s.inner.SaveEvalRun(run)
	s.persist()
}

func (s *FileStore) GetEvalRun(id string) (app.EvalRun, bool) {
	return s.inner.GetEvalRun(id)
}

func (s *FileStore) ListEvalRuns() []app.EvalRun {
	return s.inner.ListEvalRuns()
}

func (s *FileStore) SaveArtifactObject(object app.ArtifactObject) {
	s.inner.SaveArtifactObject(object)
	s.persist()
}

func (s *FileStore) ListArtifactObjects(limit int) []app.ArtifactObject {
	return s.inner.ListArtifactObjects(limit)
}

func (s *FileStore) SaveEpisodeSummary(summary app.EpisodeSummary) {
	s.inner.SaveEpisodeSummary(summary)
	s.persist()
}

func (s *FileStore) ListEpisodeSummaries(sessionID string) []app.EpisodeSummary {
	return s.inner.ListEpisodeSummaries(sessionID)
}

func (s *FileStore) persist() {
	if s.path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.inner.snapshot()
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return
	}
	if s.encryption != nil {
		raw, err = s.encryption.encrypt(raw)
		if err != nil {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

type encryptedSnapshot struct {
	Version    int    `json:"version"`
	Alg        string `json:"alg"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type fileEncryption struct {
	aead cipher.AEAD
}

func newFileEncryption(opts FileStoreOptions) (*fileEncryption, error) {
	if !opts.EncryptAtRest {
		return nil, nil
	}
	secret := strings.TrimSpace(opts.EncryptionKey)
	if secret == "" && strings.TrimSpace(opts.EncryptionKeyFile) != "" {
		raw, err := os.ReadFile(opts.EncryptionKeyFile)
		if err != nil {
			return nil, err
		}
		secret = strings.TrimSpace(string(raw))
	}
	if secret == "" {
		return nil, errors.New("state encryption is enabled but no encryption key is configured")
	}
	key := deriveStateEncryptionKey(secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &fileEncryption{aead: aead}, nil
}

func deriveStateEncryptionKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func (e *fileEncryption) encrypt(raw []byte) ([]byte, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := e.aead.Seal(nil, nonce, raw, []byte("sparkclaw-state-v1"))
	envelope := encryptedSnapshot{
		Version:    1,
		Alg:        "AES-256-GCM",
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func (e *fileEncryption) decrypt(raw []byte) ([]byte, error) {
	var envelope encryptedSnapshot
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Version == 1 && envelope.Ciphertext != "" {
		if !strings.EqualFold(envelope.Alg, "AES-256-GCM") {
			return nil, fmt.Errorf("unsupported state encryption algorithm %q", envelope.Alg)
		}
		nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
		if err != nil {
			return nil, err
		}
		ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
		if err != nil {
			return nil, err
		}
		return e.aead.Open(nil, nonce, ciphertext, []byte("sparkclaw-state-v1"))
	}
	return raw, nil
}
