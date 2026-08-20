package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileStoreRequiresDurablePath(t *testing.T) {
	for _, path := range []string{"", "   "} {
		if _, err := NewFileStore(path); err == nil || !strings.Contains(err.Error(), "path") {
			t.Fatalf("NewFileStore(%q) error = %v", path, err)
		}
	}
}

func TestFileSnapshotJSONShapeRemainsStable(t *testing.T) {
	want := []string{
		"sessions", "clients", "owner_profile", "owner_profiles", "pairing_codes", "iscp_onboardings",
		"mcp_access_tickets", "mcp_bindings", "mcp_operations", "messages", "run_feedback", "runs",
		"model_calls", "tool_calls", "document_records", "approvals", "reminders", "reminder_delivery",
		"connector_settings", "notification_bindings", "passive_notifications", "external_chat_sessions",
		"external_chat_messages", "message_receives", "message_deliveries", "channel_inbox_updates",
		"weixin_chat_sessions", "weixin_chat_messages", "credential_secrets", "browser_auth_records",
		"browser_login_blocks", "memories", "memory_candidates", "audit_events", "events", "eval_runs",
		"artifact_objects", "episode_summaries",
	}
	typeOfSnapshot := reflect.TypeOf(Snapshot{})
	got := make([]string, 0, typeOfSnapshot.NumField())
	for index := range typeOfSnapshot.NumField() {
		name := strings.Split(typeOfSnapshot.Field(index).Tag.Get("json"), ",")[0]
		got = append(got, name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Snapshot JSON fields changed:\n got %v\nwant %v", got, want)
	}

	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-shape", app.DefaultOwnerID)
	raw, err := json.Marshal(Snapshot{ISCPOnboardings: map[string]app.ISCPOnboarding{receipt.ID: receipt}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"signature"`) || strings.Contains(string(raw), "signed-ticket-value") {
		t.Fatalf("Snapshot exposed Pairing Ticket secret material: %s", raw)
	}
}

func TestFileStoreRejectsCorruptPersistedOnboarding(t *testing.T) {
	for _, mode := range []struct {
		name      string
		encrypted bool
	}{{name: "plaintext"}, {name: "encrypted", encrypted: true}} {
		for _, corruption := range []string{"key mismatch", "invalid receipt"} {
			t.Run(mode.name+"/"+corruption, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state.json")
				options := FileStoreOptions{Path: path, EncryptAtRest: mode.encrypted, EncryptionKey: "onboarding-test-key"}
				receipt := testISCPOnboarding(time.Now().UTC(), "receipt-corrupt", app.DefaultOwnerID)
				key := receipt.ID
				if corruption == "key mismatch" {
					key = "different-key"
				} else {
					receipt.MaxUses = 2
				}
				encryption, err := newFileEncryption(options)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := (osFileCommitOps{}).Encode(Snapshot{
					ISCPOnboardings: map[string]app.ISCPOnboarding{key: receipt},
				}, encryption)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := NewFileStoreWithOptions(options); err == nil {
					t.Fatal("corrupt persisted onboarding was accepted")
				}
			})
		}
	}
}

var (
	errFileCommitInjected  = errors.New("injected file commit failure")
	errFileCleanupInjected = errors.New("injected file cleanup failure")
)

type controlledFileCommitOps struct {
	base osFileCommitOps

	mu             sync.Mutex
	failStage      string
	failRemaining  int
	removeErr      error
	renameApplied  bool
	afterFileClose func()

	fileSyncEntered chan struct{}
	fileSyncRelease chan struct{}
	fileSyncOnce    sync.Once
	dirSyncEntered  chan struct{}
	dirSyncRelease  chan struct{}
	dirSyncOnce     sync.Once
}

func (o *controlledFileCommitOps) shouldFail(stage string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failStage != stage || o.failRemaining == 0 {
		return false
	}
	if o.failRemaining > 0 {
		o.failRemaining--
	}
	return true
}

func (o *controlledFileCommitOps) Encode(snapshot Snapshot, encryption *fileEncryption) ([]byte, error) {
	if o.shouldFail("encode") {
		return nil, errFileCommitInjected
	}
	return o.base.Encode(snapshot, encryption)
}

func (o *controlledFileCommitOps) MkdirAll(path string, mode os.FileMode) error {
	if o.shouldFail("mkdir") {
		return errFileCommitInjected
	}
	return o.base.MkdirAll(path, mode)
}

func (o *controlledFileCommitOps) CreateTemp(directory, pattern string) (fileCommitHandle, error) {
	if o.shouldFail("create") {
		return nil, errFileCommitInjected
	}
	handle, err := o.base.CreateTemp(directory, pattern)
	if err != nil {
		return nil, err
	}
	return &controlledFileCommitHandle{FileCommitHandle: handle, owner: o, kind: "file"}, nil
}

func (o *controlledFileCommitOps) Rename(oldPath, newPath string) error {
	if !o.shouldFail("rename") {
		return o.base.Rename(oldPath, newPath)
	}
	if o.renameApplied {
		if err := o.base.Rename(oldPath, newPath); err != nil {
			return err
		}
	}
	return errFileCommitInjected
}

func (o *controlledFileCommitOps) ReadFile(path string) ([]byte, error) {
	if o.shouldFail("read") {
		return nil, errFileCommitInjected
	}
	return o.base.ReadFile(path)
}

func (o *controlledFileCommitOps) Remove(path string) error {
	if o.removeErr != nil {
		return o.removeErr
	}
	return o.base.Remove(path)
}

func (o *controlledFileCommitOps) OpenDirectory(path string) (fileCommitHandle, error) {
	if o.shouldFail("dir_open") {
		return nil, errFileCommitInjected
	}
	handle, err := o.base.OpenDirectory(path)
	if err != nil {
		return nil, err
	}
	return &controlledFileCommitHandle{FileCommitHandle: handle, owner: o, kind: "directory"}, nil
}

type controlledFileCommitHandle struct {
	FileCommitHandle fileCommitHandle
	owner            *controlledFileCommitOps
	kind             string
}

func (h *controlledFileCommitHandle) Name() string { return h.FileCommitHandle.Name() }

func (h *controlledFileCommitHandle) Write(payload []byte) (int, error) {
	if h.owner.shouldFail("write") {
		partial := len(payload) / 2
		if partial == 0 {
			partial = 1
		}
		written, _ := h.FileCommitHandle.Write(payload[:partial])
		return written, errFileCommitInjected
	}
	return h.FileCommitHandle.Write(payload)
}

func (h *controlledFileCommitHandle) Sync() error {
	if h.kind == "file" && h.owner.fileSyncEntered != nil {
		h.owner.fileSyncOnce.Do(func() { close(h.owner.fileSyncEntered) })
		<-h.owner.fileSyncRelease
	}
	if h.kind == "directory" && h.owner.dirSyncEntered != nil {
		h.owner.dirSyncOnce.Do(func() { close(h.owner.dirSyncEntered) })
		<-h.owner.dirSyncRelease
	}
	stage := "file_sync"
	if h.kind == "directory" {
		stage = "dir_sync"
	}
	if h.owner.shouldFail(stage) {
		return errFileCommitInjected
	}
	return h.FileCommitHandle.Sync()
}

func (h *controlledFileCommitHandle) Close() error {
	err := h.FileCommitHandle.Close()
	stage := "file_close"
	if h.kind == "directory" {
		stage = "dir_close"
	}
	if h.owner.shouldFail(stage) {
		err = errors.Join(err, errFileCommitInjected)
	}
	if h.kind == "file" && h.owner.afterFileClose != nil {
		h.owner.afterFileClose()
	}
	return err
}

func TestFileOnboardingPreSubmitFailuresRestoreCompleteState(t *testing.T) {
	for _, mode := range []struct {
		name      string
		encrypted bool
	}{{name: "plaintext"}, {name: "encrypted", encrypted: true}} {
		for _, stage := range []string{"encode", "mkdir", "create", "write", "file_sync", "file_close"} {
			t.Run(mode.name+"/"+stage, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state.json")
				store, err := NewFileStoreWithOptions(FileStoreOptions{
					Path: path, EncryptAtRest: mode.encrypted, EncryptionKey: "onboarding-test-key",
				})
				if err != nil {
					t.Fatal(err)
				}
				store.SaveClient(app.Client{ID: "baseline-client", Name: "baseline"})
				store.inner.mu.Lock()
				store.inner.passiveNotificationRevs[app.DefaultOwnerID] = 17
				store.inner.mu.Unlock()
				before := store.captureFileRollback()
				controlled := &controlledFileCommitOps{failStage: stage, failRemaining: 1}
				store.commitOps = controlled

				_, err = store.SaveISCPOnboarding(context.Background(), testISCPOnboarding(time.Now().UTC(), "receipt-"+mode.name+"-"+stage, app.DefaultOwnerID))
				if StoreErrorCodeOf(err) != StoreErrorDurability || !errors.Is(err, errFileCommitInjected) {
					t.Fatalf("stage %s error = %v code=%q", stage, err, StoreErrorCodeOf(err))
				}
				after := store.captureFileRollback()
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("stage %s did not restore complete rollback state", stage)
				}
				if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".sparkclaw-state-*")); err != nil || len(matches) != 0 {
					t.Fatalf("stage %s retained temporary files %v err=%v", stage, matches, err)
				}
			})
		}
	}
}

func TestFileOnboardingDestinationReadFailureDoesNotMutate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.SaveClient(app.Client{ID: "baseline-client", Name: "baseline"})
	before := store.captureFileRollback()
	store.commitOps = &controlledFileCommitOps{failStage: "read", failRemaining: 1}

	_, err = store.SaveISCPOnboarding(context.Background(), testISCPOnboarding(time.Now().UTC(), "receipt-read-failure", app.DefaultOwnerID))
	if StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, errFileCommitInjected) {
		t.Fatalf("destination read error = %v code=%q", err, StoreErrorCodeOf(err))
	}
	if after := store.captureFileRollback(); !reflect.DeepEqual(after, before) {
		t.Fatal("destination read failure mutated in-memory state")
	}
}

func TestFileOnboardingCancellationBeforeAdmissionDoesNotMutate(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.admission.Acquire(context.Background(), fileAdmissionCapacity); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.SaveISCPOnboarding(ctx, testISCPOnboarding(time.Now().UTC(), "receipt-before-admission", app.DefaultOwnerID))
	store.admission.Release(fileAdmissionCapacity)
	if StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("pre-admission cancellation = %v code=%q", err, StoreErrorCodeOf(err))
	}
	if _, found, err := store.GetISCPOnboarding(context.Background(), "receipt-before-admission"); err != nil || found {
		t.Fatalf("canceled save mutated state: found=%v err=%v", found, err)
	}
}

func TestFileOnboardingCancellationBeforeRenameRestoresState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	before := store.captureFileRollback()
	ctx, cancel := context.WithCancel(context.Background())
	controlled := &controlledFileCommitOps{afterFileClose: cancel}
	store.commitOps = controlled
	_, err = store.SaveISCPOnboarding(ctx, testISCPOnboarding(time.Now().UTC(), "receipt-canceled", app.DefaultOwnerID))
	if StoreErrorCodeOf(err) != StoreErrorCanceled || !reflect.DeepEqual(store.captureFileRollback(), before) {
		t.Fatalf("pre-rename cancellation = %v code=%q rollback=%v", err, StoreErrorCodeOf(err), reflect.DeepEqual(store.captureFileRollback(), before))
	}
}

func TestFileOnboardingCleanupFailureRetainsPrimaryCause(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.commitOps = &controlledFileCommitOps{
		failStage: "write", failRemaining: 1, removeErr: errFileCleanupInjected,
	}
	_, err = store.SaveISCPOnboarding(context.Background(), testISCPOnboarding(time.Now().UTC(), "receipt-cleanup", app.DefaultOwnerID))
	if StoreErrorCodeOf(err) != StoreErrorDurability || !errors.Is(err, errFileCommitInjected) || !errors.Is(err, errFileCleanupInjected) {
		t.Fatalf("cleanup failure lost causes: %v", err)
	}
}

func TestFileOnboardingRenameFailureClassification(t *testing.T) {
	t.Run("previous destination is definite failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		store, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		store.SaveClient(app.Client{ID: "baseline", Name: "baseline"})
		store.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1}
		_, err = store.SaveISCPOnboarding(context.Background(), testISCPOnboarding(time.Now().UTC(), "receipt-previous", app.DefaultOwnerID))
		if StoreErrorCodeOf(err) != StoreErrorDurability || store.currentFileFence() != nil {
			t.Fatalf("rename previous classification = %v code=%q fence=%v", err, StoreErrorCodeOf(err), store.currentFileFence())
		}
	})

	t.Run("candidate destination reconciles", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		store, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		store.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1, renameApplied: true}
		receipt := testISCPOnboarding(time.Now().UTC(), "receipt-candidate", app.DefaultOwnerID)
		_, err = store.SaveISCPOnboarding(context.Background(), receipt)
		if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || store.currentFileFence() == nil {
			t.Fatalf("rename candidate classification = %v code=%q fence=%v", err, StoreErrorCodeOf(err), store.currentFileFence())
		}
		got, found, err := store.GetISCPOnboarding(context.Background(), receipt.ID)
		if err != nil || !found || got.ID != receipt.ID || store.currentFileFence() != nil {
			t.Fatalf("candidate reconciliation = %#v found=%v err=%v fence=%v", got, found, err, store.currentFileFence())
		}
	})
}

func TestFileOnboardingDirectoryFailuresFenceAndReconcile(t *testing.T) {
	for _, mode := range []struct {
		name      string
		encrypted bool
	}{{name: "plaintext"}, {name: "encrypted", encrypted: true}} {
		for _, stage := range []string{"dir_open", "dir_close"} {
			t.Run(mode.name+"/"+stage, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state.json")
				store, err := NewFileStoreWithOptions(FileStoreOptions{
					Path: path, EncryptAtRest: mode.encrypted, EncryptionKey: "onboarding-test-key",
				})
				if err != nil {
					t.Fatal(err)
				}
				store.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
				receipt := testISCPOnboarding(time.Now().UTC(), "receipt-"+mode.name+"-"+stage, app.DefaultOwnerID)

				if _, err := store.SaveISCPOnboarding(context.Background(), receipt); StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || store.currentFileFence() == nil {
					t.Fatalf("%s result = %v code=%q fence=%v", stage, err, StoreErrorCodeOf(err), store.currentFileFence())
				}
				got, found, err := store.GetISCPOnboarding(context.Background(), receipt.ID)
				if err != nil || !found || got.ID != receipt.ID || store.currentFileFence() != nil {
					t.Fatalf("%s reconciliation = %#v found=%v err=%v fence=%v", stage, got, found, err, store.currentFileFence())
				}
			})
		}
	}
}

func TestFileOnboardingFenceBlocksPrequeuedLegacyWaiters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	controlled := &controlledFileCommitOps{
		failStage: "dir_sync", failRemaining: 1,
		dirSyncEntered: make(chan struct{}), dirSyncRelease: make(chan struct{}),
	}
	store.commitOps = controlled
	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-fenced", app.DefaultOwnerID)
	saveDone := make(chan error, 1)
	go func() {
		_, err := store.SaveISCPOnboarding(context.Background(), receipt)
		saveDone <- err
	}()
	<-controlled.dirSyncEntered

	readDone := make(chan struct{})
	go func() {
		store.GetClient("missing")
		close(readDone)
	}()
	commandDone := make(chan struct{})
	go func() {
		store.SaveClient(app.Client{ID: "queued-client", Name: "queued"})
		close(commandDone)
	}()
	close(controlled.dirSyncRelease)
	if err := <-saveDone; StoreErrorCodeOf(err) != StoreErrorUnknownOutcome {
		t.Fatalf("directory sync result = %v code=%q", err, StoreErrorCodeOf(err))
	}
	assertFileAdmissionBlocked(t, readDone, "prequeued legacy read")
	assertFileAdmissionBlocked(t, commandDone, "prequeued legacy command")

	got, found, err := store.GetISCPOnboarding(context.Background(), receipt.ID)
	if err != nil || !found || got.ID != receipt.ID {
		t.Fatalf("fence reconciliation = %#v found=%v err=%v", got, found, err)
	}
	awaitFileAdmission(t, readDone, "prequeued legacy read")
	awaitFileAdmission(t, commandDone, "prequeued legacy command")
}

func TestFileOnboardingFenceRestoresPreviousDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.SaveClient(app.Client{ID: "baseline", Name: "baseline"})
	previous, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-restore", app.DefaultOwnerID)
	_, err = store.SaveISCPOnboarding(context.Background(), receipt)
	if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome {
		t.Fatalf("directory sync result = %v code=%q", err, StoreErrorCodeOf(err))
	}
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	_, found, err := store.GetISCPOnboarding(context.Background(), receipt.ID)
	if err != nil || found || store.currentFileFence() != nil {
		t.Fatalf("previous reconciliation found=%v err=%v fence=%v", found, err, store.currentFileFence())
	}
}

func TestFileOnboardingReadRejectsUnresolvedFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 2}
	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-unresolved", app.DefaultOwnerID)
	if _, err := store.SaveISCPOnboarding(context.Background(), receipt); StoreErrorCodeOf(err) != StoreErrorUnknownOutcome {
		t.Fatalf("save error = %v code=%q", err, StoreErrorCodeOf(err))
	}
	got, found, err := store.GetISCPOnboarding(context.Background(), receipt.ID)
	if got.ID != "" || found || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || store.currentFileFence() == nil {
		t.Fatalf("unresolved read = %#v found=%v err=%v fence=%v", got, found, err, store.currentFileFence())
	}
	got, found, err = store.GetISCPOnboarding(context.Background(), receipt.ID)
	if err != nil || !found || got.ID != receipt.ID || store.currentFileFence() != nil {
		t.Fatalf("resolved read = %#v found=%v err=%v fence=%v", got, found, err, store.currentFileFence())
	}
}

func TestFileOnboardingReadCannotObserveTentativeMutation(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	controlled := &controlledFileCommitOps{fileSyncEntered: make(chan struct{}), fileSyncRelease: make(chan struct{})}
	store.commitOps = controlled
	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-tentative", app.DefaultOwnerID)
	saveDone := make(chan error, 1)
	go func() {
		_, err := store.SaveISCPOnboarding(context.Background(), receipt)
		saveDone <- err
	}()
	<-controlled.fileSyncEntered
	readCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := store.GetISCPOnboarding(readCtx, receipt.ID); StoreErrorCodeOf(err) != StoreErrorTimeout {
		t.Fatalf("tentative read error = %v code=%q", err, StoreErrorCodeOf(err))
	}
	close(controlled.fileSyncRelease)
	if err := <-saveDone; err != nil {
		t.Fatal(err)
	}
}

func TestFileOnboardingCommitModeAndEncryptionParity(t *testing.T) {
	for _, mode := range []struct {
		name      string
		encrypted bool
	}{{name: "plaintext"}, {name: "encrypted", encrypted: true}} {
		t.Run(mode.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			options := FileStoreOptions{Path: path, EncryptAtRest: mode.encrypted, EncryptionKey: "onboarding-test-key"}
			store, err := NewFileStoreWithOptions(options)
			if err != nil {
				t.Fatal(err)
			}
			receipt := testISCPOnboarding(time.Now().UTC(), "receipt-"+mode.name, app.DefaultOwnerID)
			if _, err := store.SaveISCPOnboarding(context.Background(), receipt); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode.encrypted && (string(raw) == "" || containsBytes(raw, []byte(receipt.TicketID))) {
				t.Fatal("encrypted onboarding snapshot exposed receipt contents")
			}
			reloaded, err := NewFileStoreWithOptions(options)
			if err != nil {
				t.Fatal(err)
			}
			if got, found, err := reloaded.GetISCPOnboarding(context.Background(), receipt.ID); err != nil || !found || got.ID != receipt.ID {
				t.Fatalf("%s reload = %#v found=%v err=%v", mode.name, got, found, err)
			}
		})
	}
}

func containsBytes(payload, fragment []byte) bool {
	for index := 0; index+len(fragment) <= len(payload); index++ {
		if reflect.DeepEqual(payload[index:index+len(fragment)], fragment) {
			return true
		}
	}
	return false
}
