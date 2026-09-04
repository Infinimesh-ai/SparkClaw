package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"golang.org/x/sync/semaphore"
)

const fileAdmissionCapacity int64 = 1 << 20

type FileStore struct {
	inner      *MemoryStore
	path       string
	encryption *fileEncryption
	admission  *semaphore.Weighted
	timeouts   OperationTimeouts
	commitOps  fileCommitOps
	fenceMu    sync.Mutex
	fence      *fileSubmittedOutcome
}

type FileStoreOptions struct {
	Path               string
	EncryptAtRest      bool
	EncryptionKey      string
	EncryptionKeyFile  string
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	TransactionTimeout time.Duration
}

type Snapshot struct {
	Sessions              map[string]app.Session               `json:"sessions"`
	Clients               map[string]app.Client                `json:"clients"`
	OwnerProfile          app.OwnerProfile                     `json:"owner_profile"`
	OwnerProfiles         map[string]app.OwnerProfile          `json:"owner_profiles,omitempty"`
	PairingCodes          map[string]app.PairingCode           `json:"pairing_codes"`
	ISCPOnboardings       map[string]app.ISCPOnboarding        `json:"iscp_onboardings,omitempty"`
	MCPAccessTickets      map[string]app.MCPAccessTicket       `json:"mcp_access_tickets,omitempty"`
	MCPBindings           map[string]app.MCPBinding            `json:"mcp_bindings,omitempty"`
	MCPOperations         map[string]app.MCPOperation          `json:"mcp_operations,omitempty"`
	Messages              map[string][]app.Message             `json:"messages"`
	RunFeedback           map[string][]app.RunFeedback         `json:"run_feedback"`
	Runs                  map[string]app.AgentRun              `json:"runs"`
	ModelCalls            map[string]app.ModelCall             `json:"model_calls"`
	ToolCalls             map[string]app.ToolCall              `json:"tool_calls"`
	DocumentRecords       map[string]app.DocumentRecord        `json:"document_records,omitempty"`
	Approvals             map[string]app.Approval              `json:"approvals"`
	Reminders             map[string]app.Reminder              `json:"reminders"`
	ReminderDelivery      map[string]app.ReminderDelivery      `json:"reminder_delivery"`
	ConnectorSettings     map[string]app.ConnectorSetting      `json:"connector_settings,omitempty"`
	EmailProviderSettings map[string]app.EmailProviderSetting  `json:"email_provider_settings,omitempty"`
	NotificationBindings  map[string]app.NotificationBinding   `json:"notification_bindings"`
	PassiveNotifications  map[string]app.PassiveNotification   `json:"passive_notifications,omitempty"`
	ExternalChatSessions  map[string]app.ExternalChatSession   `json:"external_chat_sessions,omitempty"`
	ExternalChatMessages  map[string]app.ExternalChatMessage   `json:"external_chat_messages,omitempty"`
	MessageReceives       map[string]app.MessageReceiveRecord  `json:"message_receives,omitempty"`
	MessageDeliveries     map[string]app.MessageDeliveryRecord `json:"message_deliveries,omitempty"`
	ChannelInboxUpdates   map[string]app.ChannelInboxUpdate    `json:"channel_inbox_updates,omitempty"`
	WeixinChatSessions    map[string]app.WeixinChatSession     `json:"weixin_chat_sessions,omitempty"`
	WeixinChatMessages    map[string]app.WeixinChatMessage     `json:"weixin_chat_messages,omitempty"`
	CredentialSecrets     map[string]app.CredentialSecret      `json:"credential_secrets"`
	BrowserAuthRecords    map[string]app.BrowserAuthRecord     `json:"browser_auth_records,omitempty"`
	BrowserLoginBlocks    map[string]app.BrowserLoginBlock     `json:"browser_login_blocks,omitempty"`
	Memories              map[string]app.Memory                `json:"memories"`
	MemoryCandidates      map[string]app.MemoryCandidate       `json:"memory_candidates"`
	AuditEvents           []app.AuditEvent                     `json:"audit_events"`
	Events                []app.Event                          `json:"events"`
	EvalRuns              map[string]app.EvalRun               `json:"eval_runs"`
	ArtifactObjects       map[string]app.ArtifactObject        `json:"artifact_objects"`
	EpisodeSummaries      map[string]app.EpisodeSummary        `json:"episode_summaries"`
}

type snapshotClient struct {
	ID         string     `json:"id"`
	OwnerID    string     `json:"owner_id,omitempty"`
	ActorID    string     `json:"actor_id,omitempty"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"token_hash,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type snapshotPairingCode struct {
	ID        string     `json:"id"`
	CodeHash  string     `json:"code_hash,omitempty"`
	Status    string     `json:"status"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	ClientID  string     `json:"client_id,omitempty"`
}

type snapshotCredentialSecret struct {
	Ref       string    `json:"ref"`
	Kind      string    `json:"kind"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type snapshotJSON Snapshot

func (snapshot Snapshot) MarshalJSON() ([]byte, error) {
	clients := make(map[string]snapshotClient, len(snapshot.Clients))
	for id, client := range snapshot.Clients {
		clients[id] = snapshotClient{
			ID: client.ID, OwnerID: client.OwnerID, ActorID: client.ActorID, Name: client.Name,
			TokenHash: client.TokenHash, CreatedAt: client.CreatedAt,
			LastSeenAt: cloneTimePointer(client.LastSeenAt), RevokedAt: cloneTimePointer(client.RevokedAt),
		}
	}
	pairings := make(map[string]snapshotPairingCode, len(snapshot.PairingCodes))
	for id, code := range snapshot.PairingCodes {
		pairings[id] = snapshotPairingCode{
			ID: code.ID, CodeHash: code.CodeHash, Status: code.Status,
			ExpiresAt: code.ExpiresAt, CreatedAt: code.CreatedAt,
			ClaimedAt: cloneTimePointer(code.ClaimedAt), ClientID: code.ClientID,
		}
	}
	credentials := make(map[string]snapshotCredentialSecret, len(snapshot.CredentialSecrets))
	for ref, secret := range snapshot.CredentialSecrets {
		credentials[ref] = snapshotCredentialSecret{
			Ref: secret.Ref, Kind: secret.Kind, Value: secret.Value,
			CreatedAt: secret.CreatedAt, UpdatedAt: secret.UpdatedAt,
		}
	}
	return json.Marshal(struct {
		snapshotJSON
		Clients           map[string]snapshotClient           `json:"clients"`
		PairingCodes      map[string]snapshotPairingCode      `json:"pairing_codes"`
		CredentialSecrets map[string]snapshotCredentialSecret `json:"credential_secrets"`
	}{snapshotJSON: snapshotJSON(snapshot), Clients: clients, PairingCodes: pairings, CredentialSecrets: credentials})
}

func (snapshot *Snapshot) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		snapshotJSON
		Clients           map[string]snapshotClient           `json:"clients"`
		PairingCodes      map[string]snapshotPairingCode      `json:"pairing_codes"`
		CredentialSecrets map[string]snapshotCredentialSecret `json:"credential_secrets"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*snapshot = Snapshot(decoded.snapshotJSON)
	snapshot.Clients = make(map[string]app.Client, len(decoded.Clients))
	for id, client := range decoded.Clients {
		snapshot.Clients[id] = app.Client{
			ID: client.ID, OwnerID: client.OwnerID, ActorID: client.ActorID, Name: client.Name,
			TokenHash: client.TokenHash, CreatedAt: client.CreatedAt,
			LastSeenAt: cloneTimePointer(client.LastSeenAt), RevokedAt: cloneTimePointer(client.RevokedAt),
		}
	}
	snapshot.PairingCodes = make(map[string]app.PairingCode, len(decoded.PairingCodes))
	for id, code := range decoded.PairingCodes {
		snapshot.PairingCodes[id] = app.PairingCode{
			ID: code.ID, CodeHash: code.CodeHash, Status: code.Status,
			ExpiresAt: code.ExpiresAt, CreatedAt: code.CreatedAt,
			ClaimedAt: cloneTimePointer(code.ClaimedAt), ClientID: code.ClientID,
		}
	}
	snapshot.CredentialSecrets = make(map[string]app.CredentialSecret, len(decoded.CredentialSecrets))
	for ref, secret := range decoded.CredentialSecrets {
		snapshot.CredentialSecrets[ref] = app.CredentialSecret{
			Ref: secret.Ref, Kind: secret.Kind, Value: secret.Value,
			CreatedAt: secret.CreatedAt, UpdatedAt: secret.UpdatedAt,
		}
	}
	return nil
}

func NewFileStore(path string) (*FileStore, error) {
	return NewFileStoreWithOptions(FileStoreOptions{Path: path})
}

func NewFileStoreWithOptions(opts FileStoreOptions) (*FileStore, error) {
	timeouts := normalizeOperationTimeouts(OperationTimeouts{Read: opts.ReadTimeout, Write: opts.WriteTimeout, Transaction: opts.TransactionTimeout})
	inner := NewMemoryStoreWithOptions(timeouts)
	admission := newFileAdmission()
	path := opts.Path
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("file state path is required")
	}
	encryption, err := newFileEncryption(opts)
	if err != nil {
		return nil, err
	}
	if raw, err := os.ReadFile(path); err == nil {
		if encryption != nil {
			decrypted, err := encryption.decrypt(raw)
			if err != nil {
				return nil, err
			}
			raw = decrypted
		} else if isEncryptedSnapshotEnvelope(raw) {
			return nil, errors.New("encrypted file state requires state encryption configuration")
		}
		var snapshot Snapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return nil, err
		}
		snapshot.Clients = ensureMap(snapshot.Clients)
		snapshot.PairingCodes = ensureMap(snapshot.PairingCodes)
		if err := normalizeAndValidatePersistedClientsAndPairings(snapshot.Clients, snapshot.PairingCodes); err != nil {
			return nil, fmt.Errorf("validate client state: %w", err)
		}
		if err := normalizePersistedOwnerProfiles(&snapshot); err != nil {
			return nil, err
		}
		if err := normalizePersistedISCPOnboardings(snapshot.ISCPOnboardings); err != nil {
			return nil, err
		}
		if err := normalizeAndValidatePersistedMCPState(&snapshot); err != nil {
			return nil, fmt.Errorf("validate MCP state: %w", err)
		}
		snapshot.CredentialSecrets = ensureMap(snapshot.CredentialSecrets)
		if err := normalizeAndValidatePersistedCredentialSecrets(snapshot.CredentialSecrets); err != nil {
			return nil, fmt.Errorf("validate credential state: %w", err)
		}
		snapshot.ConnectorSettings = ensureMap(snapshot.ConnectorSettings)
		snapshot.EmailProviderSettings = ensureMap(snapshot.EmailProviderSettings)
		snapshot.NotificationBindings = ensureMap(snapshot.NotificationBindings)
		if err := normalizeAndValidatePersistedConnectorState(snapshot.ConnectorSettings, snapshot.NotificationBindings); err != nil {
			return nil, fmt.Errorf("validate connector state: %w", err)
		}
		if err := normalizeAndValidatePersistedEmailProviderState(snapshot.EmailProviderSettings); err != nil {
			return nil, fmt.Errorf("validate email provider state: %w", err)
		}
		inner.loadSnapshot(snapshot)
		if err := inner.validateSessionState(); err != nil {
			return nil, fmt.Errorf("validate session state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &FileStore{inner: inner, path: path, encryption: encryption, admission: admission, timeouts: timeouts, commitOps: osFileCommitOps{}}, nil
}

func normalizePersistedOwnerProfiles(snapshot *Snapshot) error {
	if snapshot == nil {
		return errors.New("file owner snapshot is missing")
	}
	if snapshot.OwnerProfiles == nil {
		if emptyPersistedOwnerProfile(snapshot.OwnerProfile) {
			defaultOwner := app.DefaultOwnerProfile()
			snapshot.OwnerProfile = cloneOwnerProfile(defaultOwner)
			snapshot.OwnerProfiles = map[string]app.OwnerProfile{app.DefaultOwnerID: cloneOwnerProfile(defaultOwner)}
			return nil
		}
		if snapshot.OwnerProfile.ID != app.DefaultOwnerID {
			return fmt.Errorf("legacy owner profile ID %q does not match default owner", snapshot.OwnerProfile.ID)
		}
		snapshot.OwnerProfile = cloneOwnerProfile(snapshot.OwnerProfile)
		snapshot.OwnerProfiles = map[string]app.OwnerProfile{app.DefaultOwnerID: cloneOwnerProfile(snapshot.OwnerProfile)}
		return nil
	}
	for id, profile := range snapshot.OwnerProfiles {
		if id == "" || profile.ID != id {
			return fmt.Errorf("owner profile key %q does not match embedded ID %q", id, profile.ID)
		}
	}
	defaultProfile, ok := snapshot.OwnerProfiles[app.DefaultOwnerID]
	if !ok {
		return errors.New("owner profile map is missing the default owner")
	}
	if !OwnerProfilesEqual(defaultProfile, snapshot.OwnerProfile) &&
		!legacyDefaultOwnerSeedEquivalent(defaultProfile, snapshot.OwnerProfile) {
		return errors.New("legacy owner profile does not match the default owner map entry")
	}
	snapshot.OwnerProfile = cloneOwnerProfile(defaultProfile)
	snapshot.OwnerProfiles = cloneOwnerProfileMap(snapshot.OwnerProfiles)
	return nil
}

func normalizeAndValidatePersistedMCPState(snapshot *Snapshot) error {
	if snapshot == nil {
		return errors.New("file MCP snapshot is missing")
	}
	snapshot.MCPAccessTickets = ensureMap(snapshot.MCPAccessTickets)
	secretOwners := make(map[string]string, len(snapshot.MCPAccessTickets))
	for id, ticket := range snapshot.MCPAccessTickets {
		if strings.TrimSpace(id) == "" || ticket.ID != id {
			return fmt.Errorf("access ticket key %q does not match embedded ID %q", id, ticket.ID)
		}
		if ticket.SchemaVersion != app.MCPAccessTicketSchemaVersion || ticket.Scope != app.MCPAccessConversation || strings.TrimSpace(ticket.SecretHash) == "" {
			return fmt.Errorf("access ticket %q has an invalid durable contract", id)
		}
		if existingID, exists := secretOwners[ticket.SecretHash]; exists && existingID != id {
			return fmt.Errorf("access tickets %q and %q share a secret hash", existingID, id)
		}
		secretOwners[ticket.SecretHash] = id
		ticket.IssuedAt = normalizeMCPTime(ticket.IssuedAt)
		ticket.ExpiresAt = normalizeMCPTime(ticket.ExpiresAt)
		ticket.ConsumedAt = normalizeMCPTimePointer(ticket.ConsumedAt)
		ticket.RevokedAt = normalizeMCPTimePointer(ticket.RevokedAt)
		snapshot.MCPAccessTickets[id] = ticket
	}
	snapshot.MCPBindings = ensureMap(snapshot.MCPBindings)
	for id, binding := range snapshot.MCPBindings {
		if strings.TrimSpace(id) == "" || binding.ID != id {
			return fmt.Errorf("binding key %q does not match embedded ID %q", id, binding.ID)
		}
		binding.CreatedAt = normalizeMCPTime(binding.CreatedAt)
		binding.UpdatedAt = normalizeMCPTime(binding.UpdatedAt)
		binding.LastUsedAt = normalizeMCPTimePointer(binding.LastUsedAt)
		binding.RevokedAt = normalizeMCPTimePointer(binding.RevokedAt)
		snapshot.MCPBindings[id] = binding
	}
	snapshot.MCPOperations = ensureMap(snapshot.MCPOperations)
	for id, operation := range snapshot.MCPOperations {
		if strings.TrimSpace(id) == "" || operation.ID != id {
			return fmt.Errorf("operation key %q does not match embedded ID %q", id, operation.ID)
		}
		operation.CreatedAt = normalizeMCPTime(operation.CreatedAt)
		operation.UpdatedAt = normalizeMCPTime(operation.UpdatedAt)
		operation.CompletedAt = normalizeMCPTimePointer(operation.CompletedAt)
		operation.Invocation.Deadline = normalizeMCPTime(operation.Invocation.Deadline)
		operation.Invocation.CreatedAt = normalizeMCPTime(operation.Invocation.CreatedAt)
		snapshot.MCPOperations[id] = cloneMCPOperation(operation)
	}
	return nil
}

func legacyDefaultOwnerSeedEquivalent(authority, legacy app.OwnerProfile) bool {
	isStockDefault := func(profile app.OwnerProfile) bool {
		return profile.ID == app.DefaultOwnerID && profile.Source == "web" && profile.ExternalRef == "" &&
			profile.WorkspaceRoot == "" && profile.DefaultChannel == "" && profile.DefaultBindingID == "" &&
			profile.DisplayName == "Owner" && profile.Email == "" && len(profile.Preferences) == 0 &&
			!profile.CreatedAt.IsZero() && profile.CreatedAt.Equal(profile.UpdatedAt)
	}
	return isStockDefault(authority) && isStockDefault(legacy)
}

func emptyPersistedOwnerProfile(profile app.OwnerProfile) bool {
	return profile.ID == "" && profile.Source == "" && profile.ExternalRef == "" &&
		profile.WorkspaceRoot == "" && profile.DefaultChannel == "" && profile.DefaultBindingID == "" &&
		profile.DisplayName == "" && profile.Email == "" && len(profile.Preferences) == 0 &&
		profile.CreatedAt.IsZero() && profile.UpdatedAt.IsZero()
}

func newFileAdmission() *semaphore.Weighted {
	return semaphore.NewWeighted(fileAdmissionCapacity)
}

func (s *FileStore) SaveISCPOnboarding(ctx context.Context, onboarding app.ISCPOnboarding) (app.ISCPOnboarding, error) {
	return s.saveISCPOnboarding(ctx, onboarding)
}

func (s *FileStore) GetISCPOnboarding(ctx context.Context, id string) (app.ISCPOnboarding, bool, error) {
	return s.getISCPOnboarding(ctx, id)
}

func (s *FileStore) ListISCPOnboardings(ctx context.Context, ownerID string) ([]app.ISCPOnboarding, error) {
	return s.listISCPOnboardings(ctx, ownerID)
}

func (s *FileStore) SaveMCPAccessTicket(ctx context.Context, ticket app.MCPAccessTicket) (app.MCPAccessTicket, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPAccessTicketSave, fileAdmissionCapacity)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMCPAccessTicketSave, func(ctx context.Context) (app.MCPAccessTicket, error) {
		return s.inner.SaveMCPAccessTicket(ctx, ticket)
	})
}

func (s *FileStore) GetMCPAccessTicket(ctx context.Context, id string) (app.MCPAccessTicket, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPAccessTicketGet, 1)
	if err != nil {
		return app.MCPAccessTicket{}, false, err
	}
	defer release()
	return s.inner.GetMCPAccessTicket(ctx, id)
}

func (s *FileStore) FindMCPAccessTicketBySecretHash(ctx context.Context, secretHash string) (app.MCPAccessTicket, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPAccessTicketFindHash, 1)
	if err != nil {
		return app.MCPAccessTicket{}, false, err
	}
	defer release()
	return s.inner.FindMCPAccessTicketBySecretHash(ctx, secretHash)
}

func (s *FileStore) ListMCPAccessTickets(ctx context.Context, ownerID string) ([]app.MCPAccessTicket, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPAccessTicketList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMCPAccessTickets(ctx, ownerID)
}

func (s *FileStore) RedeemMCPAccessTicket(ctx context.Context, secretHash string, peer app.MCPPeerIdentity, now time.Time) (app.MCPBinding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPAccessTicketRedeem, fileAdmissionCapacity)
	if err != nil {
		return app.MCPBinding{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMCPAccessTicketRedeem, func(ctx context.Context) (app.MCPBinding, error) {
		return s.inner.RedeemMCPAccessTicket(ctx, secretHash, peer, now)
	})
}

func (s *FileStore) RevokeMCPAccessTicket(ctx context.Context, id string, now time.Time) (app.MCPAccessTicket, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPAccessTicketRevoke, fileAdmissionCapacity)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMCPAccessTicketRevoke, func(ctx context.Context) (app.MCPAccessTicket, error) {
		return s.inner.RevokeMCPAccessTicket(ctx, id, now)
	})
}

func (s *FileStore) DeleteMCPAccessTicket(ctx context.Context, ownerID, id string) (app.MCPAccessTicket, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPAccessTicketDelete, fileAdmissionCapacity)
	if err != nil {
		return app.MCPAccessTicket{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMCPAccessTicketDelete, func(ctx context.Context) (app.MCPAccessTicket, error) {
		return s.inner.DeleteMCPAccessTicket(ctx, ownerID, id)
	})
}

func (s *FileStore) GetMCPBinding(ctx context.Context, id string) (app.MCPBinding, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPBindingGet, 1)
	if err != nil {
		return app.MCPBinding{}, false, err
	}
	defer release()
	return s.inner.GetMCPBinding(ctx, id)
}

func (s *FileStore) FindMCPBindingForPeer(ctx context.Context, domainID, deviceID, thumbprint string) (app.MCPBinding, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPBindingFindPeer, 1)
	if err != nil {
		return app.MCPBinding{}, false, err
	}
	defer release()
	return s.inner.FindMCPBindingForPeer(ctx, domainID, deviceID, thumbprint)
}

func (s *FileStore) ListMCPBindings(ctx context.Context, ownerID string) ([]app.MCPBinding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPBindingList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMCPBindings(ctx, ownerID)
}

func (s *FileStore) RevokeMCPBinding(ctx context.Context, id string, now time.Time) (app.MCPBinding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPBindingRevoke, fileAdmissionCapacity)
	if err != nil {
		return app.MCPBinding{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMCPBindingRevoke, func(ctx context.Context) (app.MCPBinding, error) {
		return s.inner.RevokeMCPBinding(ctx, id, now)
	})
}

func (s *FileStore) DeleteMCPBinding(ctx context.Context, ownerID, id string) (app.MCPBinding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPBindingDelete, fileAdmissionCapacity)
	if err != nil {
		return app.MCPBinding{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMCPBindingDelete, func(ctx context.Context) (app.MCPBinding, error) {
		return s.inner.DeleteMCPBinding(ctx, ownerID, id)
	})
}

func (s *FileStore) DeleteMCPAccessRecords(ctx context.Context, ownerID string) (MCPAccessRecordDeletion, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPAccessRecordsDelete, fileAdmissionCapacity)
	if err != nil {
		return MCPAccessRecordDeletion{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMCPAccessRecordsDelete, func(ctx context.Context) (MCPAccessRecordDeletion, error) {
		return s.inner.DeleteMCPAccessRecords(ctx, ownerID)
	})
}

func (s *FileStore) TouchMCPBinding(ctx context.Context, id, iscpSessionID string, now time.Time) error {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPBindingTouch, fileAdmissionCapacity)
	if err != nil {
		return err
	}
	defer release()
	_, err = runFileCommand(s, ctx, OperationMCPBindingTouch, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.inner.TouchMCPBinding(ctx, id, iscpSessionID, now)
	})
	return err
}

func (s *FileStore) CreateMCPOperation(ctx context.Context, operation app.MCPOperation) (app.MCPOperation, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPOperationCreate, fileAdmissionCapacity)
	if err != nil {
		return app.MCPOperation{}, false, err
	}
	defer release()
	return runFileOptionalCommand(s, ctx, OperationMCPOperationCreate, func(ctx context.Context) (app.MCPOperation, bool, error) {
		return s.inner.CreateMCPOperation(ctx, operation)
	})
}

func (s *FileStore) GetMCPOperation(ctx context.Context, id string) (app.MCPOperation, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPOperationGet, 1)
	if err != nil {
		return app.MCPOperation{}, false, err
	}
	defer release()
	return s.inner.GetMCPOperation(ctx, id)
}

func (s *FileStore) FindMCPOperationByIdempotency(ctx context.Context, bindingID, idempotencyKey string) (app.MCPOperation, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPOperationFindIdempotency, 1)
	if err != nil {
		return app.MCPOperation{}, false, err
	}
	defer release()
	return s.inner.FindMCPOperationByIdempotency(ctx, bindingID, idempotencyKey)
}

func (s *FileStore) ListMCPOperations(ctx context.Context, bindingID string) ([]app.MCPOperation, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPOperationList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMCPOperations(ctx, bindingID)
}

func (s *FileStore) UpdateMCPOperation(ctx context.Context, operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMCPOperationUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.MCPOperation{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMCPOperationUpdate, func(ctx context.Context) (app.MCPOperation, error) {
		return s.inner.UpdateMCPOperation(ctx, operation, expectedVersion)
	})
}
