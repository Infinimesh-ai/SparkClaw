package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const defaultDeliveryRecordLimit = 100

var (
	errMessageReceiveJSONDecode  = errors.New("decode persisted message receive record")
	errMessageDeliveryJSONDecode = errors.New("decode persisted message delivery record")
	errChannelInboxPayloadDecode = errors.New("decode persisted channel inbox payload")
)

func prepareMessageReceive(record, current app.MessageReceiveRecord, now time.Time) (app.MessageReceiveRecord, error) {
	record.ID = strings.TrimSpace(record.ID)
	record.OwnerID = strings.TrimSpace(record.OwnerID)
	record.ActorID = strings.TrimSpace(record.ActorID)
	record.ProviderKey = strings.TrimSpace(record.ProviderKey)
	record.SourceEndpointID = app.EndpointID(strings.TrimSpace(string(record.SourceEndpointID)))
	record.NativeMessageID = strings.TrimSpace(record.NativeMessageID)
	record.Status = strings.TrimSpace(record.Status)
	if record.SourceEndpointID == "" || record.NativeMessageID == "" || record.Status == "" {
		return app.MessageReceiveRecord{}, errors.New("receive endpoint, native message ID, and status are required")
	}
	if current.ID != "" {
		if !messageReceiveIdentityCompatible(current, record) {
			return app.MessageReceiveRecord{}, ErrMessageReceiveConflict
		}
		if record.OwnerID == "" {
			record.OwnerID = current.OwnerID
		}
		if record.ActorID == "" {
			record.ActorID = current.ActorID
		}
		if record.ProviderKey == "" {
			record.ProviderKey = current.ProviderKey
		}
		record.ID = current.ID
		record.CreatedAt = current.CreatedAt
		record.Transitions = append([]app.MessageLifecycleTransition(nil), current.Transitions...)
	}
	if record.ID == "" {
		record.ID = app.NewID("recv")
	}
	if record.Direction == "" {
		record.Direction = app.MessageDirectionReceive
	}
	if record.Direction != app.MessageDirectionReceive {
		return app.MessageReceiveRecord{}, errors.New("receive record direction must be receive")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.CreatedAt = postgresTime(record.CreatedAt)
	record.UpdatedAt = nextRepositoryTime(now, current.UpdatedAt)
	for index := range record.Transitions {
		record.Transitions[index].At = postgresTime(record.Transitions[index].At)
	}
	if len(record.Transitions) == 0 || record.Transitions[len(record.Transitions)-1].Status != record.Status {
		record.Transitions = append(record.Transitions, app.MessageLifecycleTransition{Status: record.Status, At: record.UpdatedAt})
	}
	return cloneMessageReceive(record), nil
}

func messageReceiveIdentityCompatible(current, candidate app.MessageReceiveRecord) bool {
	return sameOptionalString(current.OwnerID, candidate.OwnerID) &&
		sameOptionalString(current.ActorID, candidate.ActorID) &&
		sameOptionalString(current.ProviderKey, candidate.ProviderKey) &&
		current.SourceEndpointID == candidate.SourceEndpointID && current.NativeMessageID == candidate.NativeMessageID
}

func cloneMessageReceive(record app.MessageReceiveRecord) app.MessageReceiveRecord {
	record.CreatedAt = postgresTime(record.CreatedAt)
	record.UpdatedAt = postgresTime(record.UpdatedAt)
	record.Transitions = append([]app.MessageLifecycleTransition(nil), record.Transitions...)
	for index := range record.Transitions {
		record.Transitions[index].At = postgresTime(record.Transitions[index].At)
	}
	return record
}

func cloneMessageReceiveMap(values map[string]app.MessageReceiveRecord) map[string]app.MessageReceiveRecord {
	out := make(map[string]app.MessageReceiveRecord, len(values))
	for id, record := range values {
		out[id] = cloneMessageReceive(record)
	}
	return out
}

func prepareMessageDelivery(record, current app.MessageDeliveryRecord, now time.Time) (app.MessageDeliveryRecord, error) {
	record.ID = app.DeliveryID(strings.TrimSpace(string(record.ID)))
	record.OwnerID = strings.TrimSpace(record.OwnerID)
	record.ActorID = strings.TrimSpace(record.ActorID)
	record.Request.IdempotencyKey = strings.TrimSpace(record.Request.IdempotencyKey)
	record.Request.Target = app.EndpointID(strings.TrimSpace(string(record.Request.Target)))
	record.Status = app.DeliveryStatus(strings.TrimSpace(string(record.Status)))
	if record.OwnerID == "" || record.ActorID == "" || record.Request.IdempotencyKey == "" || record.Status == "" {
		return app.MessageDeliveryRecord{}, errors.New("delivery owner, actor, idempotency key, and status are required")
	}
	if record.ContentDigest == "" {
		record.ContentDigest = strings.TrimSpace(record.Request.ContentDigest)
	}
	if record.Request.ContentDigest == "" {
		record.Request.ContentDigest = record.ContentDigest
	}
	if current.ID != "" {
		if !messageDeliveryIdentityEqual(current, record) {
			return app.MessageDeliveryRecord{}, ErrMessageDeliveryConflict
		}
		record.ID = current.ID
		record.CreatedAt = current.CreatedAt
	}
	if record.ID == "" {
		record.ID = app.DeliveryID(app.NewID("del"))
	}
	if record.Request.ID == "" {
		record.Request.ID = record.ID
	}
	if record.Direction == "" {
		record.Direction = app.MessageDirectionSend
	}
	if record.Direction != app.MessageDirectionSend {
		return app.MessageDeliveryRecord{}, errors.New("delivery record direction must be send")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.CreatedAt = postgresTime(record.CreatedAt)
	record.UpdatedAt = nextRepositoryTime(now, current.UpdatedAt)
	record.Request.CreatedAt = postgresTime(record.Request.CreatedAt)
	if record.Request.CreatedAt.IsZero() {
		record.Request.CreatedAt = record.CreatedAt
	}
	return cloneMessageDelivery(record), nil
}

func messageDeliveryIdentityEqual(current, candidate app.MessageDeliveryRecord) bool {
	return current.OwnerID == candidate.OwnerID && current.ActorID == candidate.ActorID &&
		current.Request.IdempotencyKey == candidate.Request.IdempotencyKey &&
		current.Request.Target == candidate.Request.Target && current.ContentDigest == candidate.ContentDigest
}

func cloneMessageDelivery(record app.MessageDeliveryRecord) app.MessageDeliveryRecord {
	record.CreatedAt = postgresTime(record.CreatedAt)
	record.UpdatedAt = postgresTime(record.UpdatedAt)
	record.Request = cloneDeliveryRequest(record.Request)
	record.TargetSelection.CandidateEndpointIDs = append([]app.EndpointID(nil), record.TargetSelection.CandidateEndpointIDs...)
	if record.Receipt != nil {
		receipt := *record.Receipt
		receipt.AttemptedAt = postgresTime(receipt.AttemptedAt)
		receipt.DeliveredAt = cloneTimePointer(record.Receipt.DeliveredAt)
		if receipt.DeliveredAt != nil {
			normalized := postgresTime(*receipt.DeliveredAt)
			receipt.DeliveredAt = &normalized
		}
		receipt.PartReceipts = append([]app.PartDeliveryReceipt(nil), record.Receipt.PartReceipts...)
		record.Receipt = &receipt
	}
	return record
}

func cloneDeliveryRequest(request app.DeliveryRequest) app.DeliveryRequest {
	request.CreatedAt = postgresTime(request.CreatedAt)
	request.Authorization.Scope = append([]string(nil), request.Authorization.Scope...)
	parts := request.Content.Parts
	request.Content.Parts = make([]app.MessagePart, len(parts))
	for index, part := range parts {
		request.Content.Parts[index] = part
		if part.Resource != nil {
			resource := *part.Resource
			if part.Resource.Attributes != nil {
				resource.Attributes = make(map[string]string, len(part.Resource.Attributes))
				for key, value := range part.Resource.Attributes {
					resource.Attributes[key] = value
				}
			}
			request.Content.Parts[index].Resource = &resource
		}
	}
	if request.ResultError != nil {
		value := *request.ResultError
		request.ResultError = &value
	}
	if request.MCP != nil {
		value := *request.MCP
		request.MCP = &value
	}
	return request
}

func cloneMessageDeliveryMap(values map[string]app.MessageDeliveryRecord) map[string]app.MessageDeliveryRecord {
	out := make(map[string]app.MessageDeliveryRecord, len(values))
	for id, record := range values {
		out[id] = cloneMessageDelivery(record)
	}
	return out
}

func prepareChannelInboxUpdate(update, current app.ChannelInboxUpdate, now time.Time) (app.ChannelInboxUpdate, error) {
	update.ID = strings.TrimSpace(update.ID)
	update.BindingID = strings.TrimSpace(update.BindingID)
	update.Channel = strings.ToLower(strings.TrimSpace(update.Channel))
	update.ExternalID = strings.TrimSpace(update.ExternalID)
	update.Status = strings.TrimSpace(update.Status)
	if update.BindingID == "" || update.Channel == "" || update.ExternalID == "" {
		return app.ChannelInboxUpdate{}, errors.New("inbox binding, channel, and external ID are required")
	}
	if len(update.Payload) != 0 && !json.Valid(update.Payload) {
		return app.ChannelInboxUpdate{}, errors.Join(errChannelInboxPayloadDecode, errors.New("inbox payload must be valid JSON"))
	}
	if current.ID != "" {
		if current.BindingID != update.BindingID || current.Channel != update.Channel || current.ExternalID != update.ExternalID {
			return app.ChannelInboxUpdate{}, ErrChannelInboxUpdateConflict
		}
		update.ID = current.ID
		update.CreatedAt = current.CreatedAt
	}
	if update.ID == "" {
		update.ID = app.NewID("inbox")
	}
	if update.Status == "" {
		update.Status = "pending"
	}
	if update.AvailableAt.IsZero() {
		update.AvailableAt = now
	}
	if update.CreatedAt.IsZero() {
		update.CreatedAt = now
	}
	update.AvailableAt = postgresTime(update.AvailableAt)
	update.CreatedAt = postgresTime(update.CreatedAt)
	update.UpdatedAt = nextRepositoryTime(now, current.UpdatedAt)
	return cloneChannelInboxUpdate(update), nil
}

func cloneChannelInboxUpdate(update app.ChannelInboxUpdate) app.ChannelInboxUpdate {
	update.AvailableAt = postgresTime(update.AvailableAt)
	update.CreatedAt = postgresTime(update.CreatedAt)
	update.UpdatedAt = postgresTime(update.UpdatedAt)
	if len(update.Payload) != 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, update.Payload); err == nil {
			update.Payload = append([]byte(nil), compact.Bytes()...)
		} else {
			update.Payload = append([]byte(nil), update.Payload...)
		}
	}
	return update
}

func cloneChannelInboxUpdateMap(values map[string]app.ChannelInboxUpdate) map[string]app.ChannelInboxUpdate {
	out := make(map[string]app.ChannelInboxUpdate, len(values))
	for id, update := range values {
		out[id] = cloneChannelInboxUpdate(update)
	}
	return out
}

func sameOptionalString(current, candidate string) bool {
	return current == "" || candidate == "" || current == candidate
}

func normalizeDeliveryRecordLimit(limit int) int {
	if limit <= 0 {
		return defaultDeliveryRecordLimit
	}
	return limit
}

type messageReceiveReconciler interface {
	FindMessageReceive(context.Context, app.EndpointID, string) (app.MessageReceiveRecord, bool, error)
}

type messageDeliveryReconciler interface {
	FindMessageDeliveryByIdempotency(context.Context, string, string, string) (app.MessageDeliveryRecord, bool, error)
}

type channelInboxUpdateReconciler interface {
	FindChannelInboxUpdate(context.Context, string, string) (app.ChannelInboxUpdate, bool, error)
}

func ReconcileMessageReceiveWrite(ctx context.Context, repository messageReceiveReconciler, candidate app.MessageReceiveRecord, writeErr error) (app.MessageReceiveRecord, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome {
		return app.MessageReceiveRecord{}, writeErr
	}
	actual, found, err := repository.FindMessageReceive(ctx, candidate.SourceEndpointID, candidate.NativeMessageID)
	if err != nil {
		return app.MessageReceiveRecord{}, errors.Join(writeErr, err)
	}
	if found && reflect.DeepEqual(actual, cloneMessageReceive(candidate)) {
		return actual, nil
	}
	return app.MessageReceiveRecord{}, writeErr
}

func ReconcileMessageDeliveryWrite(ctx context.Context, repository messageDeliveryReconciler, candidate app.MessageDeliveryRecord, writeErr error) (app.MessageDeliveryRecord, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome {
		return app.MessageDeliveryRecord{}, writeErr
	}
	actual, found, err := repository.FindMessageDeliveryByIdempotency(ctx, candidate.OwnerID, candidate.ActorID, candidate.Request.IdempotencyKey)
	if err != nil {
		return app.MessageDeliveryRecord{}, errors.Join(writeErr, err)
	}
	if found && reflect.DeepEqual(actual, cloneMessageDelivery(candidate)) {
		return actual, nil
	}
	return app.MessageDeliveryRecord{}, writeErr
}

func ReconcileChannelInboxUpdateWrite(ctx context.Context, repository channelInboxUpdateReconciler, candidate app.ChannelInboxUpdate, writeErr error) (app.ChannelInboxUpdate, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome {
		return app.ChannelInboxUpdate{}, writeErr
	}
	actual, found, err := repository.FindChannelInboxUpdate(ctx, candidate.BindingID, candidate.ExternalID)
	if err != nil {
		return app.ChannelInboxUpdate{}, errors.Join(writeErr, err)
	}
	if found && reflect.DeepEqual(actual, cloneChannelInboxUpdate(candidate)) {
		return actual, nil
	}
	return app.ChannelInboxUpdate{}, writeErr
}
