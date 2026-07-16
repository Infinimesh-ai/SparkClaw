package messageplane

import (
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestNormalizeWebMultimodalMessage(t *testing.T) {
	createdAt := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	envelope, err := Normalize(Ingress{
		Session: app.Session{ID: "session_1", OwnerID: "owner_1", Source: "web"},
		Message: app.Message{
			ID:        "message_1",
			SessionID: "session_1",
			Content:   "inspect this",
			CreatedAt: createdAt,
			Attachments: []app.MessageAttachment{{
				ArtifactID:  "artifact_1",
				Name:        "screen.png",
				RelPath:     "uploads/screen.png",
				ContentType: "image/png",
				Bytes:       128,
				Width:       20,
				Height:      10,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Source.Kind != app.MessageSourceWeb || envelope.Source.EndpointID != "session:session_1" {
		t.Fatalf("unexpected source: %#v", envelope.Source)
	}
	if envelope.ReturnRoute.Mode != app.ReturnToSource || envelope.ReturnRoute.SourceEndpointID != envelope.Source.EndpointID {
		t.Fatalf("unexpected return route: %#v", envelope.ReturnRoute)
	}
	if len(envelope.Content.Parts) != 2 {
		t.Fatalf("parts = %#v", envelope.Content.Parts)
	}
	image := envelope.Content.Parts[1]
	if image.Kind != app.MessagePartImage || image.ArtifactID != "artifact_1" || image.Resource == nil || image.Resource.Ref != "uploads/screen.png" {
		t.Fatalf("unexpected image part: %#v", image)
	}
	projection := RoutingProjection(envelope)
	for _, expected := range []string{"inspect this", "screen.png path=uploads/screen.png", "content_type=image/png", "media_kind=image"} {
		if !strings.Contains(projection, expected) {
			t.Fatalf("projection %q does not contain %q", projection, expected)
		}
	}
}

func TestNormalizeThirdPartyVoiceNote(t *testing.T) {
	envelope, err := Normalize(Ingress{
		Session: app.Session{ID: "session_2", Source: "telegram"},
		Message: app.Message{
			ID:        "telegram_42",
			SessionID: "session_2",
			CreatedAt: time.Now().UTC(),
			Attachments: []app.MessageAttachment{{
				Name:        "voice.ogg",
				URI:         "artifact://messages/voice.ogg",
				ContentType: "audio/ogg",
				Source:      "telegram_voice",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Source.Kind != app.MessageSourceThirdPartyDevice || envelope.Source.Adapter != "telegram" {
		t.Fatalf("unexpected source: %#v", envelope.Source)
	}
	part := envelope.Content.Parts[0]
	if part.Kind != app.MessagePartAudio || part.Disposition != app.MessageDispositionVoiceNote {
		t.Fatalf("unexpected voice part: %#v", part)
	}
	if envelope.OwnerID != app.DefaultOwnerID || envelope.Authorization.PrincipalID != app.DefaultOwnerID {
		t.Fatalf("unexpected default authorization: %#v", envelope.Authorization)
	}
}

func TestNormalizeTimerUsesDeclaredReturnRoute(t *testing.T) {
	route := app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "endpoint_1"}
	envelope, err := Normalize(Ingress{
		Session:     app.Session{ID: "schedule_session", Source: "timer"},
		Message:     app.Message{ID: "occurrence_1", SessionID: "schedule_session", Content: "run report", CreatedAt: time.Now().UTC()},
		ScheduleID:  "schedule_1",
		ReturnRoute: &route,
	})
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Source.Kind != app.MessageSourceTimer || envelope.Source.ScheduleID != "schedule_1" || envelope.Source.EndpointID != "" {
		t.Fatalf("unexpected timer source: %#v", envelope.Source)
	}
	if envelope.ReturnRoute != route {
		t.Fatalf("return route = %#v", envelope.ReturnRoute)
	}
}

func TestNormalizeRejectsEscapingAttachmentPath(t *testing.T) {
	_, err := Normalize(Ingress{
		Session: app.Session{ID: "session_1", Source: "web"},
		Message: app.Message{
			ID:          "message_1",
			SessionID:   "session_1",
			CreatedAt:   time.Now().UTC(),
			Attachments: []app.MessageAttachment{{Name: "secret.txt", RelPath: "../secret.txt"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "inside the workspace") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
}

func TestNormalizeRejectsEmptyMessage(t *testing.T) {
	_, err := Normalize(Ingress{
		Session: app.Session{ID: "session_1", Source: "web"},
		Message: app.Message{ID: "message_1", SessionID: "session_1", CreatedAt: time.Now().UTC()},
	})
	if err == nil || !strings.Contains(err.Error(), "at least one part") {
		t.Fatalf("expected empty content rejection, got %v", err)
	}
}
