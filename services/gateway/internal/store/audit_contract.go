package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var (
	errAuditFieldsJSONDecode  = errors.New("decode audit fields JSON")
	errEventPayloadJSONDecode = errors.New("decode event payload JSON")
)

func prepareAuditEvent(event app.AuditEvent, now time.Time) (app.AuditEvent, error) {
	if event.ID == "" {
		event.ID = app.NewID("audit")
	}
	if event.Time.IsZero() {
		event.Time = now
	}
	event.Time = postgresTime(event.Time)

	fields, err := cloneAuditFields(event.Fields)
	if err != nil {
		return app.AuditEvent{}, err
	}
	event.Fields = fields
	return event, nil
}

func cloneAuditEvent(event app.AuditEvent) (app.AuditEvent, error) {
	fields, err := cloneAuditFields(event.Fields)
	if err != nil {
		return app.AuditEvent{}, err
	}
	event.Fields = fields
	return event, nil
}

func cloneAuditEventsBestEffort(events []app.AuditEvent) []app.AuditEvent {
	cloned := make([]app.AuditEvent, len(events))
	for index, event := range events {
		value, err := cloneAuditEvent(event)
		if err != nil {
			value = event
		}
		cloned[index] = value
	}
	return cloned
}

func cloneAuditFields(fields map[string]any) (map[string]any, error) {
	if fields == nil {
		return nil, nil
	}
	if _, err := json.Marshal(fields); err != nil {
		return nil, fmt.Errorf("%w: %v", errAuditFieldsJSONDecode, err)
	}
	return cloneAuditValue(reflect.ValueOf(fields)).Interface().(map[string]any), nil
}

func cloneAuditValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneAuditValue(value.Elem())
		out := reflect.New(value.Type()).Elem()
		out.Set(cloned)
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(cloneAuditValue(value.Elem()))
		return out
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			out.SetMapIndex(cloneAuditValue(iterator.Key()), cloneAuditValue(iterator.Value()))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(cloneAuditValue(value.Index(index)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(cloneAuditValue(value.Index(index)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		out.Set(value)
		for index := 0; index < value.NumField(); index++ {
			if out.Field(index).CanSet() && value.Field(index).CanInterface() {
				out.Field(index).Set(cloneAuditValue(value.Field(index)))
			}
		}
		return out
	default:
		return value
	}
}

func normalizeEventPayload(event app.Event) (app.Event, error) {
	if event.Payload == nil {
		return event, nil
	}
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return app.Event{}, err
	}
	event.Payload, err = decodeEventPayload(event.Type, raw)
	if err != nil {
		return app.Event{}, err
	}
	return event, nil
}

func decodeEventPayload(eventType string, raw []byte) (any, error) {
	switch {
	case strings.HasPrefix(eventType, "session."):
		return decodeTypedEventPayload[app.Session](raw)
	case strings.HasPrefix(eventType, "client."):
		return decodeTypedEventPayload[app.Client](raw)
	case strings.HasPrefix(eventType, "pairing_code."):
		return decodeTypedEventPayload[app.PairingCode](raw)
	case strings.HasPrefix(eventType, "owner_profile."):
		return decodeTypedEventPayload[app.OwnerProfile](raw)
	case strings.HasPrefix(eventType, "notification_binding."):
		return decodeTypedEventPayload[app.NotificationBinding](raw)
	case strings.HasPrefix(eventType, "connector."):
		return decodeTypedEventPayload[app.ConnectorSetting](raw)
	case strings.HasPrefix(eventType, "message."):
		return decodeTypedEventPayload[app.Message](raw)
	case strings.HasPrefix(eventType, "run_feedback."):
		return decodeTypedEventPayload[app.RunFeedback](raw)
	case strings.HasPrefix(eventType, "run."):
		return decodeTypedEventPayload[app.AgentRun](raw)
	case strings.HasPrefix(eventType, "model_call."):
		return decodeTypedEventPayload[app.ModelCall](raw)
	case strings.HasPrefix(eventType, "tool_call."):
		return decodeTypedEventPayload[app.ToolCall](raw)
	case strings.HasPrefix(eventType, "document."):
		return decodeTypedEventPayload[app.DocumentRecord](raw)
	case strings.HasPrefix(eventType, "approval."):
		return decodeTypedEventPayload[app.Approval](raw)
	case strings.HasPrefix(eventType, "reminder_delivery."):
		return decodeTypedEventPayload[app.ReminderDelivery](raw)
	case strings.HasPrefix(eventType, "reminder."):
		return decodeTypedEventPayload[app.Reminder](raw)
	case strings.HasPrefix(eventType, "external_chat_session."):
		return decodeTypedEventPayload[app.ExternalChatSession](raw)
	case strings.HasPrefix(eventType, "external_chat_message."):
		return decodeTypedEventPayload[app.ExternalChatMessage](raw)
	case strings.HasPrefix(eventType, "browser_auth."):
		return decodeTypedEventPayload[app.BrowserAuthRecord](raw)
	case strings.HasPrefix(eventType, "browser_login_block."):
		return decodeTypedEventPayload[app.BrowserLoginBlock](raw)
	case strings.HasPrefix(eventType, "memory_candidate."):
		return decodeTypedEventPayload[app.MemoryCandidate](raw)
	case strings.HasPrefix(eventType, "memory."):
		return decodeTypedEventPayload[app.Memory](raw)
	case strings.HasPrefix(eventType, "eval."):
		return decodeTypedEventPayload[app.EvalRun](raw)
	case strings.HasPrefix(eventType, "artifact."):
		return decodeTypedEventPayload[app.ArtifactObject](raw)
	case strings.HasPrefix(eventType, "episode_summary."):
		return decodeTypedEventPayload[app.EpisodeSummary](raw)
	default:
		var payload any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	}
}

func decodeTypedEventPayload[T any](raw []byte) (any, error) {
	var payload T
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
