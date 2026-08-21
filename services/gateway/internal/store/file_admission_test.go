package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var fileCommandMethods = map[string]struct{}{
	"AddAudit": {}, "AddMemoryCandidate": {}, "AddMessage": {},
	"ClaimDueReminders": {}, "ClaimPairingCode": {}, "CreateMCPOperation": {},
	"CreateNotificationBinding": {}, "CreatePassiveNotification": {}, "CreateSession": {}, "CreateSessionWithScope": {},
	"DeleteCredentialSecret": {}, "DeleteMCPAccessRecords": {}, "DeleteMCPAccessTicket": {},
	"DeleteMCPBinding": {}, "DeleteMemory": {}, "DeleteSession": {},
	"MarkAllPassiveNotificationsRead": {}, "MarkPassiveNotificationRead": {},
	"PruneMemories": {}, "PrunePassiveNotifications": {}, "RedeemMCPAccessTicket": {},
	"ResolveApproval": {}, "ResolveMemoryCandidate": {}, "RevokeBrowserAuthRecord": {},
	"RevokeClient": {}, "RevokeMCPAccessTicket": {}, "RevokeMCPBinding": {},
	"SaveApproval": {}, "SaveArtifactObject": {},
	"SaveBrowserAuthRecord": {}, "SaveBrowserLoginBlock": {}, "SaveChannelInboxUpdate": {},
	"SaveCredentialSecret": {}, "SaveDocumentRecord": {},
	"SaveEpisodeSummary": {}, "SaveEvalRun": {}, "SaveExternalChatMessage": {},
	"SaveExternalChatSession": {}, "SaveISCPOnboarding": {}, "SaveMCPAccessTicket": {},
	"SaveMessageDelivery": {}, "SaveMessageReceive": {}, "SaveModelCall": {},
	"SaveOwnerProfile": {}, "SavePairingCode": {},
	"SaveReminder": {}, "SaveReminderDelivery": {}, "SaveRun": {},
	"SaveRunFeedback": {}, "SaveToolCall": {}, "TouchClient": {},
	"TouchMCPBinding": {}, "UpdateBrowserLoginBlock": {}, "UpdateConnectorSetting": {}, "UpdateNotificationBinding": {},
	"UpdateMCPOperation": {}, "UpdateMemory": {}, "UpdateOwnerProfile": {},
	"UpdatePendingApproval": {}, "UpdatePendingReminder": {}, "UpdateSessionTitle": {},
}

var migratedFileAdmissions = map[string]string{
	"SaveISCPOnboarding":  "saveISCPOnboarding",
	"GetISCPOnboarding":   "getISCPOnboarding",
	"ListISCPOnboardings": "listISCPOnboardings",
	"GetOwnerProfile":     "admitMigrated", "UpdateOwnerProfile": "admitMigrated",
	"GetOwnerProfileByID": "admitMigrated", "SaveOwnerProfile": "admitMigrated",
	"ListOwnerProfiles": "admitMigrated", "FindOwnerProfileByExternalRef": "admitMigrated",
	"GetClient": "admitMigrated", "ListClients": "admitMigrated", "RevokeClient": "admitMigrated",
	"FindClientByTokenHash": "admitMigrated", "TouchClient": "admitMigrated",
	"SavePairingCode": "admitMigrated", "GetPairingCode": "admitMigrated", "ClaimPairingCode": "admitMigrated",
	"SaveCredentialSecret": "admitMigrated", "GetCredentialSecret": "admitMigrated", "DeleteCredentialSecret": "admitMigrated",
	"GetConnectorSetting": "admitMigrated", "ListConnectorSettings": "admitMigrated", "ListAllConnectorSettings": "admitMigrated",
	"UpdateConnectorSetting": "admitMigrated", "CreateNotificationBinding": "admitMigrated", "GetNotificationBinding": "admitMigrated",
	"ListNotificationBindings": "admitMigrated", "UpdateNotificationBinding": "admitMigrated",
	"CreateSession": "admitMigrated", "CreateSessionWithScope": "admitMigrated",
	"ListSessions": "admitMigrated", "GetSession": "admitMigrated",
	"UpdateSessionTitle": "admitMigrated", "DeleteSession": "admitMigrated",
	"AddMessage": "admitMigrated", "ListMessages": "admitMigrated",
	"MessageEventHead": "admitMigrated", "MessageEventsAfter": "admitMigrated",
	"SaveRunFeedback": "admitMigrated", "ListRunFeedback": "admitMigrated",
	"SaveRun": "admitMigrated", "GetRun": "admitMigrated", "ListRuns": "admitMigrated",
	"SaveModelCall": "admitMigrated", "ListModelCalls": "admitMigrated",
	"SaveToolCall": "admitMigrated", "GetToolCall": "admitMigrated", "ListToolCalls": "admitMigrated",
	"SaveEpisodeSummary": "admitMigrated", "ListEpisodeSummaries": "admitMigrated",
	"SaveDocumentRecord": "admitMigrated", "GetDocumentRecord": "admitMigrated", "ListDocumentRecords": "admitMigrated",
	"SaveApproval": "admitMigrated", "GetApproval": "admitMigrated", "FindApprovalByExternalRef": "admitMigrated",
	"UpdatePendingApproval": "admitMigrated", "ResolveApproval": "admitMigrated", "ListApprovals": "admitMigrated",
	"AddAudit": "admitMigrated", "ListAudit": "admitMigrated", "EventsAfter": "admitMigrated",
}

func TestFileStorePublicMethodsHaveOneAdmission(t *testing.T) {
	accepted := map[string]struct{}{}
	for _, methods := range s0RepositoryMethods {
		for _, method := range methods {
			if _, exists := accepted[method]; exists {
				t.Fatalf("duplicate accepted FileStore method %s", method)
			}
			accepted[method] = struct{}{}
		}
	}
	if len(accepted) != 140 {
		t.Fatalf("accepted FileStore method count = %d, want 140", len(accepted))
	}
	if len(fileCommandMethods) != 61 {
		t.Fatalf("FileStore command classification count = %d, want 61", len(fileCommandMethods))
	}
	for method := range fileCommandMethods {
		if _, exists := accepted[method]; !exists {
			t.Fatalf("command classification contains unknown FileStore method %s", method)
		}
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(files, entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || !function.Name.IsExported() || !isFileStoreMethod(function) {
				continue
			}
			name := function.Name.Name
			if _, exists := accepted[name]; !exists {
				t.Errorf("unexpected public FileStore method %s", name)
				continue
			}
			if previous := found[name]; previous != "" {
				t.Errorf("FileStore method %s is declared in both %s and %s", name, previous, entry.Name())
				continue
			}
			found[name] = entry.Name()

			want := "admitLegacyRead"
			if _, command := fileCommandMethods[name]; command {
				want = "admitLegacyCommand"
			}
			if migrated := migratedFileAdmissions[name]; migrated != "" {
				want = migrated
			}
			got := firstFileAdmission(function)
			if got != want {
				t.Errorf("%s first statement admission = %q, want %q", name, got, want)
			}
			if count := countFileAdmissions(function); count != 1 {
				t.Errorf("%s admission wrapper count = %d, want 1", name, count)
			}
		}
	}
	for method := range accepted {
		if found[method] == "" {
			t.Errorf("accepted FileStore method %s is not implemented", method)
		}
	}
	if len(found) != len(accepted) {
		t.Fatalf("admitted FileStore method count = %d, want %d", len(found), len(accepted))
	}
}

func isFileStoreMethod(function *ast.FuncDecl) bool {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return false
	}
	pointer, ok := function.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	receiver, ok := pointer.X.(*ast.Ident)
	return ok && receiver.Name == "FileStore"
}

func firstFileAdmission(function *ast.FuncDecl) string {
	if function.Body == nil || len(function.Body.List) == 0 {
		return ""
	}
	var call *ast.CallExpr
	switch statement := function.Body.List[0].(type) {
	case *ast.DeferStmt:
		call = statement.Call
	case *ast.ReturnStmt:
		if len(statement.Results) != 1 {
			return ""
		}
		call, _ = statement.Results[0].(*ast.CallExpr)
	case *ast.AssignStmt:
		if len(statement.Rhs) == 1 {
			call, _ = statement.Rhs[0].(*ast.CallExpr)
		}
	}
	if call == nil {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		acquire, nested := call.Fun.(*ast.CallExpr)
		if !nested || len(acquire.Args) != 0 {
			return ""
		}
		selector, ok = acquire.Fun.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok || receiver.Name != "s" {
		return ""
	}
	return selector.Sel.Name
}

func countFileAdmissions(function *ast.FuncDecl) int {
	count := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "admitLegacyRead" || selector.Sel.Name == "admitLegacyCommand" ||
			selector.Sel.Name == "admitMigrated" || selector.Sel.Name == "saveISCPOnboarding" ||
			selector.Sel.Name == "getISCPOnboarding" || selector.Sel.Name == "listISCPOnboardings") {
			count++
		}
		return true
	})
	return count
}

func TestFileStoreAdmissionBlocksReadersAndCommandsUntilPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := newTestFileStore(path)
	release := store.admitLegacyCommand()
	released := false
	t.Cleanup(func() {
		if !released {
			release()
		}
	})

	tentative := mustClaimTestClient(t, store.inner, app.Client{ID: "client_tentative", Name: "tentative", TokenHash: "tentative-hash"})

	readStarted := make(chan struct{})
	readDone := make(chan struct{})
	go func() {
		close(readStarted)
		_, _, _ = store.GetClient(t.Context(), tentative.ID)
		close(readDone)
	}()
	<-readStarted

	commandStarted := make(chan struct{})
	commandDone := make(chan struct{})
	go func() {
		close(commandStarted)
		_, _ = store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair_queued", CodeHash: "pair_queued_hash", Status: "pending", ExpiresAt: time.Now().UTC().Add(time.Hour)})
		close(commandDone)
	}()
	<-commandStarted

	assertFileAdmissionBlocked(t, readDone, "read")
	assertFileAdmissionBlocked(t, commandDone, "command")
	if err := store.persistSnapshotLocked(); err != nil {
		t.Fatal(err)
	}
	release()
	released = true
	awaitFileAdmission(t, readDone, "read")
	awaitFileAdmission(t, commandDone, "command")

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reloaded.GetClient(t.Context(), tentative.ID); err != nil || !ok {
		t.Fatal("persisted mutation was lost after releasing admission")
	}
	if _, ok, err := reloaded.GetPairingCode(t.Context(), "pair_queued"); err != nil || !ok {
		t.Fatal("queued command did not persist after admission")
	}
}

func TestFileStoreAdmissionAllowsConcurrentReads(t *testing.T) {
	store := newTestFileStore("")
	release := store.admitLegacyRead()
	defer release()

	done := make(chan struct{})
	go func() {
		mustListSessions(t, store)
		close(done)
	}()
	awaitFileAdmission(t, done, "concurrent read")
}

func TestFileStoreAdmissionDoesNotLetReadersBypassQueuedCommand(t *testing.T) {
	store := newTestFileStore(filepath.Join(t.TempDir(), "state.json"))
	releaseRead := store.admitLegacyRead()

	commandStarted := make(chan struct{})
	commandDone := make(chan struct{})
	go func() {
		close(commandStarted)
		_, _ = store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair_writer", CodeHash: "pair_writer_hash", Status: "pending", ExpiresAt: time.Now().UTC().Add(time.Hour)})
		close(commandDone)
	}()
	<-commandStarted
	waitForQueuedFileCommand(t, store)

	readDone := make(chan bool, 1)
	go func() {
		_, ok, _ := store.GetPairingCode(t.Context(), "pair_writer")
		readDone <- ok
	}()
	select {
	case <-readDone:
		t.Fatal("reader bypassed a queued FileStore command")
	case <-time.After(25 * time.Millisecond):
	}

	releaseRead()
	awaitFileAdmission(t, commandDone, "queued command")
	select {
	case ok := <-readDone:
		if !ok {
			t.Fatal("reader ran before the queued command became visible")
		}
	case <-time.After(time.Second):
		t.Fatal("reader remained blocked after queued command completed")
	}
}

func newTestFileStore(path string) *FileStore {
	timeouts := normalizeOperationTimeouts(OperationTimeouts{})
	return &FileStore{
		inner: NewMemoryStoreWithOptions(timeouts), path: path, admission: newFileAdmission(),
		timeouts: timeouts, commitOps: osFileCommitOps{},
	}
}

func waitForQueuedFileCommand(t *testing.T, store *FileStore) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !store.admission.TryAcquire(1) {
			return
		}
		store.admission.Release(1)
		time.Sleep(time.Millisecond)
	}
	t.Fatal("command did not queue for exclusive FileStore admission")
}

func assertFileAdmissionBlocked(t *testing.T, done <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-done:
		t.Fatalf("%s passed exclusive FileStore admission", operation)
	case <-time.After(25 * time.Millisecond):
	}
}

func awaitFileAdmission(t *testing.T, done <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("%s did not complete after FileStore admission was released", operation)
	}
}
