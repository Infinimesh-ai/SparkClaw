package store

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var s0RepositoryMethods = map[string][]string{
	"ApprovalRepository": {
		"FindApprovalByExternalRef", "GetApproval", "ListApprovals", "ResolveApproval", "SaveApproval", "UpdatePendingApproval",
	},
	"ArtifactMetadataRepository": {
		"FindArtifactObjectByURI", "ListArtifactObjects", "SaveArtifactObject",
	},
	"AuditRepository": {
		"AddAudit", "EventsAfter", "ListAudit",
	},
	"BrowserStateRepository": {
		"FindActiveBrowserLoginBlock", "FindBrowserAuthRecord", "GetBrowserAuthRecord", "GetBrowserLoginBlock", "ListBrowserAuthRecords",
		"ListBrowserLoginBlocks", "RevokeBrowserAuthRecord", "SaveBrowserAuthRecord", "SaveBrowserLoginBlock", "UpdateBrowserLoginBlock",
	},
	"ClientRepository": {
		"ClaimPairingCode", "FindClientByTokenHash", "GetClient", "GetPairingCode", "ListClients", "RevokeClient", "SaveClient",
		"SavePairingCode", "TouchClient",
	},
	"ConnectorRepository": {
		"GetConnectorSetting", "GetNotificationBinding", "ListAllConnectorSettings", "ListConnectorSettings", "ListNotificationBindings",
		"RevokeNotificationBinding", "SaveNotificationBinding", "UpdateConnectorSetting",
	},
	"ConversationRepository": {
		"AddMessage", "ListMessages", "MessageEventHead", "MessageEventsAfter",
	},
	"CredentialRepository": {
		"DeleteCredentialSecret", "GetCredentialSecret", "SaveCredentialSecret",
	},
	"DeliveryRecordRepository": {
		"FindChannelInboxUpdate", "FindMessageDeliveryByIdempotency", "FindMessageReceive", "GetChannelInboxUpdate", "GetMessageDelivery",
		"GetMessageReceive", "ListChannelInboxUpdates", "ListMessageDeliveries", "ListMessageReceives", "SaveChannelInboxUpdate",
		"SaveMessageDelivery", "SaveMessageReceive",
	},
	"DocumentRepository": {
		"GetDocumentRecord", "ListDocumentRecords", "SaveDocumentRecord",
	},
	"EvaluationRepository": {
		"GetEvalRun", "ListEvalRuns", "SaveEvalRun",
	},
	"ExternalChatRepository": {
		"FindExternalChatMessageByExternalID", "FindExternalChatSession", "FindExternalChatSessionByLinkedSessionID", "GetExternalChatMessage",
		"GetExternalChatSession", "ListExternalChatMessages", "ListExternalChatSessions", "SaveExternalChatMessage", "SaveExternalChatSession",
	},
	"ISCPOnboardingRepository": {
		"GetISCPOnboarding", "ListISCPOnboardings", "SaveISCPOnboarding",
	},
	"MCPRepository": {
		"CreateMCPOperation", "DeleteMCPAccessRecords", "DeleteMCPAccessTicket", "DeleteMCPBinding", "FindMCPAccessTicketBySecretHash",
		"FindMCPBindingForPeer", "FindMCPOperationByIdempotency", "GetMCPAccessTicket", "GetMCPBinding", "GetMCPOperation",
		"ListMCPAccessTickets", "ListMCPBindings", "ListMCPOperations", "RedeemMCPAccessTicket", "RevokeMCPAccessTicket", "RevokeMCPBinding",
		"SaveMCPAccessTicket", "TouchMCPBinding", "UpdateMCPOperation",
	},
	"MemoryRepository": {
		"AddMemoryCandidate", "DeleteMemory", "ListMemoryCandidates", "PruneMemories", "ResolveMemoryCandidate", "SearchMemories", "UpdateMemory",
	},
	"OwnerRepository": {
		"FindOwnerProfileByExternalRef", "GetOwnerProfile", "GetOwnerProfileByID", "ListOwnerProfiles", "SaveOwnerProfile", "UpdateOwnerProfile",
	},
	"PassiveNotificationRepository": {
		"CountUnreadPassiveNotifications", "CreatePassiveNotification", "GetPassiveNotification", "ListPassiveNotifications",
		"MarkAllPassiveNotificationsRead", "MarkPassiveNotificationRead", "PassiveNotificationRevision", "PrunePassiveNotifications",
	},
	"RunRepository": {
		"GetRun", "GetToolCall", "ListEpisodeSummaries", "ListModelCalls", "ListRunFeedback", "ListRuns", "ListToolCalls",
		"SaveEpisodeSummary", "SaveModelCall", "SaveRun", "SaveRunFeedback", "SaveToolCall",
	},
	"ScheduleRepository": {
		"ClaimDueReminders", "GetReminder", "ListReminderDeliveries", "ListReminders", "SaveReminder", "SaveReminderDelivery", "UpdatePendingReminder",
	},
	"SessionRepository": {
		"CreateSession", "CreateSessionWithScope", "DeleteSession", "GetSession", "ListSessions", "UpdateSessionTitle",
	},
}

func TestS0StoreMethodCatalogCharacterization(t *testing.T) {
	typeOfStore := reflect.TypeOf((*Store)(nil)).Elem()
	if typeOfStore.NumMethod() != 141 {
		t.Fatalf("Store method count = %d, want S0 baseline 141", typeOfStore.NumMethod())
	}

	owners := make(map[string]string, typeOfStore.NumMethod())
	for repository, methods := range s0RepositoryMethods {
		for _, method := range methods {
			if previous, exists := owners[method]; exists {
				t.Fatalf("Store method %s is assigned to both %s and %s", method, previous, repository)
			}
			owners[method] = repository
		}
	}
	for index := range typeOfStore.NumMethod() {
		method := typeOfStore.Method(index).Name
		if owners[method] == "" {
			t.Errorf("Store method %s has no S0 repository owner", method)
		}
	}
	for method, repository := range owners {
		if _, exists := typeOfStore.MethodByName(method); !exists {
			t.Errorf("%s assigns unknown Store method %s", repository, method)
		}
	}
	if len(owners) != typeOfStore.NumMethod() {
		t.Fatalf("S0 catalog contains %d unique methods, want %d", len(owners), typeOfStore.NumMethod())
	}
}

func TestS0SnapshotShapeCharacterization(t *testing.T) {
	want := []string{
		"Sessions:sessions", "Clients:clients", "OwnerProfile:owner_profile", "OwnerProfiles:owner_profiles,omitempty",
		"PairingCodes:pairing_codes", "ISCPOnboardings:iscp_onboardings,omitempty", "MCPAccessTickets:mcp_access_tickets,omitempty",
		"MCPBindings:mcp_bindings,omitempty", "MCPOperations:mcp_operations,omitempty", "Messages:messages", "RunFeedback:run_feedback",
		"Runs:runs", "ModelCalls:model_calls", "ToolCalls:tool_calls", "DocumentRecords:document_records,omitempty", "Approvals:approvals",
		"Reminders:reminders", "ReminderDelivery:reminder_delivery", "ConnectorSettings:connector_settings,omitempty",
		"NotificationBindings:notification_bindings", "PassiveNotifications:passive_notifications,omitempty",
		"ExternalChatSessions:external_chat_sessions,omitempty", "ExternalChatMessages:external_chat_messages,omitempty",
		"MessageReceives:message_receives,omitempty", "MessageDeliveries:message_deliveries,omitempty",
		"ChannelInboxUpdates:channel_inbox_updates,omitempty", "WeixinChatSessions:weixin_chat_sessions,omitempty",
		"WeixinChatMessages:weixin_chat_messages,omitempty", "CredentialSecrets:credential_secrets",
		"BrowserAuthRecords:browser_auth_records,omitempty", "BrowserLoginBlocks:browser_login_blocks,omitempty", "Memories:memories",
		"MemoryCandidates:memory_candidates", "AuditEvents:audit_events", "Events:events", "EvalRuns:eval_runs",
		"ArtifactObjects:artifact_objects", "EpisodeSummaries:episode_summaries",
	}
	typeOfSnapshot := reflect.TypeOf(Snapshot{})
	got := make([]string, 0, typeOfSnapshot.NumField())
	for index := range typeOfSnapshot.NumField() {
		field := typeOfSnapshot.Field(index)
		got = append(got, field.Name+":"+field.Tag.Get("json"))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot field/tag shape changed\n got: %#v\nwant: %#v", got, want)
	}
}

type s0CharacterizationBackend struct {
	name   string
	store  Store
	reopen func(t *testing.T) Store
}

func TestS0BackendNeutralContractCharacterization(t *testing.T) {
	for _, backend := range s0CharacterizationBackends(t) {
		backend := backend
		t.Run(backend.name, func(t *testing.T) {
			t.Run("success_absence_order_scope_clone", func(t *testing.T) {
				characterizeSuccessAbsenceOrderScopeAndClone(t, backend.store)
			})
			t.Run("idempotency_cas_alias", func(t *testing.T) {
				characterizeIdempotencyCASAndAliasSafety(t, backend.store)
			})
			t.Run("events", func(t *testing.T) {
				characterizeMessageEvents(t, backend.store)
			})
			t.Run("concurrency", func(t *testing.T) {
				characterizeConcurrentIdempotency(t, backend.store)
			})
			if backend.reopen != nil {
				t.Run("restart", func(t *testing.T) {
					characterizeRestart(t, backend.store, backend.reopen)
				})
			}
		})
	}
}

func s0CharacterizationBackends(t *testing.T) []s0CharacterizationBackend {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s0-characterization.json")
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return []s0CharacterizationBackend{
		{name: "memory", store: NewMemoryStore()},
		{
			name:  "file",
			store: fileStore,
			reopen: func(t *testing.T) Store {
				t.Helper()
				reloaded, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return reloaded
			},
		},
	}
}

func characterizeSuccessAbsenceOrderScopeAndClone(t *testing.T, st Store) {
	t.Helper()
	if _, ok := st.GetDocumentRecord("missing"); ok {
		t.Fatal("missing document was reported as present")
	}
	base := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	for _, record := range []app.DocumentRecord{
		{ID: "doc-old", OwnerID: "owner-a", SessionID: "session-a", GovernedPath: "/workspace/old.txt", LastActivityAt: base},
		{ID: "doc-other-owner", OwnerID: "owner-b", SessionID: "session-a", GovernedPath: "/workspace/other.txt", LastActivityAt: base.Add(2 * time.Minute)},
		{ID: "doc-new", OwnerID: "owner-a", SessionID: "session-a", GovernedPath: "/workspace/new.txt", LastActivityAt: base.Add(time.Minute)},
	} {
		st.SaveDocumentRecord(record)
	}
	got := st.ListDocumentRecords("owner-a", "session-a", 10)
	if len(got) != 2 || got[0].ID != "doc-new" || got[1].ID != "doc-old" {
		t.Fatalf("owner-scoped document order = %#v", got)
	}

	inputPreferences := map[string]string{"locale": "zh-CN"}
	st.SaveOwnerProfile(app.OwnerProfile{ID: "owner-clone", DisplayName: "Clone", Preferences: inputPreferences})
	inputPreferences["locale"] = "mutated-input"
	first, ok := st.GetOwnerProfileByID("owner-clone")
	if !ok || first.Preferences["locale"] != "zh-CN" {
		t.Fatalf("Store retained caller-owned profile map: %#v ok=%v", first, ok)
	}
	first.Preferences["locale"] = "mutated-output"
	second, ok := st.GetOwnerProfileByID("owner-clone")
	if !ok || second.Preferences["locale"] != "zh-CN" {
		t.Fatalf("Store returned backend-owned profile map: %#v ok=%v", second, ok)
	}
}

func characterizeIdempotencyCASAndAliasSafety(t *testing.T, st Store) {
	t.Helper()
	operation := app.MCPOperation{
		BindingID: "binding-characterization", IdempotencyKey: "idem-characterization", Fingerprint: "fingerprint-a",
		Invocation: app.MCPInvocationContext{Arguments: map[string]any{"nested": map[string]any{"value": "original"}}},
		Result:     json.RawMessage(`{"value":"original"}`),
	}
	created, wasCreated, err := st.CreateMCPOperation(operation)
	if err != nil || !wasCreated {
		t.Fatalf("create operation: created=%v err=%v", wasCreated, err)
	}
	replayed, wasCreated, err := st.CreateMCPOperation(operation)
	if err != nil || wasCreated || replayed.ID != created.ID {
		t.Fatalf("idempotent replay = %#v created=%v err=%v", replayed, wasCreated, err)
	}
	changed := operation
	changed.Fingerprint = "fingerprint-b"
	if _, _, err := st.CreateMCPOperation(changed); !errors.Is(err, ErrMCPOperationConflict) {
		t.Fatalf("changed idempotency replay error = %v", err)
	}

	created.Invocation.Arguments["nested"].(map[string]any)["value"] = "mutated-output"
	created.Result[0] = '['
	stored, ok := st.GetMCPOperation(created.ID)
	if !ok || stored.Invocation.Arguments["nested"].(map[string]any)["value"] != "original" || string(stored.Result) != `{"value":"original"}` {
		t.Fatalf("MCP operation alias escaped Store: %#v ok=%v", stored, ok)
	}
	stored.State = app.MCPOperationSucceeded
	updated, err := st.UpdateMCPOperation(stored, stored.Version)
	if err != nil || updated.Version != stored.Version+1 {
		t.Fatalf("CAS update = %#v err=%v", updated, err)
	}
	if _, err := st.UpdateMCPOperation(stored, stored.Version); !errors.Is(err, ErrMCPOperationVersionConflict) {
		t.Fatalf("stale CAS error = %v", err)
	}
}

func characterizeMessageEvents(t *testing.T, st Store) {
	t.Helper()
	firstSession := st.CreateSession("event-a")
	secondSession := st.CreateSession("event-b")
	first := st.AddMessage(app.Message{SessionID: firstSession.ID, Role: "user", Content: "first"})
	st.AddMessage(app.Message{SessionID: secondSession.ID, Role: "user", Content: "other"})
	second := st.AddMessage(app.Message{SessionID: firstSession.ID, Role: "assistant", Content: "second"})
	page, err := st.MessageEventsAfter(firstSession.ID, "", 10)
	if err != nil || len(page.Events) != 2 || page.Events[0].Payload.(app.Message).ID != first.ID || page.Events[1].Payload.(app.Message).ID != second.ID {
		t.Fatalf("message event order/scope = %#v err=%v", page, err)
	}
	if head, err := st.MessageEventHead(firstSession.ID); err != nil || head != page.Events[1].ID {
		t.Fatalf("message event head = %q err=%v", head, err)
	}
}

func characterizeConcurrentIdempotency(t *testing.T, st Store) {
	t.Helper()
	const workers = 16
	var wg sync.WaitGroup
	results := make(chan app.MCPOperation, workers)
	created := make(chan bool, workers)
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			operation, wasCreated, err := st.CreateMCPOperation(app.MCPOperation{
				BindingID: "binding-concurrent", IdempotencyKey: "idem-concurrent", Fingerprint: "same",
			})
			results <- operation
			created <- wasCreated
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(created)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent idempotent create: %v", err)
		}
	}
	createdCount := 0
	for value := range created {
		if value {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent created count = %d, want 1", createdCount)
	}
	ids := map[string]struct{}{}
	for operation := range results {
		ids[operation.ID] = struct{}{}
	}
	if len(ids) != 1 {
		t.Fatalf("concurrent idempotent operation IDs = %#v", ids)
	}
}

func characterizeRestart(t *testing.T, st Store, reopen func(t *testing.T) Store) {
	t.Helper()
	session := st.CreateSession("restart")
	message := st.AddMessage(app.Message{SessionID: session.ID, Role: "assistant", Content: "durable"})
	profile := st.SaveOwnerProfile(app.OwnerProfile{ID: "owner-restart", DisplayName: "Restart", Preferences: map[string]string{"key": "value"}})
	reloaded := reopen(t)
	if got, ok := reloaded.GetSession(session.ID); !ok || got.Title != session.Title {
		t.Fatalf("session did not survive restart: %#v ok=%v", got, ok)
	}
	if messages := reloaded.ListMessages(session.ID); len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("message did not survive restart: %#v", messages)
	}
	if got, ok := reloaded.GetOwnerProfileByID(profile.ID); !ok || got.Preferences["key"] != "value" {
		t.Fatalf("owner profile did not survive restart: %#v ok=%v", got, ok)
	}
}

func TestS0DefectEvidenceLegacyFilePersistenceErrorsAreDiscarded(t *testing.T) {
	source := readS0Source(t, "file.go")
	if got := strings.Count(source, "s.persist()"); got != 48 {
		t.Fatalf("legacy File persist call count = %d, want S0 defect baseline 48", got)
	}
	body := sourceFunctionBody(t, "file.go", "persist")
	if !strings.Contains(body, "_ = s.persistSnapshot()") {
		t.Fatal("legacy File persist no longer matches the recorded error-discard defect; replace this evidence in the owning migration stage")
	}
}

func TestS0DefectEvidencePostgresRowsErrIsNotChecked(t *testing.T) {
	functions := map[string]string{
		"iscp_onboarding_postgres.go": "ListISCPOnboardings",
		"mcp_access_postgres.go":      "ListMCPAccessTickets,ListMCPBindings,ListMCPOperations",
		"postgres.go":                 "PrunePassiveNotifications,ListMessageReceives,ListMessageDeliveries,PruneMemories,collectRows",
	}
	for file, names := range functions {
		for _, name := range strings.Split(names, ",") {
			body := sourceFunctionBody(t, file, name)
			if !strings.Contains(body, ".Next()") || strings.Contains(body, ".Err()") {
				t.Errorf("%s.%s no longer matches the recorded rows.Err defect evidence; replace this evidence in the owning migration stage", file, name)
			}
		}
	}
}

func TestS0DefectEvidencePostgresExecResultsAreDiscarded(t *testing.T) {
	files := []string{"postgres.go", "iscp_onboarding_postgres.go", "mcp_access_postgres.go"}
	count := 0
	for _, file := range files {
		count += strings.Count(readS0Source(t, file), "_, _ = ")
	}
	if count != 33 {
		t.Fatalf("discarded PostgreSQL result count = %d, want S0 defect baseline 33", count)
	}
}

func readS0Source(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func sourceFunctionBody(t *testing.T, file, name string) string {
	t.Helper()
	raw := readS0Source(t, file)
	parsed, err := parser.ParseFile(token.NewFileSet(), file, raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name || function.Body == nil {
			continue
		}
		return raw[function.Body.Pos()-1 : function.Body.End()-1]
	}
	t.Fatalf("function %s not found in %s", name, file)
	return ""
}

func TestS0CatalogRepositoryNamesAreStable(t *testing.T) {
	names := make([]string, 0, len(s0RepositoryMethods))
	for name := range s0RepositoryMethods {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) != 20 {
		t.Fatalf("repository count = %d, want 20: %v", len(names), names)
	}
}
