package gateway

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

// conversationFileMaterializer owns only one execution's temporary workspace.
type conversationFileMaterializer struct {
	mu        sync.Mutex
	workspace string
	service   *execution.Service
	envelope  execution.Envelope
	budget    *execution.Budget
	byPath    map[string]execution.File
	ready     map[string]bool
	reserved  map[string]bool
	inflight  map[string]chan struct{}
}

func newConversationFileMaterializer(workspace string, service *execution.Service, envelope execution.Envelope, budget *execution.Budget) *conversationFileMaterializer {
	m := &conversationFileMaterializer{workspace: workspace, service: service, envelope: envelope, budget: budget, byPath: map[string]execution.File{}, ready: map[string]bool{}, reserved: map[string]bool{}, inflight: map[string]chan struct{}{}}
	current := map[string]bool{}
	for _, file := range envelope.InputFiles {
		current[file.ID] = true
	}
	for _, message := range envelope.Messages {
		for _, file := range message.Files {
			if !current[file.ID] {
				m.byPath[filepath.Join(workspace, "conversation-files", file.ID, file.Name)] = file
			}
		}
	}
	return m
}
func (m *conversationFileMaterializer) read(ctx context.Context, abs string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		file, ok := m.byPath[filepath.Clean(abs)]
		if !ok {
			m.mu.Unlock()
			return errors.New("conversation file unavailable")
		}
		if pending := m.inflight[file.ID]; pending != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-pending:
			}
			continue
		}
		if m.inflight == nil {
			m.inflight = map[string]chan struct{}{}
		}
		done := make(chan struct{})
		m.inflight[file.ID] = done
		ready := m.ready[file.ID]
		m.mu.Unlock()
		err := m.materialize(ctx, abs, file, ready)
		m.mu.Lock()
		delete(m.inflight, file.ID)
		close(done)
		m.mu.Unlock()
		return err
	}
}

func (m *conversationFileMaterializer) materialize(ctx context.Context, abs string, file execution.File, ready bool) error {
	if ready {
		input, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return errors.New("conversation file unavailable")
		}
		defer input.Close()
		info, err := input.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(file.Size) {
			return errors.New("conversation file unavailable")
		}
		data, err := io.ReadAll(io.LimitReader(input, int64(file.Size)+1))
		if err != nil || len(data) != file.Size || execution.Digest(data) != file.SHA256 {
			return errors.New("conversation file unavailable")
		}
		return ctx.Err()
	}
	data, err := m.service.AwaitHandle(ctx, m.envelope, file.ID)
	if err != nil {
		return errors.New("conversation file unavailable")
	}
	if len(data) != file.Size || execution.Digest(data) != file.SHA256 {
		return errors.New("conversation file unavailable")
	}
	m.mu.Lock()
	if !m.reserved[file.ID] {
		if err = m.budget.Reserve(len(data)); err != nil {
			m.mu.Unlock()
			return err
		}
		m.reserved[file.ID] = true
	}
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(m.workspace, "conversation-files")
	for _, component := range []string{dir, filepath.Join(dir, file.ID)} {
		if err = os.Mkdir(component, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, statErr := os.Lstat(component)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("conversation file unavailable")
		}
	}
	output, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := output.Write(data)
	closeErr := output.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(abs)
		return errors.New("conversation file unavailable")
	}
	m.mu.Lock()
	m.ready[file.ID] = true
	m.mu.Unlock()
	return nil
}
