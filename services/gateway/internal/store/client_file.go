package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) GetClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientGet, 1)
	if err != nil {
		return app.Client{}, false, err
	}
	defer release()
	return s.inner.GetClient(ctx, id)
}

func (s *FileStore) ListClients(ctx context.Context) ([]app.Client, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListClients(ctx)
}

func (s *FileStore) RevokeClient(ctx context.Context, id string) (app.Client, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientRevoke, fileAdmissionCapacity)
	if err != nil {
		return app.Client{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationClientRevoke, func(ctx context.Context) (app.Client, error) {
		return s.inner.RevokeClient(ctx, id)
	})
}

func (s *FileStore) FindClientByTokenHash(ctx context.Context, tokenHash string) (app.Client, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientFindTokenHash, 1)
	if err != nil {
		return app.Client{}, false, err
	}
	defer release()
	return s.inner.FindClientByTokenHash(ctx, tokenHash)
}

type clientLookupResult struct {
	client app.Client
	found  bool
}

func (s *FileStore) TouchClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClientTouch, fileAdmissionCapacity)
	if err != nil {
		return app.Client{}, false, err
	}
	defer release()
	result, found, err := runFileOptionalCommand(s, ctx, OperationClientTouch, func(ctx context.Context) (clientLookupResult, bool, error) {
		client, found, err := s.inner.TouchClient(ctx, id)
		return clientLookupResult{client: client, found: found}, found, err
	})
	return result.client, found && result.found, err
}

func (s *FileStore) SavePairingCode(ctx context.Context, code app.PairingCode) (app.PairingCode, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPairingCodeSave, fileAdmissionCapacity)
	if err != nil {
		return app.PairingCode{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationPairingCodeSave, func(ctx context.Context) (app.PairingCode, error) {
		return s.inner.SavePairingCode(ctx, code)
	})
}

func (s *FileStore) GetPairingCode(ctx context.Context, id string) (app.PairingCode, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPairingCodeGet, 1)
	if err != nil {
		return app.PairingCode{}, false, err
	}
	defer release()
	return s.inner.GetPairingCode(ctx, id)
}

type pairingClaimResult struct {
	pairing app.PairingCode
	client  app.Client
}

func (s *FileStore) ClaimPairingCode(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPairingCodeClaim, fileAdmissionCapacity)
	if err != nil {
		return app.PairingCode{}, app.Client{}, err
	}
	defer release()
	result, err := runFileCommand(s, ctx, OperationPairingCodeClaim, func(ctx context.Context) (pairingClaimResult, error) {
		pairing, claimedClient, err := s.inner.ClaimPairingCode(ctx, id, client)
		return pairingClaimResult{pairing: pairing, client: claimedClient}, err
	})
	return result.pairing, result.client, err
}
