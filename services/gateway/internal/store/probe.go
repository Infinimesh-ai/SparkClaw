package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var fileProbePayload = []byte("sparkclaw-store-probe-v1\n")

func probeMemoryStore(store *MemoryStore) error {
	if store == nil {
		return errors.New("memory store is nil")
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.sessions == nil || store.ownerProfiles == nil || store.clients == nil ||
		store.approvals == nil || store.auditEvents == nil || store.mcpBindings == nil {
		return errors.New("memory store maps are not initialized")
	}
	return nil
}

func probeFileStore(ctx context.Context, store *FileStore) error {
	if store == nil || store.inner == nil {
		return errors.New("file store is nil")
	}
	if err := probeMemoryStore(store.inner); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(store.path)
	if err := store.commitOps.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create file probe directory: %w", err)
	}
	temporary, err := store.commitOps.CreateTemp(directory, ".sparkclaw-probe-*")
	if err != nil {
		return fmt.Errorf("create file probe: %w", err)
	}
	temporaryPath := temporary.Name()
	verifiedPath := temporaryPath + ".verified"
	cleanup := func() error {
		var cleanupErr error
		if err := store.commitOps.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
		if err := store.commitOps.Remove(verifiedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
		return cleanupErr
	}
	fail := func(primary error) error {
		return errors.Join(primary, temporary.Close(), cleanup())
	}
	if err := writeFileCommit(temporary, fileProbePayload); err != nil {
		return fail(fmt.Errorf("write file probe: %w", err))
	}
	if err := temporary.Sync(); err != nil {
		return fail(fmt.Errorf("sync file probe: %w", err))
	}
	if err := temporary.Close(); err != nil {
		return errors.Join(fmt.Errorf("close file probe: %w", err), cleanup())
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, cleanup())
	}
	if err := store.commitOps.Rename(temporaryPath, verifiedPath); err != nil {
		return errors.Join(fmt.Errorf("rename file probe: %w", err), cleanup())
	}
	if err := syncProbeDirectory(store, directory); err != nil {
		return errors.Join(fmt.Errorf("sync file probe directory: %w", err), cleanup())
	}
	raw, err := store.commitOps.ReadFile(verifiedPath)
	if err != nil {
		return errors.Join(fmt.Errorf("verify file probe: %w", err), cleanup())
	}
	if !bytes.Equal(raw, fileProbePayload) {
		return errors.Join(errors.New("file probe payload mismatch"), cleanup())
	}
	if err := cleanup(); err != nil {
		return fmt.Errorf("cleanup file probe: %w", err)
	}
	if err := syncProbeDirectory(store, directory); err != nil {
		return fmt.Errorf("sync file probe cleanup: %w", err)
	}
	return ctx.Err()
}

func syncProbeDirectory(store *FileStore, directoryPath string) error {
	directory, err := store.commitOps.OpenDirectory(directoryPath)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		return errors.Join(err, directory.Close())
	}
	return directory.Close()
}

func probePostgresStore(ctx context.Context, store *PostgresStore) error {
	if store == nil || store.db == nil {
		return errors.New("postgres store is nil")
	}
	if err := store.db.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres store: %w", err)
	}
	migrations, err := loadPostgresMigrations()
	if err != nil {
		return err
	}
	applied, err := readPostgresAppliedMigrations(ctx, store.db, migrations)
	if err != nil {
		return err
	}
	if len(applied) != len(migrations) {
		return fmt.Errorf("postgres migration ledger has %d entries, want %d", len(applied), len(migrations))
	}
	return nil
}
