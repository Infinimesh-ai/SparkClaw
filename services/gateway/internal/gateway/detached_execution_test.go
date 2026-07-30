package gateway

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestDetachedExecutionTimeoutDerivesFromRunBudget(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.RunMaxDurationSeconds = 600
	cfg.Model.HTTPTimeoutSeconds = 120
	want := time.Duration(600+120+detachedExecutionGraceSeconds) * time.Second
	if got := detachedExecutionTimeout(cfg); got != want {
		t.Fatalf("detached execution timeout = %v, want %v", got, want)
	}

	cfg.Runtime.RunMaxDurationSeconds = 0
	cfg.Model.HTTPTimeoutSeconds = 0
	defaults := config.Default()
	want = time.Duration(defaults.Runtime.RunMaxDurationSeconds+defaults.Model.HTTPTimeoutSeconds+detachedExecutionGraceSeconds) * time.Second
	if got := detachedExecutionTimeout(cfg); got != want {
		t.Fatalf("zero config must fall back to defaults: got %v, want %v", got, want)
	}
}

func TestDetachedExecutionContextCarriesDeadline(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	server := New(cfg, st, tools, runtime)

	ctx, cancel := server.detachedExecutionContext()
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("detached execution context must carry a deadline; the lifecycle context has none")
	}
	if until := time.Until(deadline); until <= 0 || until > detachedExecutionTimeout(cfg) {
		t.Fatalf("detached execution deadline out of range: %v from now", until)
	}
}
