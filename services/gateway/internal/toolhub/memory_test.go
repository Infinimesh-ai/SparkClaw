package toolhub

import (
	"context"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestMemoryWriteCandidateRejectsSensitiveContentByDefault(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	_, err := hub.Execute(context.Background(), "memory.write_candidate", map[string]any{
		"content":     "Deployment api_key is sk-local-test",
		"kind":        "profile",
		"sensitivity": "normal",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "appears sensitive") {
		t.Fatalf("expected sensitive memory rejection, got %v", err)
	}
	if candidates := st.ListMemoryCandidates(""); len(candidates) != 0 {
		t.Fatalf("sensitive memory candidate should not be saved: %#v", candidates)
	}
}

func TestMemoryWriteCandidateRejectsSensitiveLabelByDefault(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	_, err := hub.Execute(context.Background(), "memory.write_candidate", map[string]any{
		"content":     "Remember the deployment account detail.",
		"kind":        "profile",
		"sensitivity": "sensitive",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "sensitivity") {
		t.Fatalf("expected sensitive label rejection, got %v", err)
	}
	if candidates := st.ListMemoryCandidates(""); len(candidates) != 0 {
		t.Fatalf("sensitive labeled memory candidate should not be saved: %#v", candidates)
	}
}

func TestMemoryWriteCandidateAllowsSensitiveContentWhenConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Memory.AllowSensitiveMemory = true
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	result, err := hub.Execute(context.Background(), "memory.write_candidate", map[string]any{
		"content":     "Deployment api_key is intentionally stored for this test.",
		"kind":        "profile",
		"sensitivity": "sensitive",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output == nil {
		t.Fatal("expected memory candidate output")
	}
	if candidates := st.ListMemoryCandidates("pending"); len(candidates) != 1 {
		t.Fatalf("expected one pending candidate, got %#v", candidates)
	}
}

func TestMemoryProposeAliasesWriteCandidate(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	result, err := hub.Execute(context.Background(), "memory.propose", map[string]any{
		"content": "SparkClaw remembers compatibility aliases.",
		"kind":    "procedural",
		"reason":  "architecture docs use memory.propose",
	}, "s", "run_alias")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output == nil {
		t.Fatal("expected memory candidate output")
	}
	candidates := st.ListMemoryCandidates("pending")
	if len(candidates) != 1 || candidates[0].RunID != "run_alias" || candidates[0].Kind != "procedural" {
		t.Fatalf("memory.propose did not create expected candidate: %#v", candidates)
	}
}

func TestMemoryWriteSensitiveCreatesAcceptedSensitiveMemory(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	result, err := hub.Execute(context.Background(), "memory.write_sensitive", map[string]any{
		"content": "Deployment api_key is sk-approved-sensitive-test",
		"kind":    "credential_note",
		"reason":  "Owner explicitly approved retaining this sensitive note.",
	}, "s", "run_sensitive")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["sensitivity"] != "sensitive" {
		t.Fatalf("expected sensitive output, got %#v", out)
	}
	if pending := st.ListMemoryCandidates("pending"); len(pending) != 0 {
		t.Fatalf("sensitive memory candidate should be resolved immediately after approval: %#v", pending)
	}
	candidates := st.ListMemoryCandidates("accepted")
	if len(candidates) != 1 || candidates[0].Sensitivity != "sensitive" || candidates[0].RunID != "run_sensitive" {
		t.Fatalf("accepted sensitive candidate missing: %#v", candidates)
	}
	memories := st.SearchMemories("sk-approved-sensitive-test")
	if len(memories) != 1 || memories[0].Kind != "credential_note" || memories[0].SourceID != "run_sensitive" {
		t.Fatalf("sensitive memory not searchable: %#v", memories)
	}
}
