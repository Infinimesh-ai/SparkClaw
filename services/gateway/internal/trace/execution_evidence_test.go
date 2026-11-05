package trace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

func TestEvaluationRecorderRequiresExplicitOptIn(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.TraceDir = filepath.Join(t.TempDir(), "traces")
	if NewWriterFromConfig(cfg).ExecutionEvidence() != nil {
		t.Fatal("internal evaluation enabled by default")
	}
	if _, err := os.Stat(cfg.Storage.TraceDir); !os.IsNotExist(err) {
		t.Fatalf("default constructor wrote evidence: %v", err)
	}
	cfg.Storage.ExecutionEvidence.Enabled = true
	r := NewWriterFromConfig(cfg).ExecutionEvidence()
	if r == nil {
		t.Fatal("explicit evaluation not enabled")
	}
	scope := executionevidence.Scope{OwnerID: "owner", SessionID: "session", RunID: "run"}
	if err := r.Begin(context.Background(), scope, map[string]string{"authorization": "Bearer private-token", "question": "safe"}); err != nil {
		t.Fatal(err)
	}
	m, err := r.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Payloads) != 1 || !m.Payloads[0].Redacted {
		t.Fatalf("redaction lineage missing: %+v", m.Payloads)
	}
}
