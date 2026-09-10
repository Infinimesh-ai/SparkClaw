package emailmanagement

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeploymentBoundaryRecordAndLegacyFallback(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	s := &Service{opts: Options{WorkspaceRoot: t.TempDir()}, now: func() time.Time { return now }}
	if got, err := s.deploymentBoundary(); err != nil || !got.Equal(now) {
		t.Fatalf("legacy fallback: %v %v", got, err)
	}
	p := filepath.Join(s.opts.WorkspaceRoot, ".sparkclaw-deployment.json")
	if err := os.WriteFile(p, []byte(`{"started_at":"2026-09-01T09:00:00+08:00"}`), 0600); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	if got, err := s.deploymentBoundary(); err != nil || !got.Equal(want) {
		t.Fatalf("deployment boundary: %v %v", got, err)
	}
	if err := os.WriteFile(p, []byte(`{"started_at":"2099-01-01T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.deploymentBoundary(); err == nil {
		t.Fatal("future record accepted")
	}
}
