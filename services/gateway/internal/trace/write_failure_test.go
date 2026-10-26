package trace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
)

type failingArtifactStore struct {
	artifact.Store
	err error
}

func (s failingArtifactStore) Put(context.Context, string, string, []byte) (artifact.Object, error) {
	return artifact.Object{}, s.err
}

func TestWriterReportsArtifactFailureWithoutInventingStoredEvidence(t *testing.T) {
	root := t.TempDir()
	failure := errors.New("private backend /credential/path unavailable")
	w := NewWriter(root)
	w.artifacts = failingArtifactStore{err: failure}
	run := app.AgentRun{ID: app.NewID("run"), State: "completed"}
	object, err := w.WriteRunObject(t.Context(), RunTrace{Run: run})
	if !errors.Is(err, failure) || object != nil {
		t.Fatalf("artifact failure suppressed or nonexistent object returned: object=%+v err=%v", object, err)
	}
	if WriteFailureStage(err) != "artifact_write" || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe or unattributed error: %v stage=%s", err, WriteFailureStage(err))
	}
	if _, err := os.Stat(filepath.Join(root, run.ID+".json")); err != nil {
		t.Fatalf("successful local trace was discarded: %v", err)
	}
}

func TestWriterCanceledBeforeWriteDoesNotClaimAWriteAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := filepath.Join(t.TempDir(), "not-created")
	object, err := NewWriter(root).WriteRunObject(ctx, RunTrace{Run: app.AgentRun{ID: app.NewID("run")}})
	if object != nil || !errors.Is(err, context.Canceled) || WriteFailureStage(err) != "context" {
		t.Fatalf("wrong failure boundary: %+v %v stage=%s", object, err, WriteFailureStage(err))
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("canceled trace wrote a directory: %v", err)
	}
}
