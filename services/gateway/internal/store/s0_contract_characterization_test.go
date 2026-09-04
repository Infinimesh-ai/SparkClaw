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
		"ClaimPairingCode", "FindClientByTokenHash", "GetClient", "GetPairingCode", "ListClients", "RevokeClient",
		"SavePairingCode", "TouchClient",
	},
	"ConnectorRepository": {
		"CreateNotificationBinding", "GetConnectorSetting", "GetNotificationBinding", "ListAllConnectorSettings", "ListConnectorSettings",
		"ListNotificationBindings", "UpdateConnectorSetting", "UpdateNotificationBinding", "GetEmailProviderSetting", "ListEmailProviderSettings",
		"UpdateEmailProviderSetting",
	},
	"ConversationRepository": {
		"AddMessage", "ListMessages", "ListRecentMessages", "MessageEventHead", "MessageEventsAfter",
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
		"GetRun", "GetToolCall", "ListEpisodeSummaries", "ListRecentEpisodeSummaries", "ListModelCalls", "ListRunFeedback", "ListRuns", "ListToolCalls", "ListRecentToolCalls",
		"SaveEpisodeSummary", "SaveModelCall", "SaveRun", "SaveRunFeedback", "SaveToolCall",
	},
	"ScheduleRepository": {
		"ClaimDueReminders", "GetReminder", "ListReminderDeliveries", "ListReminders", "SaveReminder", "SaveReminderDelivery", "UpdatePendingReminder",
	},
	"SessionRepository": {
		"CreateSession", "CreateSessionWithScope", "DeleteSession", "GetSession", "ListSessions", "UpdateSessionTitle",
	},
}

func TestS0RepositoryMethodCatalogCharacterization(t *testing.T) {
	typeOfBackend := reflect.TypeOf((*testBackend)(nil)).Elem()
	if typeOfBackend.NumMethod() != 146 {
		t.Fatalf("repository method count = %d, want migrated baseline 146", typeOfBackend.NumMethod())
	}

	owners := make(map[string]string, typeOfBackend.NumMethod())
	for repository, methods := range s0RepositoryMethods {
		for _, method := range methods {
			if previous, exists := owners[method]; exists {
				t.Fatalf("repository method %s is assigned to both %s and %s", method, previous, repository)
			}
			owners[method] = repository
		}
	}
	for index := range typeOfBackend.NumMethod() {
		method := typeOfBackend.Method(index).Name
		if owners[method] == "" {
			t.Errorf("method %s has no S0 repository owner", method)
		}
	}
	for method, repository := range owners {
		if _, exists := typeOfBackend.MethodByName(method); !exists {
			t.Errorf("%s assigns unknown repository method %s", repository, method)
		}
	}
	if len(owners) != typeOfBackend.NumMethod() {
		t.Fatalf("S0 catalog contains %d unique methods, want %d", len(owners), typeOfBackend.NumMethod())
	}
}

func TestS4ProductionUsesStaticRepositoryContracts(t *testing.T) {
	gatewayRoot := filepath.Clean(filepath.Join("..", ".."))
	err := filepath.WalkDir(gatewayRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files := token.NewFileSet()
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.TypeSpec:
				if parsed.Name.Name == "store" && current.Name.Name == "Store" {
					t.Errorf("production broad Store type remains in %s", path)
				}
			case *ast.SelectorExpr:
				packageName, ok := current.X.(*ast.Ident)
				if ok && packageName.Name == "store" && current.Sel.Name == "Store" {
					t.Errorf("production broad store.Store reference remains in %s", path)
				}
			case *ast.TypeAssertExpr:
				if current.Type != nil && s4RepositoryLikeType(current.Type) {
					t.Errorf("repository capability is discovered by type assertion in %s:%d", path, files.Position(current.Pos()).Line)
				}
			case *ast.MapType:
				if s4RepositoryLikeType(current.Value) {
					t.Errorf("dynamic repository map remains in %s:%d", path, files.Position(current.Pos()).Line)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func s4RepositoryLikeType(expression ast.Expr) bool {
	switch current := expression.(type) {
	case *ast.Ident:
		name := strings.ToLower(current.Name)
		return strings.Contains(name, "repository") || strings.HasSuffix(name, "store") || name == "backend"
	case *ast.SelectorExpr:
		name := strings.ToLower(current.Sel.Name)
		return strings.Contains(name, "repository") || strings.HasSuffix(name, "store")
	case *ast.StarExpr:
		return s4RepositoryLikeType(current.X)
	case *ast.InterfaceType:
		for _, field := range current.Methods.List {
			for _, method := range field.Names {
				for _, repositoryMethods := range s0RepositoryMethods {
					for _, repositoryMethod := range repositoryMethods {
						if method.Name == repositoryMethod {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func TestS0SnapshotShapeCharacterization(t *testing.T) {
	want := []string{
		"Sessions:sessions", "Clients:clients", "OwnerProfile:owner_profile", "OwnerProfiles:owner_profiles,omitempty",
		"PairingCodes:pairing_codes", "ISCPOnboardings:iscp_onboardings,omitempty", "MCPAccessTickets:mcp_access_tickets,omitempty",
		"MCPBindings:mcp_bindings,omitempty", "MCPOperations:mcp_operations,omitempty", "Messages:messages", "RunFeedback:run_feedback",
		"Runs:runs", "ModelCalls:model_calls", "ToolCalls:tool_calls", "DocumentRecords:document_records,omitempty", "Approvals:approvals",
		"Reminders:reminders", "ReminderDelivery:reminder_delivery", "ConnectorSettings:connector_settings,omitempty",
		"EmailProviderSettings:email_provider_settings,omitempty",
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
	store  testBackend
	reopen func(t *testing.T) testBackend
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
			reopen: func(t *testing.T) testBackend {
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

func characterizeSuccessAbsenceOrderScopeAndClone(t *testing.T, st testBackend) {
	t.Helper()
	if _, ok := mustGetDocumentRecord(t, st, "missing"); ok {
		t.Fatal("missing document was reported as present")
	}
	base := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	for _, record := range []app.DocumentRecord{
		{ID: "doc-old", OwnerID: "owner-a", SessionID: "session-a", GovernedPath: "/workspace/old.txt", LastActivityAt: base},
		{ID: "doc-other-owner", OwnerID: "owner-b", SessionID: "session-a", GovernedPath: "/workspace/other.txt", LastActivityAt: base.Add(2 * time.Minute)},
		{ID: "doc-new", OwnerID: "owner-a", SessionID: "session-a", GovernedPath: "/workspace/new.txt", LastActivityAt: base.Add(time.Minute)},
	} {
		mustSaveDocumentRecord(t, st, record)
	}
	got := mustListDocumentRecords(t, st, "owner-a", "session-a", 10)
	if len(got) != 2 || got[0].ID != "doc-new" || got[1].ID != "doc-old" {
		t.Fatalf("owner-scoped document order = %#v", got)
	}

	inputPreferences := map[string]string{"locale": "zh-CN"}
	mustSaveOwnerProfile(t, st, app.OwnerProfile{ID: "owner-clone", DisplayName: "Clone", Preferences: inputPreferences})
	inputPreferences["locale"] = "mutated-input"
	first, ok := mustGetOwnerProfileByID(t, st, "owner-clone")
	if !ok || first.Preferences["locale"] != "zh-CN" {
		t.Fatalf("Store retained caller-owned profile map: %#v ok=%v", first, ok)
	}
	first.Preferences["locale"] = "mutated-output"
	second, ok := mustGetOwnerProfileByID(t, st, "owner-clone")
	if !ok || second.Preferences["locale"] != "zh-CN" {
		t.Fatalf("Store returned backend-owned profile map: %#v ok=%v", second, ok)
	}
}

func characterizeIdempotencyCASAndAliasSafety(t *testing.T, st testBackend) {
	t.Helper()
	operation := app.MCPOperation{
		BindingID: "binding-characterization", IdempotencyKey: "idem-characterization", Fingerprint: "fingerprint-a",
		Invocation: app.MCPInvocationContext{Arguments: map[string]any{"nested": map[string]any{"value": "original"}}},
		Result:     json.RawMessage(`{"value":"original"}`),
	}
	created, wasCreated, err := st.CreateMCPOperation(t.Context(), operation)
	if err != nil || !wasCreated {
		t.Fatalf("create operation: created=%v err=%v", wasCreated, err)
	}
	replayed, wasCreated, err := st.CreateMCPOperation(t.Context(), operation)
	if err != nil || wasCreated || replayed.ID != created.ID {
		t.Fatalf("idempotent replay = %#v created=%v err=%v", replayed, wasCreated, err)
	}
	changed := operation
	changed.Fingerprint = "fingerprint-b"
	if _, _, err := st.CreateMCPOperation(t.Context(), changed); !errors.Is(err, ErrMCPOperationConflict) {
		t.Fatalf("changed idempotency replay error = %v", err)
	}

	created.Invocation.Arguments["nested"].(map[string]any)["value"] = "mutated-output"
	created.Result[0] = '['
	stored, ok := mustGetMCPOperation(t, st, created.ID)
	if !ok || stored.Invocation.Arguments["nested"].(map[string]any)["value"] != "original" || string(stored.Result) != `{"value":"original"}` {
		t.Fatalf("MCP operation alias escaped Store: %#v ok=%v", stored, ok)
	}
	stored.State = app.MCPOperationSucceeded
	updated, err := st.UpdateMCPOperation(t.Context(), stored, stored.Version)
	if err != nil || updated.Version != stored.Version+1 {
		t.Fatalf("CAS update = %#v err=%v", updated, err)
	}
	if _, err := st.UpdateMCPOperation(t.Context(), stored, stored.Version); !errors.Is(err, ErrMCPOperationVersionConflict) {
		t.Fatalf("stale CAS error = %v", err)
	}
}

func characterizeMessageEvents(t *testing.T, st testBackend) {
	t.Helper()
	firstSession := mustCreateSession(t, st, "event-a")
	secondSession := mustCreateSession(t, st, "event-b")
	first := mustAddMessage(t, st, app.Message{SessionID: firstSession.ID, Role: "user", Content: "first"})
	mustAddMessage(t, st, app.Message{SessionID: secondSession.ID, Role: "user", Content: "other"})
	second := mustAddMessage(t, st, app.Message{SessionID: firstSession.ID, Role: "assistant", Content: "second"})
	page, err := st.MessageEventsAfter(t.Context(), firstSession.ID, "", 10)
	if err != nil || len(page.Events) != 2 || page.Events[0].Payload.(app.Message).ID != first.ID || page.Events[1].Payload.(app.Message).ID != second.ID {
		t.Fatalf("message event order/scope = %#v err=%v", page, err)
	}
	if head, err := st.MessageEventHead(t.Context(), firstSession.ID); err != nil || head != page.Events[1].ID {
		t.Fatalf("message event head = %q err=%v", head, err)
	}
}

func characterizeConcurrentIdempotency(t *testing.T, st testBackend) {
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
			operation, wasCreated, err := st.CreateMCPOperation(t.Context(), app.MCPOperation{
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

func characterizeRestart(t *testing.T, st testBackend, reopen func(t *testing.T) testBackend) {
	t.Helper()
	session := mustCreateSession(t, st, "restart")
	message := mustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "assistant", Content: "durable"})
	profile := mustSaveOwnerProfile(t, st, app.OwnerProfile{ID: "owner-restart", DisplayName: "Restart", Preferences: map[string]string{"key": "value"}})
	eventHead, err := st.MessageEventHead(t.Context(), session.ID)
	if err != nil || eventHead == "" {
		t.Fatalf("message event head before restart = %q err=%v", eventHead, err)
	}
	operation, created, err := st.CreateMCPOperation(t.Context(), app.MCPOperation{
		BindingID: "binding-restart", IdempotencyKey: "idem-restart", Fingerprint: "restart",
		Invocation: app.MCPInvocationContext{Arguments: map[string]any{"nested": map[string]any{"value": "durable"}}},
		Result:     json.RawMessage(`{"value":"durable"}`),
	})
	if err != nil || !created {
		t.Fatalf("create operation before restart: created=%v err=%v", created, err)
	}
	reloaded := reopen(t)
	if got, ok := mustGetSession(t, reloaded, session.ID); !ok || got.Title != session.Title {
		t.Fatalf("session did not survive restart: %#v ok=%v", got, ok)
	}
	if messages := mustListMessages(t, reloaded, session.ID); len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("message did not survive restart: %#v", messages)
	}
	if got, ok := mustGetOwnerProfileByID(t, reloaded, profile.ID); !ok || got.Preferences["key"] != "value" {
		t.Fatalf("owner profile did not survive restart: %#v ok=%v", got, ok)
	}
	page, err := reloaded.MessageEventsAfter(t.Context(), session.ID, "", 10)
	if err != nil || len(page.Events) != 1 || page.NextCursor != eventHead || messageFromEvent(t, page.Events[0]).ID != message.ID {
		t.Fatalf("message events did not survive restart: %#v err=%v", page, err)
	}
	gotOperation, ok := mustGetMCPOperation(t, reloaded, operation.ID)
	if !ok || gotOperation.Invocation.Arguments["nested"].(map[string]any)["value"] != "durable" || s0JSONValue(t, gotOperation.Result, "value") != "durable" {
		t.Fatalf("MCP operation did not survive restart: %#v ok=%v", gotOperation, ok)
	}
	gotOperation.Invocation.Arguments["nested"].(map[string]any)["value"] = "mutated-output"
	gotOperation.Result[0] = '['
	again, ok := mustGetMCPOperation(t, reloaded, operation.ID)
	if !ok || again.Invocation.Arguments["nested"].(map[string]any)["value"] != "durable" || s0JSONValue(t, again.Result, "value") != "durable" {
		t.Fatalf("reloaded MCP operation exposed backend aliases: %#v ok=%v", again, ok)
	}
}

func s0JSONValue(t *testing.T, raw json.RawMessage, key string) any {
	t.Helper()
	decoded := map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode JSON result: %v", err)
	}
	return decoded[key]
}

func TestS0DefectEvidenceLegacyFilePersistenceErrorsAreClosed(t *testing.T) {
	source := readS0ProductionSources(t, "*file.go")
	if strings.Contains(source, "s.persist()") || strings.Contains(source, "_ = s.persistSnapshot()") {
		t.Fatal("legacy File persistence error discard returned after ExternalChatRepository migration")
	}
}

func TestS0DefectEvidencePostgresExecResultsAreDiscarded(t *testing.T) {
	count := strings.Count(readS0ProductionSources(t, "*postgres.go"), "_, _ = ")
	if count != 0 {
		t.Fatalf("discarded PostgreSQL result count = %d, want 0 (every Exec result must be checked)", count)
	}
}

func readS0ProductionSources(t *testing.T, pattern string) string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source.WriteString(readS0Source(t, path))
		source.WriteByte('\n')
	}
	return source.String()
}

func readS0Source(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
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
