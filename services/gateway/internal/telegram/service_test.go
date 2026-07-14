package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestServicePersistsUpdateBeforeAdvancingOffset(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(activeTelegramBinding("bind_order", 9, 9))
	runtime := &recordingRuntime{}
	dispatcher := NewDispatcher(st, runtime, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	bot := &fakeBotAPI{}
	bot.getUpdates = func(_ context.Context, offset int64, _ int) ([]Update, error) {
		calls++
		if calls == 1 {
			if offset != 0 {
				t.Fatalf("initial offset = %d", offset)
			}
			return []Update{{UpdateID: 42, Message: telegramTextMessage(7, 9, 9, "hello")}}, nil
		}
		if _, ok := st.FindChannelInboxUpdate(binding.ID, "42"); !ok {
			t.Fatal("offset request happened before durable inbox insert")
		}
		stored, _ := st.GetNotificationBinding(binding.ID)
		if stored.ProviderCursor != "43" || offset != 43 {
			t.Fatalf("offset was not persisted after inbox insert: binding=%#v request=%d", stored, offset)
		}
		cancel()
		return nil, context.Canceled
	}
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, dispatcher).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return bot, nil })
	if err := service.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("poller did not issue confirmation request: calls=%d", calls)
	}
}

func TestServiceDeduplicatesTransportUpdates(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(activeTelegramBinding("bind_dedupe", 9, 9))
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg))
	update := Update{UpdateID: 55, Message: telegramTextMessage(8, 9, 9, "hello")}
	if err := service.persistUpdate(binding, update); err != nil {
		t.Fatal(err)
	}
	first, _ := st.FindChannelInboxUpdate(binding.ID, "55")
	if err := service.persistUpdate(binding, update); err != nil {
		t.Fatal(err)
	}
	second, _ := st.FindChannelInboxUpdate(binding.ID, "55")
	if first.ID != second.ID || len(st.ListChannelInboxUpdates("telegram", "", time.Time{}, 10)) != 1 {
		t.Fatalf("duplicate update created another inbox record: first=%#v second=%#v", first, second)
	}
}

func TestServiceRejectsUnknownUserBeforeDownloadOrAgent(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(activeTelegramBinding("bind_auth", 9, 9))
	runtime := &recordingRuntime{}
	bot := &fakeBotAPI{}
	bot.getFile = func(context.Context, string) (File, error) {
		bot.mu.Lock()
		bot.getFileCalls++
		bot.mu.Unlock()
		return File{}, errors.New("should not be called")
	}
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, runtime, cfg)).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return bot, nil })
	update := Update{UpdateID: 1, Message: telegramTextMessage(1, 10, 10, "unknown")}
	update.Message.Document = &Document{FileID: "file", FileName: "secret.pdf", FileSize: 100}
	inbox := saveInboxFixture(t, st, binding.ID, update)
	service.processInbox(context.Background(), inbox)
	stored, _ := st.GetChannelInboxUpdate(inbox.ID)
	if stored.Status != "completed" || runtime.callCount() != 0 || bot.fileCalls() != 0 {
		t.Fatalf("unknown user crossed isolation boundary: inbox=%#v calls=%d files=%d", stored, runtime.callCount(), bot.fileCalls())
	}
	if sessions := st.ListExternalChatMessages("", 10); len(sessions) != 0 {
		t.Fatalf("unknown user created chat state: %#v", sessions)
	}
}

func TestServiceActivationRequiresMatchingPrivateChallenge(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	challenge := "activation-challenge-value"
	binding := app.NotificationBinding{
		ID:                "bind_activation",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "telegram",
		Provider:          "telegram-bot-api",
		Status:            "waiting_confirm",
		CredentialRef:     "cred",
		ProviderState:     activationHash(challenge),
		DefaultForChannel: false,
	}
	expires := time.Now().UTC().Add(time.Minute)
	binding.ExpiresAt = &expires
	binding = st.SaveNotificationBinding(binding)
	bot := &fakeBotAPI{}
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg)).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return bot, nil })

	invalid := Update{UpdateID: 1, Message: telegramTextMessage(1, 11, 11, "/start wrong")}
	service.processInbox(context.Background(), saveInboxFixture(t, st, binding.ID, invalid))
	stored, _ := st.GetNotificationBinding(binding.ID)
	if stored.Status != "waiting_confirm" || bot.sentCount() != 0 {
		t.Fatalf("invalid challenge activated binding: %#v", stored)
	}

	valid := Update{UpdateID: 2, Message: telegramTextMessage(2, 11, 11, "/start "+challenge)}
	service.processInbox(context.Background(), saveInboxFixture(t, st, binding.ID, valid))
	stored, _ = st.GetNotificationBinding(binding.ID)
	if stored.Status != "active" || stored.ExternalUserID != "11" || stored.ExternalChatID != "11" || stored.ProviderState != "" || stored.ExpiresAt != nil {
		t.Fatalf("valid challenge did not activate binding: %#v", stored)
	}
	if bot.sentCount() != 1 {
		t.Fatalf("activation welcome count = %d", bot.sentCount())
	}
}

func TestServiceOrdersSameChatAndBoundsGlobalWorkers(t *testing.T) {
	cfg := telegramTestConfig(t)
	channel := cfg.Tools.Notifications.Channels["telegram"]
	channel.MaxConcurrency = 2
	channel.MaxPending = 8
	st := store.NewMemoryStore()
	bindingA := st.SaveNotificationBinding(activeTelegramBinding("bind_a", 1, 101))
	bindingB := st.SaveNotificationBinding(activeTelegramBinding("bind_b", 2, 202))
	runtime := newBlockingRuntime()
	bot := &fakeBotAPI{}
	service := NewService(st, channel, nil, NewDispatcher(st, runtime, cfg)).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return bot, nil })
	a1 := saveInboxFixture(t, st, bindingA.ID, Update{UpdateID: 1, Message: telegramTextMessage(1, 1, 101, "a1")})
	a2 := saveInboxFixture(t, st, bindingA.ID, Update{UpdateID: 2, Message: telegramTextMessage(2, 1, 101, "a2")})
	b1 := saveInboxFixture(t, st, bindingB.ID, Update{UpdateID: 3, Message: telegramTextMessage(1, 2, 202, "b1")})
	_ = a2
	service.dispatchPending(context.Background())
	first := <-runtime.started
	second := <-runtime.started
	if first == "a2" || second == "a2" || first == second {
		t.Fatalf("same-chat item started out of order: first=%q second=%q", first, second)
	}
	if runtime.maxActiveCount() != 2 {
		t.Fatalf("global concurrency = %d, want 2", runtime.maxActiveCount())
	}
	runtime.release <- struct{}{}
	runtime.release <- struct{}{}
	waitForInboxStatus(t, st, a1.ID, "completed")
	waitForInboxStatus(t, st, b1.ID, "completed")
	service.dispatchPending(context.Background())
	if third := <-runtime.started; third != "a2" {
		t.Fatalf("same-chat second item = %q, want a2", third)
	}
	runtime.release <- struct{}{}
	waitForInboxStatus(t, st, a2.ID, "completed")
}

func TestServiceRecoversOnlyExpiredProcessingLeases(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg))
	now := time.Now().UTC()
	expired := st.SaveChannelInboxUpdate(app.ChannelInboxUpdate{BindingID: "a", Channel: "telegram", ExternalID: "1", Status: "processing", AvailableAt: now.Add(-time.Second)})
	current := st.SaveChannelInboxUpdate(app.ChannelInboxUpdate{BindingID: "b", Channel: "telegram", ExternalID: "2", Status: "processing", AvailableAt: now.Add(time.Minute)})
	service.recoverExpiredLeases(now)
	expired, _ = st.GetChannelInboxUpdate(expired.ID)
	current, _ = st.GetChannelInboxUpdate(current.ID)
	if expired.Status != "pending" || current.Status != "processing" {
		t.Fatalf("lease recovery mismatch: expired=%#v current=%#v", expired, current)
	}
}

func TestServiceCancelBindingCancelsQueuedAndActiveUpdates(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(activeTelegramBinding("bind_cancel", 1, 1))
	runtime := newBlockingRuntime()
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, runtime, cfg)).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return &fakeBotAPI{}, nil })
	active := saveInboxFixture(t, st, binding.ID, Update{UpdateID: 1, Message: telegramTextMessage(1, 1, 1, "active")})
	queued := saveInboxFixture(t, st, binding.ID, Update{UpdateID: 2, Message: telegramTextMessage(2, 1, 1, "queued")})
	service.dispatchPending(context.Background())
	if started := <-runtime.started; started != "active" {
		t.Fatalf("active message = %q", started)
	}
	service.CancelBinding(binding.ID)
	waitForInboxStatus(t, st, active.ID, "canceled")
	waitForInboxStatus(t, st, queued.ID, "canceled")
	for _, id := range []string{active.ID, queued.ID} {
		inbox, _ := st.GetChannelInboxUpdate(id)
		if len(inbox.Payload) != 0 || inbox.LastError != CodeBindingUnavailable {
			t.Fatalf("canceled inbox retained replayable data: %#v", inbox)
		}
	}
}

type fakeBotAPI struct {
	mu            sync.Mutex
	getUpdates    func(context.Context, int64, int) ([]Update, error)
	getFile       func(context.Context, string) (File, error)
	downloadFile  func(context.Context, string, string, int64) (int64, error)
	sent          []string
	getFileCalls  int
	callbackCalls int
}

func (b *fakeBotAPI) GetMe(context.Context) (User, error) {
	return User{ID: 1, IsBot: true, Username: "bot"}, nil
}
func (b *fakeBotAPI) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	if b.getUpdates != nil {
		return b.getUpdates(ctx, offset, timeout)
	}
	return nil, nil
}
func (b *fakeBotAPI) GetFile(ctx context.Context, fileID string) (File, error) {
	if b.getFile != nil {
		return b.getFile(ctx, fileID)
	}
	return File{}, nil
}
func (b *fakeBotAPI) DownloadFile(ctx context.Context, source, destination string, maxBytes int64) (int64, error) {
	if b.downloadFile != nil {
		return b.downloadFile(ctx, source, destination, maxBytes)
	}
	return 0, nil
}
func (b *fakeBotAPI) SendMessage(_ context.Context, _, _ int64, message string, _ *InlineKeyboardMarkup) (Message, error) {
	b.mu.Lock()
	b.sent = append(b.sent, message)
	b.mu.Unlock()
	return Message{MessageID: int64(len(b.sent))}, nil
}
func (b *fakeBotAPI) SendChatAction(context.Context, int64, int64, string) error { return nil }
func (b *fakeBotAPI) SendPhoto(context.Context, int64, int64, string, string) (Message, error) {
	return Message{MessageID: 1}, nil
}
func (b *fakeBotAPI) SendDocument(context.Context, int64, int64, string, string, string) (Message, error) {
	return Message{MessageID: 1}, nil
}
func (b *fakeBotAPI) SendVoice(context.Context, int64, int64, string, string) (Message, error) {
	return Message{MessageID: 1}, nil
}
func (b *fakeBotAPI) AnswerCallbackQuery(context.Context, string, string) error {
	b.mu.Lock()
	b.callbackCalls++
	b.mu.Unlock()
	return nil
}
func (b *fakeBotAPI) SetMyCommands(context.Context, []BotCommand) error { return nil }
func (b *fakeBotAPI) sentCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sent)
}
func (b *fakeBotAPI) fileCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.getFileCalls
}

func (b *fakeBotAPI) callbacks() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.callbackCalls
}

type recordingRuntime struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingRuntime) HandleMessageWithAttachments(_ context.Context, _, content string, _ []agent.MessageAttachment) (agent.Result, error) {
	return r.record(content), nil
}
func (r *recordingRuntime) HandleMessageWithAttachmentsIdempotent(_ context.Context, _, _, runID, content string, _ []agent.MessageAttachment) (agent.Result, error) {
	result := r.record(content)
	result.Run.ID = runID
	result.Message.RunID = runID
	return result, nil
}
func (r *recordingRuntime) record(content string) agent.Result {
	r.mu.Lock()
	r.calls = append(r.calls, content)
	r.mu.Unlock()
	return agent.Result{Run: app.AgentRun{ID: "run", State: "completed"}, Message: app.Message{Role: "assistant", Content: "reply"}}
}
func (r *recordingRuntime) ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error) {
	return app.ToolCall{}, nil
}
func (r *recordingRuntime) ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error) {
	return agent.Result{}, false, nil
}
func (r *recordingRuntime) CompleteRunIfApprovalsResolved(string) {}
func (r *recordingRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

type blockingRuntime struct {
	mu        sync.Mutex
	active    int
	maxActive int
	started   chan string
	release   chan struct{}
}

func newBlockingRuntime() *blockingRuntime {
	return &blockingRuntime{started: make(chan string, 8), release: make(chan struct{}, 8)}
}
func (r *blockingRuntime) HandleMessageWithAttachments(ctx context.Context, sessionID, content string, attachments []agent.MessageAttachment) (agent.Result, error) {
	return r.HandleMessageWithAttachmentsIdempotent(ctx, sessionID, "", "run_"+content, content, attachments)
}
func (r *blockingRuntime) HandleMessageWithAttachmentsIdempotent(ctx context.Context, _, _, runID, content string, _ []agent.MessageAttachment) (agent.Result, error) {
	r.mu.Lock()
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()
	r.started <- content
	select {
	case <-ctx.Done():
		return agent.Result{}, ctx.Err()
	case <-r.release:
	}
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return agent.Result{Run: app.AgentRun{ID: runID, State: "completed"}, Message: app.Message{RunID: runID, Role: "assistant", Content: "reply " + content}}, nil
}
func (r *blockingRuntime) ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error) {
	return app.ToolCall{}, nil
}
func (r *blockingRuntime) ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error) {
	return agent.Result{}, false, nil
}
func (r *blockingRuntime) CompleteRunIfApprovalsResolved(string) {}
func (r *blockingRuntime) maxActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxActive
}

func telegramTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = t.TempDir()
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	return cfg
}

func activeTelegramBinding(id string, userID, chatID int64) app.NotificationBinding {
	return app.NotificationBinding{
		ID:                id,
		OwnerID:           app.DefaultOwnerID,
		Channel:           "telegram",
		Provider:          "telegram-bot-api",
		Status:            "active",
		ExternalUserID:    stringID(userID),
		ExternalChatID:    stringID(chatID),
		CredentialRef:     "cred_" + id,
		BaseURL:           "https://api.telegram.org",
		ContextToken:      stringID(chatID),
		DefaultForChannel: true,
	}
}

func stringID(value int64) string { return strconv.FormatInt(value, 10) }

func telegramTextMessage(messageID, userID, chatID int64, text string) *Message {
	return &Message{MessageID: messageID, From: &User{ID: userID, FirstName: text}, Chat: Chat{ID: chatID, Type: "private"}, Text: text, Date: time.Now().Unix()}
}

func saveInboxFixture(t *testing.T, st store.Store, bindingID string, update Update) app.ChannelInboxUpdate {
	t.Helper()
	raw, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	chatID, threadID := updateChat(update)
	return st.SaveChannelInboxUpdate(app.ChannelInboxUpdate{BindingID: bindingID, Channel: "telegram", ExternalID: stringID(update.UpdateID), ChatKey: bindingID + ":" + stringID(chatID) + ":" + stringID(threadID), Payload: raw, Status: "pending"})
}

func activationHash(challenge string) string {
	_, hash, _ := newActivationChallengeForTest(challenge)
	return hash
}

func newActivationChallengeForTest(challenge string) (string, string, error) {
	sum := sha256.Sum256([]byte(challenge))
	return challenge, fmt.Sprintf("sha256:%x", sum[:]), nil
}

func waitForInboxStatus(t *testing.T, st store.Store, id, status string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inbox, ok := st.GetChannelInboxUpdate(id); ok && inbox.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	inbox, _ := st.GetChannelInboxUpdate(id)
	t.Fatalf("inbox %s status = %q, want %q", id, inbox.Status, status)
}

func writeDownloadFixture(destination string, raw []byte) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(destination, raw, 0o600); err != nil {
		return 0, err
	}
	return int64(len(raw)), nil
}
