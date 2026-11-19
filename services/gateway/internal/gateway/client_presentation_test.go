package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
)

// The desktop's local schedule display travels with message.send; the
// backend answers with a client action instead of reading schedules.
func TestLinkDesktopSchedulePresentation(t *testing.T) {
	b := newLinkTestBackend(t)
	desk, _, deskRec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, contract.Conditions{Attachments: true, Screen: contract.ScreenLarge})
	phone, _, phoneRec, _ := newLinkTestDevice(t, b, contract.ClassPhone, contract.Conditions{Attachments: true, Screen: contract.ScreenSmall})
	writeLinkDirectory(t, b.stateDir, deskRec, phoneRec)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, c := range []*linkclient.Client{desk, phone} {
		c.Start(ctx)
		defer c.Close()
		if err := c.WaitReady(ctx); err != nil {
			t.Fatal(err)
		}
	}
	presentation := &contract.ClientPresentation{SchemaVersion: 1, ScheduleShow: true, Timezone: "Asia/Shanghai", RequestedAt: "2026-10-29T18:00:00Z", LocalConversationID: "22222222-2222-4222-8222-222222222222"}
	send := func(c *linkclient.Client, key string) contract.RunStatus {
		var header contract.SessionHeader
		if err := c.Request(ctx, contract.OpSessionCreate, contract.SessionCreateParams{Title: "日程", ClientRequestID: "create-" + key}, &header); err != nil {
			t.Fatal(err)
		}
		var sent contract.MessageSendResult
		if err := c.Request(ctx, contract.OpMessageSend, contract.MessageSendParams{SessionID: header.ID, ClientMessageID: key, Content: "显示明天的日程", ClientTimezone: "Asia/Shanghai", ClientPresentation: presentation}, &sent); err != nil {
			t.Fatal(err)
		}
		var status contract.RunStatus
		waitUntil(t, 30*time.Second, func() bool {
			err := c.Request(ctx, contract.OpRunStatus, contract.RunRef{SessionID: header.ID, RunID: sent.RunID}, &status)
			return err == nil && status.State != contract.RunRunning
		})
		return status
	}
	status := send(desk, "desk-schedule-1")
	if status.FinalMessage == nil || !strings.Contains(status.FinalMessage.Content, "已请求") {
		t.Fatalf("status %+v", status)
	}
	var extra struct {
		ClientAction *app.ClientAction `json:"client_action"`
	}
	if err := json.Unmarshal(status.FinalMessage.Extra, &extra); err != nil || extra.ClientAction == nil {
		t.Fatalf("extra %s %v", status.FinalMessage.Extra, err)
	}
	action := extra.ClientAction
	if action.Type != app.ClientActionScheduleShow || action.RequestID != "desk-schedule-1" || action.ConversationID != presentation.LocalConversationID || action.DateStart != "2026-10-31" || action.DateEnd != "2026-11-01" {
		t.Fatalf("action %+v", action)
	}
	// Only a desktop can display its local schedules.
	if status := send(phone, "phone-schedule-1"); status.FinalMessage != nil && strings.Contains(string(status.FinalMessage.Extra), "client_action") {
		t.Fatalf("phone got a desktop action %+v", status)
	}
}
