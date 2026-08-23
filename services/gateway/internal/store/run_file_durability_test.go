package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileRunWritesRollbackOnDefiniteFailure(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*FileStore) error
		absent func(*FileStore) bool
	}{
		{
			name: "run",
			invoke: func(repository *FileStore) error {
				_, err := repository.SaveRun(t.Context(), app.AgentRun{ID: "run-definite", SessionID: "session", State: "running"})
				return err
			},
			absent: func(repository *FileStore) bool {
				_, found, err := repository.GetRun(t.Context(), "run-definite")
				return err == nil && !found
			},
		},
		{
			name: "tool call",
			invoke: func(repository *FileStore) error {
				_, err := repository.SaveToolCall(t.Context(), app.ToolCall{ID: "tool-definite", SessionID: "session", RunID: "run", Tool: "files.read"})
				return err
			},
			absent: func(repository *FileStore) bool {
				_, found, err := repository.GetToolCall(t.Context(), "tool-definite")
				return err == nil && !found
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			repository.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
			err = test.invoke(repository)
			if StoreErrorCodeOf(err) != StoreErrorDurability || !errors.Is(err, errFileCommitInjected) || repository.currentFileFence() != nil {
				t.Fatalf("definite failure = %v code=%q fence=%v", err, StoreErrorCodeOf(err), repository.currentFileFence())
			}
			if !test.absent(repository) {
				t.Fatal("definite failure left the in-memory candidate visible")
			}
		})
	}
}

func TestFileRunUnknownOutcomesReconcileAndSurviveRestart(t *testing.T) {
	t.Run("run", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		repository, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		repository.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1, renameApplied: true}
		proposed := app.AgentRun{ID: "run-unknown", SessionID: "session", State: "completed", StartedAt: time.Now().UTC()}
		candidate, writeErr := repository.SaveRun(t.Context(), proposed)
		if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || candidate.ID != proposed.ID || repository.currentFileFence() == nil {
			t.Fatalf("unknown SaveRun = %#v err=%v fence=%v", candidate, writeErr, repository.currentFileFence())
		}
		reconciled, err := ReconcileRunWrite(t.Context(), repository, candidate, writeErr)
		if err != nil || !runRecordsEqual(reconciled, candidate) || repository.currentFileFence() != nil {
			t.Fatalf("ReconcileRunWrite = %#v err=%v fence=%v", reconciled, err, repository.currentFileFence())
		}
		restarted, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		stored, found, err := restarted.GetRun(t.Context(), proposed.ID)
		if err != nil || !found || !runRecordsEqual(stored, candidate) {
			t.Fatalf("restarted run = %#v found=%t err=%v", stored, found, err)
		}
	})

	t.Run("tool call", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		repository, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
		proposed := app.ToolCall{
			ID: "tool-unknown", SessionID: "session", RunID: "run", Tool: "files.read", Status: app.ToolCallStatusCompleted,
			Arguments: map[string]any{"path": "document.txt"}, StartedAt: time.Now().UTC(),
		}
		candidate, writeErr := repository.SaveToolCall(t.Context(), proposed)
		if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || candidate.ID != proposed.ID || repository.currentFileFence() == nil {
			t.Fatalf("unknown SaveToolCall = %#v err=%v fence=%v", candidate, writeErr, repository.currentFileFence())
		}
		reconciled, err := ReconcileToolCallWrite(t.Context(), repository, candidate, writeErr)
		if err != nil || !runRecordsEqual(reconciled, candidate) || repository.currentFileFence() != nil {
			t.Fatalf("ReconcileToolCallWrite = %#v err=%v fence=%v", reconciled, err, repository.currentFileFence())
		}
		restarted, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		stored, found, err := restarted.GetToolCall(t.Context(), proposed.ID)
		if err != nil || !found || !runRecordsEqual(stored, candidate) {
			t.Fatalf("restarted tool call = %#v found=%t err=%v", stored, found, err)
		}
	})
}

func TestRunRepositoryConcurrentAccess(t *testing.T) {
	backends := []struct {
		name string
		new  func(*testing.T) RunRepository
	}{
		{name: "memory", new: func(*testing.T) RunRepository { return NewMemoryStore() }},
		{name: "file", new: func(t *testing.T) RunRepository {
			repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return repository
		}},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.new(t)
			const workers = 24
			errorsFound := make(chan error, workers*4)
			var wait sync.WaitGroup
			for index := range workers {
				wait.Add(1)
				go func() {
					defer wait.Done()
					runID := app.NewID("run_race")
					if _, err := repository.SaveRun(t.Context(), app.AgentRun{ID: runID, SessionID: "session-race", State: "running"}); err != nil {
						errorsFound <- err
						return
					}
					toolID := app.NewID("tool_race")
					if _, err := repository.SaveToolCall(t.Context(), app.ToolCall{ID: toolID, SessionID: "session-race", RunID: runID, Tool: "files.read", Arguments: map[string]any{"index": index}}); err != nil {
						errorsFound <- err
						return
					}
					if _, found, err := repository.GetRun(t.Context(), runID); err != nil || !found {
						if err == nil {
							err = errors.New("concurrent run was not found")
						}
						errorsFound <- err
					}
					if _, found, err := repository.GetToolCall(t.Context(), toolID); err != nil || !found {
						if err == nil {
							err = errors.New("concurrent tool call was not found")
						}
						errorsFound <- err
					}
				}()
			}
			wait.Wait()
			close(errorsFound)
			for err := range errorsFound {
				t.Error(err)
			}
			runs, err := repository.ListRuns(t.Context(), "session-race")
			if err != nil || len(runs) != workers {
				t.Fatalf("concurrent runs = %d err=%v", len(runs), err)
			}
			calls, err := repository.ListToolCalls(t.Context(), "session-race")
			if err != nil || len(calls) != workers {
				t.Fatalf("concurrent tool calls = %d err=%v", len(calls), err)
			}
		})
	}
}
