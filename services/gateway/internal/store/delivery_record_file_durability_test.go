package store

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileDeliveryRecordDefiniteFailuresRestoreCompleteState(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*FileStore) (any, error)
	}{
		{
			name: "receive",
			invoke: func(repository *FileStore) (any, error) {
				return repository.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
					ID: "receive-definite", SourceEndpointID: "endpoint-definite", NativeMessageID: "native-definite", Status: "received",
				})
			},
		},
		{
			name: "delivery",
			invoke: func(repository *FileStore) (any, error) {
				return repository.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
					ID: "delivery-definite", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:definite",
					Request: app.DeliveryRequest{IdempotencyKey: "key-definite", Target: "endpoint", ContentDigest: "sha256:definite"},
				})
			},
		},
		{
			name: "inbox",
			invoke: func(repository *FileStore) (any, error) {
				return repository.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
					ID: "inbox-definite", BindingID: "binding-definite", Channel: "telegram", ExternalID: "external-definite", Payload: json.RawMessage(`{"id":"definite"}`),
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, stage := range []string{"encode", "mkdir", "create", "write", "file_sync", "file_close", "rename"} {
				t.Run(stage, func(t *testing.T) {
					repository, err := NewFileStore(t.TempDir() + "/state.json")
					if err != nil {
						t.Fatal(err)
					}
					before := repository.captureFileRollback()
					repository.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
					candidate, writeErr := test.invoke(repository)
					if !reflect.ValueOf(candidate).IsZero() || StoreErrorCodeOf(writeErr) != StoreErrorDurability || repository.currentFileFence() != nil {
						t.Fatalf("stage=%s candidate=%#v err=%v code=%q fence=%v", stage, candidate, writeErr, StoreErrorCodeOf(writeErr), repository.currentFileFence())
					}
					if after := repository.captureFileRollback(); !reflect.DeepEqual(after, before) {
						t.Fatalf("stage %s did not restore the complete snapshot", stage)
					}
				})
			}
		})
	}
}

func TestFileDeliveryRecordUnknownOutcomesReconcileAndSurviveRestart(t *testing.T) {
	tests := []struct {
		name      string
		ops       *controlledFileCommitOps
		write     func(*FileStore) (any, error)
		reconcile func(DeliveryRecordRepository, any, error) (any, error)
	}{
		{
			name: "receive rename applied",
			ops:  &controlledFileCommitOps{failStage: "rename", failRemaining: 1, renameApplied: true},
			write: func(repository *FileStore) (any, error) {
				return repository.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
					ID: "receive-unknown", SourceEndpointID: "endpoint-unknown", NativeMessageID: "native-unknown", Status: "received",
				})
			},
			reconcile: func(repository DeliveryRecordRepository, candidate any, writeErr error) (any, error) {
				return ReconcileMessageReceiveWrite(t.Context(), repository, candidate.(app.MessageReceiveRecord), writeErr)
			},
		},
		{
			name: "delivery directory open",
			ops:  &controlledFileCommitOps{failStage: "dir_open", failRemaining: 1},
			write: func(repository *FileStore) (any, error) {
				return repository.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
					ID: "delivery-unknown-open", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:open",
					Request: app.DeliveryRequest{IdempotencyKey: "key-open", Target: "endpoint", ContentDigest: "sha256:open"},
				})
			},
			reconcile: func(repository DeliveryRecordRepository, candidate any, writeErr error) (any, error) {
				return ReconcileMessageDeliveryWrite(t.Context(), repository, candidate.(app.MessageDeliveryRecord), writeErr)
			},
		},
		{
			name: "inbox directory sync",
			ops:  &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1},
			write: func(repository *FileStore) (any, error) {
				return repository.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
					ID: "inbox-unknown-sync", BindingID: "binding-sync", Channel: "telegram", ExternalID: "external-sync", Payload: json.RawMessage(`{"id":"sync"}`),
				})
			},
			reconcile: func(repository DeliveryRecordRepository, candidate any, writeErr error) (any, error) {
				return ReconcileChannelInboxUpdateWrite(t.Context(), repository, candidate.(app.ChannelInboxUpdate), writeErr)
			},
		},
		{
			name: "delivery directory close",
			ops:  &controlledFileCommitOps{failStage: "dir_close", failRemaining: 1},
			write: func(repository *FileStore) (any, error) {
				return repository.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
					ID: "delivery-unknown-close", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:close",
					Request: app.DeliveryRequest{IdempotencyKey: "key-close", Target: "endpoint", ContentDigest: "sha256:close"},
				})
			},
			reconcile: func(repository DeliveryRecordRepository, candidate any, writeErr error) (any, error) {
				return ReconcileMessageDeliveryWrite(t.Context(), repository, candidate.(app.MessageDeliveryRecord), writeErr)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir() + "/state.json"
			repository, err := NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			repository.commitOps = test.ops
			candidate, writeErr := test.write(repository)
			if reflect.ValueOf(candidate).IsZero() || StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || repository.currentFileFence() == nil {
				t.Fatalf("candidate=%#v err=%v code=%q fence=%v", candidate, writeErr, StoreErrorCodeOf(writeErr), repository.currentFileFence())
			}
			reconciled, err := test.reconcile(repository, candidate, writeErr)
			if err != nil || !reflect.DeepEqual(reconciled, candidate) || repository.currentFileFence() != nil {
				t.Fatalf("same-instance reconciled=%#v err=%v fence=%v", reconciled, err, repository.currentFileFence())
			}
			restarted, err := NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			var persisted any
			switch value := candidate.(type) {
			case app.MessageReceiveRecord:
				persisted, _, _ = restarted.FindMessageReceive(t.Context(), value.SourceEndpointID, value.NativeMessageID)
			case app.MessageDeliveryRecord:
				persisted, _, _ = restarted.FindMessageDeliveryByIdempotency(t.Context(), value.OwnerID, value.ActorID, value.Request.IdempotencyKey)
			case app.ChannelInboxUpdate:
				persisted, _, _ = restarted.FindChannelInboxUpdate(t.Context(), value.BindingID, value.ExternalID)
			}
			reconciled, err = test.reconcile(restarted, candidate, writeErr)
			if err != nil || !reflect.DeepEqual(reconciled, candidate) {
				t.Fatalf("restart reconciled=%#v persisted=%#v candidate=%#v err=%v", reconciled, persisted, candidate, err)
			}
		})
	}
}
