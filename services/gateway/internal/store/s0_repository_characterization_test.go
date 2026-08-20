package store

import (
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestS0BackendNeutralRepositorySuccessAndAbsence(t *testing.T) {
	cases := map[string]func(*testing.T, Store){
		"OwnerRepository":               characterizeS0OwnerRepository,
		"ClientRepository":              characterizeS0ClientRepository,
		"ISCPOnboardingRepository":      characterizeS0ISCPOnboardingRepository,
		"CredentialRepository":          characterizeS0CredentialRepository,
		"SessionRepository":             characterizeS0SessionRepository,
		"ConversationRepository":        characterizeS0ConversationRepository,
		"RunRepository":                 characterizeS0RunRepository,
		"ApprovalRepository":            characterizeS0ApprovalRepository,
		"ScheduleRepository":            characterizeS0ScheduleRepository,
		"ConnectorRepository":           characterizeS0ConnectorRepository,
		"ExternalChatRepository":        characterizeS0ExternalChatRepository,
		"DeliveryRecordRepository":      characterizeS0DeliveryRecordRepository,
		"MCPRepository":                 characterizeS0MCPRepository,
		"BrowserStateRepository":        characterizeS0BrowserStateRepository,
		"MemoryRepository":              characterizeS0MemoryRepository,
		"AuditRepository":               characterizeS0AuditRepository,
		"EvaluationRepository":          characterizeS0EvaluationRepository,
		"ArtifactMetadataRepository":    characterizeS0ArtifactMetadataRepository,
		"DocumentRepository":            characterizeS0DocumentRepository,
		"PassiveNotificationRepository": characterizeS0PassiveNotificationRepository,
	}
	if len(cases) != len(s0RepositoryMethods) {
		t.Fatalf("backend-neutral repository cases = %d, want %d", len(cases), len(s0RepositoryMethods))
	}
	for repository := range s0RepositoryMethods {
		if cases[repository] == nil {
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
			for _, backend := range newS0RepositoryBackends(t) {
				t.Run(backend.name, func(t *testing.T) {
					cases[repository](t, backend.store)
				})
			}
		})
	}
}

func newS0RepositoryBackends(t *testing.T) []s0CharacterizationBackend {
	t.Helper()
	fileStore, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return []s0CharacterizationBackend{{name: "memory", store: NewMemoryStore()}, {name: "file", store: fileStore}}
}

func characterizeS0OwnerRepository(t *testing.T, st Store) {
	if _, ok := st.GetOwnerProfileByID("missing"); ok {
		t.Fatal("missing owner profile was found")
	}
	profile := app.OwnerProfile{ID: "owner-s0", Source: "test", ExternalRef: "external-s0", DisplayName: "first"}
	st.SaveOwnerProfile(profile)
	profile.DisplayName = "updated"
	st.SaveOwnerProfile(profile)
	got, ok := st.FindOwnerProfileByExternalRef("test", "external-s0")
	if !ok || got.DisplayName != "updated" {
		t.Fatalf("owner profile overwrite/lookup = %#v ok=%v", got, ok)
	}
}

func characterizeS0ClientRepository(t *testing.T, st Store) {
	if _, ok := st.GetClient("missing"); ok {
		t.Fatal("missing client was found")
	}
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	st.SaveClient(app.Client{ID: "client-old", Name: "old", TokenHash: "hash-old", CreatedAt: base})
	st.SaveClient(app.Client{ID: "client-new", Name: "new", TokenHash: "hash-new", CreatedAt: base.Add(time.Minute)})
	st.SaveClient(app.Client{ID: "client-new", Name: "updated", TokenHash: "hash-new", CreatedAt: base.Add(time.Minute)})
	clients := st.ListClients()
	if len(clients) != 2 || clients[0].ID != "client-new" {
		t.Fatalf("client duplicate/order = %#v", clients)
	}
	if found, ok := st.FindClientByTokenHash("hash-new"); !ok || found.Name != "updated" {
		t.Fatalf("client token lookup = %#v ok=%v", found, ok)
	}
	st.SavePairingCode(app.PairingCode{ID: "pair-s0", CodeHash: "pair-hash", Status: "pending", ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if _, err := st.ClaimPairingCode("pair-s0", "client-new"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPairingCode("pair-s0", "client-old"); err == nil {
		t.Fatal("claimed pairing code was claimed twice")
	}
	if _, err := st.RevokeClient("client-new"); err != nil {
		t.Fatal(err)
	}
}

func characterizeS0ISCPOnboardingRepository(t *testing.T, st Store) {
	if _, ok := st.GetISCPOnboarding("missing"); ok {
		t.Fatal("missing ISCP onboarding was found")
	}
	receipt := testISCPOnboarding(time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC), "iscp-s0", "owner-s0")
	if _, err := st.SaveISCPOnboarding(receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveISCPOnboarding(receipt); !errors.Is(err, ErrISCPOnboardingConflict) {
		t.Fatalf("duplicate ISCP onboarding error = %v", err)
	}
	if got := st.ListISCPOnboardings("other-owner"); len(got) != 0 {
		t.Fatalf("ISCP onboarding crossed owner scope: %#v", got)
	}
}

func characterizeS0CredentialRepository(t *testing.T, st Store) {
	if _, ok := st.GetCredentialSecret("missing"); ok {
		t.Fatal("missing credential was found")
	}
	st.SaveCredentialSecret(app.CredentialSecret{Ref: "credential-s0", Kind: "token", Value: "first"})
	st.SaveCredentialSecret(app.CredentialSecret{Ref: "credential-s0", Kind: "token", Value: "updated"})
	if got, ok := st.GetCredentialSecret("credential-s0"); !ok || got.Value != "updated" {
		t.Fatalf("credential overwrite = %#v ok=%v", got, ok)
	}
	if err := st.DeleteCredentialSecret("credential-s0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetCredentialSecret("credential-s0"); ok {
		t.Fatal("deleted credential was found")
	}
}

func characterizeS0SessionRepository(t *testing.T, st Store) {
	if _, ok := st.GetSession("missing"); ok {
		t.Fatal("missing session was found")
	}
	hidden := st.CreateSessionWithScope("hidden", "owner-s0", "/workspace", "test", true)
	visible := st.CreateSession("visible")
	if sessions := st.ListSessions(); len(sessions) != 1 || sessions[0].ID != visible.ID {
		t.Fatalf("visible session filter = %#v", sessions)
	}
	updated, err := st.UpdateSessionTitle(visible.ID, "updated")
	if err != nil || updated.Title != "updated" {
		t.Fatalf("session update = %#v err=%v", updated, err)
	}
	if _, err := st.DeleteSession(hidden.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetSession(hidden.ID); ok {
		t.Fatal("deleted session was found")
	}
}

func characterizeS0ConversationRepository(t *testing.T, st Store) {
	if got := st.ListMessages("missing"); len(got) != 0 {
		t.Fatalf("missing conversation messages = %#v", got)
	}
	session := st.CreateSession("conversation")
	message := st.AddMessage(app.Message{ID: "message-s0", SessionID: session.ID, Role: "user", Content: "first"})
	reused := st.AddMessage(app.Message{ID: message.ID, SessionID: session.ID, Role: "user", Content: "replacement"})
	if messages := st.ListMessages(session.ID); len(messages) != 1 || reused.ID != message.ID || messages[0].Content != "first" {
		t.Fatalf("message duplicate reuse = %#v reused=%#v", messages, reused)
	}
}

func characterizeS0RunRepository(t *testing.T, st Store) {
	if _, ok := st.GetRun("missing"); ok {
		t.Fatal("missing run was found")
	}
	first := app.AgentRun{ID: "run-s0", SessionID: "session-s0", State: "running", StartedAt: time.Now().UTC()}
	st.SaveRun(first)
	first.State = "completed"
	st.SaveRun(first)
	if got, ok := st.GetRun(first.ID); !ok || got.State != "completed" {
		t.Fatalf("run overwrite = %#v ok=%v", got, ok)
	}
	if got := st.ListRuns("other-session"); len(got) != 0 {
		t.Fatalf("run crossed session scope: %#v", got)
	}
}

func characterizeS0DocumentRepository(t *testing.T, st Store) {
	if _, ok := st.GetDocumentRecord("missing"); ok {
		t.Fatal("missing document was found")
	}
	record := st.SaveDocumentRecord(app.DocumentRecord{ID: "document-s0", OwnerID: "owner-s0", SessionID: "session-s0", Name: "first", LastActivityAt: time.Now().UTC()})
	record.Name = "updated"
	st.SaveDocumentRecord(record)
	if got, ok := st.GetDocumentRecord(record.ID); !ok || got.Name != "updated" {
		t.Fatalf("document overwrite = %#v ok=%v", got, ok)
	}
}

func characterizeS0ApprovalRepository(t *testing.T, st Store) {
	if _, ok := st.GetApproval("missing"); ok {
		t.Fatal("missing approval was found")
	}
	approval := app.Approval{ID: "approval-s0", Source: app.ApprovalSourceHappyTeamPlan, ExternalID: "external-s0", Status: "pending", Summary: "first"}
	st.SaveApproval(approval)
	approval.Summary = "updated"
	st.SaveApproval(approval)
	if got, ok := st.FindApprovalByExternalRef(approval.Source, approval.ExternalID); !ok || got.Summary != "updated" {
		t.Fatalf("approval overwrite/lookup = %#v ok=%v", got, ok)
	}
	if _, err := st.ResolveApproval(approval.ID, "approved", "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdatePendingApproval(approval); err == nil {
		t.Fatal("resolved approval accepted pending update")
	}
}

func characterizeS0ScheduleRepository(t *testing.T, st Store) {
	if _, ok := st.GetReminder("missing"); ok {
		t.Fatal("missing reminder was found")
	}
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	late := st.SaveReminder(app.Reminder{ID: "reminder-late", Text: "late", DueTime: base.Add(time.Hour), Status: "pending"})
	early := st.SaveReminder(app.Reminder{ID: "reminder-early", Text: "early", DueTime: base, Status: "pending"})
	early.Text = "updated"
	st.SaveReminder(early)
	items := st.ListReminders(app.ReminderFilter{Status: "pending"})
	if len(items) != 2 || items[0].ID != early.ID || items[1].ID != late.ID || items[0].Text != "updated" {
		t.Fatalf("reminder duplicate/order/filter = %#v", items)
	}
	st.SaveReminderDelivery(app.ReminderDelivery{ID: "delivery-s0", ReminderID: early.ID, Status: "sent", Attempt: 1})
	if deliveries := st.ListReminderDeliveries(early.ID); len(deliveries) != 1 {
		t.Fatalf("reminder delivery list = %#v", deliveries)
	}
}

func characterizeS0ConnectorRepository(t *testing.T, st Store) {
	if _, ok := st.GetConnectorSetting("owner-s0", "telegram"); ok {
		t.Fatal("missing connector setting was found")
	}
	setting, err := st.UpdateConnectorSetting(app.ConnectorSetting{OwnerID: "owner-s0", Channel: "telegram", Enabled: true}, 0)
	if err != nil || setting.Version != 1 {
		t.Fatalf("connector setting create = %#v err=%v", setting, err)
	}
	if _, err := st.UpdateConnectorSetting(setting, 0); !errors.Is(err, ErrConnectorSettingConflict) {
		t.Fatalf("connector setting stale CAS = %v", err)
	}
	binding := st.SaveNotificationBinding(app.NotificationBinding{ID: "binding-s0", OwnerID: "owner-s0", Channel: "telegram", Status: "active"})
	if got, ok := st.GetNotificationBinding(binding.ID); !ok || got.Channel != "telegram" {
		t.Fatalf("notification binding = %#v ok=%v", got, ok)
	}
}

func characterizeS0PassiveNotificationRepository(t *testing.T, st Store) {
	if _, ok := st.GetPassiveNotification("owner-s0", "missing"); ok {
		t.Fatal("missing passive notification was found")
	}
	notification := testPassiveNotification("passive-s0", "endpoint-s0", "delivery-s0", "fingerprint-s0")
	notification.OwnerID = "owner-s0"
	created, inserted, err := st.CreatePassiveNotification(notification)
	if err != nil || !inserted || created.ID != notification.ID {
		t.Fatalf("passive notification create = %#v inserted=%v err=%v", created, inserted, err)
	}
}

func characterizeS0ExternalChatRepository(t *testing.T, st Store) {
	if _, ok := st.GetExternalChatSession("missing"); ok {
		t.Fatal("missing external chat session was found")
	}
	chat := st.SaveExternalChatSession(app.ExternalChatSession{ID: "chat-s0", BindingID: "binding-s0", Channel: "telegram", ExternalChatID: "chat", Status: "active"})
	if got, ok := st.GetExternalChatSession(chat.ID); !ok || got.Channel != "telegram" {
		t.Fatalf("external chat session = %#v ok=%v", got, ok)
	}
}

func characterizeS0DeliveryRecordRepository(t *testing.T, st Store) {
	if _, ok := st.GetMessageReceive("missing"); ok {
		t.Fatal("missing receive record was found")
	}
	record := st.SaveMessageReceive(app.MessageReceiveRecord{ID: "receive-s0", OwnerID: "owner-s0", ActorID: "actor-s0", SourceEndpointID: "endpoint-s0", NativeMessageID: "native-s0", Status: "received"})
	if got, ok := st.GetMessageReceive(record.ID); !ok || got.Direction != app.MessageDirectionReceive {
		t.Fatalf("receive record = %#v ok=%v", got, ok)
	}
}

func characterizeS0MCPRepository(t *testing.T, st Store) {
	if _, ok := st.GetMCPAccessTicket("missing"); ok {
		t.Fatal("missing MCP ticket was found")
	}
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(time.Now().UTC(), "mcp-s0-hash"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := st.GetMCPAccessTicket(ticket.ID); !ok || got.SecretHash != ticket.SecretHash {
		t.Fatalf("MCP ticket = %#v ok=%v", got, ok)
	}
}

func characterizeS0BrowserStateRepository(t *testing.T, st Store) {
	if _, ok := st.GetBrowserAuthRecord("missing"); ok {
		t.Fatal("missing browser auth record was found")
	}
	record := st.SaveBrowserAuthRecord(app.BrowserAuthRecord{ID: "browser-auth-s0", OwnerID: "owner-s0", BrowserProfileID: "profile-s0", SiteOrigin: "https://example.com"})
	if got, ok := st.GetBrowserAuthRecord(record.ID); !ok || got.SiteOrigin != "https://example.com" {
		t.Fatalf("browser auth record = %#v ok=%v", got, ok)
	}
}

func characterizeS0MemoryRepository(t *testing.T, st Store) {
	if got := st.SearchMemories("missing"); len(got) != 0 {
		t.Fatalf("missing memory search = %#v", got)
	}
	candidate := st.AddMemoryCandidate(app.MemoryCandidate{ID: "candidate-s0", SessionID: "session-s0", RunID: "run-s0", Status: "pending", Content: "remember"})
	if items := st.ListMemoryCandidates("pending"); len(items) != 1 || items[0].ID != candidate.ID {
		t.Fatalf("memory candidate = %#v", items)
	}
}

func characterizeS0AuditRepository(t *testing.T, st Store) {
	if got := st.ListAudit("session-s0"); len(got) != 0 {
		t.Fatalf("missing audit list = %#v", got)
	}
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	st.AddAudit(app.AuditEvent{ID: "audit-old", SessionID: "session-s0", Type: "old", Time: base})
	st.AddAudit(app.AuditEvent{ID: "audit-new", SessionID: "session-s0", Type: "new", Time: base.Add(time.Minute)})
	items := st.ListAudit("session-s0")
	if len(items) != 2 || items[0].ID != "audit-new" || len(st.ListAudit("other-session")) != 0 {
		t.Fatalf("audit order/scope = %#v", items)
	}
}

func characterizeS0EvaluationRepository(t *testing.T, st Store) {
	if _, ok := st.GetEvalRun("missing"); ok {
		t.Fatal("missing evaluation run was found")
	}
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	st.SaveEvalRun(app.EvalRun{ID: "eval-old", Status: "passed", Summary: "old", StartedAt: base})
	st.SaveEvalRun(app.EvalRun{ID: "eval-new", Status: "failed", Summary: "first", StartedAt: base.Add(time.Minute)})
	st.SaveEvalRun(app.EvalRun{ID: "eval-new", Status: "passed", Summary: "updated", StartedAt: base.Add(time.Minute)})
	items := st.ListEvalRuns()
	if len(items) != 2 || items[0].ID != "eval-new" || items[0].Summary != "updated" {
		t.Fatalf("evaluation duplicate/order = %#v", items)
	}
}

func characterizeS0ArtifactMetadataRepository(t *testing.T, st Store) {
	if _, ok := st.FindArtifactObjectByURI("artifact://missing", "", ""); ok {
		t.Fatal("missing artifact was found")
	}
	st.SaveArtifactObject(app.ArtifactObject{ID: "artifact-s0", URI: "artifact://s0", Key: "s0", CreatedAt: time.Now().UTC()})
	if got, ok := st.FindArtifactObjectByURI("artifact://s0", "", ""); !ok || got.ID != "artifact-s0" {
		t.Fatalf("artifact lookup = %#v ok=%v", got, ok)
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
		binding := st.SaveNotificationBinding(app.NotificationBinding{ID: "binding-restart", OwnerID: "owner-s0", Channel: "telegram", Status: "active", Scopes: []string{"send"}})
		reloaded, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := reloaded.GetNotificationBinding(binding.ID); !ok || len(got.Scopes) != 1 || got.Scopes[0] != "send" {
			t.Fatalf("notification binding did not survive restart: %#v ok=%v", got, ok)
		}
	})
}

func TestS0DefectEvidenceMutableAliases(t *testing.T) {
	checks := map[string]func(*testing.T, Store) bool{
		"ClientRepository":              s0ClientAliasSafe,
		"ConversationRepository":        s0ConversationAliasSafe,
		"RunRepository":                 s0RunAliasSafe,
		"ApprovalRepository":            s0ApprovalAliasSafe,
		"ScheduleRepository":            s0ScheduleAliasSafe,
		"ConnectorRepository":           s0ConnectorAliasSafe,
		"PassiveNotificationRepository": s0PassiveAliasSafe,
		"DeliveryRecordRepository":      s0DeliveryAliasSafe,
		"BrowserStateRepository":        s0BrowserAliasSafe,
		"MemoryRepository":              s0MemoryAliasSafe,
		"AuditRepository":               s0AuditAliasSafe,
		"EvaluationRepository":          s0EvaluationAliasSafe,
	}
	for repository, check := range checks {
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

func s0ClientAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	lastSeen := time.Now().UTC()
	st.SaveClient(app.Client{ID: "client-alias", LastSeenAt: &lastSeen})
	got, _ := st.GetClient("client-alias")
	*got.LastSeenAt = got.LastSeenAt.Add(time.Hour)
	again, _ := st.GetClient("client-alias")
	return !again.LastSeenAt.Equal(*got.LastSeenAt)
}

func s0ConversationAliasSafe(t *testing.T, st Store) bool {
	t.Helper()
	session := st.CreateSession("alias")
	st.AddMessage(app.Message{ID: "message-alias", SessionID: session.ID, Attachments: []app.MessageAttachment{{Name: "original"}}})
	got := st.ListMessages(session.ID)
	got[0].Attachments[0].Name = "mutated"
	return st.ListMessages(session.ID)[0].Attachments[0].Name != "mutated"
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
	st.SaveNotificationBinding(app.NotificationBinding{ID: "binding-alias", Channel: "telegram", Status: "active", Scopes: []string{"original"}})
	got, _ := st.GetNotificationBinding("binding-alias")
	got.Scopes[0] = "mutated"
	again, _ := st.GetNotificationBinding("binding-alias")
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
