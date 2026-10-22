package browsercontrol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMailObserverTargetsAreBoundedEvidenceOnArrivalHints(t *testing.T) {
	received := time.Now().UTC()
	valid := []MailObserverTarget{{ProviderMessageID: "ZC0001-abc"}, {ProviderItemID: "AQMk/item+=", ReceivedAt: &received}}
	if !validMailObserverTargets("mailbox_changed", valid) || !validMailObserverTargets("watch_state", nil) {
		t.Fatal("valid candidates rejected")
	}
	many := make([]MailObserverTarget, maxMailObserverTargets+1)
	for i := range many {
		many[i] = MailObserverTarget{ProviderMessageID: "id"}
	}
	zero := time.Time{}
	for name, targets := range map[string][]MailObserverTarget{
		"empty identity": {{}},
		"spaces":         {{ProviderMessageID: "a b"}},
		"too long":       {{ProviderItemID: strings.Repeat("x", 513)}},
		"zero time":      {{ProviderItemID: "x", ReceivedAt: &zero}},
		"too many":       many,
	} {
		if validMailObserverTargets("mailbox_changed", targets) {
			t.Fatalf("%s accepted", name)
		}
	}
	if validMailObserverTargets("watch_state", valid) {
		t.Fatal("candidates are only meaningful on arrival hints")
	}
}

// The Controller feed (tools/browser-controller/src/mail-observer-feed.mjs)
// appends `targets` to mailbox_changed events; this Gateway decodes Controller
// responses with DisallowUnknownFields, so the field must stay in the struct.
func TestControllerFeedEventWithTargetsStrictlyDecodes(t *testing.T) {
	raw := `{"schema_version":1,"epoch":"e","events":[{"epoch":"e","sequence":1,"provider":"qq_mail","owner_scope":"` + strings.Repeat("a", 64) +
		`","mailbox_id":"box","binding_generation":2,"credential_generation":7,"watch_epoch":"w","kind":"mailbox_changed","reason":"qq_inbound_envelope","state":"watching","observed_at":"2026-10-22T08:00:00.000Z","targets":[{"provider_message_id":"ZC0001"},{"provider_item_id":"item","received_at":"2026-09-24T09:00:00.123456789+08:00"}]}]}`
	var batch MailObserverBatch
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&batch); err != nil {
		t.Fatal(err)
	}
	event := batch.Events[0]
	if len(event.Targets) != 2 || event.Targets[0].ProviderMessageID != "ZC0001" || event.Targets[1].ReceivedAt == nil || event.Targets[1].ReceivedAt.Nanosecond() != 123456789 {
		t.Fatalf("decoded targets: %+v", event.Targets)
	}
	if !validMailObserverTargets(event.Kind, event.Targets) {
		t.Fatal("controller-shaped candidates rejected")
	}
}
