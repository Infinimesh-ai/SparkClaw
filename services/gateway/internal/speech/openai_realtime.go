package speech

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const maxRealtimeEventBytes = 64 << 10

type openAIRealtimeSession struct {
	owner   *OpenAIHTTPTranscriber
	conn    *websocket.Conn
	ready   RealtimeEvent
	release func()
	close   sync.Once
}

func (t *OpenAIHTTPTranscriber) StartRealtime(ctx context.Context, input RealtimeRequest) (RealtimeSession, error) {
	release, err := t.acquireOperation(ctx)
	if err != nil {
		return nil, err
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = time.Duration(RealtimeConnectTimeout) * time.Second
	conn, response, err := dialer.DialContext(ctx, t.realtimeEndpoint(), http.Header{
		"X-SparkClaw-Request-ID": []string{input.RequestID},
	})
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		release()
		if ctx.Err() != nil {
			return nil, contextSpeechError(ctx.Err())
		}
		return nil, NewError(CodeUnavailable, "realtime speech service is unavailable", true, err)
	}
	session := &openAIRealtimeSession{owner: t, conn: conn, release: release}
	conn.SetReadLimit(maxRealtimeEventBytes)
	if err := session.writeJSON(ctx, map[string]any{
		"event":      "start",
		"protocol":   RealtimeProtocol,
		"request_id": input.RequestID,
		"language":   input.Language,
		"format": map[string]any{
			"sample_rate": RealtimeSampleRate, "channels": 1, "bits_per_sample": 16,
		},
	}); err != nil {
		_ = session.Close()
		return nil, err
	}
	ready, err := session.ReadEvent(ctx)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if ready.Event != "ready" || ready.Protocol != RealtimeProtocol || ready.Format == nil ||
		ready.Format.SampleRate != RealtimeSampleRate || ready.Format.Channels != 1 || ready.Format.BitsPerSample != 16 ||
		ready.Format.FrameMS != RealtimeFrameMS || ready.Limits == nil ||
		ready.Limits.MaxFrameSamples != RealtimeFrameSamples || ready.Limits.MaxAudioSeconds <= 0 ||
		ready.Limits.MaxAudioSeconds > input.MaxAudioSeconds {
		_ = session.Close()
		if ready.Event == "fallback" {
			code := ready.Code
			if code == "" {
				code = CodeInferenceFailed
			}
			return nil, NewError(code, "realtime speech service is unavailable", ready.Retryable, nil)
		}
		return nil, NewError(CodeStreamProtocol, "realtime speech service returned an invalid ready event", false, nil)
	}
	session.ready = ready
	t.sessionsMu.Lock()
	t.sessions[session] = struct{}{}
	t.sessionsMu.Unlock()
	return session, nil
}

func (s *openAIRealtimeSession) ReadyEvent() RealtimeEvent {
	return s.ready
}

func (s *openAIRealtimeSession) WriteAudio(ctx context.Context, sequence uint32, pcm16 []byte) error {
	if len(pcm16) == 0 || len(pcm16)%2 != 0 || len(pcm16) > RealtimeFrameSamples*2 {
		return NewError(CodeStreamProtocol, "realtime audio frame is invalid", false, nil)
	}
	frame := make([]byte, 8+len(pcm16))
	binary.BigEndian.PutUint32(frame[0:4], sequence)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(pcm16)/2))
	copy(frame[8:], pcm16)
	if err := setWriteDeadline(s.conn, ctx); err != nil {
		return contextSpeechError(err)
	}
	if err := s.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return realtimeTransportError(ctx, err)
	}
	return nil
}

func (s *openAIRealtimeSession) Finish(ctx context.Context, lastSequence uint32, capturedMS int64, reason string) error {
	return s.writeJSON(ctx, map[string]any{
		"event": "finish", "last_sequence": lastSequence,
		"captured_ms": capturedMS, "reason": reason,
	})
}

func (s *openAIRealtimeSession) Cancel(ctx context.Context, lastSequence uint32) error {
	return s.writeJSON(ctx, map[string]any{"event": "cancel", "last_sequence": lastSequence})
}

func (s *openAIRealtimeSession) ReadEvent(ctx context.Context) (RealtimeEvent, error) {
	if err := setReadDeadline(s.conn, ctx); err != nil {
		return RealtimeEvent{}, contextSpeechError(err)
	}
	var event RealtimeEvent
	if err := s.conn.ReadJSON(&event); err != nil {
		return RealtimeEvent{}, realtimeTransportError(ctx, err)
	}
	switch event.Event {
	case "ready", "ack", "partial", "final", "fallback", "error":
		return event, nil
	default:
		return RealtimeEvent{}, NewError(CodeStreamProtocol, "realtime speech service returned an unknown event", false, nil)
	}
}

func (s *openAIRealtimeSession) Close() error {
	var closeErr error
	s.close.Do(func() {
		closeErr = s.conn.Close()
		s.owner.sessionsMu.Lock()
		delete(s.owner.sessions, s)
		s.owner.sessionsMu.Unlock()
		s.release()
	})
	return closeErr
}

func (s *openAIRealtimeSession) writeJSON(ctx context.Context, payload any) error {
	if err := setWriteDeadline(s.conn, ctx); err != nil {
		return contextSpeechError(err)
	}
	if err := s.conn.WriteJSON(payload); err != nil {
		return realtimeTransportError(ctx, err)
	}
	return nil
}

func (t *OpenAIHTTPTranscriber) realtimeEndpoint() string {
	parsed, _ := url.Parse(strings.TrimRight(t.cfg.BaseURL, "/"))
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/audio/realtime"
	return parsed.String()
}

func setReadDeadline(conn *websocket.Conn, ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return conn.SetReadDeadline(time.Time{})
	}
	return conn.SetReadDeadline(deadline)
}

func setWriteDeadline(conn *websocket.Conn, ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return conn.SetWriteDeadline(time.Time{})
	}
	return conn.SetWriteDeadline(deadline)
}

func realtimeTransportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return contextSpeechError(ctx.Err())
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code == websocket.CloseNormalClosure {
		return NewError(CodeCancelled, "realtime speech session closed", true, err)
	}
	return NewError(CodeUnavailable, "realtime speech transport failed", true, err)
}
