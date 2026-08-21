package store

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type s0RepositoryCharacterizationCase struct {
	checks map[string]func(*testing.T, Store)
}

func s0RepositoryChecks(check func(*testing.T, Store, string), dimensions ...string) s0RepositoryCharacterizationCase {
	if check == nil {
		panic("S0 repository characterization check is nil")
	}
	checks := make(map[string]func(*testing.T, Store), len(dimensions))
	for _, dimension := range dimensions {
		if dimension == "" {
			panic("S0 repository characterization dimension is empty")
		}
		if _, exists := checks[dimension]; exists {
			panic("duplicate S0 repository characterization dimension: " + dimension)
		}
		dimension := dimension
		checks[dimension] = func(t *testing.T, st Store) {
			check(t, st, dimension)
		}
	}
	return s0RepositoryCharacterizationCase{checks: checks}
}

var s0RepositoryCharacterizationCases = map[string]s0RepositoryCharacterizationCase{
	"OwnerRepository":               s0RepositoryChecks(characterizeS0OwnerRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionDuplicate),
	"ClientRepository":              s0RepositoryChecks(characterizeS0ClientRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope, s0DimensionDuplicate, s0DimensionConflictDeletion),
	"ISCPOnboardingRepository":      s0RepositoryChecks(characterizeS0ISCPOnboardingRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope, s0DimensionDuplicate, s0DimensionConflictDeletion),
	"CredentialRepository":          s0RepositoryChecks(characterizeS0CredentialRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionDuplicate, s0DimensionConflictDeletion),
	"SessionRepository":             s0RepositoryChecks(characterizeS0SessionRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope, s0DimensionConflictDeletion),
	"ConversationRepository":        s0RepositoryChecks(characterizeS0ConversationRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionDuplicate),
	"RunRepository":                 s0RepositoryChecks(characterizeS0RunRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope),
	"DocumentRepository":            s0RepositoryChecks(characterizeS0DocumentRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionDuplicate),
	"ApprovalRepository":            s0RepositoryChecks(characterizeS0ApprovalRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope, s0DimensionDuplicate, s0DimensionConflictDeletion),
	"ScheduleRepository":            s0RepositoryChecks(characterizeS0ScheduleRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope, s0DimensionDuplicate),
	"ConnectorRepository":           s0RepositoryChecks(characterizeS0ConnectorRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionDuplicate),
	"PassiveNotificationRepository": s0RepositoryChecks(characterizeS0PassiveNotificationRepository, s0DimensionSuccess, s0DimensionAbsence),
	"ExternalChatRepository":        s0RepositoryChecks(characterizeS0ExternalChatRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope),
	"DeliveryRecordRepository":      s0RepositoryChecks(characterizeS0DeliveryRecordRepository, s0DimensionSuccess, s0DimensionAbsence),
	"MCPRepository":                 s0RepositoryChecks(characterizeS0MCPRepository, s0DimensionSuccess, s0DimensionAbsence),
	"BrowserStateRepository":        s0RepositoryChecks(characterizeS0BrowserStateRepository, s0DimensionSuccess, s0DimensionAbsence),
	"MemoryRepository":              s0RepositoryChecks(characterizeS0MemoryRepository, s0DimensionSuccess, s0DimensionAbsence),
	"AuditRepository":               s0RepositoryChecks(characterizeS0AuditRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope, s0DimensionEventSequence),
	"EvaluationRepository":          s0RepositoryChecks(characterizeS0EvaluationRepository, s0DimensionSuccess, s0DimensionAbsence, s0DimensionOrderScope, s0DimensionDuplicate),
	"ArtifactMetadataRepository":    s0RepositoryChecks(characterizeS0ArtifactMetadataRepository, s0DimensionSuccess, s0DimensionAbsence),
}

func TestS0BackendNeutralRepositoryCharacterization(t *testing.T) {
	cases := s0RepositoryCharacterizationCases
	if len(cases) != len(s0RepositoryMethods) {
		t.Fatalf("backend-neutral repository cases = %d, want %d", len(cases), len(s0RepositoryMethods))
	}
	for repository := range s0RepositoryMethods {
		if len(cases[repository].checks) == 0 {
			t.Errorf("backend-neutral repository case is missing for %s", repository)
		}
	}
	for repository := range cases {
		if _, ok := s0RepositoryMethods[repository]; !ok {
			t.Errorf("backend-neutral repository cases include unknown %s", repository)
		}
	}
	repositories := make([]string, 0, len(cases))
	for repository := range cases {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	for _, repository := range repositories {
		t.Run(repository, func(t *testing.T) {
			characterization := cases[repository]
			dimensions := make([]string, 0, len(characterization.checks))
			for dimension := range characterization.checks {
				dimensions = append(dimensions, dimension)
			}
			sort.Strings(dimensions)
			for _, dimension := range dimensions {
				t.Run(s0DimensionSubtestName(dimension), func(t *testing.T) {
					for _, backend := range newS0RepositoryBackends(t) {
						t.Run(backend.name, func(t *testing.T) {
							characterization.checks[dimension](t, backend.store)
						})
					}
				})
			}
		})
	}
}

func s0DimensionSubtestName(dimension string) string {
	return strings.NewReplacer("/", "_", " ", "_").Replace(dimension)
}

func newS0RepositoryBackends(t *testing.T) []s0CharacterizationBackend {
	t.Helper()
	fileStore, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return []s0CharacterizationBackend{{name: "memory", store: NewMemoryStore()}, {name: "file", store: fileStore}}
}

func characterizeS0OwnerRepository(t *testing.T, st Store, dimension string) {
	profile := app.OwnerProfile{ID: "owner-s0", Source: "test", ExternalRef: "external-s0", DisplayName: "first"}
	switch dimension {
	case s0DimensionSuccess:
		mustSaveOwnerProfile(t, st, profile)
		if got, ok := mustFindOwnerProfileByExternalRef(t, st, profile.Source, profile.ExternalRef); !ok || got.ID != profile.ID {
			t.Fatalf("owner profile save/lookup = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := mustGetOwnerProfileByID(t, st, "missing"); ok {
			t.Fatal("missing owner profile was found")
		}
	case s0DimensionDuplicate:
		mustSaveOwnerProfile(t, st, profile)
		profile.DisplayName = "updated"
		mustSaveOwnerProfile(t, st, profile)
		matches := 0
		for _, got := range mustListOwnerProfiles(t, st) {
			if got.ID == profile.ID {
				matches++
				if got.DisplayName != "updated" {
					t.Fatalf("owner profile overwrite = %#v", got)
				}
			}
		}
		if matches != 1 {
			t.Fatalf("owner profile ID occurrences = %d, want 1", matches)
		}
	default:
		t.Fatalf("unexpected OwnerRepository dimension %q", dimension)
	}
}

func characterizeS0ClientRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		client := mustClaimTestClient(t, st, app.Client{ID: "client-s0", Name: "client", TokenHash: "hash-s0"})
		if got, ok, err := st.FindClientByTokenHash(t.Context(), client.TokenHash); err != nil || !ok || got.ID != client.ID {
			t.Fatalf("client save/token lookup = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok, err := st.GetClient(t.Context(), "missing"); err != nil || ok {
			t.Fatal("missing client was found")
		}
	case s0DimensionOrderScope:
		mustClaimTestClient(t, st, app.Client{ID: "client-old", Name: "old", TokenHash: "hash-old"})
		mustClaimTestClient(t, st, app.Client{ID: "client-new", Name: "new", TokenHash: "hash-new"})
		if got, err := st.ListClients(t.Context()); err != nil || len(got) != 2 || got[0].ID != "client-new" || got[1].ID != "client-old" {
			t.Fatalf("client order = %#v", got)
		}
	case s0DimensionDuplicate:
		pairing := app.PairingCode{ID: "pair-duplicate", CodeHash: "pair-duplicate-hash", Status: "pending", ExpiresAt: time.Now().UTC().Add(time.Hour)}
		if _, err := st.SavePairingCode(t.Context(), pairing); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SavePairingCode(t.Context(), pairing); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("duplicate pairing save = %v", err)
		}
	case s0DimensionConflictDeletion:
		if _, err := st.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-s0", CodeHash: "pair-hash", Status: "pending", ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.ClaimPairingCode(t.Context(), "pair-s0", app.Client{ID: "client-s0", Name: "client", TokenHash: "client-s0-hash"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.ClaimPairingCode(t.Context(), "pair-s0", app.Client{ID: "other-client", Name: "other", TokenHash: "other-hash"}); err == nil {
			t.Fatal("claimed pairing code was claimed twice")
		}
		if revoked, err := st.RevokeClient(t.Context(), "client-s0"); err != nil || revoked.RevokedAt == nil {
			t.Fatalf("client revoke = %#v err=%v", revoked, err)
		}
	default:
		t.Fatalf("unexpected ClientRepository dimension %q", dimension)
	}
}

func characterizeS0ISCPOnboardingRepository(t *testing.T, st Store, dimension string) {
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	switch dimension {
	case s0DimensionSuccess:
		receipt := testISCPOnboarding(base, "iscp-s0", "owner-s0")
		if _, err := st.SaveISCPOnboarding(context.Background(), receipt); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := st.GetISCPOnboarding(context.Background(), receipt.ID); err != nil || !ok || got.OwnerID != receipt.OwnerID {
			t.Fatalf("ISCP onboarding save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok, err := st.GetISCPOnboarding(context.Background(), "missing"); err != nil || ok {
			t.Fatal("missing ISCP onboarding was found")
		}
	case s0DimensionOrderScope:
		for _, receipt := range []app.ISCPOnboarding{
			testISCPOnboarding(base, "iscp-old", "owner-s0"),
			testISCPOnboarding(base.Add(time.Minute), "iscp-new", "owner-s0"),
			testISCPOnboarding(base.Add(2*time.Minute), "iscp-other", "other-owner"),
		} {
			if _, err := st.SaveISCPOnboarding(context.Background(), receipt); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := st.ListISCPOnboardings(context.Background(), "owner-s0"); err != nil || len(got) != 2 || got[0].ID != "iscp-new" || got[1].ID != "iscp-old" {
			t.Fatalf("ISCP onboarding order/scope = %#v", got)
		}
	case s0DimensionDuplicate:
		receipt := testISCPOnboarding(base, "iscp-s0", "owner-s0")
		if _, err := st.SaveISCPOnboarding(context.Background(), receipt); err != nil {
			t.Fatal(err)
		}
		_, _ = st.SaveISCPOnboarding(context.Background(), receipt)
		if got, err := st.ListISCPOnboardings(context.Background(), "owner-s0"); err != nil || len(got) != 1 {
			t.Fatalf("duplicate ISCP onboarding created extra records: %#v", got)
		}
	case s0DimensionConflictDeletion:
		receipt := testISCPOnboarding(base, "iscp-s0", "owner-s0")
		if _, err := st.SaveISCPOnboarding(context.Background(), receipt); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SaveISCPOnboarding(context.Background(), receipt); !errors.Is(err, ErrISCPOnboardingConflict) {
			t.Fatalf("duplicate ISCP onboarding error = %v", err)
		}
	default:
		t.Fatalf("unexpected ISCPOnboardingRepository dimension %q", dimension)
	}
}

func characterizeS0CredentialRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		if _, err := st.SaveCredentialSecret(context.Background(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-s0", Kind: "token", Value: "first"})); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := st.GetCredentialSecret(context.Background(), "credential-s0"); err != nil || !ok || got.Value != "first" {
			t.Fatalf("credential save/get = %#v ok=%v err=%v", got, ok, err)
		}
	case s0DimensionAbsence:
		if _, ok, err := st.GetCredentialSecret(context.Background(), "missing"); err != nil || ok {
			t.Fatalf("missing credential result ok=%v err=%v", ok, err)
		}
	case s0DimensionDuplicate:
		created, err := st.SaveCredentialSecret(context.Background(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-s0", Kind: "token", Value: "first"}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.SaveCredentialSecret(context.Background(), NewCredentialReplace(created, app.CredentialSecret{Ref: "credential-s0", Kind: "token", Value: "updated"})); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := st.GetCredentialSecret(context.Background(), "credential-s0"); err != nil || !ok || got.Value != "updated" {
			t.Fatalf("credential overwrite = %#v ok=%v err=%v", got, ok, err)
		}
	case s0DimensionConflictDeletion:
		created, err := st.SaveCredentialSecret(context.Background(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-s0", Kind: "token", Value: "first"}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DeleteCredentialSecret(context.Background(), NewCredentialDeleteCondition(created)); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := st.GetCredentialSecret(context.Background(), "credential-s0"); err != nil || ok {
			t.Fatalf("deleted credential result ok=%v err=%v", ok, err)
		}
	default:
		t.Fatalf("unexpected CredentialRepository dimension %q", dimension)
	}
}

func characterizeS0SessionRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		session := mustCreateSession(t, st, "session")
		updated, err := st.UpdateSessionTitle(t.Context(), session.ID, "updated")
		if err != nil || updated.Title != "updated" {
			t.Fatalf("session create/update = %#v err=%v", updated, err)
		}
	case s0DimensionAbsence:
		if _, ok := mustGetSession(t, st, "missing"); ok {
			t.Fatal("missing session was found")
		}
	case s0DimensionOrderScope:
		hidden := mustCreateSessionWithScope(t, st, "hidden", "owner-s0", "/workspace", "test", true)
		visible := mustCreateSession(t, st, "visible")
		if got := mustListSessions(t, st); len(got) != 1 || got[0].ID != visible.ID || got[0].ID == hidden.ID {
			t.Fatalf("visible session scope = %#v", got)
		}
	case s0DimensionConflictDeletion:
		session := mustCreateSession(t, st, "session")
		if _, err := st.DeleteSession(t.Context(), session.ID); err != nil {
			t.Fatal(err)
		}
		if _, ok := mustGetSession(t, st, session.ID); ok {
			t.Fatal("deleted session was found")
		}
	default:
		t.Fatalf("unexpected SessionRepository dimension %q", dimension)
	}
}

func characterizeS0ConversationRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		session := mustCreateSession(t, st, "conversation")
		message := mustAddMessage(t, st, app.Message{ID: "message-s0", SessionID: session.ID, Role: "user", Content: "first"})
		if got := mustListMessages(t, st, session.ID); len(got) != 1 || got[0].ID != message.ID {
			t.Fatalf("message append/list = %#v", got)
		}
	case s0DimensionAbsence:
		if got := mustListMessages(t, st, "missing"); len(got) != 0 {
			t.Fatalf("missing conversation messages = %#v", got)
		}
	case s0DimensionDuplicate:
		session := mustCreateSession(t, st, "conversation")
		message := mustAddMessage(t, st, app.Message{ID: "message-s0", SessionID: session.ID, Role: "user", Content: "first"})
		reused := mustAddMessage(t, st, app.Message{ID: message.ID, SessionID: session.ID, Role: "user", Content: "replacement"})
		if got := mustListMessages(t, st, session.ID); len(got) != 1 || reused.ID != message.ID || got[0].Content != "first" {
			t.Fatalf("message duplicate reuse = %#v reused=%#v", got, reused)
		}
	default:
		t.Fatalf("unexpected ConversationRepository dimension %q", dimension)
	}
}

func characterizeS0RunRepository(t *testing.T, st Store, dimension string) {
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	switch dimension {
	case s0DimensionSuccess:
		run := app.AgentRun{ID: "run-s0", SessionID: "session-s0", State: "running", StartedAt: base}
		st.SaveRun(run)
		if got, ok := st.GetRun(run.ID); !ok || got.State != run.State {
			t.Fatalf("run save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetRun("missing"); ok {
			t.Fatal("missing run was found")
		}
	case s0DimensionOrderScope:
		st.SaveRun(app.AgentRun{ID: "run-old", SessionID: "session-s0", State: "completed", StartedAt: base})
		st.SaveRun(app.AgentRun{ID: "run-new", SessionID: "session-s0", State: "completed", StartedAt: base.Add(time.Minute)})
		st.SaveRun(app.AgentRun{ID: "run-other", SessionID: "other-session", State: "completed", StartedAt: base.Add(2 * time.Minute)})
		if got := st.ListRuns("session-s0"); len(got) != 2 || got[0].ID != "run-new" || got[1].ID != "run-old" {
			t.Fatalf("run order/scope = %#v", got)
		}
	default:
		t.Fatalf("unexpected RunRepository dimension %q", dimension)
	}
}

func characterizeS0DocumentRepository(t *testing.T, st Store, dimension string) {
	record := app.DocumentRecord{ID: "document-s0", OwnerID: "owner-s0", SessionID: "session-s0", Name: "first", LastActivityAt: time.Now().UTC()}
	switch dimension {
	case s0DimensionSuccess:
		saved := st.SaveDocumentRecord(record)
		if got, ok := st.GetDocumentRecord(saved.ID); !ok || got.Name != record.Name {
			t.Fatalf("document save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetDocumentRecord("missing"); ok {
			t.Fatal("missing document was found")
		}
	case s0DimensionDuplicate:
		record = st.SaveDocumentRecord(record)
		record.Name = "updated"
		st.SaveDocumentRecord(record)
		if got := st.ListDocumentRecords("owner-s0", "session-s0", 0); len(got) != 1 || got[0].Name != "updated" {
			t.Fatalf("document overwrite created duplicates: %#v", got)
		}
	default:
		t.Fatalf("unexpected DocumentRepository dimension %q", dimension)
	}
}

func characterizeS0ApprovalRepository(t *testing.T, st Store, dimension string) {
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	switch dimension {
	case s0DimensionSuccess:
		approval := app.Approval{ID: "approval-s0", Source: app.ApprovalSourceHappyTeamPlan, ExternalID: "external-s0", Status: "pending", Summary: "first", CreatedAt: base}
		st.SaveApproval(approval)
		if got, ok := st.FindApprovalByExternalRef(approval.Source, approval.ExternalID); !ok || got.ID != approval.ID {
			t.Fatalf("approval save/lookup = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetApproval("missing"); ok {
			t.Fatal("missing approval was found")
		}
	case s0DimensionOrderScope:
		st.SaveApproval(app.Approval{ID: "approval-rejected", Status: "rejected", CreatedAt: base.Add(2 * time.Minute)})
		st.SaveApproval(app.Approval{ID: "approval-old", Status: "pending", CreatedAt: base})
		st.SaveApproval(app.Approval{ID: "approval-new", Status: "pending", CreatedAt: base.Add(time.Minute)})
		if got := st.ListApprovals("pending"); len(got) != 2 || got[0].ID != "approval-new" || got[1].ID != "approval-old" {
			t.Fatalf("approval order/filter = %#v", got)
		}
	case s0DimensionDuplicate:
		approval := app.Approval{ID: "approval-s0", Status: "pending", Summary: "first", CreatedAt: base}
		st.SaveApproval(approval)
		approval.Summary = "updated"
		st.SaveApproval(approval)
		if got := st.ListApprovals("pending"); len(got) != 1 || got[0].Summary != "updated" {
			t.Fatalf("approval overwrite created duplicates: %#v", got)
		}
	case s0DimensionConflictDeletion:
		approval := app.Approval{ID: "approval-s0", Status: "pending", CreatedAt: base}
		st.SaveApproval(approval)
		if _, err := st.ResolveApproval(approval.ID, "approved", "done"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpdatePendingApproval(approval); err == nil {
			t.Fatal("resolved approval accepted pending update")
		}
	default:
		t.Fatalf("unexpected ApprovalRepository dimension %q", dimension)
	}
}

func characterizeS0ScheduleRepository(t *testing.T, st Store, dimension string) {
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	switch dimension {
	case s0DimensionSuccess:
		reminder := st.SaveReminder(app.Reminder{ID: "reminder-s0", Text: "reminder", DueTime: base, Status: "pending"})
		st.SaveReminderDelivery(app.ReminderDelivery{ID: "delivery-s0", ReminderID: reminder.ID, Status: "sent", Attempt: 1})
		if got := st.ListReminderDeliveries(reminder.ID); len(got) != 1 || got[0].ID != "delivery-s0" {
			t.Fatalf("reminder delivery save/list = %#v", got)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetReminder("missing"); ok {
			t.Fatal("missing reminder was found")
		}
	case s0DimensionOrderScope:
		st.SaveReminder(app.Reminder{ID: "reminder-late", DueTime: base.Add(time.Hour), Status: "pending"})
		st.SaveReminder(app.Reminder{ID: "reminder-early", DueTime: base, Status: "pending"})
		st.SaveReminder(app.Reminder{ID: "reminder-done", DueTime: base.Add(-time.Hour), Status: "sent"})
		if got := st.ListReminders(app.ReminderFilter{Status: "pending"}); len(got) != 2 || got[0].ID != "reminder-early" || got[1].ID != "reminder-late" {
			t.Fatalf("reminder order/filter = %#v", got)
		}
	case s0DimensionDuplicate:
		reminder := st.SaveReminder(app.Reminder{ID: "reminder-s0", Text: "first", DueTime: base, Status: "pending"})
		reminder.Text = "updated"
		st.SaveReminder(reminder)
		if got := st.ListReminders(app.ReminderFilter{Status: "pending"}); len(got) != 1 || got[0].Text != "updated" {
			t.Fatalf("reminder overwrite created duplicates: %#v", got)
		}
	default:
		t.Fatalf("unexpected ScheduleRepository dimension %q", dimension)
	}
}

func characterizeS0ConnectorRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		setting, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner-s0", Channel: "telegram", Enabled: true}, 0)
		if err != nil || setting.Version != 1 {
			t.Fatalf("connector setting create = %#v err=%v", setting, err)
		}
		binding := mustCreateNotificationBindingFixture(t, st, app.NotificationBinding{ID: "binding-s0", OwnerID: "owner-s0", Channel: "telegram", Status: "active"})
		if got, ok := mustGetNotificationBindingFixture(t, st, binding.ID); !ok || got.Channel != binding.Channel {
			t.Fatalf("notification binding save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok, err := st.GetConnectorSetting(t.Context(), "owner-s0", "telegram"); err != nil || ok {
			t.Fatal("missing connector setting was found")
		}
	case s0DimensionDuplicate:
		binding := mustCreateNotificationBindingFixture(t, st, app.NotificationBinding{ID: "binding-s0", OwnerID: "owner-s0", Channel: "telegram", Status: "active"})
		replacement := binding
		replacement.Status = "revoked"
		mustUpdateNotificationBindingFixture(t, st, binding, replacement)
		if got := mustListNotificationBindingsFixture(t, st, "telegram", ""); len(got) != 1 || got[0].Status != "revoked" {
			t.Fatalf("notification binding overwrite created duplicates: %#v", got)
		}
	default:
		t.Fatalf("unexpected ConnectorRepository dimension %q", dimension)
	}
}

func characterizeS0PassiveNotificationRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		notification := testPassiveNotification("passive-s0", "endpoint-s0", "delivery-s0", "fingerprint-s0")
		notification.OwnerID = "owner-s0"
		created, inserted, err := st.CreatePassiveNotification(notification)
		if err != nil || !inserted || created.ID != notification.ID {
			t.Fatalf("passive notification create = %#v inserted=%v err=%v", created, inserted, err)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetPassiveNotification("owner-s0", "missing"); ok {
			t.Fatal("missing passive notification was found")
		}
	default:
		t.Fatalf("unexpected PassiveNotificationRepository dimension %q", dimension)
	}
}

func characterizeS0ExternalChatRepository(t *testing.T, st Store, dimension string) {
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	switch dimension {
	case s0DimensionSuccess:
		chat := st.SaveExternalChatSession(app.ExternalChatSession{ID: "chat-s0", BindingID: "binding-s0", Channel: "telegram", ExternalChatID: "chat", Status: "active"})
		if got, ok := st.GetExternalChatSession(chat.ID); !ok || got.Channel != "telegram" {
			t.Fatalf("external chat session save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetExternalChatSession("missing"); ok {
			t.Fatal("missing external chat session was found")
		}
	case s0DimensionOrderScope:
		st.SaveExternalChatSession(app.ExternalChatSession{ID: "chat-old", BindingID: "binding-old", Channel: "telegram", ExternalChatID: "old", Status: "active", UpdatedAt: base})
		st.SaveExternalChatSession(app.ExternalChatSession{ID: "chat-new", BindingID: "binding-new", Channel: "telegram", ExternalChatID: "new", Status: "active", UpdatedAt: base.Add(time.Minute)})
		st.SaveExternalChatSession(app.ExternalChatSession{ID: "chat-other", BindingID: "binding-other", Channel: "weixin", ExternalChatID: "other", Status: "revoked", UpdatedAt: base.Add(2 * time.Minute)})
		if got := st.ListExternalChatSessions("telegram", "active"); len(got) != 2 || got[0].ID != "chat-new" || got[1].ID != "chat-old" {
			t.Fatalf("external chat order/scope = %#v", got)
		}
	default:
		t.Fatalf("unexpected ExternalChatRepository dimension %q", dimension)
	}
}

func characterizeS0DeliveryRecordRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		record := st.SaveMessageReceive(app.MessageReceiveRecord{ID: "receive-s0", OwnerID: "owner-s0", ActorID: "actor-s0", SourceEndpointID: "endpoint-s0", NativeMessageID: "native-s0", Status: "received"})
		if got, ok := st.GetMessageReceive(record.ID); !ok || got.Direction != app.MessageDirectionReceive {
			t.Fatalf("receive record save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetMessageReceive("missing"); ok {
			t.Fatal("missing receive record was found")
		}
	default:
		t.Fatalf("unexpected DeliveryRecordRepository dimension %q", dimension)
	}
}

func characterizeS0MCPRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(time.Now().UTC(), "mcp-s0-hash"))
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := st.GetMCPAccessTicket(ticket.ID); !ok || got.SecretHash != ticket.SecretHash {
			t.Fatalf("MCP ticket save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetMCPAccessTicket("missing"); ok {
			t.Fatal("missing MCP ticket was found")
		}
	default:
		t.Fatalf("unexpected MCPRepository dimension %q", dimension)
	}
}

func characterizeS0BrowserStateRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		record := st.SaveBrowserAuthRecord(app.BrowserAuthRecord{ID: "browser-auth-s0", OwnerID: "owner-s0", BrowserProfileID: "profile-s0", SiteOrigin: "https://example.com"})
		if got, ok := st.GetBrowserAuthRecord(record.ID); !ok || got.SiteOrigin != record.SiteOrigin {
			t.Fatalf("browser auth record save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetBrowserAuthRecord("missing"); ok {
			t.Fatal("missing browser auth record was found")
		}
	default:
		t.Fatalf("unexpected BrowserStateRepository dimension %q", dimension)
	}
}

func characterizeS0MemoryRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		candidate := st.AddMemoryCandidate(app.MemoryCandidate{ID: "candidate-s0", SessionID: "session-s0", RunID: "run-s0", Status: "pending", Content: "remember"})
		if got := st.ListMemoryCandidates("pending"); len(got) != 1 || got[0].ID != candidate.ID {
			t.Fatalf("memory candidate add/list = %#v", got)
		}
	case s0DimensionAbsence:
		if got := st.SearchMemories("missing"); len(got) != 0 {
			t.Fatalf("missing memory search = %#v", got)
		}
	default:
		t.Fatalf("unexpected MemoryRepository dimension %q", dimension)
	}
}

func characterizeS0AuditRepository(t *testing.T, st Store, dimension string) {
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	switch dimension {
	case s0DimensionSuccess:
		st.AddAudit(app.AuditEvent{ID: "audit-s0", SessionID: "session-s0", Type: "saved", Time: base})
		if got := st.ListAudit("session-s0"); len(got) != 1 || got[0].ID != "audit-s0" {
			t.Fatalf("audit add/list = %#v", got)
		}
	case s0DimensionAbsence:
		if got := st.ListAudit("missing-session"); len(got) != 0 {
			t.Fatalf("missing audit list = %#v", got)
		}
	case s0DimensionOrderScope:
		st.AddAudit(app.AuditEvent{ID: "audit-old", SessionID: "session-s0", Type: "old", Time: base})
		st.AddAudit(app.AuditEvent{ID: "audit-new", SessionID: "session-s0", Type: "new", Time: base.Add(time.Minute)})
		st.AddAudit(app.AuditEvent{ID: "audit-other", SessionID: "other-session", Type: "other", Time: base.Add(2 * time.Minute)})
		if got := st.ListAudit("session-s0"); len(got) != 2 || got[0].ID != "audit-new" || got[1].ID != "audit-old" {
			t.Fatalf("audit order/scope = %#v", got)
		}
	case s0DimensionEventSequence:
		mustSaveOwnerProfile(t, st, app.OwnerProfile{ID: "owner-event-old", DisplayName: "old"})
		mustSaveOwnerProfile(t, st, app.OwnerProfile{ID: "owner-event-new", DisplayName: "new"})
		events := st.EventsAfter("", "")
		if len(events) != 2 || events[0].Type != "owner_profile.updated" || events[1].Type != "owner_profile.updated" {
			t.Fatalf("event order = %#v", events)
		}
		if after := st.EventsAfter("", events[0].ID); len(after) != 1 || after[0].ID != events[1].ID {
			t.Fatalf("event after-cursor sequence = %#v", after)
		}
	default:
		t.Fatalf("unexpected AuditRepository dimension %q", dimension)
	}
}

func characterizeS0EvaluationRepository(t *testing.T, st Store, dimension string) {
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	switch dimension {
	case s0DimensionSuccess:
		run := app.EvalRun{ID: "eval-s0", Status: "passed", Summary: "saved", StartedAt: base}
		st.SaveEvalRun(run)
		if got, ok := st.GetEvalRun(run.ID); !ok || got.Summary != run.Summary {
			t.Fatalf("evaluation save/get = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.GetEvalRun("missing"); ok {
			t.Fatal("missing evaluation run was found")
		}
	case s0DimensionOrderScope:
		st.SaveEvalRun(app.EvalRun{ID: "eval-old", Status: "passed", StartedAt: base})
		st.SaveEvalRun(app.EvalRun{ID: "eval-new", Status: "passed", StartedAt: base.Add(time.Minute)})
		if got := st.ListEvalRuns(); len(got) != 2 || got[0].ID != "eval-new" || got[1].ID != "eval-old" {
			t.Fatalf("evaluation order = %#v", got)
		}
	case s0DimensionDuplicate:
		run := app.EvalRun{ID: "eval-s0", Status: "failed", Summary: "first", StartedAt: base}
		st.SaveEvalRun(run)
		run.Status = "passed"
		run.Summary = "updated"
		st.SaveEvalRun(run)
		if got := st.ListEvalRuns(); len(got) != 1 || got[0].Summary != "updated" {
			t.Fatalf("evaluation overwrite created duplicates: %#v", got)
		}
	default:
		t.Fatalf("unexpected EvaluationRepository dimension %q", dimension)
	}
}

func characterizeS0ArtifactMetadataRepository(t *testing.T, st Store, dimension string) {
	switch dimension {
	case s0DimensionSuccess:
		object := app.ArtifactObject{ID: "artifact-s0", URI: "artifact://s0", Key: "s0", CreatedAt: time.Now().UTC()}
		st.SaveArtifactObject(object)
		if got, ok := st.FindArtifactObjectByURI(object.URI, "", ""); !ok || got.ID != object.ID {
			t.Fatalf("artifact save/lookup = %#v ok=%v", got, ok)
		}
	case s0DimensionAbsence:
		if _, ok := st.FindArtifactObjectByURI("artifact://missing", "", ""); ok {
			t.Fatal("missing artifact was found")
		}
	default:
		t.Fatalf("unexpected ArtifactMetadataRepository dimension %q", dimension)
	}
}

func TestS0FileRepositoryRestartGaps(t *testing.T) {
	t.Run("ScheduleRepository", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "schedule.json")
		st, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		reminder := st.SaveReminder(app.Reminder{ID: "reminder-restart", Text: "restart", DueTime: time.Now().UTC(), Status: "pending"})
		st.SaveReminderDelivery(app.ReminderDelivery{ID: "delivery-restart", ReminderID: reminder.ID, Status: "sent", Attempt: 1})
		reloaded, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := reloaded.GetReminder(reminder.ID); !ok || got.LastDeliveryID != "delivery-restart" || got.Status != "sent" {
			t.Fatalf("reminder did not survive restart: %#v ok=%v", got, ok)
		}
		if got := reloaded.ListReminderDeliveries(reminder.ID); len(got) != 1 || got[0].ID != "delivery-restart" {
			t.Fatalf("reminder delivery did not survive restart: %#v", got)
		}
	})
	t.Run("ConnectorRepository", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "connector.json")
		st, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		binding := mustCreateNotificationBindingFixture(t, st, app.NotificationBinding{ID: "binding-restart", OwnerID: "owner-s0", Channel: "telegram", Status: "active", Scopes: []string{"send"}})
		reloaded, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := mustGetNotificationBindingFixture(t, reloaded, binding.ID); !ok || len(got.Scopes) != 1 || got.Scopes[0] != "send" {
			t.Fatalf("notification binding did not survive restart: %#v ok=%v", got, ok)
		}
	})
}

var s0MutableAliasChecks = map[string]func(*testing.T, Store) bool{
	"RunRepository":                 s0RunAliasSafe,
	"ApprovalRepository":            s0ApprovalAliasSafe,
	"ScheduleRepository":            s0ScheduleAliasSafe,
	"PassiveNotificationRepository": s0PassiveAliasSafe,
	"DeliveryRecordRepository":      s0DeliveryAliasSafe,
	"BrowserStateRepository":        s0BrowserAliasSafe,
	"MemoryRepository":              s0MemoryAliasSafe,
	"AuditRepository":               s0AuditAliasSafe,
	"EvaluationRepository":          s0EvaluationAliasSafe,
}

func TestS0ConversationRepositoryMutableValuesAreIsolated(t *testing.T) {
	for _, backend := range newS0RepositoryBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			if !s0ConversationAliasSafe(t, backend.store) {
				t.Fatal("ConversationRepository exposed a mutable message alias")
			}
		})
	}
}

func TestS0ConnectorRepositoryMutableValuesAreIsolated(t *testing.T) {
	for _, backend := range newS0RepositoryBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			if !s0ConnectorAliasSafe(t, backend.store) {
				t.Fatal("ConnectorRepository exposed a mutable binding alias")
			}
		})
	}
}

func TestS0DefectEvidenceMutableAliases(t *testing.T) {
	for repository, check := range s0MutableAliasChecks {
		t.Run(repository, func(t *testing.T) {
			for _, backend := range newS0RepositoryBackends(t) {
				t.Run(backend.name, func(t *testing.T) {
					if check(t, backend.store) {
						t.Fatal("mutable record unexpectedly became alias-safe; replace this defect evidence with an isolation assertion")
					}
				})
			}
		})
	}
}

func s0ConversationAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	session := mustCreateSession(t, st, "alias")
	mustAddMessage(t, st, app.Message{ID: "message-alias", SessionID: session.ID, Attachments: []app.MessageAttachment{{Name: "original"}}})
	got := mustListMessages(t, st, session.ID)
	got[0].Attachments[0].Name = "mutated"
	return mustListMessages(t, st, session.ID)[0].Attachments[0].Name != "mutated"
}

func s0RunAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	st.SaveToolCall(app.ToolCall{ID: "tool-alias", Arguments: map[string]any{"value": "original"}})
	got, _ := st.GetToolCall("tool-alias")
	got.Arguments["value"] = "mutated"
	again, _ := st.GetToolCall("tool-alias")
	return again.Arguments["value"] != "mutated"
}

func s0ApprovalAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	st.SaveApproval(app.Approval{ID: "approval-alias", Status: "pending", Arguments: map[string]any{"value": "original"}})
	got, _ := st.GetApproval("approval-alias")
	got.Arguments["value"] = "mutated"
	again, _ := st.GetApproval("approval-alias")
	return again.Arguments["value"] != "mutated"
}

func s0ScheduleAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	sentAt := time.Now().UTC()
	st.SaveReminder(app.Reminder{ID: "reminder-alias", Status: "sent", SentAt: &sentAt})
	got, _ := st.GetReminder("reminder-alias")
	*got.SentAt = got.SentAt.Add(time.Hour)
	again, _ := st.GetReminder("reminder-alias")
	return !again.SentAt.Equal(*got.SentAt)
}

func s0ConnectorAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	mustCreateNotificationBindingFixture(t, st, app.NotificationBinding{ID: "binding-alias", Channel: "telegram", Status: "active", Scopes: []string{"original"}})
	got, _ := mustGetNotificationBindingFixture(t, st, "binding-alias")
	got.Scopes[0] = "mutated"
	again, _ := mustGetNotificationBindingFixture(t, st, "binding-alias")
	return again.Scopes[0] != "mutated"
}

func s0PassiveAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	notification := testPassiveNotification("passive-alias", "endpoint-alias", "delivery-alias", "fingerprint-alias")
	created, _, err := st.CreatePassiveNotification(notification)
	if err != nil {
		t.Fatal(err)
	}
	read, err := st.MarkPassiveNotificationRead(created.OwnerID, created.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	*read.ReadAt = read.ReadAt.Add(time.Hour)
	again, _ := st.GetPassiveNotification(created.OwnerID, created.ID)
	return !again.ReadAt.Equal(*read.ReadAt)
}

func s0DeliveryAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	st.SaveMessageReceive(app.MessageReceiveRecord{ID: "receive-alias", SourceEndpointID: "endpoint-alias", NativeMessageID: "native-alias", Status: "received"})
	got, _ := st.GetMessageReceive("receive-alias")
	got.Transitions[0].Status = "mutated"
	again, _ := st.GetMessageReceive("receive-alias")
	return again.Transitions[0].Status != "mutated"
}

func s0BrowserAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	st.SaveBrowserLoginBlock(app.BrowserLoginBlock{ID: "browser-alias", ResumeArgs: map[string]any{"value": "original"}})
	got, _ := st.GetBrowserLoginBlock("browser-alias")
	got.ResumeArgs["value"] = "mutated"
	again, _ := st.GetBrowserLoginBlock("browser-alias")
	return again.ResumeArgs["value"] != "mutated"
}

func s0MemoryAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	resolvedAt := time.Now().UTC()
	st.AddMemoryCandidate(app.MemoryCandidate{ID: "candidate-alias", Status: "resolved", ResolvedAt: &resolvedAt})
	got := st.ListMemoryCandidates("resolved")
	*got[0].ResolvedAt = got[0].ResolvedAt.Add(time.Hour)
	again := st.ListMemoryCandidates("resolved")
	return !again[0].ResolvedAt.Equal(*got[0].ResolvedAt)
}

func s0AuditAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	st.AddAudit(app.AuditEvent{ID: "audit-alias", Fields: map[string]any{"value": "original"}})
	got := st.ListAudit("")
	got[0].Fields["value"] = "mutated"
	return st.ListAudit("")[0].Fields["value"] != "mutated"
}

func s0EvaluationAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	st.SaveEvalRun(app.EvalRun{ID: "eval-alias", Cases: []app.EvalCase{{Name: "original"}}})
	got, _ := st.GetEvalRun("eval-alias")
	got.Cases[0].Name = "mutated"
	again, _ := st.GetEvalRun("eval-alias")
	return again.Cases[0].Name != "mutated"
}
