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
	ISCPOnboardings      map[string]app.ISCPOnboarding        `json:"iscp_onboardings,omitempty"`
	MCPAccessTickets     map[string]app.MCPAccessTicket       `json:"mcp_access_tickets,omitempty"`
	MCPBindings          map[string]app.MCPBinding            `json:"mcp_bindings,omitempty"`
	MCPOperations        map[string]app.MCPOperation          `json:"mcp_operations,omitempty"`
	Messages             map[string][]app.Message             `json:"messages"`
	RunFeedback          map[string][]app.RunFeedback         `json:"run_feedback"`
	Runs                 map[string]app.AgentRun              `json:"runs"`
	ModelCalls           map[string]app.ModelCall             `json:"model_calls"`
	ToolCalls            map[string]app.ToolCall              `json:"tool_calls"`
	DocumentRecords      map[string]app.DocumentRecord        `json:"document_records,omitempty"`
	Approvals            map[string]app.Approval              `json:"approvals"`
	Reminders            map[string]app.Reminder              `json:"reminders"`
	ReminderDelivery     map[string]app.ReminderDelivery      `json:"reminder_delivery"`
	ConnectorSettings    map[string]app.ConnectorSetting      `json:"connector_settings,omitempty"`
	NotificationBindings map[string]app.NotificationBinding   `json:"notification_bindings"`
	PassiveNotifications map[string]app.PassiveNotification   `json:"passive_notifications,omitempty"`
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

func (s *FileStore) SaveISCPOnboarding(onboarding app.ISCPOnboarding) (app.ISCPOnboarding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.inner.SaveISCPOnboarding(onboarding)
	if err != nil {
		return app.ISCPOnboarding{}, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		s.inner.mu.Lock()
		delete(s.inner.iscpOnboardings, out.ID)
		s.inner.mu.Unlock()
		return app.ISCPOnboarding{}, err
	}
	return out, nil
}

func (s *FileStore) GetISCPOnboarding(id string) (app.ISCPOnboarding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.GetISCPOnboarding(id)
}

func (s *FileStore) ListISCPOnboardings(ownerID string) []app.ISCPOnboarding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.ListISCPOnboardings(ownerID)
}

func (s *FileStore) SaveMCPAccessTicket(ticket app.MCPAccessTicket) (app.MCPAccessTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.inner.GetMCPAccessTicket(ticket.ID)
	out, err := s.inner.SaveMCPAccessTicket(ticket)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		s.inner.mu.Lock()
		if existed {
			s.inner.mcpAccessTickets[out.ID] = previous
		} else {
			delete(s.inner.mcpAccessTickets, out.ID)
		}
		s.inner.mu.Unlock()
		return app.MCPAccessTicket{}, err
	}
	return out, nil
}

func (s *FileStore) GetMCPAccessTicket(id string) (app.MCPAccessTicket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.GetMCPAccessTicket(id)
}

func (s *FileStore) FindMCPAccessTicketBySecretHash(secretHash string) (app.MCPAccessTicket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.FindMCPAccessTicketBySecretHash(secretHash)
}

func (s *FileStore) ListMCPAccessTickets(ownerID string) []app.MCPAccessTicket {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.ListMCPAccessTickets(ownerID)
}

func (s *FileStore) RedeemMCPAccessTicket(secretHash string, peer app.MCPPeerIdentity, now time.Time) (app.MCPBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.inner.FindMCPAccessTicketBySecretHash(secretHash)
	out, err := s.inner.RedeemMCPAccessTicket(secretHash, peer, now)
	if err != nil {
		return out, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		s.inner.mu.Lock()
		if existed {
			s.inner.mcpAccessTickets[previous.ID] = previous
		}
		delete(s.inner.mcpBindings, out.ID)
		delete(s.inner.sessions, out.LinkedSessionID)
		s.inner.mu.Unlock()
		return app.MCPBinding{}, err
	}
	return out, nil
}

func (s *FileStore) RevokeMCPAccessTicket(id string, now time.Time) (app.MCPAccessTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.inner.GetMCPAccessTicket(id)
	out, err := s.inner.RevokeMCPAccessTicket(id, now)
	if err != nil {
		return out, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		if existed {
			s.inner.mu.Lock()
			s.inner.mcpAccessTickets[id] = previous
			s.inner.mu.Unlock()
		}
		return app.MCPAccessTicket{}, err
	}
	return out, nil
}

func (s *FileStore) DeleteMCPAccessTicket(ownerID, id string) (app.MCPAccessTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.inner.DeleteMCPAccessTicket(ownerID, id)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		s.inner.mu.Lock()
		s.inner.mcpAccessTickets[id] = out
		s.inner.mu.Unlock()
		return app.MCPAccessTicket{}, err
	}
	return out, nil
}

func (s *FileStore) GetMCPBinding(id string) (app.MCPBinding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.GetMCPBinding(id)
}
func (s *FileStore) FindMCPBindingForPeer(domainID, deviceID, thumbprint string) (app.MCPBinding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.FindMCPBindingForPeer(domainID, deviceID, thumbprint)
}
func (s *FileStore) ListMCPBindings(ownerID string) []app.MCPBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.ListMCPBindings(ownerID)
}
func (s *FileStore) RevokeMCPBinding(id string, now time.Time) (app.MCPBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.inner.GetMCPBinding(id)
	previousOperations := s.inner.ListMCPOperations(id)
	out, err := s.inner.RevokeMCPBinding(id, now)
	if err != nil {
		return out, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		if existed {
			s.inner.mu.Lock()
			s.inner.mcpBindings[id] = previous
			for _, operation := range previousOperations {
				s.inner.mcpOperations[operation.ID] = operation
			}
			s.inner.mu.Unlock()
		}
		return app.MCPBinding{}, err
	}
	return out, nil
}

func (s *FileStore) DeleteMCPBinding(ownerID, id string) (app.MCPBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previousOperations := s.inner.ListMCPOperations(id)
	out, err := s.inner.DeleteMCPBinding(ownerID, id)
	if err != nil {
		return app.MCPBinding{}, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		s.inner.mu.Lock()
		s.inner.mcpBindings[id] = out
		for _, operation := range previousOperations {
			s.inner.mcpOperations[operation.ID] = operation
		}
		s.inner.mu.Unlock()
		return app.MCPBinding{}, err
	}
	return out, nil
}

func (s *FileStore) DeleteMCPAccessRecords(ownerID string) (MCPAccessRecordDeletion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previousTickets := cloneMCPAccessTicketMap(s.inner.mcpAccessTickets)
	previousBindings := cloneMCPBindingMap(s.inner.mcpBindings)
	previousOperations := cloneMCPOperationMap(s.inner.mcpOperations)
	out, err := s.inner.DeleteMCPAccessRecords(ownerID)
	if err != nil {
		return MCPAccessRecordDeletion{}, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		s.inner.mu.Lock()
		s.inner.mcpAccessTickets = previousTickets
		s.inner.mcpBindings = previousBindings
		s.inner.mcpOperations = previousOperations
		s.inner.mu.Unlock()
		return MCPAccessRecordDeletion{}, err
	}
	return out, nil
}

func (s *FileStore) TouchMCPBinding(id, iscpSessionID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.inner.GetMCPBinding(id)
	err := s.inner.TouchMCPBinding(id, iscpSessionID, now)
	if err != nil {
		return err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		if existed {
			s.inner.mu.Lock()
			s.inner.mcpBindings[id] = previous
			s.inner.mu.Unlock()
		}
		return err
	}
	return nil
}
func (s *FileStore) CreateMCPOperation(operation app.MCPOperation) (app.MCPOperation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, created, err := s.inner.CreateMCPOperation(operation)
	if err != nil || !created {
		return out, created, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		s.inner.mu.Lock()
		delete(s.inner.mcpOperations, out.ID)
		s.inner.mu.Unlock()
		return app.MCPOperation{}, false, err
	}
	return out, true, nil
}
func (s *FileStore) GetMCPOperation(id string) (app.MCPOperation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.GetMCPOperation(id)
}
func (s *FileStore) FindMCPOperationByIdempotency(bindingID, idempotencyKey string) (app.MCPOperation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.FindMCPOperationByIdempotency(bindingID, idempotencyKey)
}
func (s *FileStore) ListMCPOperations(bindingID string) []app.MCPOperation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.ListMCPOperations(bindingID)
}
func (s *FileStore) UpdateMCPOperation(operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.inner.GetMCPOperation(operation.ID)
	out, err := s.inner.UpdateMCPOperation(operation, expectedVersion)
	if err != nil {
		return out, err
	}
	if err := s.persistSnapshotLocked(); err != nil {
		if existed {
			s.inner.mu.Lock()
			s.inner.mcpOperations[operation.ID] = previous
			s.inner.mu.Unlock()
		}
		return app.MCPOperation{}, err
	}
	return out, nil
}

func (s *FileStore) AddMessage(message app.Message) app.Message {
	out := s.inner.AddMessage(message)
	s.persist()
	return out
}

func (s *FileStore) SaveDocumentRecord(record app.DocumentRecord) app.DocumentRecord {
	out := s.inner.SaveDocumentRecord(record)
	s.persist()
	return out
}

func (s *FileStore) GetDocumentRecord(id string) (app.DocumentRecord, bool) {
	return s.inner.GetDocumentRecord(id)
}

func (s *FileStore) ListDocumentRecords(ownerID, sessionID string, limit int) []app.DocumentRecord {
	return s.inner.ListDocumentRecords(ownerID, sessionID, limit)
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

func (s *FileStore) GetApproval(id string) (app.Approval, bool) {
	return s.inner.GetApproval(id)
}

func (s *FileStore) FindApprovalByExternalRef(source app.ApprovalSource, externalID string) (app.Approval, bool) {
	return s.inner.FindApprovalByExternalRef(source, externalID)
}

func (s *FileStore) UpdatePendingApproval(approval app.Approval) (app.Approval, error) {
	out, err := s.inner.UpdatePendingApproval(approval)
	if err == nil {
		s.persist()
	}
	return out, err
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

func (s *FileStore) GetConnectorSetting(ownerID, channel string) (app.ConnectorSetting, bool) {
	return s.inner.GetConnectorSetting(ownerID, channel)
}

func (s *FileStore) ListConnectorSettings(ownerID string) []app.ConnectorSetting {
	return s.inner.ListConnectorSettings(ownerID)
}

func (s *FileStore) UpdateConnectorSetting(setting app.ConnectorSetting, expectedVersion int64) (app.ConnectorSetting, error) {
	out, err := s.inner.UpdateConnectorSetting(setting, expectedVersion)
	if err == nil {
		s.persist()
	}
	return out, err
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

func (s *FileStore) CreatePassiveNotification(notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	out, created, err := s.inner.CreatePassiveNotification(notification)
	if err != nil || !created {
		// Idempotent replays change nothing; skip the full snapshot rewrite.
		return out, created, err
	}
	if err := s.persistSnapshot(); err != nil {
		return app.PassiveNotification{}, false, err
	}
	return out, created, nil
}

func (s *FileStore) GetPassiveNotification(ownerID, id string) (app.PassiveNotification, bool) {
	return s.inner.GetPassiveNotification(ownerID, id)
}

func (s *FileStore) ListPassiveNotifications(ownerID, after string, limit int) []app.PassiveNotification {
	return s.inner.ListPassiveNotifications(ownerID, after, limit)
}

func (s *FileStore) CountUnreadPassiveNotifications(ownerID string) int {
	return s.inner.CountUnreadPassiveNotifications(ownerID)
}

func (s *FileStore) MarkPassiveNotificationRead(ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	out, err := s.inner.MarkPassiveNotificationRead(ownerID, id, readAt)
	if err == nil {
		err = s.persistSnapshot()
	}
	return out, err
}

func (s *FileStore) MarkAllPassiveNotificationsRead(ownerID string, readAt time.Time) (int, error) {
	count, err := s.inner.MarkAllPassiveNotificationsRead(ownerID, readAt)
	if err == nil && count > 0 {
		err = s.persistSnapshot()
	}
	return count, err
}

func (s *FileStore) PrunePassiveNotifications(cutoff time.Time, maxPerOwner int) int {
	removed := s.inner.PrunePassiveNotifications(cutoff, maxPerOwner)
	if removed > 0 {
		s.persist()
	}
	return removed
}

func (s *FileStore) PassiveNotificationRevision(ownerID string) uint64 {
	return s.inner.PassiveNotificationRevision(ownerID)
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

func (s *FileStore) UpdateBrowserLoginBlock(block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	out, err := s.inner.UpdateBrowserLoginBlock(block, expectedVersion)
	if err == nil {
		s.persist()
	}
	return out, err
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

func (s *FileStore) MessageEventHead(sessionID string) (string, error) {
	return s.inner.MessageEventHead(sessionID)
}

func (s *FileStore) MessageEventsAfter(sessionID, after string, limit int) (MessageEventPage, error) {
	return s.inner.MessageEventsAfter(sessionID, after, limit)
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

func (s *FileStore) FindArtifactObjectByURI(uri, sessionID, runID string) (app.ArtifactObject, bool) {
	return s.inner.FindArtifactObjectByURI(uri, sessionID, runID)
}

func (s *FileStore) SaveEpisodeSummary(summary app.EpisodeSummary) {
	s.inner.SaveEpisodeSummary(summary)
	s.persist()
}

func (s *FileStore) ListEpisodeSummaries(sessionID string) []app.EpisodeSummary {
	return s.inner.ListEpisodeSummaries(sessionID)
}

func (s *FileStore) persist() {
	_ = s.persistSnapshot()
}

func (s *FileStore) persistSnapshot() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistSnapshotLocked()
}

func (s *FileStore) persistSnapshotLocked() error {
	if s.path == "" {
		return nil
	}
	snapshot := s.inner.snapshot()
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if s.encryption != nil {
		raw, err = s.encryption.encrypt(raw)
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
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
