package toolhub

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func sentToolArgs() map[string]any {
	args := validEmailSendArgs()
	for _, key := range []string{"recipient", "subject", "body", "send_script_revision"} {
		delete(args, key)
	}
	args["read_script_revision"] = "1"
	args["sent_match"] = map[string]any{"folder": "sent", "subject": "验收进度汇报 [original-3]", "recipient": "sink@example.test", "interval_start": "2026-10-23T13:00:00Z", "interval_end": "2026-10-23T14:00:00Z"}
	return args
}

func TestEmailReadSentMatchClosedSchemaAndOwner(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "sent", "owner", t.TempDir(), "web", false)
	reader := &fakeEmailReader{}
	hub := New(config.Default(), st).WithEmailReader(reader)
	t.Cleanup(func() { _ = hub.Close() })
	args := sentToolArgs()
	if _, err := hub.Execute(t.Context(), app.ToolEmailRead, args, session.ID, "sent"); err != nil {
		t.Fatal(err)
	}
	if len(reader.requests) != 1 || reader.owner != "owner" || reader.requests[0].SentMatch == nil || reader.requests[0].SentMatch.Subject != args["sent_match"].(map[string]any)["subject"] || reader.requests[0].AccountAddress != "" {
		t.Fatal("typed predicate/owner binding lost")
	}
	for _, mode := range []string{"missing", "extra", "null", "array", "account", "native id", "url", "invalid interval"} {
		t.Run(mode, func(t *testing.T) {
			args := sentToolArgs()
			m := args["sent_match"].(map[string]any)
			switch mode {
			case "missing":
				delete(m, "recipient")
			case "extra":
				m["query"] = "in:anywhere"
			case "null":
				args["sent_match"] = nil
			case "array":
				args["sent_match"] = []any{}
			case "account":
				args["account_address"] = "other@example.test"
			case "native id":
				m["provider_message_id"] = "forged"
			case "url":
				m["url"] = "https://example.test"
			case "invalid interval":
				m["interval_end"] = m["interval_start"]
			}
			if _, err := hub.Execute(t.Context(), app.ToolEmailRead, args, session.ID, "sent"); err == nil {
				t.Fatal("invalid selector reached reader")
			}
		})
	}
	if len(reader.requests) != 1 {
		t.Fatal("invalid selectors dispatched")
	}
}
