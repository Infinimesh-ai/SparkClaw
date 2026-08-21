package store

import (
	"context"
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
		snapshot.CredentialSecrets = ensureMap(snapshot.CredentialSecrets)
		if err := normalizeAndValidatePersistedCredentialSecrets(snapshot.CredentialSecrets); err != nil {
			return nil, fmt.Errorf("validate credential state: %w", err)
		}
		snapshot.ConnectorSettings = ensureMap(snapshot.ConnectorSettings)
		snapshot.NotificationBindings = ensureMap(snapshot.NotificationBindings)
		if err := normalizeAndValidatePersistedConnectorState(snapshot.ConnectorSettings, snapshot.NotificationBindings); err != nil {
			return nil, fmt.Errorf("validate connector state: %w", err)
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

func (s *FileStore) admitLegacyRead() func() {
	return s.admitLegacy(1)
}

func (s *FileStore) admitLegacyCommand() func() {
	return s.admitLegacy(fileAdmissionCapacity)
}

func (s *FileStore) admitLegacy(weight int64) func() {
	for {
		if fence := s.currentFileFence(); fence != nil {
			<-fence.done
			continue
		}
		if err := s.admission.Acquire(context.Background(), weight); err != nil {
			panic(fmt.Sprintf("acquire FileStore admission: %v", err))
		}
		if fence := s.currentFileFence(); fence == nil {
			return func() { s.admission.Release(weight) }
		} else {
			s.admission.Release(weight)
			<-fence.done
		}
	}
}

func (s *FileStore) CreateSession(ctx context.Context, title string) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionCreate, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationSessionCreate, func(ctx context.Context) (app.Session, error) {
		return s.inner.CreateSession(ctx, title)
	})
}

func (s *FileStore) CreateSessionWithScope(ctx context.Context, title, ownerID, workspaceRoot, source string, hidden bool) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionCreateWithScope, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationSessionCreateWithScope, func(ctx context.Context) (app.Session, error) {
		return s.inner.CreateSessionWithScope(ctx, title, ownerID, workspaceRoot, source, hidden)
	})
}

func (s *FileStore) ListSessions(ctx context.Context) ([]app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListSessions(ctx)
}

func (s *FileStore) GetSession(ctx context.Context, id string) (app.Session, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionGet, 1)
	if err != nil {
		return app.Session{}, false, err
	}
	defer release()
	return s.inner.GetSession(ctx, id)
}

func (s *FileStore) UpdateSessionTitle(ctx context.Context, id, title string) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionUpdateTitle, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationSessionUpdateTitle, func(ctx context.Context) (app.Session, error) {
		return s.inner.UpdateSessionTitle(ctx, id, title)
	})
}

func (s *FileStore) DeleteSession(ctx context.Context, id string) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionDelete, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationSessionDelete, func(ctx context.Context) (app.Session, error) {
		return s.inner.DeleteSession(ctx, id)
	})
}

func (s *FileStore) GetClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientGet, 1)
	if err != nil {
		return app.Client{}, false, err
	}
	defer release()
	return s.inner.GetClient(ctx, id)
}

func (s *FileStore) ListClients(ctx context.Context) ([]app.Client, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListClients(ctx)
}

func (s *FileStore) RevokeClient(ctx context.Context, id string) (app.Client, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientRevoke, fileAdmissionCapacity)
	if err != nil {
		return app.Client{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationClientRevoke, func(ctx context.Context) (app.Client, error) {
		return s.inner.RevokeClient(ctx, id)
	})
}

func (s *FileStore) FindClientByTokenHash(ctx context.Context, tokenHash string) (app.Client, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientFindTokenHash, 1)
	if err != nil {
		return app.Client{}, false, err
	}
	defer release()
	return s.inner.FindClientByTokenHash(ctx, tokenHash)
}

type clientLookupResult struct {
	client app.Client
	found  bool
}

func (s *FileStore) TouchClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientTouch, fileAdmissionCapacity)
	if err != nil {
		return app.Client{}, false, err
	}
	defer release()
	result, found, err := runFileOptionalCommand(s, ctx, OperationClientTouch, func(ctx context.Context) (clientLookupResult, bool, error) {
		client, found, err := s.inner.TouchClient(ctx, id)
		return clientLookupResult{client: client, found: found}, found, err
	})
	return result.client, found && result.found, err
}

func (s *FileStore) GetOwnerProfile(ctx context.Context) (app.OwnerProfile, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileGet, 1)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	defer release()
	return s.inner.GetOwnerProfile(ctx)
}

func (s *FileStore) UpdateOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationOwnerProfileUpdate, func(ctx context.Context) (app.OwnerProfile, error) {
		return s.inner.UpdateOwnerProfile(ctx, profile)
	})
}

func (s *FileStore) GetOwnerProfileByID(ctx context.Context, id string) (app.OwnerProfile, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileGetByID, 1)
	if err != nil {
		return app.OwnerProfile{}, false, err
	}
	defer release()
	return s.inner.GetOwnerProfileByID(ctx, id)
}

func (s *FileStore) SaveOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileSave, fileAdmissionCapacity)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationOwnerProfileSave, func(ctx context.Context) (app.OwnerProfile, error) {
		return s.inner.SaveOwnerProfile(ctx, profile)
	})
}

func (s *FileStore) ListOwnerProfiles(ctx context.Context) ([]app.OwnerProfile, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListOwnerProfiles(ctx)
}

func (s *FileStore) FindOwnerProfileByExternalRef(ctx context.Context, source, externalRef string) (app.OwnerProfile, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileFindExternalRef, 1)
	if err != nil {
		return app.OwnerProfile{}, false, err
	}
	defer release()
	return s.inner.FindOwnerProfileByExternalRef(ctx, source, externalRef)
}

func (s *FileStore) SavePairingCode(ctx context.Context, code app.PairingCode) (app.PairingCode, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPairingCodeSave, fileAdmissionCapacity)
	if err != nil {
		return app.PairingCode{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationPairingCodeSave, func(ctx context.Context) (app.PairingCode, error) {
		return s.inner.SavePairingCode(ctx, code)
	})
}

func (s *FileStore) GetPairingCode(ctx context.Context, id string) (app.PairingCode, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPairingCodeGet, 1)
	if err != nil {
		return app.PairingCode{}, false, err
	}
	defer release()
	return s.inner.GetPairingCode(ctx, id)
}

type pairingClaimResult struct {
	pairing app.PairingCode
	client  app.Client
}

func (s *FileStore) ClaimPairingCode(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPairingCodeClaim, fileAdmissionCapacity)
	if err != nil {
		return app.PairingCode{}, app.Client{}, err
	}
	defer release()
	result, err := runFileCommand(s, ctx, OperationPairingCodeClaim, func(ctx context.Context) (pairingClaimResult, error) {
		pairing, claimedClient, err := s.inner.ClaimPairingCode(ctx, id, client)
		return pairingClaimResult{pairing: pairing, client: claimedClient}, err
	})
	return result.pairing, result.client, err
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

func (s *FileStore) SaveMCPAccessTicket(ticket app.MCPAccessTicket) (app.MCPAccessTicket, error) {
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyRead()()
	return s.inner.GetMCPAccessTicket(id)
}

func (s *FileStore) FindMCPAccessTicketBySecretHash(secretHash string) (app.MCPAccessTicket, bool) {
	defer s.admitLegacyRead()()
	return s.inner.FindMCPAccessTicketBySecretHash(secretHash)
}

func (s *FileStore) ListMCPAccessTickets(ownerID string) []app.MCPAccessTicket {
	defer s.admitLegacyRead()()
	return s.inner.ListMCPAccessTickets(ownerID)
}

func (s *FileStore) RedeemMCPAccessTicket(secretHash string, peer app.MCPPeerIdentity, now time.Time) (app.MCPBinding, error) {
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyRead()()
	return s.inner.GetMCPBinding(id)
}
func (s *FileStore) FindMCPBindingForPeer(domainID, deviceID, thumbprint string) (app.MCPBinding, bool) {
	defer s.admitLegacyRead()()
	return s.inner.FindMCPBindingForPeer(domainID, deviceID, thumbprint)
}
func (s *FileStore) ListMCPBindings(ownerID string) []app.MCPBinding {
	defer s.admitLegacyRead()()
	return s.inner.ListMCPBindings(ownerID)
}
func (s *FileStore) RevokeMCPBinding(id string, now time.Time) (app.MCPBinding, error) {
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyCommand()()
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
	defer s.admitLegacyRead()()
	return s.inner.GetMCPOperation(id)
}
func (s *FileStore) FindMCPOperationByIdempotency(bindingID, idempotencyKey string) (app.MCPOperation, bool) {
	defer s.admitLegacyRead()()
	return s.inner.FindMCPOperationByIdempotency(bindingID, idempotencyKey)
}
func (s *FileStore) ListMCPOperations(bindingID string) []app.MCPOperation {
	defer s.admitLegacyRead()()
	return s.inner.ListMCPOperations(bindingID)
}
func (s *FileStore) UpdateMCPOperation(operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error) {
	defer s.admitLegacyCommand()()
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

func (s *FileStore) AddMessage(ctx context.Context, message app.Message) (app.Message, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationAddMessage, fileAdmissionCapacity)
	if err != nil {
		return app.Message{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationConversationAddMessage, func(ctx context.Context) (app.Message, error) {
		return s.inner.AddMessage(ctx, message)
	})
}

func (s *FileStore) SaveDocumentRecord(ctx context.Context, record app.DocumentRecord) (app.DocumentRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationDocumentRecordSave, fileAdmissionCapacity)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationDocumentRecordSave, func(ctx context.Context) (app.DocumentRecord, error) {
		return s.inner.SaveDocumentRecord(ctx, record)
	})
}

func (s *FileStore) GetDocumentRecord(ctx context.Context, id string) (app.DocumentRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationDocumentRecordGet, 1)
	if err != nil {
		return app.DocumentRecord{}, false, err
	}
	defer release()
	return s.inner.GetDocumentRecord(ctx, id)
}

func (s *FileStore) ListDocumentRecords(ctx context.Context, ownerID, sessionID string, limit int) ([]app.DocumentRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationDocumentRecordList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListDocumentRecords(ctx, ownerID, sessionID, limit)
}

func (s *FileStore) ListMessages(ctx context.Context, sessionID string) ([]app.Message, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationListMessages, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMessages(ctx, sessionID)
}

func (s *FileStore) SaveRunFeedback(ctx context.Context, feedback app.RunFeedback) (app.RunFeedback, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunFeedbackSave, fileAdmissionCapacity)
	if err != nil {
		return app.RunFeedback{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationRunFeedbackSave, func(ctx context.Context) (app.RunFeedback, error) {
		return s.inner.SaveRunFeedback(ctx, feedback)
	})
}

func (s *FileStore) ListRunFeedback(ctx context.Context, runID string) ([]app.RunFeedback, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunFeedbackList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListRunFeedback(ctx, runID)
}

func (s *FileStore) SaveRun(ctx context.Context, run app.AgentRun) (app.AgentRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunSave, fileAdmissionCapacity)
	if err != nil {
		return app.AgentRun{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationRunSave, func(ctx context.Context) (app.AgentRun, error) {
		return s.inner.SaveRun(ctx, run)
	})
}

func (s *FileStore) GetRun(ctx context.Context, id string) (app.AgentRun, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunGet, 1)
	if err != nil {
		return app.AgentRun{}, false, err
	}
	defer release()
	return s.inner.GetRun(ctx, id)
}

func (s *FileStore) ListRuns(ctx context.Context, sessionID string) ([]app.AgentRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListRuns(ctx, sessionID)
}

func (s *FileStore) SaveModelCall(ctx context.Context, call app.ModelCall) (app.ModelCall, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationModelCallSave, fileAdmissionCapacity)
	if err != nil {
		return app.ModelCall{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationModelCallSave, func(ctx context.Context) (app.ModelCall, error) {
		return s.inner.SaveModelCall(ctx, call)
	})
}

func (s *FileStore) ListModelCalls(ctx context.Context, sessionID, runID string) ([]app.ModelCall, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationModelCallList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListModelCalls(ctx, sessionID, runID)
}

func (s *FileStore) SaveToolCall(ctx context.Context, call app.ToolCall) (app.ToolCall, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationToolCallSave, fileAdmissionCapacity)
	if err != nil {
		return app.ToolCall{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationToolCallSave, func(ctx context.Context) (app.ToolCall, error) {
		return s.inner.SaveToolCall(ctx, call)
	})
}

func (s *FileStore) GetToolCall(ctx context.Context, id string) (app.ToolCall, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationToolCallGet, 1)
	if err != nil {
		return app.ToolCall{}, false, err
	}
	defer release()
	return s.inner.GetToolCall(ctx, id)
}

func (s *FileStore) ListToolCalls(ctx context.Context, sessionID string) ([]app.ToolCall, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationToolCallList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListToolCalls(ctx, sessionID)
}

func (s *FileStore) SaveApproval(ctx context.Context, approval app.Approval) (app.Approval, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalSave, fileAdmissionCapacity)
	if err != nil {
		return app.Approval{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationApprovalSave, func(ctx context.Context) (app.Approval, error) {
		return s.inner.SaveApproval(ctx, approval)
	})
}

func (s *FileStore) GetApproval(ctx context.Context, id string) (app.Approval, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalGet, 1)
	if err != nil {
		return app.Approval{}, false, err
	}
	defer release()
	return s.inner.GetApproval(ctx, id)
}

func (s *FileStore) FindApprovalByExternalRef(ctx context.Context, source app.ApprovalSource, externalID string) (app.Approval, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalFindExternalRef, 1)
	if err != nil {
		return app.Approval{}, false, err
	}
	defer release()
	return s.inner.FindApprovalByExternalRef(ctx, source, externalID)
}

func (s *FileStore) UpdatePendingApproval(ctx context.Context, command ApprovalUpdateCommand) (app.Approval, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalUpdatePending, fileAdmissionCapacity)
	if err != nil {
		return app.Approval{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationApprovalUpdatePending, func(ctx context.Context) (app.Approval, error) {
		return s.inner.UpdatePendingApproval(ctx, command)
	})
}

func (s *FileStore) ResolveApproval(ctx context.Context, id, status, note string) (app.Approval, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalResolve, fileAdmissionCapacity)
	if err != nil {
		return app.Approval{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationApprovalResolve, func(ctx context.Context) (app.Approval, error) {
		return s.inner.ResolveApproval(ctx, id, status, note)
	})
}

func (s *FileStore) ListApprovals(ctx context.Context, status string) ([]app.Approval, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListApprovals(ctx, status)
}

func (s *FileStore) SaveReminder(ctx context.Context, reminder app.Reminder) (app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderSave, fileAdmissionCapacity)
	if err != nil {
		return app.Reminder{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationReminderSave, func(ctx context.Context) (app.Reminder, error) {
		return s.inner.SaveReminder(ctx, reminder)
	})
}

func (s *FileStore) UpdatePendingReminder(ctx context.Context, reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderUpdatePending, fileAdmissionCapacity)
	if err != nil {
		return app.Reminder{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationReminderUpdatePending, func(ctx context.Context) (app.Reminder, error) {
		return s.inner.UpdatePendingReminder(ctx, reminder, expectedUpdatedAt)
	})
}

func (s *FileStore) GetReminder(ctx context.Context, id string) (app.Reminder, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderGet, 1)
	if err != nil {
		return app.Reminder{}, false, err
	}
	defer release()
	return s.inner.GetReminder(ctx, id)
}

func (s *FileStore) ListReminders(ctx context.Context, filter app.ReminderFilter) ([]app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListReminders(ctx, filter)
}

func (s *FileStore) ClaimDueReminders(ctx context.Context, now, staleBefore time.Time, limit int) ([]app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderClaimDue, fileAdmissionCapacity)
	if err != nil {
		return nil, err
	}
	defer release()
	out, _, err := runFileOptionalCommand(s, ctx, OperationReminderClaimDue, func(ctx context.Context) ([]app.Reminder, bool, error) {
		claimed, err := s.inner.ClaimDueReminders(ctx, now, staleBefore, limit)
		return claimed, len(claimed) > 0, err
	})
	return out, err
}

func (s *FileStore) SaveReminderDelivery(ctx context.Context, delivery app.ReminderDelivery) (app.ReminderDelivery, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderDeliverySave, fileAdmissionCapacity)
	if err != nil {
		return app.ReminderDelivery{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationReminderDeliverySave, func(ctx context.Context) (app.ReminderDelivery, error) {
		return s.inner.SaveReminderDelivery(ctx, delivery)
	})
}

func (s *FileStore) ListReminderDeliveries(ctx context.Context, reminderID string) ([]app.ReminderDelivery, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderDeliveryList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListReminderDeliveries(ctx, reminderID)
}

func (s *FileStore) CreatePassiveNotification(ctx context.Context, notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationCreate, fileAdmissionCapacity)
	if err != nil {
		return app.PassiveNotification{}, false, err
	}
	defer release()
	return runFileOptionalCommand(s, ctx, OperationPassiveNotificationCreate, func(ctx context.Context) (app.PassiveNotification, bool, error) {
		return s.inner.CreatePassiveNotification(ctx, notification)
	})
}

func (s *FileStore) GetPassiveNotification(ctx context.Context, ownerID, id string) (app.PassiveNotification, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationGet, 1)
	if err != nil {
		return app.PassiveNotification{}, false, err
	}
	defer release()
	return s.inner.GetPassiveNotification(ctx, ownerID, id)
}

func (s *FileStore) ListPassiveNotifications(ctx context.Context, ownerID, after string, limit int) ([]app.PassiveNotification, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListPassiveNotifications(ctx, ownerID, after, limit)
}

func (s *FileStore) CountUnreadPassiveNotifications(ctx context.Context, ownerID string) (int, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationCount, 1)
	if err != nil {
		return 0, err
	}
	defer release()
	return s.inner.CountUnreadPassiveNotifications(ctx, ownerID)
}

func (s *FileStore) MarkPassiveNotificationRead(ctx context.Context, ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationMarkRead, fileAdmissionCapacity)
	if err != nil {
		return app.PassiveNotification{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationPassiveNotificationMarkRead, func(ctx context.Context) (app.PassiveNotification, error) {
		return s.inner.MarkPassiveNotificationRead(ctx, ownerID, id, readAt)
	})
}

func (s *FileStore) MarkAllPassiveNotificationsRead(ctx context.Context, ownerID string, readAt time.Time) (int, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationMarkAll, fileAdmissionCapacity)
	if err != nil {
		return 0, err
	}
	defer release()
	count, _, err := runFileOptionalCommand(s, ctx, OperationPassiveNotificationMarkAll, func(ctx context.Context) (int, bool, error) {
		count, err := s.inner.MarkAllPassiveNotificationsRead(ctx, ownerID, readAt)
		return count, count > 0, err
	})
	return count, err
}

func (s *FileStore) PrunePassiveNotifications(ctx context.Context, cutoff time.Time, maxPerOwner int) (int, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationPrune, fileAdmissionCapacity)
	if err != nil {
		return 0, err
	}
	defer release()
	removed, _, err := runFileOptionalCommand(s, ctx, OperationPassiveNotificationPrune, func(ctx context.Context) (int, bool, error) {
		removed, err := s.inner.PrunePassiveNotifications(ctx, cutoff, maxPerOwner)
		return removed, removed > 0, err
	})
	return removed, err
}

func (s *FileStore) PassiveNotificationRevision(ctx context.Context, ownerID string) (uint64, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationRevision, 1)
	if err != nil {
		return 0, err
	}
	defer release()
	return s.inner.PassiveNotificationRevision(ctx, ownerID)
}

func (s *FileStore) SaveExternalChatSession(ctx context.Context, session app.ExternalChatSession) (app.ExternalChatSession, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionSave, fileAdmissionCapacity)
	if err != nil {
		return app.ExternalChatSession{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationExternalChatSessionSave, func(ctx context.Context) (app.ExternalChatSession, error) {
		return s.inner.SaveExternalChatSession(ctx, session)
	})
}

func (s *FileStore) GetExternalChatSession(ctx context.Context, id string) (app.ExternalChatSession, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionGet, 1)
	if err != nil {
		return app.ExternalChatSession{}, false, err
	}
	defer release()
	return s.inner.GetExternalChatSession(ctx, id)
}

func (s *FileStore) ListExternalChatSessions(ctx context.Context, channel, status string) ([]app.ExternalChatSession, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListExternalChatSessions(ctx, channel, status)
}

func (s *FileStore) FindExternalChatSession(ctx context.Context, bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionFind, 1)
	if err != nil {
		return app.ExternalChatSession{}, false, err
	}
	defer release()
	return s.inner.FindExternalChatSession(ctx, bindingID, externalChatID, externalThreadID)
}

func (s *FileStore) FindExternalChatSessionByLinkedSessionID(ctx context.Context, sessionID string) (app.ExternalChatSession, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionFindLink, 1)
	if err != nil {
		return app.ExternalChatSession{}, false, err
	}
	defer release()
	return s.inner.FindExternalChatSessionByLinkedSessionID(ctx, sessionID)
}

func (s *FileStore) SaveExternalChatMessage(ctx context.Context, message app.ExternalChatMessage) (app.ExternalChatMessage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatMessageSave, fileAdmissionCapacity)
	if err != nil {
		return app.ExternalChatMessage{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationExternalChatMessageSave, func(ctx context.Context) (app.ExternalChatMessage, error) {
		return s.inner.SaveExternalChatMessage(ctx, message)
	})
}

func (s *FileStore) GetExternalChatMessage(ctx context.Context, id string) (app.ExternalChatMessage, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatMessageGet, 1)
	if err != nil {
		return app.ExternalChatMessage{}, false, err
	}
	defer release()
	return s.inner.GetExternalChatMessage(ctx, id)
}

func (s *FileStore) FindExternalChatMessageByExternalID(ctx context.Context, chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatMessageFind, 1)
	if err != nil {
		return app.ExternalChatMessage{}, false, err
	}
	defer release()
	return s.inner.FindExternalChatMessageByExternalID(ctx, chatSessionID, externalMessageID)
}

func (s *FileStore) ListExternalChatMessages(ctx context.Context, chatSessionID string, limit int) ([]app.ExternalChatMessage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatMessageList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListExternalChatMessages(ctx, chatSessionID, limit)
}

func (s *FileStore) SaveMessageReceive(ctx context.Context, record app.MessageReceiveRecord) (app.MessageReceiveRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageReceiveSave, fileAdmissionCapacity)
	if err != nil {
		return app.MessageReceiveRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMessageReceiveSave, func(ctx context.Context) (app.MessageReceiveRecord, error) {
		return s.inner.SaveMessageReceive(ctx, record)
	})
}

func (s *FileStore) GetMessageReceive(ctx context.Context, id string) (app.MessageReceiveRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageReceiveGet, 1)
	if err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	defer release()
	return s.inner.GetMessageReceive(ctx, id)
}

func (s *FileStore) FindMessageReceive(ctx context.Context, sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageReceiveFind, 1)
	if err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	defer release()
	return s.inner.FindMessageReceive(ctx, sourceEndpointID, nativeMessageID)
}

func (s *FileStore) ListMessageReceives(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageReceiveRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageReceiveList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMessageReceives(ctx, ownerID, actorID, limit)
}

func (s *FileStore) SaveMessageDelivery(ctx context.Context, record app.MessageDeliveryRecord) (app.MessageDeliveryRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageDeliverySave, fileAdmissionCapacity)
	if err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMessageDeliverySave, func(ctx context.Context) (app.MessageDeliveryRecord, error) {
		return s.inner.SaveMessageDelivery(ctx, record)
	})
}

func (s *FileStore) GetMessageDelivery(ctx context.Context, id app.DeliveryID) (app.MessageDeliveryRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageDeliveryGet, 1)
	if err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	defer release()
	return s.inner.GetMessageDelivery(ctx, id)
}

func (s *FileStore) FindMessageDeliveryByIdempotency(ctx context.Context, ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageDeliveryFind, 1)
	if err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	defer release()
	return s.inner.FindMessageDeliveryByIdempotency(ctx, ownerID, actorID, idempotencyKey)
}

func (s *FileStore) ListMessageDeliveries(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageDeliveryRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageDeliveryList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMessageDeliveries(ctx, ownerID, actorID, limit)
}

func (s *FileStore) SaveChannelInboxUpdate(ctx context.Context, update app.ChannelInboxUpdate) (app.ChannelInboxUpdate, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChannelInboxUpdateSave, fileAdmissionCapacity)
	if err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationChannelInboxUpdateSave, func(ctx context.Context) (app.ChannelInboxUpdate, error) {
		return s.inner.SaveChannelInboxUpdate(ctx, update)
	})
}

func (s *FileStore) GetChannelInboxUpdate(ctx context.Context, id string) (app.ChannelInboxUpdate, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChannelInboxUpdateGet, 1)
	if err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	defer release()
	return s.inner.GetChannelInboxUpdate(ctx, id)
}

func (s *FileStore) FindChannelInboxUpdate(ctx context.Context, bindingID, externalID string) (app.ChannelInboxUpdate, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChannelInboxUpdateFind, 1)
	if err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	defer release()
	return s.inner.FindChannelInboxUpdate(ctx, bindingID, externalID)
}

func (s *FileStore) ListChannelInboxUpdates(ctx context.Context, channel, status string, readyBefore time.Time, limit int) ([]app.ChannelInboxUpdate, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChannelInboxUpdateList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListChannelInboxUpdates(ctx, channel, status, readyBefore, limit)
}

func (s *FileStore) SaveCredentialSecret(ctx context.Context, command CredentialSaveCommand) (app.CredentialSecret, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationCredentialSecretSave, fileAdmissionCapacity)
	if err != nil {
		return app.CredentialSecret{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationCredentialSecretSave, func(ctx context.Context) (app.CredentialSecret, error) {
		return s.inner.SaveCredentialSecret(ctx, command)
	})
}

func (s *FileStore) GetCredentialSecret(ctx context.Context, ref string) (app.CredentialSecret, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationCredentialSecretGet, 1)
	if err != nil {
		return app.CredentialSecret{}, false, err
	}
	defer release()
	return s.inner.GetCredentialSecret(ctx, ref)
}

func (s *FileStore) DeleteCredentialSecret(ctx context.Context, condition CredentialDeleteCondition) (app.CredentialSecret, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationCredentialSecretDelete, fileAdmissionCapacity)
	if err != nil {
		return app.CredentialSecret{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationCredentialSecretDelete, func(ctx context.Context) (app.CredentialSecret, error) {
		return s.inner.DeleteCredentialSecret(ctx, condition)
	})
}

func (s *FileStore) SaveBrowserAuthRecord(ctx context.Context, record app.BrowserAuthRecord) (app.BrowserAuthRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthSave, fileAdmissionCapacity)
	if err != nil {
		return app.BrowserAuthRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationBrowserAuthSave, func(ctx context.Context) (app.BrowserAuthRecord, error) {
		return s.inner.SaveBrowserAuthRecord(ctx, record)
	})
}

func (s *FileStore) GetBrowserAuthRecord(ctx context.Context, id string) (app.BrowserAuthRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthGet, 1)
	if err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	defer release()
	return s.inner.GetBrowserAuthRecord(ctx, id)
}

func (s *FileStore) FindBrowserAuthRecord(ctx context.Context, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthFind, 1)
	if err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	defer release()
	return s.inner.FindBrowserAuthRecord(ctx, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
}

func (s *FileStore) ListBrowserAuthRecords(ctx context.Context, ownerID, browserProfileID string) ([]app.BrowserAuthRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListBrowserAuthRecords(ctx, ownerID, browserProfileID)
}

func (s *FileStore) RevokeBrowserAuthRecord(ctx context.Context, id, reason string) (app.BrowserAuthRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthRevoke, fileAdmissionCapacity)
	if err != nil {
		return app.BrowserAuthRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationBrowserAuthRevoke, func(ctx context.Context) (app.BrowserAuthRecord, error) {
		return s.inner.RevokeBrowserAuthRecord(ctx, id, reason)
	})
}

func (s *FileStore) SaveBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock) (app.BrowserLoginBlock, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockSave, fileAdmissionCapacity)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationBrowserLoginBlockSave, func(ctx context.Context) (app.BrowserLoginBlock, error) {
		return s.inner.SaveBrowserLoginBlock(ctx, block)
	})
}

func (s *FileStore) UpdateBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationBrowserLoginBlockUpdate, func(ctx context.Context) (app.BrowserLoginBlock, error) {
		return s.inner.UpdateBrowserLoginBlock(ctx, block, expectedVersion)
	})
}

func (s *FileStore) GetBrowserLoginBlock(ctx context.Context, id string) (app.BrowserLoginBlock, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockGet, 1)
	if err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	defer release()
	return s.inner.GetBrowserLoginBlock(ctx, id)
}

func (s *FileStore) FindActiveBrowserLoginBlock(ctx context.Context, sessionID string) (app.BrowserLoginBlock, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockFindActive, 1)
	if err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	defer release()
	return s.inner.FindActiveBrowserLoginBlock(ctx, sessionID)
}

func (s *FileStore) ListBrowserLoginBlocks(ctx context.Context, sessionID, status string) ([]app.BrowserLoginBlock, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListBrowserLoginBlocks(ctx, sessionID, status)
}

func (s *FileStore) AddMemoryCandidate(ctx context.Context, candidate app.MemoryCandidate) (app.MemoryCandidate, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryCandidateAdd, fileAdmissionCapacity)
	if err != nil {
		return app.MemoryCandidate{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMemoryCandidateAdd, func(ctx context.Context) (app.MemoryCandidate, error) {
		return s.inner.AddMemoryCandidate(ctx, candidate)
	})
}

func (s *FileStore) ResolveMemoryCandidate(ctx context.Context, id, status string) (app.MemoryCandidate, *app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryCandidateResolve, fileAdmissionCapacity)
	if err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	defer release()
	type result struct {
		candidate app.MemoryCandidate
		memory    *app.Memory
	}
	out, err := runFileCommand(s, ctx, OperationMemoryCandidateResolve, func(ctx context.Context) (result, error) {
		candidate, memory, err := s.inner.ResolveMemoryCandidate(ctx, id, status)
		return result{candidate: candidate, memory: memory}, err
	})
	return out.candidate, out.memory, err
}

func (s *FileStore) ListMemoryCandidates(ctx context.Context, status string) ([]app.MemoryCandidate, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryCandidateList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMemoryCandidates(ctx, status)
}

func (s *FileStore) SearchMemories(ctx context.Context, query string) ([]app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemorySearch, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.SearchMemories(ctx, query)
}

func (s *FileStore) UpdateMemory(ctx context.Context, id, kind, content string) (app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.Memory{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMemoryUpdate, func(ctx context.Context) (app.Memory, error) {
		return s.inner.UpdateMemory(ctx, id, kind, content)
	})
}

func (s *FileStore) DeleteMemory(ctx context.Context, id string) (app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryDelete, fileAdmissionCapacity)
	if err != nil {
		return app.Memory{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMemoryDelete, func(ctx context.Context) (app.Memory, error) {
		return s.inner.DeleteMemory(ctx, id)
	})
}

func (s *FileStore) PruneMemories(ctx context.Context, cutoff time.Time) ([]app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryPrune, fileAdmissionCapacity)
	if err != nil {
		return nil, err
	}
	defer release()
	out, _, err := runFileOptionalCommand(s, ctx, OperationMemoryPrune, func(ctx context.Context) ([]app.Memory, bool, error) {
		pruned, err := s.inner.PruneMemories(ctx, cutoff)
		return pruned, len(pruned) > 0, err
	})
	return out, err
}

func (s *FileStore) AddAudit(ctx context.Context, event app.AuditEvent) error {
	ctx, release, err := s.admitMigrated(ctx, OperationAuditAdd, fileAdmissionCapacity)
	if err != nil {
		return err
	}
	defer release()
	_, err = runFileCommand(s, ctx, OperationAuditAdd, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.inner.AddAudit(ctx, event)
	})
	return err
}

func (s *FileStore) ListAudit(ctx context.Context, sessionID string) ([]app.AuditEvent, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationAuditList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListAudit(ctx, sessionID)
}

func (s *FileStore) EventsAfter(ctx context.Context, sessionID, after string) ([]app.Event, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationAuditEventsAfter, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.EventsAfter(ctx, sessionID, after)
}

func (s *FileStore) MessageEventHead(ctx context.Context, sessionID string) (string, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationMessageHead, 1)
	if err != nil {
		return "", err
	}
	defer release()
	return s.inner.MessageEventHead(ctx, sessionID)
}

func (s *FileStore) MessageEventsAfter(ctx context.Context, sessionID, after string, limit int) (MessageEventPage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationMessagesAfter, 1)
	if err != nil {
		return MessageEventPage{}, err
	}
	defer release()
	return s.inner.MessageEventsAfter(ctx, sessionID, after, limit)
}

func (s *FileStore) SaveEvalRun(ctx context.Context, run app.EvalRun) (app.EvalRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEvaluationSave, fileAdmissionCapacity)
	if err != nil {
		return app.EvalRun{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationEvaluationSave, func(ctx context.Context) (app.EvalRun, error) {
		return s.inner.SaveEvalRun(ctx, run)
	})
}

func (s *FileStore) GetEvalRun(ctx context.Context, id string) (app.EvalRun, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEvaluationGet, 1)
	if err != nil {
		return app.EvalRun{}, false, err
	}
	defer release()
	return s.inner.GetEvalRun(ctx, id)
}

func (s *FileStore) ListEvalRuns(ctx context.Context) ([]app.EvalRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEvaluationList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListEvalRuns(ctx)
}

func (s *FileStore) SaveArtifactObject(ctx context.Context, object app.ArtifactObject) (app.ArtifactObject, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationArtifactMetadataSave, fileAdmissionCapacity)
	if err != nil {
		return app.ArtifactObject{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationArtifactMetadataSave, func(ctx context.Context) (app.ArtifactObject, error) {
		return s.inner.SaveArtifactObject(ctx, object)
	})
}

func (s *FileStore) ListArtifactObjects(ctx context.Context, limit int) ([]app.ArtifactObject, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationArtifactMetadataList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListArtifactObjects(ctx, limit)
}

func (s *FileStore) FindArtifactObjectByURI(ctx context.Context, uri, sessionID, runID string) (app.ArtifactObject, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationArtifactMetadataFindByURI, 1)
	if err != nil {
		return app.ArtifactObject{}, false, err
	}
	defer release()
	return s.inner.FindArtifactObjectByURI(ctx, uri, sessionID, runID)
}

func (s *FileStore) SaveEpisodeSummary(ctx context.Context, summary app.EpisodeSummary) (app.EpisodeSummary, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEpisodeSummarySave, fileAdmissionCapacity)
	if err != nil {
		return app.EpisodeSummary{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationEpisodeSummarySave, func(ctx context.Context) (app.EpisodeSummary, error) {
		return s.inner.SaveEpisodeSummary(ctx, summary)
	})
}

func (s *FileStore) ListEpisodeSummaries(ctx context.Context, sessionID string) ([]app.EpisodeSummary, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEpisodeSummaryList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListEpisodeSummaries(ctx, sessionID)
}

func (s *FileStore) persistSnapshot() error {
	if s.path == "" {
		return nil
	}
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

func isEncryptedSnapshotEnvelope(raw []byte) bool {
	var envelope encryptedSnapshot
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	return envelope.Ciphertext != ""
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
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Ciphertext != "" {
		if envelope.Version != 1 {
			return nil, fmt.Errorf("unsupported state encryption version %d", envelope.Version)
		}
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
