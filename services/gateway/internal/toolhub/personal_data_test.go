package toolhub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestEmailAndCalendarLocalTools(t *testing.T) {
	root := t.TempDir()
	writePersonalFixtures(t, root)
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	search, err := hub.Execute(context.Background(), "email.search", map[string]any{"query": "deployment"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	searchOut := search.Output.(map[string]any)
	if searchOut["count"] != 1 {
		t.Fatalf("unexpected email search output: %#v", searchOut)
	}

	draft, err := hub.Execute(context.Background(), "email.draft_reply", map[string]any{"thread_id": "thread_alpha", "body": "Thanks, I will review it."}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	draftOut := draft.Output.(map[string]any)
	if draftOut["status"] != "email_reply_draft_written" {
		t.Fatalf("unexpected draft output: %#v", draftOut)
	}
	send, err := hub.Execute(context.Background(), "email.send", map[string]any{
		"to":      []any{"owner@example.test"},
		"subject": "SparkClaw checklist",
		"body":    "Deployment is ready.",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	sendOut := send.Output.(map[string]any)
	if sendOut["status"] != "sent_mock" {
		t.Fatalf("unexpected send output: %#v", sendOut)
	}
	if raw, err := os.ReadFile(filepath.Join(root, ".sparkclaw", "mock", "email_outbox.jsonl")); err != nil || !strings.Contains(string(raw), "SparkClaw checklist") {
		t.Fatalf("mock outbox missing send record raw=%q err=%v", raw, err)
	}

	events, err := hub.Execute(context.Background(), "calendar.read", map[string]any{"from": "2026-05-22T00:00:00Z", "to": "2026-05-22T23:59:59Z"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	eventsOut := events.Output.(map[string]any)
	if eventsOut["count"] != 2 {
		t.Fatalf("unexpected calendar output: %#v", eventsOut)
	}

	proposal, err := hub.Execute(context.Background(), "calendar.propose_event", map[string]any{"title": "Demo", "start": "2026-05-23T10:00:00Z", "end": "2026-05-23T10:30:00Z"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	proposalOut := proposal.Output.(map[string]any)
	if proposalOut["status"] != "calendar_event_proposal_written" {
		t.Fatalf("unexpected proposal output: %#v", proposalOut)
	}
	created, err := hub.Execute(context.Background(), "calendar.create", map[string]any{"title": "Demo", "start": "2026-05-23T10:00:00Z", "end": "2026-05-23T10:30:00Z"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	createdOut := created.Output.(map[string]any)
	if createdOut["status"] != "calendar_event_created" {
		t.Fatalf("unexpected calendar create output: %#v", createdOut)
	}
	if raw, err := os.ReadFile(filepath.Join(root, ".sparkclaw", "mock", "calendar_created_events.jsonl")); err != nil || !strings.Contains(string(raw), "Demo") {
		t.Fatalf("mock calendar create log missing event raw=%q err=%v", raw, err)
	}
}

func TestNotifyAskApprovalCreatesPendingApproval(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	result, err := hub.Execute(context.Background(), "notify.ask_approval", map[string]any{
		"summary": "Confirm sending the deployment note",
		"reason":  "The user should approve external communication.",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["status"] != "approval_requested" || out["approval_id"] == "" {
		t.Fatalf("unexpected notify output: %#v", out)
	}
	approvals := st.ListApprovals("pending")
	if len(approvals) != 1 || approvals[0].Summary != "Confirm sending the deployment note" || approvals[0].Tool != "notify.ask_approval" {
		t.Fatalf("notify approval not saved: %#v", approvals)
	}
	calls := st.ListToolCalls("s")
	if len(calls) != 1 || calls[0].Status != "approval_pending" || calls[0].ApprovalID != approvals[0].ID {
		t.Fatalf("notify tool call not saved: %#v", calls)
	}
}

func writePersonalFixtures(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".sparkclaw", "mock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "email_threads.json"), []byte(`[{"id":"thread_alpha","subject":"DGX Spark deployment checklist","from":"alex@example.test","to":["owner@example.test"],"date":"2026-05-22T09:00:00Z","labels":["inbox"],"messages":[{"from":"alex@example.test","date":"2026-05-22T09:00:00Z","body":"Please review deployment."}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calendar_events.json"), []byte(`[{"id":"event_1","title":"Standup","start":"2026-05-22T10:00:00Z","end":"2026-05-22T10:30:00Z"},{"id":"event_2","title":"Review","start":"2026-05-22T15:00:00Z","end":"2026-05-22T16:00:00Z"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
}
