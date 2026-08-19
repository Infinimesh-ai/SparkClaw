package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"github.com/gorilla/websocket"
)

type fakeGatewayRealtimeSession struct {
	ready  speech.RealtimeEvent
	events chan speech.RealtimeEvent
	closed chan struct{}
	once   sync.Once
	audio  []byte
}

func newFakeGatewayRealtimeSession() *fakeGatewayRealtimeSession {
	return &fakeGatewayRealtimeSession{
		ready: speech.RealtimeEvent{
			Event: "ready", Protocol: speech.RealtimeProtocol,
			Format: &speech.RealtimeFormat{SampleRate: 16000, Channels: 1, BitsPerSample: 16, FrameMS: 100},
			Limits: &speech.RealtimeLimits{MaxAudioSeconds: 60, MaxFrameSamples: 1600},
		},
		events: make(chan speech.RealtimeEvent, 4),
		closed: make(chan struct{}),
	}
}

func (s *fakeGatewayRealtimeSession) ReadyEvent() speech.RealtimeEvent { return s.ready }

func (s *fakeGatewayRealtimeSession) WriteAudio(_ context.Context, sequence uint32, pcm []byte) error {
	s.audio = append(s.audio, pcm...)
	accepted := sequence
	s.events <- speech.RealtimeEvent{Event: "ack", AcceptedSequence: &accepted, ReceivedAudioMS: 100}
	s.events <- speech.RealtimeEvent{Event: "partial", Revision: 1, Text: "live partial", AudioEndMS: 100}
	return nil
}

func (s *fakeGatewayRealtimeSession) Finish(_ context.Context, _ uint32, capturedMS int64, reason string) error {
	s.events <- speech.RealtimeEvent{
		Event: "final", Revision: 2, Text: "live final", DurationMS: capturedMS,
		StopReason: reason, Model: "test-asr",
	}
	return nil
}

func (s *fakeGatewayRealtimeSession) Cancel(context.Context, uint32) error { return nil }

func (s *fakeGatewayRealtimeSession) ReadEvent(ctx context.Context) (speech.RealtimeEvent, error) {
	select {
	case event := <-s.events:
		return event, nil
	case <-s.closed:
		return speech.RealtimeEvent{}, speech.NewError(speech.CodeCancelled, "closed", true, nil)
	case <-ctx.Done():
		return speech.RealtimeEvent{}, ctx.Err()
	}
}

func (s *fakeGatewayRealtimeSession) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func TestSpeechRealtimeTicketRelaysPartialAndFinalOnce(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	cfg.Speech.Model = "test-asr"
	cfg.Speech.MaxAudioSeconds = 60
	sessionRecord := st.CreateSession("Voice")
	realtime := newFakeGatewayRealtimeSession()
	var started speech.RealtimeRequest
	fake := &fakeSpeechTranscriber{
		status: speech.Status{Enabled: true, Ready: true, SupportsStreaming: true},
		startRealtime: func(_ context.Context, request speech.RealtimeRequest) (speech.RealtimeSession, error) {
			started = request
			return realtime, nil
		},
	}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(fake))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"session_id": sessionRecord.ID, "request_id": "voice-realtime-1", "language": "auto",
	})
	response, err := http.Post(ts.URL+"/api/speech/realtime-sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("ticket returned %d", response.StatusCode)
	}
	var issued struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	if issued.ID == "" || !strings.Contains(issued.URL, "ticket=") || started.SessionID != sessionRecord.ID {
		t.Fatalf("unexpected ticket/start request: %#v %#v", issued, started)
	}
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + issued.URL
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var event speech.RealtimeEvent
	if err := client.ReadJSON(&event); err != nil || event.Event != "ready" {
		t.Fatalf("ready event: %#v err=%v", event, err)
	}
	pcm := make([]byte, 3200)
	frame := make([]byte, 8+len(pcm))
	binary.BigEndian.PutUint32(frame[0:4], 0)
	binary.BigEndian.PutUint32(frame[4:8], 1600)
	copy(frame[8:], pcm)
	if err := client.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
	if err := client.ReadJSON(&event); err != nil || event.Event != "ack" || event.AcceptedSequence == nil || *event.AcceptedSequence != 0 {
		t.Fatalf("ack event: %#v err=%v", event, err)
	}
	if err := client.ReadJSON(&event); err != nil || event.Event != "partial" || event.Text != "live partial" {
		t.Fatalf("partial event: %#v err=%v", event, err)
	}
	if err := client.WriteJSON(map[string]any{
		"event": "finish", "last_sequence": 0, "captured_ms": 100, "reason": "manual_stop",
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.ReadJSON(&event); err != nil || event.Event != "final" || event.Text != "live final" {
		t.Fatalf("final event: %#v err=%v", event, err)
	}
	if len(realtime.audio) != len(pcm) {
		t.Fatalf("relayed PCM bytes = %d", len(realtime.audio))
	}
	if replay, _, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		replay.Close()
		t.Fatal("single-use realtime ticket was replayed")
	}
}

func TestValidateRealtimeAudioFrameRejectsMalformedOrDiscontinuousInput(t *testing.T) {
	validPCM := make([]byte, speech.RealtimeFrameSamples*2)
	valid := make([]byte, 8+len(validPCM))
	binary.BigEndian.PutUint32(valid[0:4], 3)
	binary.BigEndian.PutUint32(valid[4:8], speech.RealtimeFrameSamples)
	copy(valid[8:], validPCM)
	if sequence, pcm, code := validateRealtimeAudioFrame(valid, 3); code != "" || sequence != 3 || len(pcm) != len(validPCM) {
		t.Fatalf("valid frame rejected: sequence=%d bytes=%d code=%q", sequence, len(pcm), code)
	}
	for name, frame := range map[string][]byte{
		"short_header":    {0, 0, 0},
		"sequence_gap":    append([]byte(nil), valid...),
		"length_mismatch": append([]byte(nil), valid[:len(valid)-2]...),
	} {
		if name == "sequence_gap" {
			binary.BigEndian.PutUint32(frame[0:4], 4)
		}
		if _, _, code := validateRealtimeAudioFrame(frame, 3); code != speech.CodeStreamProtocol {
			t.Errorf("%s code = %q", name, code)
		}
	}
}

func TestRealtimeAudioOverrunUsesFiveSecondHardBound(t *testing.T) {
	if realtimeAudioOverrun(49, 0) {
		t.Fatal("4.9 seconds of unacknowledged audio exceeded the bound")
	}
	if !realtimeAudioOverrun(50, 0) {
		t.Fatal("five seconds of unacknowledged audio did not fail closed")
	}
	if realtimeAudioOverrun(60, 55) {
		t.Fatal("acknowledged frames were not removed from the bound")
	}
}
