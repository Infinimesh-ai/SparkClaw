package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileConnectorPreSubmitFailuresRestoreCompleteState(t *testing.T) {
	for _, mode := range []struct {
		name      string
		encrypted bool
	}{{name: "plaintext"}, {name: "encrypted", encrypted: true}} {
		for _, stage := range []string{"encode", "mkdir", "create", "write", "file_sync", "file_close"} {
			t.Run(mode.name+"/"+stage, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "connector.json")
				st, err := NewFileStoreWithOptions(FileStoreOptions{
					Path: path, EncryptAtRest: mode.encrypted, EncryptionKey: "connector-file-test-key",
				})
				if err != nil {
					t.Fatal(err)
				}
				before := st.captureFileRollback()
				st.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
				candidate, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
					OwnerID: "owner", Channel: "alpha", Enabled: true,
				}, 0)
				if candidate.Channel != "" || StoreErrorCodeOf(err) != StoreErrorDurability || !errors.Is(err, errFileCommitInjected) {
					t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
				}
				if after := st.captureFileRollback(); !reflect.DeepEqual(after, before) {
					t.Fatalf("stage %s did not restore connector, audit, and event state", stage)
				}
				if matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), ".sparkclaw-state-*")); globErr != nil || len(matches) != 0 {
					t.Fatalf("stage %s retained temporary files %v err=%v", stage, matches, globErr)
				}
			})
		}
	}
}

func TestFileConnectorRenameOutcomesAreReconciled(t *testing.T) {
	t.Run("previous destination", func(t *testing.T) {
		st, err := NewFileStore(filepath.Join(t.TempDir(), "connector.json"))
		if err != nil {
			t.Fatal(err)
		}
		baseline, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "baseline"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		st.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1}
		candidate, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha"}, 0)
		if candidate.Channel != "" || StoreErrorCodeOf(err) != StoreErrorDurability || st.currentFileFence() != nil {
			t.Fatalf("candidate=%#v err=%v code=%q fence=%v", candidate, err, StoreErrorCodeOf(err), st.currentFileFence())
		}
		if got, found, readErr := st.GetConnectorSetting(t.Context(), baseline.OwnerID, baseline.Channel); readErr != nil || !found || got.Version != baseline.Version {
			t.Fatalf("baseline=%#v found=%v err=%v", got, found, readErr)
		}
	})

	t.Run("candidate destination", func(t *testing.T) {
		st, err := NewFileStore(filepath.Join(t.TempDir(), "connector.json"))
		if err != nil {
			t.Fatal(err)
		}
		st.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1, renameApplied: true}
		candidate, err := st.CreateNotificationBinding(t.Context(), app.NotificationBinding{
			ID: "binding-rename", OwnerID: "owner", ActorID: "actor", Channel: "telegram",
			Provider: "telegram-bot-api", Status: app.NotificationBindingStarting,
		})
		if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || candidate.ID == "" || st.currentFileFence() == nil {
			t.Fatalf("candidate=%#v err=%v code=%q fence=%v", candidate, err, StoreErrorCodeOf(err), st.currentFileFence())
		}
		got, found, err := st.GetNotificationBinding(t.Context(), candidate.ID)
		if err != nil || !found || !NotificationBindingsEqual(got, candidate) || st.currentFileFence() != nil {
			t.Fatalf("reconciled=%#v found=%v err=%v fence=%v", got, found, err, st.currentFileFence())
		}
	})
}

func TestFileConnectorDirectoryFailuresFenceAndReconcile(t *testing.T) {
	for _, stage := range []string{"dir_open", "dir_sync", "dir_close"} {
		t.Run(stage, func(t *testing.T) {
			st, err := NewFileStore(filepath.Join(t.TempDir(), "connector.json"))
			if err != nil {
				t.Fatal(err)
			}
			st.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
			candidate, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
				OwnerID: "owner", Channel: "alpha", Enabled: true,
			}, 0)
			if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || candidate.Channel != "alpha" || st.currentFileFence() == nil {
				t.Fatalf("candidate=%#v err=%v code=%q fence=%v", candidate, err, StoreErrorCodeOf(err), st.currentFileFence())
			}
			got, found, err := st.GetConnectorSetting(t.Context(), candidate.OwnerID, candidate.Channel)
			if err != nil || !found || got.Version != candidate.Version || st.currentFileFence() != nil {
				t.Fatalf("reconciled=%#v found=%v err=%v fence=%v", got, found, err, st.currentFileFence())
			}
		})
	}
}

func TestFileConnectorDestinationReadFailureDoesNotMutate(t *testing.T) {
	st, err := NewFileStore(filepath.Join(t.TempDir(), "connector.json"))
	if err != nil {
		t.Fatal(err)
	}
	before := st.captureFileRollback()
	st.commitOps = &controlledFileCommitOps{failStage: "read", failRemaining: 1}
	candidate, err := st.UpdateConnectorSetting(context.Background(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha"}, 0)
	if candidate.Channel != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, errFileCommitInjected) {
		t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
	}
	if after := st.captureFileRollback(); !reflect.DeepEqual(after, before) {
		t.Fatal("destination read failure mutated connector state")
	}
}

func TestFileConnectorSourceUsesOnlyMigratedCommands(t *testing.T) {
	raw, err := os.ReadFile("connector_file.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	if strings.Contains(source, "s.persist()") || strings.Contains(source, "context.Background()") {
		t.Fatal("ConnectorRepository File path restored legacy persistence or context ownership")
	}
	for _, operation := range []string{
		"OperationConnectorSettingGet", "OperationConnectorSettingList", "OperationConnectorSettingListAll", "OperationConnectorSettingUpdate",
		"OperationNotificationBindingCreate", "OperationNotificationBindingGet", "OperationNotificationBindingList", "OperationNotificationBindingUpdate",
	} {
		if !strings.Contains(source, operation) {
			t.Fatalf("ConnectorRepository File path omitted %s", operation)
		}
	}
}
