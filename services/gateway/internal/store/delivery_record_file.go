package store

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveMessageReceive(ctx context.Context, record app.MessageReceiveRecord) (app.MessageReceiveRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageReceiveSave, fileAdmissionCapacity)
	if err != nil {
		return app.MessageReceiveRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMessageReceiveSave, func(ctx context.Context) (app.MessageReceiveRecord, error) {
		return s.inner.SaveMessageReceive(ctx, record)
	})
}

func (s *FileStore) GetMessageReceive(ctx context.Context, id string) (app.MessageReceiveRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageReceiveGet, 1)
	if err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	defer release()
	return s.inner.GetMessageReceive(ctx, id)
}

func (s *FileStore) FindMessageReceive(ctx context.Context, sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageReceiveFind, 1)
	if err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	defer release()
	return s.inner.FindMessageReceive(ctx, sourceEndpointID, nativeMessageID)
}

func (s *FileStore) ListMessageReceives(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageReceiveRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageReceiveList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMessageReceives(ctx, ownerID, actorID, limit)
}

func (s *FileStore) SaveMessageDelivery(ctx context.Context, record app.MessageDeliveryRecord) (app.MessageDeliveryRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageDeliverySave, fileAdmissionCapacity)
	if err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMessageDeliverySave, func(ctx context.Context) (app.MessageDeliveryRecord, error) {
		return s.inner.SaveMessageDelivery(ctx, record)
	})
}

func (s *FileStore) GetMessageDelivery(ctx context.Context, id app.DeliveryID) (app.MessageDeliveryRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageDeliveryGet, 1)
	if err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	defer release()
	return s.inner.GetMessageDelivery(ctx, id)
}

func (s *FileStore) FindMessageDeliveryByIdempotency(ctx context.Context, ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageDeliveryFind, 1)
	if err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	defer release()
	return s.inner.FindMessageDeliveryByIdempotency(ctx, ownerID, actorID, idempotencyKey)
}

func (s *FileStore) ListMessageDeliveries(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageDeliveryRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMessageDeliveryList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMessageDeliveries(ctx, ownerID, actorID, limit)
}

func (s *FileStore) SaveChannelInboxUpdate(ctx context.Context, update app.ChannelInboxUpdate) (app.ChannelInboxUpdate, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChannelInboxUpdateSave, fileAdmissionCapacity)
	if err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationChannelInboxUpdateSave, func(ctx context.Context) (app.ChannelInboxUpdate, error) {
		return s.inner.SaveChannelInboxUpdate(ctx, update)
	})
}

func (s *FileStore) GetChannelInboxUpdate(ctx context.Context, id string) (app.ChannelInboxUpdate, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChannelInboxUpdateGet, 1)
	if err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	defer release()
	return s.inner.GetChannelInboxUpdate(ctx, id)
}

func (s *FileStore) FindChannelInboxUpdate(ctx context.Context, bindingID, externalID string) (app.ChannelInboxUpdate, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChannelInboxUpdateFind, 1)
	if err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	defer release()
	return s.inner.FindChannelInboxUpdate(ctx, bindingID, externalID)
}

func (s *FileStore) ListChannelInboxUpdates(ctx context.Context, channel, status string, readyBefore time.Time, limit int) ([]app.ChannelInboxUpdate, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChannelInboxUpdateList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListChannelInboxUpdates(ctx, channel, status, readyBefore, limit)
}
