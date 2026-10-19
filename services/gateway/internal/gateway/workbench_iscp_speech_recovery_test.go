package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
)

func TestISCPSpeechTerminalSurvivesRestartWithoutProviderReplay(t *testing.T) {
	server, _, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	server.cfg.Speech.Enabled, server.cfg.Speech.Backend = true, "openai-http"
	realtime := newFakeGatewayRealtimeSession()
	starts := 0
	server.speech = &fakeSpeechTranscriber{status: speech.Status{Enabled: true, Ready: true, SupportsStreaming: true}, startRealtime: func(context.Context, speech.RealtimeRequest) (speech.RealtimeSession, error) {
		starts++
		return realtime, nil
	}}
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	opened := handler(ctx, domainTestRequest("speech.session.open", []byte(`{"session_id":"`+iscpTestInstallation+`","request_id":"durable-final","language":"auto"}`)))
	var start struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(opened.Body, &start)
	if opened.Status != 200 || start.SessionID == "" {
		t.Fatalf("open %+v", opened)
	}
	frameBody, _ := json.Marshal(map[string]any{"session_id": start.SessionID, "sequence": 1, "pcm16": base64.StdEncoding.EncodeToString(make([]byte, 3200))})
	if got := handler(ctx, domainTestRequest("speech.session.frame", frameBody)); got.Status != 200 {
		t.Fatal(got)
	}
	finishBody, _ := json.Marshal(map[string]any{"session_id": start.SessionID, "last_sequence": 1, "total_samples": 1600, "reason": "manual_stop"})
	finish := domainTestRequest("speech.session.finish", finishBody)
	if got := handler(ctx, finish); got.Status != 200 {
		t.Fatal(got)
	}
	pollBody, _ := json.Marshal(map[string]any{"session_id": start.SessionID, "after": 0})
	poll := domainTestRequest("speech.session.events", pollBody)
	var final []byte
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := handler(ctx, poll)
		if got.Status == 200 && bytes.Contains(got.Body, []byte("live final")) {
			final = got.Body
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(final) == 0 {
		t.Fatal("final missing")
	}
	// A brand new adapter has no in-memory stream. It must answer only from the
	// original encrypted checkpoint, even after cancellation and a lost reply.
	cancelBody, _ := json.Marshal(map[string]string{"session_id": start.SessionID})
	if got := handler(ctx, domainTestRequest("speech.session.cancel", cancelBody)); got.Status != 200 {
		t.Fatal(got)
	}
	restarted, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Reading a completed transcript does not require a live provider backend.
	server.speech = nil
	recovered := restarted(ctx, poll)
	if recovered.Status != 200 || !bytes.Equal(recovered.Body, final) {
		t.Fatalf("lost terminal: %s / %s", final, recovered.Body)
	}
	if got := restarted(ctx, finish); got.Status != 200 || starts != 1 {
		t.Fatalf("finish repeated provider %+v starts=%d", got, starts)
	}
	// A different installation cannot read the old data via the same session ID.
	poll.InstallationID = "other-installation"
	if got := restarted(ctx, poll); got.Status == 200 {
		t.Fatal("cross-installation terminal disclosure")
	}
	entries, err := os.ReadDir(filepath.Join(server.executionRoot, "iscp-domain-receipts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(server.executionRoot, "iscp-domain-receipts", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("live final")) {
			t.Fatal("plaintext transcript")
		}
	}
}

func TestISCPSpeechIncompleteHistoryAndStorageFailureNeverDiscloseTerminal(t *testing.T) {
	root := t.TempDir()
	receipts, err := newISCPDomainReceipts(root)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &iscpDomainAdapter{receipts: receipts}
	if err := adapter.prepareSpeechCheckpoint("original:authorization:1", "stream"); err != nil {
		t.Fatal(err)
	}
	if got := adapter.recoveredSpeechEvents("original:authorization:1", "stream", 0); got.status != 409 {
		t.Fatalf("incomplete is not unknown: %+v", got)
	}
	session := &iscpSpeechSession{id: "stream", scope: "original:authorization:1", sequence: 1, closed: true, storageReserved: true, events: []iscpSpeechEvent{{1, speech.RealtimeEvent{Event: "final", Revision: 1, Text: "secret final"}}}}
	oldRoot := receipts.root
	receipts.root = filepath.Join(root, "missing")
	if err := adapter.persistSpeechTerminal(session); err == nil || session.terminalStored {
		t.Fatal("storage failure published terminal")
	}
	receipts.root = oldRoot
	if got := adapter.recoveredSpeechEvents(session.scope, session.id, 0); got.status != 409 {
		t.Fatal("failed save disclosed final")
	}
	if err := adapter.persistSpeechTerminal(session); err != nil {
		t.Fatal(err)
	}
	if receipts.reserved != 0 {
		t.Fatal("terminal reservation leaked")
	}
	if got := adapter.recoveredSpeechEvents("original:authorization:2", "stream", 0); got.status != 404 {
		t.Fatal("authority change disclosed final")
	}
	if got := adapter.recoveredSpeechEvents(session.scope, session.id, 2); got.status != 409 {
		t.Fatal("future cursor accepted")
	}
	got := adapter.recoveredSpeechEvents(session.scope, session.id, 0)
	if got.status != 200 || !bytes.Contains(got.body, []byte("secret final")) {
		t.Fatal("original terminal missing")
	}
	// The retained snapshot may start after acknowledged partial events. An old
	// cursor is a gap, never evidence that the original final does not exist.
	if err := adapter.prepareSpeechCheckpoint(session.scope, "after-ack"); err != nil {
		t.Fatal(err)
	}
	acknowledged := &iscpSpeechSession{id: "after-ack", scope: session.scope, ack: 4, sequence: 5, closed: true, storageReserved: true, events: []iscpSpeechEvent{{5, speech.RealtimeEvent{Event: "final", Revision: 4, Text: "retained final"}}}}
	if err := adapter.persistSpeechTerminal(acknowledged); err != nil {
		t.Fatal(err)
	}
	if got := adapter.recoveredSpeechEvents(session.scope, "after-ack", 0); got.status != 409 || !bytes.Contains(got.body, []byte("cursor_gap")) {
		t.Fatal("old cursor was treated as missing final")
	}
	if got := adapter.recoveredSpeechEvents(session.scope, "after-ack", 4); got.status != 200 || !bytes.Contains(got.body, []byte("retained final")) {
		t.Fatal("old cursor lost terminal")
	}
}
