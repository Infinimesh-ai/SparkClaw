package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestSessionDeleteClosesOwnedRecordsAndPreservesIsolation(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			harness := newSessionRepositoryHarness(t, backend)
			target, err := harness.repository.CreateSession(t.Context(), "target")
			if err != nil {
				t.Fatal(err)
			}
			other, err := harness.repository.CreateSession(t.Context(), "other")
			if err != nil {
				t.Fatal(err)
			}
			populateSessionDeleteFixture(harness.memory, target.ID, other.ID)
			target, _, err = harness.repository.GetSession(t.Context(), target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if file, ok := harness.repository.(*FileStore); ok {
				if err := file.persistSnapshot(); err != nil {
					t.Fatal(err)
				}
			}

			deleted, err := harness.repository.DeleteSession(t.Context(), target.ID)
			if err != nil || !sessionsEqual(deleted, target) {
				t.Fatalf("deleted=%#v err=%v", deleted, err)
			}
			assertSessionDeleteFixture(t, harness.memory, target.ID, other.ID)
			if harness.restart != nil {
				restarted := harness.restart(t)
				file := restarted.(*FileStore)
				assertSessionDeleteFixture(t, file.inner, target.ID, other.ID)
				if _, found, err := restarted.GetSession(t.Context(), target.ID); err != nil || found {
					t.Fatalf("deleted session after restart found=%v err=%v", found, err)
				}
			}
		})
	}
}

func populateSessionDeleteFixture(store *MemoryStore, targetID, otherID string) {
	now := time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC)
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, fixture := range []struct {
		sessionID string
		suffix    string
	}{
		{sessionID: targetID, suffix: "target"},
		{sessionID: otherID, suffix: "other"},
	} {
		runID := "run-" + fixture.suffix
		reminderID := "reminder-" + fixture.suffix
		chatID := "chat-" + fixture.suffix
		session := store.sessions[fixture.sessionID]
		session.Source = "telegram"
		session.Hidden = true
		store.sessions[fixture.sessionID] = session
		artifact := app.ArtifactObject{ID: "artifact-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID, URI: "file:///" + fixture.suffix, CreatedAt: now}
		store.messages[fixture.sessionID] = []app.Message{{ID: "message-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID, CreatedAt: now}}
		store.runs[runID] = app.AgentRun{ID: runID, SessionID: fixture.sessionID, StartedAt: now}
		store.runFeedback[runID] = []app.RunFeedback{{ID: "feedback-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID}}
		store.modelCalls["model-"+fixture.suffix] = app.ModelCall{ID: "model-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID}
		store.toolCalls["tool-"+fixture.suffix] = app.ToolCall{ID: "tool-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID}
		store.documentRecords["document-"+fixture.suffix] = app.DocumentRecord{ID: "document-" + fixture.suffix, SessionID: fixture.sessionID}
		store.approvals["approval-"+fixture.suffix] = app.Approval{ID: "approval-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID}
		store.reminders[reminderID] = app.Reminder{ID: reminderID, SessionID: fixture.sessionID, RunID: runID}
		store.reminderDelivery["reminder-delivery-"+fixture.suffix] = app.ReminderDelivery{ID: "reminder-delivery-" + fixture.suffix, ReminderID: reminderID}
		store.memoryCandidates["candidate-"+fixture.suffix] = app.MemoryCandidate{ID: "candidate-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID}
		store.memories["memory-"+fixture.suffix] = app.Memory{ID: "memory-" + fixture.suffix, SourceID: runID}
		store.browserLoginBlocks["block-"+fixture.suffix] = migrateLegacyBrowserLoginBlock(app.BrowserLoginBlock{ID: "block-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID})
		store.artifactObjects[artifact.ID] = artifact
		store.indexArtifactObjectLocked(artifact)
		store.episodeSummaries["episode-"+fixture.suffix] = app.EpisodeSummary{ID: "episode-" + fixture.suffix, SessionID: fixture.sessionID, RunID: runID}
		store.externalChatSessions[chatID] = app.ExternalChatSession{
			ID: chatID, OwnerID: session.OwnerID, AuthorizedOwnerID: session.OwnerID, AuthorizedActorID: session.OwnerID,
			WorkspaceRoot: session.WorkspaceRoot, Channel: "telegram", LinkedSessionID: fixture.sessionID,
		}
		store.externalChatMessages["chat-message-"+fixture.suffix] = app.ExternalChatMessage{ID: "chat-message-" + fixture.suffix, ChatSessionID: chatID, Channel: "telegram"}
		store.auditEvents = append(store.auditEvents, app.AuditEvent{ID: "audit-fixture-" + fixture.suffix, SessionID: fixture.sessionID})
		store.events = append(store.events, app.Event{ID: "event-fixture-" + fixture.suffix, SessionID: fixture.sessionID})
	}
	store.messageReceives["receive-retained"] = app.MessageReceiveRecord{ID: "receive-retained"}
	store.messageDeliveries["delivery-retained"] = app.MessageDeliveryRecord{ID: "delivery-retained"}
	store.browserAuthRecords["browser-auth-retained"] = app.BrowserAuthRecord{ID: "browser-auth-retained", SessionRef: targetID}
	store.evalRuns["eval-retained"] = app.EvalRun{ID: "eval-retained"}
}

func assertSessionDeleteFixture(t testing.TB, store *MemoryStore, targetID, otherID string) {
	t.Helper()
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, key := range []string{targetID, "run-target", "model-target", "tool-target", "document-target", "approval-target", "reminder-target", "reminder-delivery-target", "candidate-target", "memory-target", "block-target", "artifact-target", "episode-target", "chat-target", "chat-message-target"} {
		if sessionDeleteFixtureContains(store, key) {
			t.Errorf("delete closure retained %q", key)
		}
	}
	for _, key := range []string{otherID, "run-other", "model-other", "tool-other", "document-other", "approval-other", "reminder-other", "reminder-delivery-other", "candidate-other", "memory-other", "block-other", "artifact-other", "episode-other", "chat-other", "chat-message-other"} {
		if !sessionDeleteFixtureContains(store, key) {
			t.Errorf("delete closure removed isolated %q", key)
		}
	}
	if len(store.messages[targetID]) != 0 || len(store.runFeedback["run-target"]) != 0 {
		t.Error("delete closure retained target messages or feedback")
	}
	if len(store.messages[otherID]) != 1 || len(store.runFeedback["run-other"]) != 1 {
		t.Error("delete closure removed isolated messages or feedback")
	}
	if _, exists := store.artifactObjectIDsByURI["file:///target"]; exists {
		t.Error("delete closure retained the target artifact URI index")
	}
	if _, exists := store.artifactObjectIDsByURI["file:///other"]; !exists {
		t.Error("delete closure removed the isolated artifact URI index")
	}
	if _, ok := store.messageReceives["receive-retained"]; !ok {
		t.Error("delete closure removed a receive record")
	}
	if _, ok := store.messageDeliveries["delivery-retained"]; !ok {
		t.Error("delete closure removed a delivery record")
	}
	if _, ok := store.browserAuthRecords["browser-auth-retained"]; !ok {
		t.Error("delete closure removed a browser auth record")
	}
	if _, ok := store.evalRuns["eval-retained"]; !ok {
		t.Error("delete closure removed an eval record")
	}
	for _, row := range store.auditEvents {
		if row.SessionID == targetID {
			t.Errorf("delete closure retained target audit %#v", row)
		}
	}
	for _, row := range store.events {
		if row.SessionID == targetID {
			t.Errorf("delete closure retained target event %#v", row)
		}
	}
}

func sessionDeleteFixtureContains(store *MemoryStore, key string) bool {
	if _, ok := store.sessions[key]; ok {
		return true
	}
	if _, ok := store.runs[key]; ok {
		return true
	}
	if _, ok := store.modelCalls[key]; ok {
		return true
	}
	if _, ok := store.toolCalls[key]; ok {
		return true
	}
	if _, ok := store.documentRecords[key]; ok {
		return true
	}
	if _, ok := store.approvals[key]; ok {
		return true
	}
	if _, ok := store.reminders[key]; ok {
		return true
	}
	if _, ok := store.reminderDelivery[key]; ok {
		return true
	}
	if _, ok := store.memoryCandidates[key]; ok {
		return true
	}
	if _, ok := store.memories[key]; ok {
		return true
	}
	if _, ok := store.browserLoginBlocks[key]; ok {
		return true
	}
	if _, ok := store.artifactObjects[key]; ok {
		return true
	}
	if _, ok := store.episodeSummaries[key]; ok {
		return true
	}
	if _, ok := store.externalChatSessions[key]; ok {
		return true
	}
	_, ok := store.externalChatMessages[key]
	return ok
}
