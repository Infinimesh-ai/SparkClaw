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
	Sessions             map[string]app.Session             `json:"sessions"`
	Clients              map[string]app.Client              `json:"clients"`
	OwnerProfile         app.OwnerProfile                   `json:"owner_profile"`
	OwnerProfiles        map[string]app.OwnerProfile        `json:"owner_profiles,omitempty"`
	PairingCodes         map[string]app.PairingCode         `json:"pairing_codes"`
	Messages             map[string][]app.Message           `json:"messages"`
	RunFeedback          map[string][]app.RunFeedback       `json:"run_feedback"`
	Runs                 map[string]app.AgentRun            `json:"runs"`
	ModelCalls           map[string]app.ModelCall           `json:"model_calls"`
	ToolCalls            map[string]app.ToolCall            `json:"tool_calls"`
	Approvals            map[string]app.Approval            `json:"approvals"`
	Reminders            map[string]app.Reminder            `json:"reminders"`
	ReminderDelivery     map[string]app.ReminderDelivery    `json:"reminder_delivery"`
	NotificationBindings map[string]app.NotificationBinding `json:"notification_bindings"`
	WeixinChatSessions   map[string]app.WeixinChatSession   `json:"weixin_chat_sessions"`
	WeixinChatMessages   map[string]app.WeixinChatMessage   `json:"weixin_chat_messages"`
	CredentialSecrets    map[string]app.CredentialSecret    `json:"credential_secrets"`
	Memories             map[string]app.Memory              `json:"memories"`
	MemoryCandidates     map[string]app.MemoryCandidate     `json:"memory_candidates"`
	AuditEvents          []app.AuditEvent                   `json:"audit_events"`
	Events               []app.Event                        `json:"events"`
	EvalRuns             map[string]app.EvalRun             `json:"eval_runs"`
	ArtifactObjects      map[string]app.ArtifactObject      `json:"artifact_objects"`
	EpisodeSummaries     map[string]app.EpisodeSummary      `json:"episode_summaries"`
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

func (s *FileStore) GetReminder(id string) (app.Reminder, bool) {
	return s.inner.GetReminder(id)
}

func (s *FileStore) ListReminders(filter app.ReminderFilter) []app.Reminder {
	return s.inner.ListReminders(filter)
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

func (s *FileStore) SaveWeixinChatSession(session app.WeixinChatSession) app.WeixinChatSession {
	out := s.inner.SaveWeixinChatSession(session)
	s.persist()
	return out
}

func (s *FileStore) GetWeixinChatSession(id string) (app.WeixinChatSession, bool) {
	return s.inner.GetWeixinChatSession(id)
}

func (s *FileStore) FindWeixinChatSession(bindingID, externalUserID string) (app.WeixinChatSession, bool) {
	return s.inner.FindWeixinChatSession(bindingID, externalUserID)
}

func (s *FileStore) FindWeixinChatSessionByLinkedSessionID(sessionID string) (app.WeixinChatSession, bool) {
	return s.inner.FindWeixinChatSessionByLinkedSessionID(sessionID)
}

func (s *FileStore) SaveWeixinChatMessage(message app.WeixinChatMessage) app.WeixinChatMessage {
	out := s.inner.SaveWeixinChatMessage(message)
	s.persist()
	return out
}

func (s *FileStore) GetWeixinChatMessage(id string) (app.WeixinChatMessage, bool) {
	return s.inner.GetWeixinChatMessage(id)
}

func (s *FileStore) FindWeixinChatMessageByExternalID(chatSessionID, externalMessageID string) (app.WeixinChatMessage, bool) {
	return s.inner.FindWeixinChatMessageByExternalID(chatSessionID, externalMessageID)
}

func (s *FileStore) ListWeixinChatMessages(chatSessionID string, limit int) []app.WeixinChatMessage {
	return s.inner.ListWeixinChatMessages(chatSessionID, limit)
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
