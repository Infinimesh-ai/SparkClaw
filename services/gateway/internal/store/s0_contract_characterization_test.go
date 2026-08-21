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
	"strconv"
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
		"ListNotificationBindings", "UpdateConnectorSetting", "UpdateNotificationBinding",
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
	if typeOfStore.NumMethod() != 140 {
		t.Fatalf("Store method count = %d, want migrated baseline 140", typeOfStore.NumMethod())
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

func TestS0ProductionStoreConsumerInventory(t *testing.T) {
	wantDirectConsumers := map[string]int{
		"cmd/sparkclaw/bootstrap.go:func newGatewayServices":             1,
		"cmd/sparkclaw/connectors.go:func newConnectorAssembly":          1,
		"cmd/sparkclaw/main.go:func newStore":                            1,
		"internal/agent/agent.go:func NewRuntime":                        1,
		"internal/agent/agent.go:func NewRuntimeWithContext":             1,
		"internal/agent/agent.go:type Runtime":                           1,
		"internal/agent/tool_exposure.go:func newToolExposureEngine":     1,
		"internal/agent/tool_exposure.go:type toolExposureEngine":        1,
		"internal/gateway/server.go:func New":                            1,
		"internal/gateway/server.go:func NewWithTrace":                   1,
		"internal/gateway/server.go:func runHasPendingApproval":          1,
		"internal/gateway/server.go:type Server":                         1,
		"internal/happyapproval/service.go:func New":                     1,
		"internal/happyapproval/service.go:type Service":                 1,
		"internal/iscpbridge/adapter.go:func NewGatewayAdapter":          1,
		"internal/iscpbridge/adapter.go:type GatewayAdapter":             1,
		"internal/mcpaccess/operation.go:func rejectPendingApprovals":    1,
		"internal/mcpaccess/operation.go:func updateOperationRecord":     1,
		"internal/mcpaccess/provider.go:func NewProvider":                1,
		"internal/mcpaccess/provider.go:type Provider":                   1,
		"internal/mcpaccess/service.go:func New":                         1,
		"internal/mcpaccess/service.go:func finalizeRevokedOperations":   1,
		"internal/mcpaccess/service.go:func runHasApprovedApproval":      1,
		"internal/mcpaccess/service.go:func runHasPendingApproval":       1,
		"internal/mcpaccess/service.go:type Service":                     1,
		"internal/notification/notification.go:func NewWeixinAdapter":    1,
		"internal/notification/notification.go:func SendWeixinFile":      1,
		"internal/notification/notification.go:func SendWeixinImage":     1,
		"internal/notification/notification.go:func SendWeixinText":      1,
		"internal/notification/notification.go:func SendWeixinTyping":    1,
		"internal/notification/notification.go:type WeixinAdapter":       1,
		"internal/reminder/scheduler.go:func NewMessageScheduler":        1,
		"internal/reminder/scheduler.go:type Scheduler":                  1,
		"internal/remindertarget/target.go:func NewResolver":             1,
		"internal/remindertarget/target.go:type Resolver":                1,
		"internal/store/artifact_helpers.go:func ArchiveToolObservation": 1,
		"internal/telegram/dispatcher.go:func NewDispatcher":             1,
		"internal/telegram/dispatcher.go:type Dispatcher":                1,
		"internal/telegram/notification.go:func NewNotificationAdapter":  1,
		"internal/telegram/notification.go:type NotificationAdapter":     1,
		"internal/telegram/service.go:func NewService":                   1,
		"internal/telegram/service.go:type Service":                      1,
		"internal/toolhub/toolhub.go:func New":                           1,
		"internal/toolhub/toolhub.go:type ToolHub":                       1,
		"internal/weixin/chat.go:func NewDispatcher":                     1,
		"internal/weixin/chat.go:func NewDispatcherWithConfig":           1,
		"internal/weixin/chat.go:type Dispatcher":                        1,
		"internal/weixin/media.go:func NewMediaAdapter":                  1,
		"internal/weixin/media.go:type MediaAdapter":                     1,
		"internal/weixin/syncer.go:func NewSyncer":                       1,
		"internal/weixin/syncer.go:type Syncer":                          1,
	}
	wantLocalInterfaces := map[string][]string{
		"internal/delivery/content.go:governedArtifactStore":            {"GetSession", "ListArtifactObjects"},
		"internal/delivery/record.go:externalDeliveryStore":             {"GetExternalChatSession", "SaveExternalChatMessage"},
		"internal/delivery/resource.go:artifactStore":                   {"ListArtifactObjects"},
		"internal/delivery/resource.go:endpointResourceStore":           {"GetSession", "ListArtifactObjects"},
		"internal/delivery/web.go:webMessageStore":                      {"AddMessage", "ListMessages"},
		"internal/iscppairing/service.go:Repository":                    {"AddAudit"},
		"internal/messagecontrol/endpoint_registry.go:endpointStore":    {"GetExternalChatSession", "GetNotificationBinding", "GetSession", "ListExternalChatSessions"},
		"internal/messagecontrol/endpoint_registry.go:mcpEndpointStore": {"GetMCPBinding"},
		"internal/messagecontrol/receive_lifecycle.go:receiveStore":     {"FindMessageReceive", "SaveMessageReceive"},
		"internal/messagecontrol/schedule_registry.go:scheduleStore": {
			"ClaimDueReminders", "GetExternalChatSession", "GetNotificationBinding", "GetReminder", "GetSession", "ListExternalChatSessions", "ListReminders", "SaveReminder", "UpdatePendingReminder",
		},
	}
	wantAnonymousInterfaces := map[string][]string{
		"internal/mcpaccess/audit.go:func auditOperationStore:anonymous interface 1": {"AddAudit", "GetMCPBinding"},
		"internal/mcpaccess/audit.go:func operationSessionID:anonymous interface 1":  {"GetMCPBinding"},
	}

	gotDirectConsumers, gotLocalInterfaces, gotAnonymousInterfaces := collectS0ProductionStoreConsumers(t)
	if !reflect.DeepEqual(gotDirectConsumers, wantDirectConsumers) {
		t.Fatalf("direct production store.Store consumers changed\n got: %#v\nwant: %#v", gotDirectConsumers, wantDirectConsumers)
	}
	if !reflect.DeepEqual(gotLocalInterfaces, wantLocalInterfaces) {
		t.Fatalf("local Store-compatible consumer interfaces changed\n got: %#v\nwant: %#v", gotLocalInterfaces, wantLocalInterfaces)
	}
	if !reflect.DeepEqual(gotAnonymousInterfaces, wantAnonymousInterfaces) {
		t.Fatalf("anonymous Store-compatible consumer interfaces changed\n got: %#v\nwant: %#v", gotAnonymousInterfaces, wantAnonymousInterfaces)
	}
	assertS0ProductionConsumerDocumentation(t)
}

func collectS0ProductionStoreConsumers(t *testing.T) (map[string]int, map[string][]string, map[string][]string) {
	t.Helper()
	gatewayRoot := filepath.Clean(filepath.Join("..", ".."))
	storeMethods := map[string]struct{}{}
	typeOfStore := reflect.TypeOf((*Store)(nil)).Elem()
	for index := range typeOfStore.NumMethod() {
		storeMethods[typeOfStore.Method(index).Name] = struct{}{}
	}
	direct := map[string]int{}
	localInterfaces := map[string][]string{}
	anonymousInterfaces := map[string][]string{}
	err := filepath.WalkDir(gatewayRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(gatewayRoot, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
		if err != nil {
			return err
		}
		if parsed.Name.Name == "store" {
			for _, declaration := range parsed.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if count := countS0StoreIdentifiers(function); count != 0 {
					direct[filepath.ToSlash(relative)+":func "+function.Name.Name] = count
				}
			}
			return nil
		}
		interfaces := map[string]*ast.InterfaceType{}
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, rawSpec := range general.Specs {
				spec := rawSpec.(*ast.TypeSpec)
				if typed, ok := spec.Type.(*ast.InterfaceType); ok {
					interfaces[spec.Name.Name] = typed
				}
			}
		}
		for name := range interfaces {
			methods := s0InterfaceStoreMethods(name, interfaces, storeMethods, map[string]bool{})
			if len(methods) != 0 {
				localInterfaces[filepath.ToSlash(relative)+":"+name] = methods
			}
		}
		for _, declaration := range parsed.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				anonymousIndex := 0
				ast.Inspect(typed, func(node ast.Node) bool {
					interfaceType, ok := node.(*ast.InterfaceType)
					if !ok {
						return true
					}
					methods := s0AnonymousInterfaceStoreMethods(interfaceType, interfaces, storeMethods)
					if len(methods) == 0 {
						return true
					}
					anonymousIndex++
					key := filepath.ToSlash(relative) + ":func " + typed.Name.Name + ":anonymous interface " + strconv.Itoa(anonymousIndex)
					anonymousInterfaces[key] = methods
					return true
				})
				if count := countS0StoreSelectors(typed); count != 0 {
					direct[filepath.ToSlash(relative)+":func "+typed.Name.Name] = count
				}
			case *ast.GenDecl:
				for _, rawSpec := range typed.Specs {
					spec, ok := rawSpec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if count := countS0StoreSelectors(spec); count != 0 {
						direct[filepath.ToSlash(relative)+":type "+spec.Name.Name] = count
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return direct, localInterfaces, anonymousInterfaces
}

func countS0StoreIdentifiers(node ast.Node) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		if _, qualified := node.(*ast.SelectorExpr); qualified {
			return false
		}
		if identifier, ok := node.(*ast.Ident); ok && identifier.Name == "Store" {
			count++
		}
		return true
	})
	return count
}

func countS0StoreSelectors(node ast.Node) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Store" {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if ok && packageName.Name == "store" {
			count++
		}
		return true
	})
	return count
}

func s0InterfaceStoreMethods(name string, interfaces map[string]*ast.InterfaceType, storeMethods map[string]struct{}, visiting map[string]bool) []string {
	if visiting[name] {
		return nil
	}
	visiting[name] = true
	defer delete(visiting, name)
	methods := map[string]struct{}{}
	for _, field := range interfaces[name].Methods.List {
		if len(field.Names) == 0 {
			if embedded, ok := field.Type.(*ast.Ident); ok && interfaces[embedded.Name] != nil {
				for _, method := range s0InterfaceStoreMethods(embedded.Name, interfaces, storeMethods, visiting) {
					methods[method] = struct{}{}
				}
			}
			continue
		}
		for _, method := range field.Names {
			if _, exists := storeMethods[method.Name]; exists {
				methods[method.Name] = struct{}{}
			}
		}
	}
	return sortedKeys(methods)
}

func s0AnonymousInterfaceStoreMethods(interfaceType *ast.InterfaceType, interfaces map[string]*ast.InterfaceType, storeMethods map[string]struct{}) []string {
	methods := map[string]struct{}{}
	for _, field := range interfaceType.Methods.List {
		if len(field.Names) == 0 {
			if embedded, ok := field.Type.(*ast.Ident); ok && interfaces[embedded.Name] != nil {
				for _, method := range s0InterfaceStoreMethods(embedded.Name, interfaces, storeMethods, map[string]bool{}) {
					methods[method] = struct{}{}
				}
			}
			continue
		}
		for _, method := range field.Names {
			if _, exists := storeMethods[method.Name]; exists {
				methods[method.Name] = struct{}{}
			}
		}
	}
	return sortedKeys(methods)
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
	profile := mustSaveOwnerProfile(t, st, app.OwnerProfile{ID: "owner-restart", DisplayName: "Restart", Preferences: map[string]string{"key": "value"}})
	eventHead, err := st.MessageEventHead(session.ID)
	if err != nil || eventHead == "" {
		t.Fatalf("message event head before restart = %q err=%v", eventHead, err)
	}
	operation, created, err := st.CreateMCPOperation(app.MCPOperation{
		BindingID: "binding-restart", IdempotencyKey: "idem-restart", Fingerprint: "restart",
		Invocation: app.MCPInvocationContext{Arguments: map[string]any{"nested": map[string]any{"value": "durable"}}},
		Result:     json.RawMessage(`{"value":"durable"}`),
	})
	if err != nil || !created {
		t.Fatalf("create operation before restart: created=%v err=%v", created, err)
	}
	reloaded := reopen(t)
	if got, ok := reloaded.GetSession(session.ID); !ok || got.Title != session.Title {
		t.Fatalf("session did not survive restart: %#v ok=%v", got, ok)
	}
	if messages := reloaded.ListMessages(session.ID); len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("message did not survive restart: %#v", messages)
	}
	if got, ok := mustGetOwnerProfileByID(t, reloaded, profile.ID); !ok || got.Preferences["key"] != "value" {
		t.Fatalf("owner profile did not survive restart: %#v ok=%v", got, ok)
	}
	page, err := reloaded.MessageEventsAfter(session.ID, "", 10)
	if err != nil || len(page.Events) != 1 || page.NextCursor != eventHead || messageFromEvent(t, page.Events[0]).ID != message.ID {
		t.Fatalf("message events did not survive restart: %#v err=%v", page, err)
	}
	gotOperation, ok := reloaded.GetMCPOperation(operation.ID)
	if !ok || gotOperation.Invocation.Arguments["nested"].(map[string]any)["value"] != "durable" || s0JSONValue(t, gotOperation.Result, "value") != "durable" {
		t.Fatalf("MCP operation did not survive restart: %#v ok=%v", gotOperation, ok)
	}
	gotOperation.Invocation.Arguments["nested"].(map[string]any)["value"] = "mutated-output"
	gotOperation.Result[0] = '['
	again, ok := reloaded.GetMCPOperation(operation.ID)
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

func TestS0DefectEvidenceLegacyFilePersistenceErrorsAreDiscarded(t *testing.T) {
	source := readS0Source(t, "file.go")
	if got := strings.Count(source, "s.persist()"); got != 36 {
		t.Fatalf("legacy File persist call count = %d, want remaining S3 defect baseline 36", got)
	}
	body := sourceFunctionBody(t, "file.go", "persist")
	if !strings.Contains(body, "_ = s.persistSnapshot()") {
		t.Fatal("legacy File persist no longer matches the recorded error-discard defect; replace this evidence in the owning migration stage")
	}
}

type s0PostgresRowsErrCase struct {
	repository string
	file       string
	function   string
	loops      int
}

var s0PostgresRowsErrCases = []s0PostgresRowsErrCase{
	{"MCPRepository", "mcp_access_postgres.go", "ListMCPAccessTickets", 1},
	{"MCPRepository", "mcp_access_postgres.go", "ListMCPBindings", 1},
	{"MCPRepository", "mcp_access_postgres.go", "ListMCPOperations", 1},
	{"PassiveNotificationRepository", "postgres.go", "PrunePassiveNotifications", 2},
	{"DeliveryRecordRepository", "postgres.go", "ListMessageReceives", 1},
	{"DeliveryRecordRepository", "postgres.go", "ListMessageDeliveries", 1},
	{"MemoryRepository", "postgres.go", "PruneMemories", 1},
	{"shared", "postgres.go", "collectRows", 1},
}

func TestS0DefectEvidencePostgresRowsErrIsNotChecked(t *testing.T) {
	loopCount := 0
	for _, testCase := range s0PostgresRowsErrCases {
		t.Run(testCase.repository+"/"+testCase.function, func(t *testing.T) {
			body := sourceFunctionBody(t, testCase.file, testCase.function)
			if !strings.Contains(body, ".Next()") || strings.Contains(body, ".Err()") {
				t.Errorf("%s.%s no longer matches the recorded rows.Err defect evidence; replace this evidence in the owning migration stage", testCase.file, testCase.function)
			}
			if got := strings.Count(body, ".Next()"); got != testCase.loops {
				t.Errorf("%s.%s row loops = %d, want %d", testCase.file, testCase.function, got, testCase.loops)
			}
			loopCount += strings.Count(body, ".Next()")
		})
	}
	if loopCount != 9 {
		t.Fatalf("unchecked PostgreSQL row loop count = %d, want remaining S0 defect baseline 9", loopCount)
	}
}

func TestS0DefectEvidencePostgresExecResultsAreDiscarded(t *testing.T) {
	files := []string{"postgres.go", "iscp_onboarding_postgres.go", "mcp_access_postgres.go"}
	count := 0
	for _, file := range files {
		count += strings.Count(readS0Source(t, file), "_, _ = ")
	}
	if count != 24 {
		t.Fatalf("discarded PostgreSQL result count = %d, want remaining S3 defect baseline 24", count)
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
