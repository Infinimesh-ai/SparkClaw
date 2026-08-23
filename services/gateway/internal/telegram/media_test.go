package telegram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestVoiceUsesNeutralTranscriberAndCleansTemporaryFiles(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	linked := storetest.MustCreateSessionWithScope(t, st, "Telegram", app.DefaultOwnerID, cfg.Workspaces.DefaultRoot, "telegram", true)
	chat := storetest.MustSaveExternalChatSession(t, st, app.ExternalChatSession{BindingID: "bind", Channel: "telegram", ExternalUserID: "1", ExternalChatID: "1", LinkedSessionID: linked.ID, WorkspaceRoot: cfg.Workspaces.DefaultRoot})
	bot := &fakeBotAPI{}
	bot.getFile = func(context.Context, string) (File, error) {
		return File{FilePath: "voice/input.ogg", FileSize: 32}, nil
	}
	bot.downloadFile = func(_ context.Context, _, destination string, _ int64) (int64, error) {
		return writeDownloadFixture(destination, []byte("raw voice"))
	}
	transcriber := &stubVoiceTranscriber{text: "transcribed locally"}
	var tempDir string
	dispatcher := NewDispatcher(st, &recordingRuntime{}, cfg, transcriber).WithClient(bot).WithAudioNormalizer(
		func(_ context.Context, input, output string, _ int) error {
			tempDir = filepath.Dir(input)
			return os.WriteFile(output, pcm16WAVFixture(16000), 0o600)
		},
	)
	_, text, err := dispatcher.messageAttachments(context.Background(), chat, &Message{MessageID: 77, Voice: &Voice{FileID: "voice", Duration: 1, FileSize: 32}})
	if err != nil {
		t.Fatal(err)
	}
	if text != "transcribed locally" || transcriber.callCount() != 1 || transcriber.last.DurationMS != 1000 {
		t.Fatalf("unexpected transcription: text=%q calls=%d request=%#v", text, transcriber.callCount(), transcriber.last)
	}
	if tempDir == "" {
		t.Fatal("normalizer did not receive temporary paths")
	}
	if _, err := os.Stat(tempDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("voice temporary directory was retained: %v", err)
	}
}

func TestVoiceUnavailableStopsBeforeTelegramDownload(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	linked := storetest.MustCreateSessionWithScope(t, st, "Telegram", app.DefaultOwnerID, cfg.Workspaces.DefaultRoot, "telegram", true)
	chat := storetest.MustSaveExternalChatSession(t, st, app.ExternalChatSession{BindingID: "bind", Channel: "telegram", ExternalChatID: "1", LinkedSessionID: linked.ID, WorkspaceRoot: cfg.Workspaces.DefaultRoot})
	bot := &fakeBotAPI{}
	bot.getFile = func(context.Context, string) (File, error) {
		bot.mu.Lock()
		bot.getFileCalls++
		bot.mu.Unlock()
		return File{}, nil
	}
	dispatcher := NewDispatcher(st, &recordingRuntime{}, cfg, DisabledVoiceTranscriber{}).WithClient(bot)
	_, _, err := dispatcher.messageAttachments(context.Background(), chat, &Message{MessageID: 1, Voice: &Voice{FileID: "voice", Duration: 1}})
	if connectorErrorCode(err) != CodeVoiceUnavailable || bot.fileCalls() != 0 {
		t.Fatalf("unavailable voice crossed download boundary: err=%v calls=%d", err, bot.fileCalls())
	}
}

func TestAttachmentLimitsPathCleaningAndExecutableRejection(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	linked := storetest.MustCreateSessionWithScope(t, st, "Telegram", app.DefaultOwnerID, cfg.Workspaces.DefaultRoot, "telegram", true)
	chat := storetest.MustSaveExternalChatSession(t, st, app.ExternalChatSession{BindingID: "bind", Channel: "telegram", ExternalChatID: "1", LinkedSessionID: linked.ID, WorkspaceRoot: cfg.Workspaces.DefaultRoot})
	bot := &fakeBotAPI{}
	bot.getFile = func(context.Context, string) (File, error) {
		return File{FilePath: "docs/report.pdf", FileSize: 10}, nil
	}
	var downloadedPath string
	bot.downloadFile = func(_ context.Context, _, destination string, _ int64) (int64, error) {
		downloadedPath = destination
		return writeDownloadFixture(destination, []byte("%PDF-1.7\nfixture"))
	}
	dispatcher := NewDispatcher(st, &recordingRuntime{}, cfg).WithClient(bot)
	attachments, _, err := dispatcher.messageAttachments(context.Background(), chat, &Message{Document: &Document{FileID: "doc", FileName: "../../report.pdf", MimeType: "application/pdf", FileSize: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || strings.Contains(attachments[0].Name, "..") || !strings.HasPrefix(attachments[0].RelPath, "uploads/telegram/") {
		t.Fatalf("attachment was not confined and cleaned: %#v", attachments)
	}
	if confined, ok := workspacePath(cfg.Workspaces.DefaultRoot, attachments[0].RelPath); !ok || confined != downloadedPath {
		t.Fatalf("download escaped workspace: confined=%q downloaded=%q ok=%v", confined, downloadedPath, ok)
	}

	bot.downloadFile = func(_ context.Context, _, destination string, _ int64) (int64, error) {
		downloadedPath = destination
		return writeDownloadFixture(destination, []byte{'M', 'Z', 0, 0, 0})
	}
	_, _, err = dispatcher.messageAttachments(context.Background(), chat, &Message{Document: &Document{FileID: "doc", FileName: "malware.pdf", MimeType: "application/pdf", FileSize: 5}})
	if connectorErrorCode(err) != CodeAttachmentUnsupported {
		t.Fatalf("executable masquerade was not rejected: %v", err)
	}
	if _, statErr := os.Stat(downloadedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected attachment was not cleaned up: %v", statErr)
	}

	bot.getFileCalls = 0
	_, _, err = dispatcher.messageAttachments(context.Background(), chat, &Message{Document: &Document{FileID: "large", FileName: "large.pdf", FileSize: cfg.Tools.Notifications.Channels["telegram"].MaxDownloadBytes + 1}})
	if connectorErrorCode(err) != CodeAttachmentTooLarge || bot.fileCalls() != 0 {
		t.Fatalf("declared oversize attachment reached Telegram: err=%v calls=%d", err, bot.fileCalls())
	}
}

func TestOutboundResolutionRejectsUploadsAndUnregisteredAbsolutePaths(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	linked := storetest.MustCreateSessionWithScope(t, st, "Telegram", app.DefaultOwnerID, cfg.Workspaces.DefaultRoot, "telegram", true)
	chat := storetest.MustSaveExternalChatSession(t, st, app.ExternalChatSession{BindingID: "bind", Channel: "telegram", ExternalChatID: "1", LinkedSessionID: linked.ID, WorkspaceRoot: cfg.Workspaces.DefaultRoot})
	dispatcher := NewDispatcher(st, &recordingRuntime{}, cfg)
	uploadPath, _ := workspacePath(chat.WorkspaceRoot, "uploads/source.pdf")
	outputPath, _ := workspacePath(chat.WorkspaceRoot, "outputs/result.pdf")
	if _, err := writeDownloadFixture(uploadPath, []byte("upload")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeDownloadFixture(outputPath, []byte("output")); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := dispatcher.resolveWorkspaceOutput(t.Context(), chat, "uploads/source.pdf"); err != nil || ok {
		t.Fatal("uploads path was eligible for outbound delivery")
	}
	if resolved, ok, err := dispatcher.resolveWorkspaceOutput(t.Context(), chat, "outputs/result.pdf"); err != nil || !ok || resolved != outputPath {
		t.Fatalf("workspace output did not resolve: %q ok=%v err=%v", resolved, ok, err)
	}
	if _, ok, err := dispatcher.resolveWorkspaceOutput(t.Context(), chat, outputPath); err != nil || ok {
		t.Fatal("unregistered absolute path was eligible for outbound delivery")
	}
	storetest.MustSaveArtifactObject(t, st, app.ArtifactObject{ID: "artifact", SessionID: linked.ID, Key: "outputs/result.pdf", Path: outputPath})
	if resolved, ok, err := dispatcher.resolveWorkspaceOutput(t.Context(), chat, outputPath); err != nil || !ok || resolved != outputPath {
		t.Fatalf("registered artifact did not resolve: %q ok=%v err=%v", resolved, ok, err)
	}
}

func TestApprovalCallbackExecutesOnce(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := activeTelegramBinding("bind_approval", 1, 1)
	linked := storetest.MustCreateSessionWithScope(t, st, "Telegram", app.DefaultOwnerID, cfg.Workspaces.DefaultRoot, "telegram", true)
	storetest.MustSaveExternalChatSession(t, st, app.ExternalChatSession{BindingID: binding.ID, Channel: "telegram", ExternalUserID: "1", ExternalChatID: "1", LinkedSessionID: linked.ID, WorkspaceRoot: cfg.Workspaces.DefaultRoot, Status: "active"})
	run := app.AgentRun{ID: "run_approval", SessionID: linked.ID, State: "approval_pending"}
	testSaveRun(st, run)
	call := app.ToolCall{ID: "call_approval", SessionID: linked.ID, RunID: run.ID, Status: "approval_pending"}
	testSaveToolCall(st, call)
	approval := app.Approval{ID: "approval_opaque_id", SessionID: linked.ID, RunID: run.ID, ToolCallID: call.ID, Status: "pending", Tool: "file.delete"}
	storetest.MustSaveApproval(t, st, approval)
	runtime := &approvalRuntime{}
	bot := &fakeBotAPI{}
	dispatcher := NewDispatcher(st, runtime, cfg).WithClient(bot)
	update := Update{UpdateID: 1, CallbackQuery: &CallbackQuery{ID: "callback-1", From: User{ID: 1}, Data: "approval:" + approval.ID + ":approve", Message: telegramTextMessage(1, 1, 1, "")}}
	if err := dispatcher.HandleUpdate(context.Background(), binding, update); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.HandleUpdate(context.Background(), binding, update); err != nil {
		t.Fatal(err)
	}
	resolved := storetest.MustListApprovals(t, st, "approved")
	if len(resolved) != 1 || runtime.executeCount() != 1 || bot.callbacks() != 2 {
		t.Fatalf("approval callback was not idempotent: approvals=%#v executes=%d callbacks=%d", resolved, runtime.executeCount(), bot.callbacks())
	}
}

type stubVoiceTranscriber struct {
	mu    sync.Mutex
	text  string
	calls int
	last  VoiceTranscriptionRequest
}

func (s *stubVoiceTranscriber) Available(context.Context) error { return nil }
func (s *stubVoiceTranscriber) Transcribe(_ context.Context, request VoiceTranscriptionRequest) (string, error) {
	s.mu.Lock()
	s.calls++
	s.last = request
	s.mu.Unlock()
	return s.text, nil
}
func (s *stubVoiceTranscriber) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type approvalRuntime struct {
	mu       sync.Mutex
	executes int
}

func (r *approvalRuntime) HandleMessageWithAttachments(context.Context, string, string, []agent.MessageAttachment) (agent.Result, error) {
	return agent.Result{}, nil
}
func (r *approvalRuntime) ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error) {
	r.mu.Lock()
	r.executes++
	r.mu.Unlock()
	return app.ToolCall{}, nil
}
func (r *approvalRuntime) ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error) {
	return agent.Result{}, false, nil
}
func (r *approvalRuntime) CompleteRunIfApprovalsResolved(context.Context, string) error { return nil }
func (r *approvalRuntime) executeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.executes
}
