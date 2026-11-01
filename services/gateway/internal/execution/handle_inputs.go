package execution

import (
	"context"
	"sort"
	"time"
)

// handleStage is process-only: bytes and pending requests never enter a fence.
// All fields are protected by Service.mu.
type handleStage struct {
	declared map[string]File
	pending  map[string]*handleWait
	uploaded map[string][]byte
	reserved int
	done     chan struct{}
}
type handleWait struct {
	ready   chan struct{}
	waiters int
}

func newHandleStage(e Envelope) *handleStage {
	stage := &handleStage{declared: map[string]File{}, pending: map[string]*handleWait{}, uploaded: map[string][]byte{}, reserved: ContextBytes, done: make(chan struct{})}
	current := map[string]bool{}
	for _, f := range e.InputFiles {
		current[f.ID] = true
		stage.reserved += f.Size
	}
	for _, m := range e.Messages {
		for _, f := range m.Files {
			if !current[f.ID] {
				stage.declared[f.ID] = f
			}
		}
	}
	return stage
}

func (s *Service) uploadHandleLocked(key, file, digest string, raw []byte) error {
	f, ok := s.control.Fences[key]
	stage := s.handles[key]
	if !ok || stage == nil || (f.State != "accepted" && f.State != "running") || !f.Deadline.After(s.now()) || s.revokedClients[f.ClientID] {
		return ErrConflict
	}
	manifest, ok := stage.declared[file]
	if !ok || manifest.SHA256 != digest || manifest.Size != len(raw) || len(raw) > ResultBytes {
		return ErrConflict
	}
	if prior, ok := stage.uploaded[file]; ok {
		if Digest(prior) == digest {
			return nil
		}
		return ErrConflict
	}
	wait := stage.pending[file]
	if wait == nil {
		return ErrConflict
	}
	stage.uploaded[file] = append([]byte(nil), raw...)
	close(wait.ready)
	return nil
}

func (s *Service) pendingInputsLocked(key string) []PendingInput {
	stage := s.handles[key]
	if stage == nil {
		return nil
	}
	rows := make([]PendingInput, 0, len(stage.pending))
	for id := range stage.pending {
		if _, done := stage.uploaded[id]; !done {
			f := stage.declared[id]
			rows = append(rows, PendingInput{FileID: id, Size: f.Size, SHA256: f.SHA256})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].FileID < rows[j].FileID })
	return rows
}

// AwaitHandle requests only an admitted handle and returns verified bytes.
// The caller must place them only in its ephemeral execution workspace.
func (s *Service) AwaitHandle(ctx context.Context, e Envelope, fileID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := keyFor(e.OwnerID, e.ClientID, e.RequestID)
	s.mu.Lock()
	stage := s.handles[key]
	fence, ok := s.control.Fences[key]
	if !ok || s.closed || stage == nil || fence.InstallationID != e.InstallationID || (fence.State != "accepted" && fence.State != "running") || !fence.Deadline.After(s.now()) || s.revokedClients[e.ClientID] {
		s.mu.Unlock()
		return nil, ErrConflict
	}
	f, ok := stage.declared[fileID]
	if !ok {
		s.mu.Unlock()
		return nil, ErrConflict
	}
	if data, ok := stage.uploaded[fileID]; ok {
		out := append([]byte(nil), data...)
		s.mu.Unlock()
		return out, nil
	}
	wait := stage.pending[fileID]
	if wait == nil {
		if stage.reserved+f.Size > TaskBytes {
			s.mu.Unlock()
			return nil, ErrCapacity
		}
		stage.reserved += f.Size
		wait = &handleWait{ready: make(chan struct{})}
		stage.pending[fileID] = wait
	}
	wait.waiters++
	s.mu.Unlock()
	waitDuration := min(60*time.Second, fence.Deadline.Sub(s.now()))
	if deadline, exists := ctx.Deadline(); exists {
		waitDuration = min(waitDuration, time.Until(deadline))
	}
	timer := time.NewTimer(max(0, waitDuration))
	defer timer.Stop()
	select {
	case <-wait.ready:
	case <-stage.done:
	case <-ctx.Done():
	case <-timer.C:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handles[key] != stage {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrExpired
	}
	wait.waiters--
	current := s.control.Fences[key]
	if err := ctx.Err(); err != nil || s.closed || s.revokedClients[e.ClientID] || !current.Deadline.After(s.now()) || (current.State != "accepted" && current.State != "running") {
		if wait.waiters == 0 {
			delete(stage.pending, fileID)
			if _, ok := stage.uploaded[fileID]; !ok {
				stage.reserved -= f.Size
			}
		}
		if err != nil {
			return nil, err
		}
		return nil, ErrExpired
	}
	if data, ok := stage.uploaded[fileID]; ok {
		if wait.waiters == 0 {
			delete(stage.pending, fileID)
		}
		return append([]byte(nil), data...), nil
	}
	if wait.waiters == 0 {
		delete(stage.pending, fileID)
		stage.reserved -= f.Size
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrExpired
}

// dropHandleStageLocked wakes even a read whose caller failed to derive its
// context from the execution. Late uploads can never recreate this stage.
func (s *Service) dropHandleStageLocked(key string) {
	if stage := s.handles[key]; stage != nil {
		close(stage.done)
		delete(s.handles, key)
	}
}
