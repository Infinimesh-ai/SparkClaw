package messagecontrol

import (
	"context"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type receiveStore interface {
	SaveMessageReceive(context.Context, app.MessageReceiveRecord) (app.MessageReceiveRecord, error)
	FindMessageReceive(context.Context, app.EndpointID, string) (app.MessageReceiveRecord, bool, error)
}

type ReceiveLifecycle struct {
	store receiveStore
}

func NewReceiveLifecycle(st receiveStore) ReceiveLifecycle {
	return ReceiveLifecycle{store: st}
}

func (l ReceiveLifecycle) Begin(ctx context.Context, endpoint app.MessageEndpoint, nativeMessageID string) (app.MessageReceiveRecord, bool, error) {
	nativeMessageID = strings.TrimSpace(nativeMessageID)
	if l.store == nil || endpoint.ID == "" || nativeMessageID == "" {
		return app.MessageReceiveRecord{}, false, nil
	}
	if existing, ok, err := l.store.FindMessageReceive(ctx, endpoint.ID, nativeMessageID); err != nil {
		return app.MessageReceiveRecord{}, false, err
	} else if ok {
		if existing.Status == "failed" {
			existing.Status = "received"
			saved, err := l.save(ctx, existing)
			return saved, true, err
		}
		existing.Status = "duplicate"
		saved, err := l.save(ctx, existing)
		return saved, false, err
	}
	record := app.MessageReceiveRecord{
		Direction: app.MessageDirectionReceive, OwnerID: endpoint.OwnerID, ActorID: endpoint.ActorID,
		ProviderKey: endpoint.ProviderKey, SourceEndpointID: endpoint.ID, NativeMessageID: nativeMessageID,
		Status: "received", SoftwareDisplayName: endpoint.SoftwareDisplayName,
		RecipientDisplayName: endpoint.RecipientDisplayName, AccountDisplayName: endpoint.AccountDisplayName,
	}
	saved, err := l.save(ctx, record)
	return saved, true, err
}

func (l ReceiveLifecycle) Advance(ctx context.Context, record app.MessageReceiveRecord, status, linkedMessageID, linkedRunID string) (app.MessageReceiveRecord, error) {
	if l.store == nil || record.ID == "" {
		return record, nil
	}
	record.Status = strings.TrimSpace(status)
	if linkedMessageID != "" {
		record.LinkedMessageID = linkedMessageID
	}
	if linkedRunID != "" {
		record.LinkedRunID = linkedRunID
	}
	return l.save(ctx, record)
}

func (l ReceiveLifecycle) save(ctx context.Context, record app.MessageReceiveRecord) (app.MessageReceiveRecord, error) {
	saved, err := l.store.SaveMessageReceive(ctx, record)
	return store.ReconcileMessageReceiveWrite(ctx, l.store, saved, err)
}
