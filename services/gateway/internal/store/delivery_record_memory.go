package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveMessageReceive(ctx context.Context, record app.MessageReceiveRecord) (app.MessageReceiveRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveSave, ctx); err != nil {
		return app.MessageReceiveRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMessageReceiveSave, ctx); err != nil {
		return app.MessageReceiveRecord{}, err
	}
	record.ID = strings.TrimSpace(record.ID)
	record.SourceEndpointID = app.EndpointID(strings.TrimSpace(string(record.SourceEndpointID)))
	record.NativeMessageID = strings.TrimSpace(record.NativeMessageID)
	current, exists := s.messageReceives[record.ID]
	for _, candidate := range s.messageReceives {
		if candidate.SourceEndpointID != record.SourceEndpointID || candidate.NativeMessageID != record.NativeMessageID {
			continue
		}
		if exists && current.ID != candidate.ID {
			return app.MessageReceiveRecord{}, storeError(ctx, OperationMessageReceiveSave, StoreErrorConflict, ErrMessageReceiveConflict)
		}
		current, exists = candidate, true
		break
	}
	prepared, err := prepareMessageReceive(record, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrMessageReceiveConflict) {
			code = StoreErrorConflict
		}
		return app.MessageReceiveRecord{}, storeError(ctx, OperationMessageReceiveSave, code, err)
	}
	s.messageReceives[prepared.ID] = cloneMessageReceive(prepared)
	s.appendAuditLocked("message.receive."+prepared.Status, "", prepared.LinkedRunID, "gateway", prepared.ProviderKey, map[string]any{
		"receive_id": prepared.ID, "endpoint_id": prepared.SourceEndpointID,
	})
	return cloneMessageReceive(prepared), nil
}

func (s *MemoryStore) GetMessageReceive(ctx context.Context, id string) (app.MessageReceiveRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveGet, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageReceiveGet, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	record, ok := s.messageReceives[strings.TrimSpace(id)]
	return cloneMessageReceive(record), ok, nil
}

func (s *MemoryStore) FindMessageReceive(ctx context.Context, sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveFind, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	sourceEndpointID = app.EndpointID(strings.TrimSpace(string(sourceEndpointID)))
	nativeMessageID = strings.TrimSpace(nativeMessageID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageReceiveFind, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	for _, record := range s.messageReceives {
		if record.SourceEndpointID == sourceEndpointID && record.NativeMessageID == nativeMessageID {
			return cloneMessageReceive(record), true, nil
		}
	}
	return app.MessageReceiveRecord{}, false, nil
}

func (s *MemoryStore) ListMessageReceives(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageReceiveRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveList, ctx); err != nil {
		return nil, err
	}
	ownerID, actorID = strings.TrimSpace(ownerID), strings.TrimSpace(actorID)
	limit = normalizeDeliveryRecordLimit(limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageReceiveList, ctx); err != nil {
		return nil, err
	}
	out := []app.MessageReceiveRecord{}
	for _, record := range s.messageReceives {
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if actorID != "" && record.ActorID != actorID {
			continue
		}
		out = append(out, cloneMessageReceive(record))
	}
	slices.SortFunc(out, func(a, b app.MessageReceiveRecord) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) SaveMessageDelivery(ctx context.Context, record app.MessageDeliveryRecord) (app.MessageDeliveryRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliverySave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliverySave, ctx); err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMessageDeliverySave, ctx); err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	record.ID = app.DeliveryID(strings.TrimSpace(string(record.ID)))
	current, exists := s.messageDeliveries[string(record.ID)]
	for _, candidate := range s.messageDeliveries {
		if candidate.OwnerID != strings.TrimSpace(record.OwnerID) || candidate.ActorID != strings.TrimSpace(record.ActorID) ||
			candidate.Request.IdempotencyKey != strings.TrimSpace(record.Request.IdempotencyKey) {
			continue
		}
		if exists && current.ID != candidate.ID {
			return app.MessageDeliveryRecord{}, storeError(ctx, OperationMessageDeliverySave, StoreErrorConflict, ErrMessageDeliveryConflict)
		}
		if candidate.ID != record.ID {
			if !messageDeliveryIdentityEqual(candidate, record) {
				return app.MessageDeliveryRecord{}, storeError(ctx, OperationMessageDeliverySave, StoreErrorConflict, ErrMessageDeliveryConflict)
			}
			return cloneMessageDelivery(candidate), nil
		}
		current, exists = candidate, true
		break
	}
	prepared, err := prepareMessageDelivery(record, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrMessageDeliveryConflict) {
			code = StoreErrorConflict
		}
		return app.MessageDeliveryRecord{}, storeError(ctx, OperationMessageDeliverySave, code, err)
	}
	s.messageDeliveries[string(prepared.ID)] = cloneMessageDelivery(prepared)
	s.appendAuditLocked("message.send."+string(prepared.Status), "", prepared.Request.RunID, prepared.ActorID, prepared.SoftwareDisplayName, map[string]any{
		"delivery_id": prepared.ID, "endpoint_id": prepared.Request.Target, "origin": prepared.Origin,
	})
	return cloneMessageDelivery(prepared), nil
}

func (s *MemoryStore) GetMessageDelivery(ctx context.Context, id app.DeliveryID) (app.MessageDeliveryRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliveryGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliveryGet, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageDeliveryGet, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	record, ok := s.messageDeliveries[strings.TrimSpace(string(id))]
	return cloneMessageDelivery(record), ok, nil
}

func (s *MemoryStore) FindMessageDeliveryByIdempotency(ctx context.Context, ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliveryFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliveryFind, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	ownerID, actorID, idempotencyKey = strings.TrimSpace(ownerID), strings.TrimSpace(actorID), strings.TrimSpace(idempotencyKey)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageDeliveryFind, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	for _, record := range s.messageDeliveries {
		if record.OwnerID == ownerID && record.ActorID == actorID && record.Request.IdempotencyKey == idempotencyKey {
			return cloneMessageDelivery(record), true, nil
		}
	}
	return app.MessageDeliveryRecord{}, false, nil
}

func (s *MemoryStore) ListMessageDeliveries(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageDeliveryRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliveryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliveryList, ctx); err != nil {
		return nil, err
	}
	ownerID, actorID = strings.TrimSpace(ownerID), strings.TrimSpace(actorID)
	limit = normalizeDeliveryRecordLimit(limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageDeliveryList, ctx); err != nil {
		return nil, err
	}
	out := []app.MessageDeliveryRecord{}
	for _, record := range s.messageDeliveries {
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if actorID != "" && record.ActorID != actorID {
			continue
		}
		out = append(out, cloneMessageDelivery(record))
	}
	slices.SortFunc(out, func(a, b app.MessageDeliveryRecord) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) SaveChannelInboxUpdate(ctx context.Context, update app.ChannelInboxUpdate) (app.ChannelInboxUpdate, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateSave, ctx); err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationChannelInboxUpdateSave, ctx); err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	update.ID = strings.TrimSpace(update.ID)
	update.BindingID = strings.TrimSpace(update.BindingID)
	update.Channel = strings.ToLower(strings.TrimSpace(update.Channel))
	update.ExternalID = strings.TrimSpace(update.ExternalID)
	current, exists := s.channelInboxUpdates[update.ID]
	for _, candidate := range s.channelInboxUpdates {
		if candidate.BindingID != update.BindingID || candidate.ExternalID != update.ExternalID {
			continue
		}
		if exists && current.ID != candidate.ID {
			return app.ChannelInboxUpdate{}, storeError(ctx, OperationChannelInboxUpdateSave, StoreErrorConflict, ErrChannelInboxUpdateConflict)
		}
		if candidate.ID != update.ID {
			if candidate.Channel != update.Channel {
				return app.ChannelInboxUpdate{}, storeError(ctx, OperationChannelInboxUpdateSave, StoreErrorConflict, ErrChannelInboxUpdateConflict)
			}
			return cloneChannelInboxUpdate(candidate), nil
		}
		current, exists = candidate, true
		break
	}
	prepared, err := prepareChannelInboxUpdate(update, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrChannelInboxUpdateConflict) {
			code = StoreErrorConflict
		}
		return app.ChannelInboxUpdate{}, storeError(ctx, OperationChannelInboxUpdateSave, code, err)
	}
	s.channelInboxUpdates[prepared.ID] = cloneChannelInboxUpdate(prepared)
	return cloneChannelInboxUpdate(prepared), nil
}

func (s *MemoryStore) GetChannelInboxUpdate(ctx context.Context, id string) (app.ChannelInboxUpdate, bool, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateGet, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationChannelInboxUpdateGet, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	update, ok := s.channelInboxUpdates[strings.TrimSpace(id)]
	return cloneChannelInboxUpdate(update), ok, nil
}

func (s *MemoryStore) FindChannelInboxUpdate(ctx context.Context, bindingID, externalID string) (app.ChannelInboxUpdate, bool, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateFind, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	bindingID, externalID = strings.TrimSpace(bindingID), strings.TrimSpace(externalID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationChannelInboxUpdateFind, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	for _, update := range s.channelInboxUpdates {
		if update.BindingID == bindingID && update.ExternalID == externalID {
			return cloneChannelInboxUpdate(update), true, nil
		}
	}
	return app.ChannelInboxUpdate{}, false, nil
}

func (s *MemoryStore) ListChannelInboxUpdates(ctx context.Context, channel, status string, readyBefore time.Time, limit int) ([]app.ChannelInboxUpdate, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateList, ctx); err != nil {
		return nil, err
	}
	channel, status = strings.ToLower(strings.TrimSpace(channel)), strings.TrimSpace(status)
	readyBefore = postgresTime(readyBefore)
	limit = normalizeDeliveryRecordLimit(limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationChannelInboxUpdateList, ctx); err != nil {
		return nil, err
	}
	out := []app.ChannelInboxUpdate{}
	for _, update := range s.channelInboxUpdates {
		if channel != "" && update.Channel != channel {
			continue
		}
		if status != "" && update.Status != status {
			continue
		}
		if !readyBefore.IsZero() && update.AvailableAt.After(readyBefore) {
			continue
		}
		out = append(out, cloneChannelInboxUpdate(update))
	}
	slices.SortFunc(out, func(a, b app.ChannelInboxUpdate) int {
		if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
