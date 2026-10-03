package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestR3OnlineScheduleExpiryAdmissionAndContentLifetime(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := &Server{lifecycleCtx: ctx, r3Root: filepath.Join(t.TempDir(), "r3"), r3Executor: func(context.Context, r3execution.Envelope, map[string][]byte) (r3execution.Output, error) {
		calls.Add(1)
		return r3execution.Output{Content: "scheduled answer"}, nil
	}}
	service, err := s.r3ExecutionService()
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	e := r3execution.Envelope{SchemaVersion: 1, OwnerID: "owner", ClientID: "client", DeploymentID: "deployment", InstallationID: "11111111-1111-4111-8111-111111111111", ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444", Messages: []r3execution.Message{{Role: "user", Content: "scheduled task"}}}
	if err = service.Bind(e.OwnerID, e.ClientID, e.InstallationID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	now := time.Now().UTC()
	key := scheduleKey(e.OwnerID, e.ClientID, e.RequestID)
	registry := s.scheduleRegistry()
	registry.mu.Lock()
	registry.entries[key] = &r3Schedule{e: e, digest: r3execution.Digest(raw), due: now.Add(time.Second), expiry: now.Add(r3ScheduleLease), state: "leased"}
	registry.mu.Unlock()
	s.r3ScheduleTick(now.Add(31 * time.Second))
	if calls.Load() != 0 {
		t.Fatal("expired client lease executed schedule")
	}
	registry.mu.Lock()
	if len(registry.entries) != 0 {
		t.Fatal("expired context retained")
	}
	registry.entries[key] = &r3Schedule{e: e, digest: r3execution.Digest(raw), due: now, expiry: now.Add(r3ScheduleLease), state: "leased"}
	registry.mu.Unlock()
	for range 4 {
		s.r3ScheduleTick(now)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := service.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		if err == nil && status.State == "completed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	service.Wait()
	if calls.Load() != 1 {
		t.Fatalf("schedule executions=%d", calls.Load())
	}
	s.revokeR3Schedules(e.ClientID)
	registry.mu.Lock()
	remaining := len(registry.entries)
	registry.mu.Unlock()
	if remaining != 0 {
		t.Fatal("revoked client retained scheduled context")
	}
}

func TestR3StartupRemovesOnlyOwnedDeploymentMemoryWorkspaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "r3")
	scratch, err := r3MemoryWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(scratch, "synthetic-private-input"), []byte("orphaned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	other, err := r3MemoryWorkspace(root + "other")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(other)
	if err = r3MemorySweep(root); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(scratch); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restart left temporary content", err)
	}
	if _, err = os.Stat(other); err != nil {
		t.Fatal("other deployment workspace removed", err)
	}
}
