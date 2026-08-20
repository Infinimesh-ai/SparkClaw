package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type fileCommitHandle interface {
	Name() string
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type fileCommitOps interface {
	Encode(Snapshot, *fileEncryption) ([]byte, error)
	MkdirAll(string, os.FileMode) error
	CreateTemp(string, string) (fileCommitHandle, error)
	Rename(string, string) error
	ReadFile(string) ([]byte, error)
	Remove(string) error
	OpenDirectory(string) (fileCommitHandle, error)
}

type osFileCommitOps struct{}

func (osFileCommitOps) Encode(snapshot Snapshot, encryption *fileEncryption) ([]byte, error) {
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	if encryption != nil {
		return encryption.encrypt(raw)
	}
	return raw, nil
}

func (osFileCommitOps) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}

func (osFileCommitOps) CreateTemp(directory, pattern string) (fileCommitHandle, error) {
	return os.CreateTemp(directory, pattern)
}

func (osFileCommitOps) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (osFileCommitOps) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (osFileCommitOps) Remove(path string) error {
	return os.Remove(path)
}

func (osFileCommitOps) OpenDirectory(path string) (fileCommitHandle, error) {
	return os.Open(path)
}

type fileRollbackState struct {
	snapshot                Snapshot
	passiveNotificationRevs map[string]uint64
}

type fileSubmittedOutcome struct {
	operation      StoreOperation
	candidate      [sha256.Size]byte
	previous       [sha256.Size]byte
	previousExists bool
	rollback       fileRollbackState
	done           chan struct{}
}

func (s *FileStore) currentFileFence() *fileSubmittedOutcome {
	s.fenceMu.Lock()
	defer s.fenceMu.Unlock()
	return s.fence
}

func (s *FileStore) installFileFence(fence *fileSubmittedOutcome) {
	s.fenceMu.Lock()
	defer s.fenceMu.Unlock()
	if s.fence != nil {
		panic("install FileStore fence while another outcome is pending")
	}
	s.fence = fence
}

func (s *FileStore) clearFileFence(fence *fileSubmittedOutcome) bool {
	s.fenceMu.Lock()
	defer s.fenceMu.Unlock()
	if s.fence != fence {
		return false
	}
	s.fence = nil
	close(fence.done)
	return true
}

func (s *FileStore) admitMigrated(ctx context.Context, operation StoreOperation, weight int64) (context.Context, func(), error) {
	ctx, cancel := operationContext(ctx, operation, s.timeouts)
	for {
		if err := operationContextError(operation, ctx); err != nil {
			cancel()
			return nil, nil, err
		}
		if fence := s.currentFileFence(); fence != nil {
			if err := s.reconcileFileFence(ctx, operation, fence); err != nil {
				cancel()
				return nil, nil, err
			}
			continue
		}
		if err := s.admission.Acquire(ctx, weight); err != nil {
			cancel()
			return nil, nil, contextStoreError(operation, ctx, err)
		}
		if fence := s.currentFileFence(); fence == nil {
			return ctx, func() {
				s.admission.Release(weight)
				cancel()
			}, nil
		} else {
			s.admission.Release(weight)
			if err := s.reconcileFileFence(ctx, operation, fence); err != nil {
				cancel()
				return nil, nil, err
			}
		}
	}
}

func (s *FileStore) saveISCPOnboarding(ctx context.Context, onboarding app.ISCPOnboarding) (app.ISCPOnboarding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationISCPOnboardingSave, fileAdmissionCapacity)
	if err != nil {
		return app.ISCPOnboarding{}, err
	}
	defer release()
	if s.path == "" {
		return app.ISCPOnboarding{}, storeError(OperationISCPOnboardingSave, StoreErrorInvalid, errors.New("file state path is required"))
	}

	rollback := s.captureFileRollback()
	previous, previousExists, err := s.readFileDestination()
	if err != nil {
		return app.ISCPOnboarding{}, storeError(OperationISCPOnboardingSave, StoreErrorUnavailable, err)
	}
	out, err := s.inner.SaveISCPOnboarding(ctx, onboarding)
	if err != nil {
		return app.ISCPOnboarding{}, rebindStoreOperation(err, OperationISCPOnboardingSave)
	}
	candidate, err := s.commitOps.Encode(s.inner.snapshot(), s.encryption)
	if err != nil {
		s.restoreFileRollback(rollback)
		return app.ISCPOnboarding{}, storeError(OperationISCPOnboardingSave, StoreErrorDurability, err)
	}
	if err := s.commitOnboardingSnapshot(ctx, candidate, previous, previousExists); err != nil {
		if StoreErrorCodeOf(err) == StoreErrorUnknownOutcome {
			fence := &fileSubmittedOutcome{
				operation: OperationISCPOnboardingSave, candidate: sha256.Sum256(candidate),
				previous: sha256.Sum256(previous), previousExists: previousExists,
				rollback: rollback, done: make(chan struct{}),
			}
			s.installFileFence(fence)
			return app.ISCPOnboarding{}, err
		}
		s.restoreFileRollback(rollback)
		return app.ISCPOnboarding{}, err
	}
	return out, nil
}

func (s *FileStore) getISCPOnboarding(ctx context.Context, id string) (app.ISCPOnboarding, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationISCPOnboardingGet, 1)
	if err != nil {
		return app.ISCPOnboarding{}, false, err
	}
	defer release()
	return s.inner.GetISCPOnboarding(ctx, id)
}

func (s *FileStore) listISCPOnboardings(ctx context.Context, ownerID string) ([]app.ISCPOnboarding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationISCPOnboardingList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListISCPOnboardings(ctx, ownerID)
}

func (s *FileStore) commitOnboardingSnapshot(ctx context.Context, candidate, previous []byte, previousExists bool) error {
	directory := filepath.Dir(s.path)
	if err := s.commitOps.MkdirAll(directory, 0o755); err != nil {
		return storeError(OperationISCPOnboardingSave, StoreErrorDurability, err)
	}
	temporary, err := s.commitOps.CreateTemp(directory, ".sparkclaw-state-*")
	if err != nil {
		return storeError(OperationISCPOnboardingSave, StoreErrorDurability, err)
	}
	temporaryPath := temporary.Name()
	failBeforeSubmit := func(primary error) error {
		if closeErr := temporary.Close(); closeErr != nil {
			primary = errors.Join(primary, closeErr)
		}
		if removeErr := s.commitOps.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			primary = errors.Join(primary, removeErr)
		}
		return storeError(OperationISCPOnboardingSave, StoreErrorDurability, primary)
	}
	if err := writeFileCommit(temporary, candidate); err != nil {
		return failBeforeSubmit(err)
	}
	if err := temporary.Sync(); err != nil {
		return failBeforeSubmit(err)
	}
	if err := temporary.Close(); err != nil {
		if removeErr := s.commitOps.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
		return storeError(OperationISCPOnboardingSave, StoreErrorDurability, err)
	}
	if err := operationContextError(OperationISCPOnboardingSave, ctx); err != nil {
		if removeErr := s.commitOps.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
		return err
	}
	if err := s.commitOps.Rename(temporaryPath, s.path); err != nil {
		destination, readErr := s.commitOps.ReadFile(s.path)
		if readErr == nil {
			digest := sha256.Sum256(destination)
			if digest == sha256.Sum256(candidate) {
				return storeError(OperationISCPOnboardingSave, StoreErrorUnknownOutcome, err)
			}
			if previousExists && digest == sha256.Sum256(previous) {
				if removeErr := s.commitOps.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = errors.Join(err, removeErr)
				}
				return storeError(OperationISCPOnboardingSave, StoreErrorDurability, err)
			}
			return storeError(OperationISCPOnboardingSave, StoreErrorUnknownOutcome, err)
		}
		if errors.Is(readErr, os.ErrNotExist) && !previousExists {
			if removeErr := s.commitOps.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
			return storeError(OperationISCPOnboardingSave, StoreErrorDurability, err)
		}
		return storeError(OperationISCPOnboardingSave, StoreErrorUnknownOutcome, errors.Join(err, readErr))
	}
	if err := s.syncFileDirectory(); err != nil {
		return storeError(OperationISCPOnboardingSave, StoreErrorUnknownOutcome, err)
	}
	return nil
}

func writeFileCommit(file fileCommitHandle, payload []byte) error {
	for len(payload) > 0 {
		written, err := file.Write(payload)
		if written > 0 {
			payload = payload[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (s *FileStore) syncFileDirectory() error {
	directory, err := s.commitOps.OpenDirectory(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	if syncErr := directory.Sync(); syncErr != nil {
		return errors.Join(syncErr, directory.Close())
	}
	return directory.Close()
}

func (s *FileStore) readFileDestination() ([]byte, bool, error) {
	raw, err := s.commitOps.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func (s *FileStore) captureFileRollback() fileRollbackState {
	state := fileRollbackState{snapshot: s.inner.snapshot()}
	s.inner.mu.RLock()
	state.passiveNotificationRevs = cloneMap(s.inner.passiveNotificationRevs)
	s.inner.mu.RUnlock()
	return state
}

func (s *FileStore) restoreFileRollback(state fileRollbackState) {
	s.inner.loadSnapshot(state.snapshot)
	s.inner.mu.Lock()
	s.inner.passiveNotificationRevs = cloneMap(state.passiveNotificationRevs)
	s.inner.mu.Unlock()
}

func (s *FileStore) reconcileFileFence(ctx context.Context, operation StoreOperation, fence *fileSubmittedOutcome) error {
	if err := s.admission.Acquire(ctx, fileAdmissionCapacity); err != nil {
		return contextStoreError(operation, ctx, err)
	}
	defer s.admission.Release(fileAdmissionCapacity)
	if s.currentFileFence() != fence {
		return nil
	}
	raw, err := s.commitOps.ReadFile(s.path)
	if err == nil {
		digest := sha256.Sum256(raw)
		switch {
		case digest == fence.candidate:
			if err := s.syncFileDirectory(); err != nil {
				return storeError(operation, StoreErrorUnknownOutcome, err)
			}
			s.clearFileFence(fence)
			return nil
		case fence.previousExists && digest == fence.previous:
			s.restoreFileRollback(fence.rollback)
			s.clearFileFence(fence)
			return nil
		default:
			return storeError(operation, StoreErrorCorrupt, errors.New("file state differs from submitted and previous snapshots"))
		}
	}
	if errors.Is(err, os.ErrNotExist) && !fence.previousExists {
		s.restoreFileRollback(fence.rollback)
		s.clearFileFence(fence)
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return storeError(operation, StoreErrorCorrupt, errors.New("file state disappeared after submitted replacement"))
	}
	return storeError(operation, StoreErrorUnknownOutcome, err)
}

func rebindStoreOperation(err error, operation StoreOperation) error {
	var typed *StoreError
	if errors.As(err, &typed) {
		return &StoreError{Code: typed.Code, Operation: operation, Err: typed.Err}
	}
	return storeError(operation, StoreErrorInternal, fmt.Errorf("unclassified backend failure: %w", err))
}
