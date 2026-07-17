package messagecontrol

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type receiveStore interface {
	SaveMessageReceive(app.MessageReceiveRecord) app.MessageReceiveRecord
	FindMessageReceive(app.EndpointID, string) (app.MessageReceiveRecord, bool)
}

type ReceiveLifecycle struct {
	store receiveStore
}

func NewReceiveLifecycle(st receiveStore) ReceiveLifecycle {
	return ReceiveLifecycle{store: st}
}

func (l ReceiveLifecycle) Begin(endpoint app.MessageEndpoint, nativeMessageID string) (app.MessageReceiveRecord, bool) {
	nativeMessageID = strings.TrimSpace(nativeMessageID)
	if l.store == nil || endpoint.ID == "" || nativeMessageID == "" {
		return app.MessageReceiveRecord{}, false
	}
	if existing, ok := l.store.FindMessageReceive(endpoint.ID, nativeMessageID); ok {
		if existing.Status == "failed" {
			existing.Status = "received"
			return l.store.SaveMessageReceive(existing), true
		}
		existing.Status = "duplicate"
		return l.store.SaveMessageReceive(existing), false
	}
	record := app.MessageReceiveRecord{
		Direction: app.MessageDirectionReceive, OwnerID: endpoint.OwnerID, ActorID: endpoint.ActorID,
		ProviderKey: endpoint.ProviderKey, SourceEndpointID: endpoint.ID, NativeMessageID: nativeMessageID,
		Status: "received", SoftwareDisplayName: endpoint.SoftwareDisplayName,
		RecipientDisplayName: endpoint.RecipientDisplayName, AccountDisplayName: endpoint.AccountDisplayName,
	}
	return l.store.SaveMessageReceive(record), true
}

func (l ReceiveLifecycle) Advance(record app.MessageReceiveRecord, status, linkedMessageID, linkedRunID string) app.MessageReceiveRecord {
	if l.store == nil || record.ID == "" {
		return record
	}
	record.Status = strings.TrimSpace(status)
	if linkedMessageID != "" {
		record.LinkedMessageID = linkedMessageID
	}
	if linkedRunID != "" {
		record.LinkedRunID = linkedRunID
	}
	return l.store.SaveMessageReceive(record)
}
