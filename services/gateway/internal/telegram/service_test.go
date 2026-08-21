package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func telegramTestRuntimeScope() connectorruntime.RuntimeScope {
	return connectorruntime.RuntimeScope{
		Channel:      "telegram",
		OwnerEnabled: func(string) bool { return true },
	}
}

func TestServicePersistsUpdateBeforeAdvancingOffset(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := storetest.MustCreateNotificationBinding(t, st, activeTelegramBinding("bind_order", 9, 9))
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
		if _, ok, err := st.FindChannelInboxUpdate(t.Context(), binding.ID, "42"); err != nil || !ok {
			t.Fatalf("offset request happened before durable inbox insert: %v", err)
		}
		stored, _ := storetest.MustGetNotificationBinding(t, st, binding.ID)
		if stored.ProviderCursor != "43" || offset != 43 {
			t.Fatalf("offset was not persisted after inbox insert: binding=%#v request=%d", stored, offset)
		}
		cancel()
		return nil, context.Canceled
	}
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, dispatcher).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return bot, nil })
	if err := service.Run(ctx, telegramTestRuntimeScope()); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("poller did not issue confirmation request: calls=%d", calls)
	}
}

func TestServicePollsEveryTelegramBinding(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	bindingA := storetest.MustCreateNotificationBinding(t, st, activeTelegramBinding("bind_poll_a", 1, 101))
	bindingB := storetest.MustCreateNotificationBinding(t, st, activeTelegramBinding("bind_poll_b", 2, 202))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	polled := map[string]int{}
	newBot := func(bindingID string) *fakeBotAPI {
		bot := &fakeBotAPI{}
		bot.getUpdates = func(context.Context, int64, int) ([]Update, error) {
			polled[bindingID]++
			if polled[bindingA.ID] > 0 && polled[bindingB.ID] > 0 {
				cancel()
				return nil, context.Canceled
			}
			return nil, nil
		}
		return bot
	}
	bots := map[string]*fakeBotAPI{
		bindingA.ID: newBot(bindingA.ID),
		bindingB.ID: newBot(bindingB.ID),
	}
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg)).
		WithClientFactory(func(_ context.Context, binding app.NotificationBinding) (BotAPI, error) {
			return bots[binding.ID], nil
		})

	if err := service.Run(ctx, telegramTestRuntimeScope()); err != nil {
		t.Fatal(err)
	}
	if polled[bindingA.ID] == 0 || polled[bindingB.ID] == 0 {
		t.Fatalf("not every Telegram binding was polled: %#v", polled)
	}
}

func TestServiceDeduplicatesTransportUpdates(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := storetest.MustCreateNotificationBinding(t, st, activeTelegramBinding("bind_dedupe", 9, 9))
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg))
	update := Update{UpdateID: 55, Message: telegramTextMessage(8, 9, 9, "hello")}
	if err := service.persistUpdate(t.Context(), binding, update); err != nil {
		t.Fatal(err)
	}
	first, ok, err := st.FindChannelInboxUpdate(t.Context(), binding.ID, "55")
	if err != nil || !ok {
		t.Fatalf("first update lookup failed: ok=%v err=%v", ok, err)
	}
	if err := service.persistUpdate(t.Context(), binding, update); err != nil {
		t.Fatal(err)
	}
	second, ok, err := st.FindChannelInboxUpdate(t.Context(), binding.ID, "55")
	if err != nil || !ok {
		t.Fatalf("second update lookup failed: ok=%v err=%v", ok, err)
	}
	updates, err := st.ListChannelInboxUpdates(t.Context(), "telegram", "", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(updates) != 1 {
		t.Fatalf("duplicate update created another inbox record: first=%#v second=%#v", first, second)
	}
}

func TestServiceRejectsUnknownUserBeforeDownloadOrAgent(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := storetest.MustCreateNotificationBinding(t, st, activeTelegramBinding("bind_auth", 9, 9))
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
	stored, _, err := st.GetChannelInboxUpdate(t.Context(), inbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "completed" || runtime.callCount() != 0 || bot.fileCalls() != 0 {
		t.Fatalf("unknown user crossed isolation boundary: inbox=%#v calls=%d files=%d", stored, runtime.callCount(), bot.fileCalls())
	}
	if sessions := storetest.MustListExternalChatMessages(t, st, "", 10); len(sessions) != 0 {
		t.Fatalf("unknown user created chat state: %#v", sessions)
	}
}

func TestServiceClaimsFirstFreshPrivateMessage(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	createdAt := time.Now().UTC()
	binding := app.NotificationBinding{
		ID:                "bind_activation",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "telegram",
		Provider:          "telegram-bot-api",
		Status:            "active",
		CredentialRef:     "cred",
		DefaultForChannel: false,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
	binding = storetest.MustCreateNotificationBinding(t, st, binding)
	bot := &fakeBotAPI{}
	runtime := &recordingRuntime{}
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, runtime, cfg)).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return bot, nil })

	stale := Update{UpdateID: 1, Message: telegramTextMessage(1, 10, 10, "old message")}
	stale.Message.Date = createdAt.Add(-2 * time.Minute).Unix()
	service.processInbox(context.Background(), saveInboxFixture(t, st, binding.ID, stale))
	group := Update{UpdateID: 2, Message: telegramTextMessage(2, 20, -20, "/start")}
	group.Message.Chat.Type = "group"
	service.processInbox(context.Background(), saveInboxFixture(t, st, binding.ID, group))
	stored, _ := storetest.MustGetNotificationBinding(t, st, binding.ID)
	if stored.ExternalUserID != "" || stored.ExternalChatID != "" || bot.sentCount() != 0 || runtime.callCount() != 0 {
		t.Fatalf("stale or non-private message claimed binding: binding=%#v replies=%d calls=%d", stored, bot.sentCount(), runtime.callCount())
	}

	fresh := Update{UpdateID: 3, Message: telegramTextMessage(3, 11, 11, "/start")}
	service.processInbox(context.Background(), saveInboxFixture(t, st, binding.ID, fresh))
	stored, _ = storetest.MustGetNotificationBinding(t, st, binding.ID)
	if stored.Status != "active" || stored.ExternalUserID != "11" || stored.ExternalChatID != "11" || stored.ContextToken != "11" || !stored.DefaultForChannel {
		t.Fatalf("fresh private message did not claim binding: %#v", stored)
	}
	if bot.sentCount() != 1 || runtime.callCount() != 0 || !strings.Contains(bot.sentMessages()[0], "connected") {
		t.Fatalf("plain /start should only acknowledge the claim: replies=%#v calls=%d", bot.sentMessages(), runtime.callCount())
	}
	unknown := Update{UpdateID: 4, Message: telegramTextMessage(4, 12, 12, "hello")}
	service.processInbox(context.Background(), saveInboxFixture(t, st, binding.ID, unknown))
	if bot.sentCount() != 1 || runtime.callCount() != 0 {
		t.Fatalf("another chat crossed claimed binding: replies=%d calls=%d", bot.sentCount(), runtime.callCount())
	}
	if !hasAuditType(mustTelegramListAudit(t, st, ""), "telegram.binding.claimed") {
		t.Fatalf("binding claim was not audited: %#v", mustTelegramListAudit(t, st, ""))
	}
}

func TestServiceDispatchesFirstOrdinaryMessageAfterClaim(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:            "bind_first_message",
		OwnerID:       app.DefaultOwnerID,
		Channel:       "telegram",
		Provider:      "telegram-bot-api",
		Status:        "active",
		CredentialRef: "cred",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	runtime := &recordingRuntime{}
	bot := &fakeBotAPI{}
	deliverer := &recordingWorkflowResultDeliverer{}
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, runtime, cfg).WithResultDeliverer(deliverer)).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return bot, nil })

	update := Update{UpdateID: 1, Message: telegramTextMessage(1, 42, 42, "hello from phone")}
	service.processInbox(context.Background(), saveInboxFixture(t, st, binding.ID, update))
	stored, _ := storetest.MustGetNotificationBinding(t, st, binding.ID)
	if stored.ExternalUserID != "42" || stored.ExternalChatID != "42" || runtime.callCount() != 1 || deliverer.callCount() != 1 || bot.sentCount() != 0 {
		t.Fatalf("first ordinary message did not use exactly one result delivery: binding=%#v calls=%d deliveries=%d direct_replies=%d", stored, runtime.callCount(), deliverer.callCount(), bot.sentCount())
	}
	ingress := runtime.lastIngress()
	if ingress.Source.Kind != app.MessageSourceThirdPartyDevice || ingress.Source.EndpointID == "" || strings.HasPrefix(string(ingress.Source.EndpointID), "session:") || ingress.Source.NativeMessageID == "" || ingress.OwnerID != app.DefaultOwnerID || ingress.ReturnRoute.SourceEndpointID != ingress.Source.EndpointID {
		t.Fatalf("Telegram ingress did not preserve the external endpoint identity: %#v", ingress)
	}
}

func TestServiceClaimBindingAllowsOnlyOneConcurrentChat(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:            "bind_race",
		OwnerID:       app.DefaultOwnerID,
		Channel:       "telegram",
		Provider:      "telegram-bot-api",
		Status:        "active",
		CredentialRef: "cred",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg))
	updates := []Update{
		{UpdateID: 1, Message: telegramTextMessage(1, 101, 1001, "/start")},
		{UpdateID: 2, Message: telegramTextMessage(2, 202, 2002, "/start")},
	}
	claimed := make(chan bool, len(updates))
	ctx := t.Context()
	var wg sync.WaitGroup
	for _, update := range updates {
		update := update
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, authorized, claimedNow, err := service.claimBinding(ctx, binding.ID, update)
			claimed <- err == nil && authorized && claimedNow
		}()
	}
	wg.Wait()
	close(claimed)
	winners := 0
	for won := range claimed {
		if won {
			winners++
		}
	}
	stored, _ := storetest.MustGetNotificationBinding(t, st, binding.ID)
	if winners != 1 || (stored.ExternalUserID != "101" && stored.ExternalUserID != "202") {
		t.Fatalf("concurrent claim winners=%d binding=%#v", winners, stored)
	}
}

func TestServiceCursorUpdatePreservesConcurrentClaim(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	staleSnapshot := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:            "bind_cursor_claim",
		OwnerID:       app.DefaultOwnerID,
		Channel:       "telegram",
		Provider:      "telegram-bot-api",
		Status:        "active",
		CredentialRef: "cred",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg))
	_, authorized, claimed, err := service.claimBinding(t.Context(), staleSnapshot.ID, Update{
		UpdateID: 1,
		Message:  telegramTextMessage(1, 77, 77, "/start"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !authorized || !claimed {
		t.Fatal("fixture claim failed")
	}
	service.saveBindingCursor(t.Context(), staleSnapshot, 2)
	stored, _ := storetest.MustGetNotificationBinding(t, st, staleSnapshot.ID)
	if stored.ProviderCursor != "2" || stored.ExternalUserID != "77" || stored.ExternalChatID != "77" {
		t.Fatalf("cursor update overwrote recipient claim: %#v", stored)
	}
}

func TestServiceClaimsMultipleBotsForDifferentUsers(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	newBinding := func(id, credentialRef string) app.NotificationBinding {
		return storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
			ID:            id,
			OwnerID:       app.DefaultOwnerID,
			Channel:       "telegram",
			Provider:      "telegram-bot-api",
			Status:        "active",
			CredentialRef: credentialRef,
			CreatedAt:     time.Now().UTC(),
		})
	}
	bindingA := newBinding("bind_user_a", "cred_bot_a")
	bindingB := newBinding("bind_user_b", "cred_bot_b")
	bots := map[string]*fakeBotAPI{
		bindingA.ID: {},
		bindingB.ID: {},
	}
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg)).
		WithClientFactory(func(_ context.Context, binding app.NotificationBinding) (BotAPI, error) {
			return bots[binding.ID], nil
		})

	service.processInbox(context.Background(), saveInboxFixture(t, st, bindingA.ID, Update{
		UpdateID: 1,
		Message:  telegramTextMessage(1, 101, 1001, "/start"),
	}))
	service.processInbox(context.Background(), saveInboxFixture(t, st, bindingB.ID, Update{
		UpdateID: 2,
		Message:  telegramTextMessage(2, 202, 2002, "/start"),
	}))

	activatedA, _ := storetest.MustGetNotificationBinding(t, st, bindingA.ID)
	activatedB, _ := storetest.MustGetNotificationBinding(t, st, bindingB.ID)
	if activatedA.Status != "active" || activatedA.ExternalUserID != "101" || activatedA.ExternalChatID != "1001" || activatedA.CredentialRef != "cred_bot_a" {
		t.Fatalf("first Telegram user activation mismatch: %#v", activatedA)
	}
	if activatedB.Status != "active" || activatedB.ExternalUserID != "202" || activatedB.ExternalChatID != "2002" || activatedB.CredentialRef != "cred_bot_b" {
		t.Fatalf("second Telegram user activation mismatch: %#v", activatedB)
	}
	if bots[bindingA.ID].sentCount() != 1 || bots[bindingB.ID].sentCount() != 1 {
		t.Fatalf("activation acknowledgements crossed bots: first=%d second=%d", bots[bindingA.ID].sentCount(), bots[bindingB.ID].sentCount())
	}
}

func TestServiceOrdersSameChatAndBoundsGlobalWorkers(t *testing.T) {
	cfg := telegramTestConfig(t)
	channel := cfg.Tools.Notifications.Channels["telegram"]
	channel.MaxConcurrency = 2
	channel.MaxPending = 8
	st := store.NewMemoryStore()
	bindingA := storetest.MustCreateNotificationBinding(t, st, activeTelegramBinding("bind_a", 1, 101))
	bindingB := storetest.MustCreateNotificationBinding(t, st, activeTelegramBinding("bind_b", 2, 202))
	runtime := newBlockingRuntime()
	bot := &fakeBotAPI{}
	service := NewService(st, channel, nil, NewDispatcher(st, runtime, cfg).WithResultDeliverer(&recordingWorkflowResultDeliverer{})).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return bot, nil })
	a1 := saveInboxFixture(t, st, bindingA.ID, Update{UpdateID: 1, Message: telegramTextMessage(1, 1, 101, "a1")})
	a2 := saveInboxFixture(t, st, bindingA.ID, Update{UpdateID: 2, Message: telegramTextMessage(2, 1, 101, "a2")})
	b1 := saveInboxFixture(t, st, bindingB.ID, Update{UpdateID: 3, Message: telegramTextMessage(1, 2, 202, "b1")})
	_ = a2
	service.dispatchPending(context.Background(), telegramTestRuntimeScope())
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
	service.dispatchPending(context.Background(), telegramTestRuntimeScope())
	if third := <-runtime.started; third != "a2" {
		t.Fatalf("same-chat second item = %q, want a2", third)
	}
	runtime.release <- struct{}{}
	waitForInboxStatus(t, st, a2.ID, "completed")
}

func TestServiceOwnerGateDrainsDispatchedWorkAndSuspendsPendingInbox(t *testing.T) {
	cfg := telegramTestConfig(t)
	channel := cfg.Tools.Notifications.Channels["telegram"]
	channel.MaxConcurrency = 1
	channel.MaxPending = 8
	st := store.NewMemoryStore()
	binding := activeTelegramBinding("bind_owner_gate", 1, 101)
	binding.OwnerID = "owner-a"
	binding = storetest.MustCreateNotificationBinding(t, st, binding)
	runtime := newBlockingRuntime()
	service := NewService(st, channel, nil, NewDispatcher(st, runtime, cfg).WithResultDeliverer(&recordingWorkflowResultDeliverer{})).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return &fakeBotAPI{}, nil })
	first := saveInboxFixture(t, st, binding.ID, Update{UpdateID: 1, Message: telegramTextMessage(1, 1, 101, "first")})
	second := saveInboxFixture(t, st, binding.ID, Update{UpdateID: 2, Message: telegramTextMessage(2, 1, 101, "second")})
	var enabled atomic.Bool
	enabled.Store(true)
	scope := connectorruntime.RuntimeScope{
		Channel:      "telegram",
		OwnerEnabled: func(ownerID string) bool { return ownerID == "owner-a" && enabled.Load() },
	}
	bindings, err := service.pollingBindings(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].ID != binding.ID {
		t.Fatalf("enabled owner polling bindings = %#v", bindings)
	}
	service.dispatchPending(context.Background(), scope)
	if started := <-runtime.started; started != "first" {
		t.Fatalf("first dispatched item = %q", started)
	}
	enabled.Store(false)
	bindings, err = service.pollingBindings(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("disabled owner remained pollable: %#v", bindings)
	}
	runtime.release <- struct{}{}
	waitForInboxStatus(t, st, first.ID, "completed")
	service.dispatchPending(context.Background(), scope)
	if pending, _, err := st.GetChannelInboxUpdate(t.Context(), second.ID); err != nil || pending.Status != "pending" {
		t.Fatalf("disabled owner's queued inbox was not suspended: %#v err=%v", pending, err)
	}
	select {
	case started := <-runtime.started:
		t.Fatalf("disabled owner's pending inbox dispatched as %q", started)
	case <-time.After(50 * time.Millisecond):
	}
	enabled.Store(true)
	service.dispatchPending(context.Background(), scope)
	if started := <-runtime.started; started != "second" {
		t.Fatalf("resumed inbox item = %q, want second", started)
	}
	runtime.release <- struct{}{}
	waitForInboxStatus(t, st, second.ID, "completed")
}

func TestServiceRecoversOnlyExpiredProcessingLeases(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, &recordingRuntime{}, cfg))
	now := time.Now().UTC()
	expired, err := st.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{BindingID: "a", Channel: "telegram", ExternalID: "1", Status: "processing", AvailableAt: now.Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	current, err := st.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{BindingID: "b", Channel: "telegram", ExternalID: "2", Status: "processing", AvailableAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.recoverExpiredLeases(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	expired, _, err = st.GetChannelInboxUpdate(t.Context(), expired.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err = st.GetChannelInboxUpdate(t.Context(), current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Status != "pending" || current.Status != "processing" {
		t.Fatalf("lease recovery mismatch: expired=%#v current=%#v", expired, current)
	}
}

func TestServiceCancelBindingCancelsQueuedAndActiveUpdates(t *testing.T) {
	cfg := telegramTestConfig(t)
	st := store.NewMemoryStore()
	binding := storetest.MustCreateNotificationBinding(t, st, activeTelegramBinding("bind_cancel", 1, 1))
	runtime := newBlockingRuntime()
	service := NewService(st, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(st, runtime, cfg)).
		WithClientFactory(func(context.Context, app.NotificationBinding) (BotAPI, error) { return &fakeBotAPI{}, nil })
	active := saveInboxFixture(t, st, binding.ID, Update{UpdateID: 1, Message: telegramTextMessage(1, 1, 1, "active")})
	queued := saveInboxFixture(t, st, binding.ID, Update{UpdateID: 2, Message: telegramTextMessage(2, 1, 1, "queued")})
	service.dispatchPending(context.Background(), telegramTestRuntimeScope())
	if started := <-runtime.started; started != "active" {
		t.Fatalf("active message = %q", started)
	}
	service.CancelBinding(binding.ID)
	waitForInboxStatus(t, st, active.ID, "canceled")
	waitForInboxStatus(t, st, queued.ID, "canceled")
	for _, id := range []string{active.ID, queued.ID} {
		inbox, _, err := st.GetChannelInboxUpdate(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if len(inbox.Payload) != 0 || inbox.LastError != CodeBindingUnavailable {
			t.Fatalf("canceled inbox retained replayable data: %#v", inbox)
		}
	}
}

type failingInboxListStore struct {
	store.Store
	err error
}

func (s failingInboxListStore) ListChannelInboxUpdates(context.Context, string, string, time.Time, int) ([]app.ChannelInboxUpdate, error) {
	return nil, s.err
}

func TestServiceCancelBindingStopsActiveUpdateWhenInboxStoreFails(t *testing.T) {
	cfg := telegramTestConfig(t)
	base := store.NewMemoryStore()
	service := NewService(failingInboxListStore{Store: base, err: errors.New("inbox unavailable")}, cfg.Tools.Notifications.Channels["telegram"], nil, NewDispatcher(base, &recordingRuntime{}, cfg))
	activeCtx, activeCancel := context.WithCancel(t.Context())
	service.registerActive("binding-failure", "inbox-active", activeCancel)
	service.CancelBinding("binding-failure")
	select {
	case <-activeCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("active Telegram update was not canceled after inbox Store failure")
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

type recordingWorkflowResultDeliverer struct {
	mu      sync.Mutex
	results []app.WorkflowResult
}

func (d *recordingWorkflowResultDeliverer) DeliverWorkflowResult(_ context.Context, result app.WorkflowResult) (app.DeliveryReceipt, error) {
	d.mu.Lock()
	d.results = append(d.results, result)
	d.mu.Unlock()
	now := time.Now().UTC()
	return app.DeliveryReceipt{DeliveryID: app.DeliveryID("test_delivery"), EndpointID: result.ReturnRoute.SourceEndpointID, Status: app.DeliverySucceeded, AttemptedAt: now, DeliveredAt: &now}, nil
}

func (d *recordingWorkflowResultDeliverer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.results)
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
	messageID := int64(len(b.sent))
	b.mu.Unlock()
	return Message{MessageID: messageID}, nil
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
func (b *fakeBotAPI) sentMessages() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sent...)
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
	mu        sync.Mutex
	calls     []string
	ingresses []app.MessageIngressContext
}

func (r *recordingRuntime) HandleMessageWithIngress(_ context.Context, _, _, runID, content string, _ []agent.MessageAttachment, ingress app.MessageIngressContext) (agent.Result, error) {
	r.mu.Lock()
	r.ingresses = append(r.ingresses, ingress)
	r.mu.Unlock()
	result := r.record(content)
	result.Run.ID = runID
	result.Message.RunID = runID
	return result, nil
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
func (r *recordingRuntime) CompleteRunIfApprovalsResolved(context.Context, string) error { return nil }
func (r *recordingRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}
func (r *recordingRuntime) lastIngress() app.MessageIngressContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ingresses) == 0 {
		return app.MessageIngressContext{}
	}
	return r.ingresses[len(r.ingresses)-1]
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
func (r *blockingRuntime) CompleteRunIfApprovalsResolved(context.Context, string) error { return nil }
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
	channel := cfg.Tools.Notifications.Channels["telegram"]
	channel.Enabled = true
	cfg.Tools.Notifications.Channels["telegram"] = channel
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
	stored, err := st.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{BindingID: bindingID, Channel: "telegram", ExternalID: stringID(update.UpdateID), ChatKey: bindingID + ":" + stringID(chatID) + ":" + stringID(threadID), Payload: raw, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func hasAuditType(events []app.AuditEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func waitForInboxStatus(t *testing.T, st store.Store, id, status string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inbox, ok, err := st.GetChannelInboxUpdate(t.Context(), id); err == nil && ok && inbox.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	inbox, _, err := st.GetChannelInboxUpdate(t.Context(), id)
	t.Fatalf("inbox %s status = %q, want %q (err=%v)", id, inbox.Status, status, err)
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
