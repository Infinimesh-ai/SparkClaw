package speech

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestOpenAIHTTPTranscriberNativeRealtimeSession(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"supports_streaming": true, "protocol": RealtimeProtocol,
				"sample_rate": RealtimeSampleRate, "frame_ms": RealtimeFrameMS,
			})
		case "/v1/audio/realtime":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			var start map[string]any
			if err := conn.ReadJSON(&start); err != nil {
				t.Error(err)
				return
			}
			if start["event"] != "start" || start["protocol"] != RealtimeProtocol {
				t.Errorf("unexpected start event: %#v", start)
				return
			}
			_ = conn.WriteJSON(RealtimeEvent{
				Event: "ready", Protocol: RealtimeProtocol,
				Format: &RealtimeFormat{SampleRate: RealtimeSampleRate, Channels: 1, BitsPerSample: 16, FrameMS: RealtimeFrameMS},
				Limits: &RealtimeLimits{MaxAudioSeconds: 60, MaxFrameSamples: RealtimeFrameSamples},
			})
			messageType, frame, err := conn.ReadMessage()
			if err != nil || messageType != websocket.BinaryMessage {
				t.Errorf("read audio frame: type=%d err=%v", messageType, err)
				return
			}
			if binary.BigEndian.Uint32(frame[:4]) != 0 || binary.BigEndian.Uint32(frame[4:8]) != 100 {
				t.Errorf("unexpected audio frame: %v", frame[:8])
				return
			}
			sequence := uint32(0)
			_ = conn.WriteJSON(RealtimeEvent{Event: "ack", AcceptedSequence: &sequence, ReceivedAudioMS: 6})
			_ = conn.WriteJSON(RealtimeEvent{Event: "partial", Revision: 1, Text: "live"})
			var finish map[string]any
			if err := conn.ReadJSON(&finish); err != nil {
				t.Error(err)
				return
			}
			_ = conn.WriteJSON(RealtimeEvent{Event: "final", Revision: 2, Text: "live final", DurationMS: 6})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := testSpeechConfig(server.URL)
	cfg.MaxPending = 0
	transcriber, err := NewOpenAIHTTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Close()
	status := transcriber.Status(context.Background())
	if !status.SupportsStreaming || status.Realtime == nil || status.Realtime.Protocol != RealtimeProtocol {
		t.Fatalf("native realtime status missing: %#v", status)
	}
	session, err := transcriber.StartRealtime(context.Background(), RealtimeRequest{
		RequestID: "voice-live", SessionID: "session-a", Language: "auto", MaxAudioSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.ReadyEvent().Event != "ready" {
		t.Fatalf("unexpected ready event: %#v", session.ReadyEvent())
	}
	if _, err := transcriber.Transcribe(context.Background(), Request{RequestID: "voice-busy"}); errorCode(err) != CodeBusy {
		t.Fatalf("batch must share realtime admission: %v", err)
	}
	if err := session.WriteAudio(context.Background(), 0, make([]byte, 200)); err != nil {
		t.Fatal(err)
	}
	ack, err := session.ReadEvent(context.Background())
	if err != nil || ack.AcceptedSequence == nil || *ack.AcceptedSequence != 0 {
		t.Fatalf("unexpected ack: %#v err=%v", ack, err)
	}
	partial, err := session.ReadEvent(context.Background())
	if err != nil || partial.Event != "partial" || partial.Text != "live" {
		t.Fatalf("unexpected partial: %#v err=%v", partial, err)
	}
	if err := session.Finish(context.Background(), 0, 6, "manual_stop"); err != nil {
		t.Fatal(err)
	}
	final, err := session.ReadEvent(context.Background())
	if err != nil || final.Event != "final" || final.Text != "live final" {
		t.Fatalf("unexpected final: %#v err=%v", final, err)
	}
}

func TestRealtimeEndpointUsesWebSocketScheme(t *testing.T) {
	transcriber, err := NewOpenAIHTTP(testSpeechConfig("https://speech.example.test/base"))
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Close()
	if got := transcriber.realtimeEndpoint(); !strings.HasPrefix(got, "wss://") || !strings.HasSuffix(got, "/base/v1/audio/realtime") {
		t.Fatalf("unexpected realtime endpoint: %s", got)
	}
}

func TestOpenAIRealtimeRejectsMissingReadyLimitsAndReleasesAdmission(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/realtime" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var start map[string]any
		_ = conn.ReadJSON(&start)
		_ = conn.WriteJSON(RealtimeEvent{
			Event: "ready", Protocol: RealtimeProtocol,
			Format: &RealtimeFormat{SampleRate: RealtimeSampleRate, Channels: 1, BitsPerSample: 16, FrameMS: RealtimeFrameMS},
		})
	}))
	defer server.Close()

	cfg := testSpeechConfig(server.URL)
	cfg.MaxPending = 0
	transcriber, err := NewOpenAIHTTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Close()
	_, err = transcriber.StartRealtime(context.Background(), RealtimeRequest{
		RequestID: "voice-invalid-ready", SessionID: "session-a", Language: "auto", MaxAudioSeconds: 60,
	})
	if code, _ := ErrorDetails(err); code != CodeStreamProtocol {
		t.Fatalf("invalid ready code = %q err=%v", code, err)
	}
	if err := transcriber.acquire(context.Background()); err != nil {
		t.Fatalf("invalid ready leaked admission: %v", err)
	}
	<-transcriber.admitted
}
