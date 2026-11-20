package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// The speech domain over the link layer (device condition microphone): a
// recorded transcription of an uploaded recording, and realtime sessions
// whose audio frames are numbered and whose events are read with a cursor.
// A session's terminal events are stored before any caller can observe
// them, so a device that lost its connection reads the same final text
// after a backend restart, and the provider is never asked twice.

type speechEvent struct {
	Sequence uint64               `json:"sequence"`
	Event    speech.RealtimeEvent `json:"event"`
}

type speechSession struct {
	mu               sync.Mutex
	id, device       string
	session          speech.RealtimeSession
	ctx              context.Context
	cancel           context.CancelFunc
	last             uint32
	samples          int64
	digests          map[uint32]string
	providerAck      uint32
	sequence, ack    uint64
	events           []speechEvent
	finished, closed bool
	terminalStored   bool
}

type speechCheckpoint struct {
	SessionID string        `json:"session_id"`
	After     uint64        `json:"after"`
	Sequence  uint64        `json:"sequence"`
	Events    []speechEvent `json:"events"`
	Closed    bool          `json:"closed"`
}

// linkSpeech holds the realtime sessions and recorded transcriptions in
// progress and the stored terminal checkpoints.
type linkSpeech struct {
	dir        string
	mu         sync.Mutex
	sessions   map[string]*speechSession
	recordings map[string]context.CancelFunc
}

func newLinkSpeech(dir string) (*linkSpeech, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &linkSpeech{dir: dir, sessions: map[string]*speechSession{}, recordings: map[string]context.CancelFunc{}}, nil
}

func (l *linkSpeech) checkpointPath(device, id string) string {
	sum := sha256.Sum256([]byte(device + "\x00" + id))
	return filepath.Join(l.dir, hex.EncodeToString(sum[:])+".json")
}

var (
	errSpeechCheckpointPending = errors.New("speech checkpoint pending")
	errSpeechCheckpointMissing = errors.New("speech checkpoint missing")
)

// prepare records that a session exists before it starts, so a terminal
// that a lost process never stored is reported unknown, not absent.
func (l *linkSpeech) prepare(device, id string) error {
	return link.WriteJSONAtomic(l.checkpointPath(device, id), speechCheckpoint{SessionID: id})
}

// persistTerminal stores a session's terminal events; called with
// session.mu held.
func (l *linkSpeech) persistTerminal(session *speechSession) error {
	if session.terminalStored {
		return nil
	}
	if len(session.events) == 0 || !speechTerminal(session.events[len(session.events)-1].Event) {
		session.sequence++
		session.events = append(session.events, speechEvent{session.sequence, speech.RealtimeEvent{Event: "error", Code: "speech_stream_closed"}})
	}
	checkpoint := speechCheckpoint{SessionID: session.id, After: session.ack, Sequence: session.sequence, Events: session.events, Closed: true}
	if err := link.WriteJSONAtomic(l.checkpointPath(session.device, session.id), checkpoint); err != nil {
		return err
	}
	session.terminalStored = true
	return nil
}

func (l *linkSpeech) recovered(device, id string, after uint64) (any, error) {
	raw, err := os.ReadFile(l.checkpointPath(device, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, speechDomainError("speech_session_not_found")
	}
	var checkpoint speechCheckpoint
	if err != nil || json.Unmarshal(raw, &checkpoint) != nil || checkpoint.SessionID != id {
		return nil, speechDomainError("speech_receipt_unavailable")
	}
	if !checkpoint.Closed {
		return nil, speechDomainError("speech_session_outcome_unknown")
	}
	if after < checkpoint.After || after > checkpoint.Sequence {
		return nil, speechDomainError("cursor_gap")
	}
	events := []speechEvent{}
	for _, event := range checkpoint.Events {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	return map[string]any{"session_id": id, "events": events, "closed": true}, nil
}

func speechTerminal(event speech.RealtimeEvent) bool {
	return event.Event == "final" || event.Event == "error" || event.Event == "cancelled"
}

// speechDomainError names a speech failure by its reason.
func speechDomainError(reason string) error {
	code := contract.CodeInvalidRequest
	switch reason {
	case "speech_unavailable", "speech_disabled", "speech_stream_unavailable", "speech_receipt_unavailable":
		code = contract.CodeBackendUnavailable
	case "speech_session_not_found":
		code = contract.CodeNotFound
	case "speech_session_closed", "speech_frame_conflict", "speech_stream_overrun", "speech_request_in_progress", "cursor_gap", "speech_audio_unavailable":
		code = contract.CodeConflict
	case "speech_session_capacity":
		code = contract.CodeBusy
	case "speech_frame_too_large", "speech_audio_too_long":
		code = contract.CodeTooLarge
	case "speech_session_outcome_unknown", "speech_frame_outcome_unknown":
		code = contract.CodeOutcomeUnknown
	}
	return linkDomainError(code, reason, reason, code == contract.CodeBackendUnavailable || code == contract.CodeBusy)
}

// speechProviderError carries the provider's own failure reason.
func speechProviderError(err error) error {
	reason, _ := speech.ErrorDetails(err)
	code := linkCodeForStatus(speechHTTPStatus(err))
	return linkDomainError(code, reason, reason, code == contract.CodeBackendUnavailable)
}

type speechTranscribeArgs struct {
	SessionID   string          `json:"session_id"`
	RequestID   string          `json:"request_id"`
	Language    string          `json:"language"`
	AudioObject domainObjectRef `json:"audio_object"`
}

func (s *Server) speechTranscribe(ctx context.Context, device string, input speechTranscribeArgs) (any, error) {
	if !execution.UUID(input.SessionID) || !speechRequestIDPattern.MatchString(input.RequestID) {
		return nil, speechDomainError("invalid_input")
	}
	language := strings.TrimSpace(input.Language)
	if language == "" {
		language = s.cfg.Speech.DefaultLanguage
	}
	if language != "auto" && !speechLanguagePattern.MatchString(language) {
		return nil, speechDomainError("invalid_language")
	}
	if !s.cfg.Speech.Enabled || s.cfg.Speech.Backend == "disabled" {
		return nil, speechDomainError("speech_disabled")
	}
	purpose := input.AudioObject.Purpose
	if purpose != "speech_recording" && purpose != "speech_audio" {
		return nil, speechDomainError("invalid_audio_object")
	}
	raw, err := s.link.domainObj.readAll(device, input.AudioObject, purpose, s.cfg.Speech.MaxUploadBytes)
	if err != nil {
		return nil, speechDomainError("speech_audio_unavailable")
	}
	info, err := speech.ValidatePCM16WAV(raw, speechMaxAudioSeconds(s.cfg))
	if err != nil {
		return nil, speechProviderError(err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.Speech.TimeoutSeconds)*time.Second)
	defer cancel()
	l := s.link.speech
	key := device + "\x00" + input.SessionID + "\x00" + input.RequestID
	l.mu.Lock()
	if _, exists := l.recordings[key]; exists {
		l.mu.Unlock()
		return nil, speechDomainError("speech_request_in_progress")
	}
	l.recordings[key] = cancel
	l.mu.Unlock()
	defer func() { l.mu.Lock(); delete(l.recordings, key); l.mu.Unlock() }()
	result, err := s.speech.Transcribe(requestCtx, speech.Request{RequestID: input.RequestID, SessionID: input.SessionID, Language: language, PCM16WAV: raw, DurationMS: info.DurationMS})
	if err != nil {
		return nil, speechProviderError(err)
	}
	// The uploaded recording is a domain object that expires on its own.
	return map[string]any{"id": app.NewID("stt"), "request_id": input.RequestID, "session_id": input.SessionID, "text": result.Text, "language": result.Language,
		"duration_ms": info.DurationMS, "inference_ms": result.InferenceMS, "model": result.Model, "audio_retained": true, "audio_expires_at": input.AudioObject.ExpiresAt}, nil
}

type speechRequestArgs struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Language  string `json:"language,omitempty"`
}

func (s *Server) speechCancelRecording(device string, input speechRequestArgs) (any, error) {
	if !execution.UUID(input.SessionID) || !speechRequestIDPattern.MatchString(input.RequestID) {
		return nil, speechDomainError("invalid_input")
	}
	l := s.link.speech
	l.mu.Lock()
	cancel := l.recordings[device+"\x00"+input.SessionID+"\x00"+input.RequestID]
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return map[string]bool{"cancelled": true}, nil
}

func (s *Server) speechOpen(ctx context.Context, p link.Principal, input speechRequestArgs) (any, error) {
	if !execution.UUID(input.SessionID) || !speechRequestIDPattern.MatchString(input.RequestID) {
		return nil, speechDomainError("invalid_input")
	}
	if input.Language == "" {
		input.Language = s.cfg.Speech.DefaultLanguage
	}
	if input.Language != "auto" && !speechLanguagePattern.MatchString(input.Language) {
		return nil, speechDomainError("invalid_language")
	}
	status := s.speech.Status(ctx)
	if !s.cfg.Speech.Enabled || s.cfg.Speech.Backend == "disabled" || !status.Ready || !status.SupportsStreaming {
		return nil, speechDomainError("speech_stream_unavailable")
	}
	l := s.link.speech
	l.mu.Lock()
	defer l.mu.Unlock()
	count, active := 0, 0
	for _, existing := range l.sessions {
		existing.mu.Lock()
		closed := existing.closed
		existing.mu.Unlock()
		if !closed {
			active++
			if existing.device == p.DeviceID {
				count++
			}
		}
	}
	if len(l.sessions) >= 128 || count >= 2 || active >= 32 {
		return nil, speechDomainError("speech_session_capacity")
	}
	id := app.NewID("speech")
	if err := l.prepare(p.DeviceID, id); err != nil {
		return nil, speechDomainError("speech_receipt_unavailable")
	}
	connectCtx, cancel := context.WithTimeout(ctx, speech.RealtimeConnectTimeout*time.Second)
	defer cancel()
	stream, err := s.speech.StartRealtime(connectCtx, speech.RealtimeRequest{RequestID: input.RequestID, SessionID: input.SessionID, Language: input.Language, MaxAudioSeconds: speechMaxAudioSeconds(s.cfg)})
	if err != nil {
		return nil, speechProviderError(err)
	}
	// The device's client connection ends the session if it is revoked.
	connected, release, err := s.clientConnectionContext(s.executionContext(), p.ClientID)
	if err != nil {
		stream.Close()
		return nil, link.Errorf(contract.CodeRevoked, "this device was revoked")
	}
	connected, stop := context.WithTimeout(connected, time.Duration(speechMaxAudioSeconds(s.cfg)+speech.RealtimeFinalTimeout+30)*time.Second)
	session := &speechSession{id: id, device: p.DeviceID, session: stream, ctx: connected, cancel: stop, digests: map[uint32]string{}}
	l.sessions[id] = session
	go l.readEvents(session, release)
	return map[string]any{"session_id": id, "ready": stream.ReadyEvent()}, nil
}

func (l *linkSpeech) readEvents(session *speechSession, release func()) {
	defer release()
	defer session.cancel()
	defer session.session.Close()
	defer func() {
		session.mu.Lock()
		session.closed = true
		err := l.persistTerminal(session)
		session.mu.Unlock()
		if err == nil {
			time.AfterFunc(time.Minute, func() { l.mu.Lock(); delete(l.sessions, session.id); l.mu.Unlock() })
		}
	}()
	for {
		event, err := session.session.ReadEvent(session.ctx)
		if err != nil {
			session.mu.Lock()
			if !session.closed && len(session.events) < 64 {
				session.sequence++
				session.events = append(session.events, speechEvent{session.sequence, speech.RealtimeEvent{Event: "error", Code: "speech_stream_closed"}})
			}
			session.mu.Unlock()
			return
		}
		session.mu.Lock()
		if len(session.events) >= 63 {
			session.sequence++
			session.events = append(session.events, speechEvent{session.sequence, speech.RealtimeEvent{Event: "error", Code: "speech_stream_overrun"}})
			session.closed = true
			session.mu.Unlock()
			return
		}
		if event.AcceptedSequence != nil {
			shifted := *event.AcceptedSequence + 1
			event.AcceptedSequence = &shifted
		}
		session.sequence++
		session.events = append(session.events, speechEvent{session.sequence, event})
		if event.AcceptedSequence != nil && *event.AcceptedSequence > session.providerAck {
			session.providerAck = *event.AcceptedSequence
		}
		terminal := speechTerminal(event)
		session.mu.Unlock()
		if terminal {
			return
		}
	}
}

type speechSessionArgs struct {
	SessionID    string `json:"session_id"`
	Sequence     uint32 `json:"sequence,omitempty"`
	PCM16        string `json:"pcm16,omitempty"`
	LastSequence uint32 `json:"last_sequence,omitempty"`
	TotalSamples int64  `json:"total_samples,omitempty"`
	Reason       string `json:"reason,omitempty"`
	After        uint64 `json:"after,omitempty"`
}

// speechSessionCall serves frame, finish, cancel and events for one open
// session of this device.
func (s *Server) speechSessionCall(ctx context.Context, device, method string, input speechSessionArgs) (any, error) {
	if input.SessionID == "" {
		return nil, speechDomainError("invalid_input")
	}
	l := s.link.speech
	l.mu.Lock()
	session := l.sessions[input.SessionID]
	l.mu.Unlock()
	if session == nil && method == "speech.session.events" {
		return l.recovered(device, input.SessionID, input.After)
	}
	if session == nil || session.device != device {
		return nil, speechDomainError("speech_session_not_found")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	switch method {
	case "speech.session.frame":
		if session.closed || session.finished || session.ctx.Err() != nil {
			return nil, speechDomainError("speech_session_closed")
		}
		if len(input.PCM16) > base64.StdEncoding.EncodedLen(speech.RealtimeFrameSamples*2) {
			return nil, speechDomainError("speech_frame_too_large")
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(input.PCM16)
		if err != nil || len(raw) == 0 || len(raw)%2 != 0 {
			return nil, speechDomainError("speech_invalid_frame")
		}
		digest := execution.Digest(raw)
		if input.Sequence <= session.last {
			// A repeated frame is answered from its digest, never sent again.
			if session.digests[input.Sequence] != digest {
				return nil, speechDomainError("speech_frame_conflict")
			}
			return map[string]any{"accepted_sequence": input.Sequence, "received_audio_ms": session.samples * 1000 / speech.RealtimeSampleRate}, nil
		}
		if input.Sequence != session.last+1 || input.Sequence-session.providerAck > speech.RealtimeMaxUnackedMS/speech.RealtimeFrameMS {
			return nil, speechDomainError("speech_stream_overrun")
		}
		if session.samples+int64(len(raw)/2) > int64(speechMaxAudioSeconds(s.cfg)*speech.RealtimeSampleRate) {
			return nil, speechDomainError("speech_audio_too_long")
		}
		if err = session.session.WriteAudio(ctx, input.Sequence-1, raw); err != nil {
			// The provider may already have the frame: end the recording
			// instead of accepting a retry that could append it twice.
			session.closed = true
			session.cancel()
			_ = session.session.Close()
			if len(session.events) < 64 {
				session.sequence++
				session.events = append(session.events, speechEvent{session.sequence, speech.RealtimeEvent{Event: "error", Code: "speech_frame_outcome_unknown"}})
			}
			return nil, speechProviderError(err)
		}
		session.last = input.Sequence
		session.samples += int64(len(raw) / 2)
		session.digests[input.Sequence] = digest
		if input.Sequence > 64 {
			delete(session.digests, input.Sequence-64)
		}
		return map[string]any{"accepted_sequence": input.Sequence, "received_audio_ms": session.samples * 1000 / speech.RealtimeSampleRate}, nil
	case "speech.session.finish":
		if session.closed || session.ctx.Err() != nil {
			return nil, speechDomainError("speech_session_closed")
		}
		if input.LastSequence == 0 || input.LastSequence != session.last || input.TotalSamples != session.samples {
			return nil, speechDomainError("speech_frame_conflict")
		}
		if session.finished {
			return map[string]bool{"finishing": true}, nil
		}
		if err := session.session.Finish(ctx, input.LastSequence-1, input.TotalSamples*1000/speech.RealtimeSampleRate, input.Reason); err != nil {
			return nil, speechProviderError(err)
		}
		session.finished = true
		return map[string]bool{"finishing": true}, nil
	case "speech.session.cancel":
		if !session.closed {
			last := session.last
			if last > 0 {
				last--
			}
			_ = session.session.Cancel(ctx, last)
			session.closed = true
			session.cancel()
			_ = session.session.Close()
		}
		return map[string]bool{"cancelled": true}, nil
	case "speech.session.events":
		if session.closed || len(session.events) > 0 && speechTerminal(session.events[len(session.events)-1].Event) {
			if err := l.persistTerminal(session); err != nil {
				return nil, speechDomainError("speech_receipt_unavailable")
			}
			return l.recovered(device, session.id, input.After)
		}
		if input.After < session.ack || input.After > session.sequence {
			return nil, speechDomainError("cursor_gap")
		}
		session.ack = input.After
		for len(session.events) > 0 && session.events[0].Sequence <= input.After {
			session.events = session.events[1:]
		}
		return map[string]any{"session_id": session.id, "events": append([]speechEvent{}, session.events...), "closed": session.closed}, nil
	}
	return nil, link.Errorf(contract.CodeUnsupportedCapability, "unknown speech method")
}

func init() {
	microphone := func(call func(context.Context, *Server, *link.Request, json.RawMessage) (any, error)) func(context.Context, *Server, *link.Request, json.RawMessage) (any, error) {
		return func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			if !req.Principal.Conditions.Microphone {
				return nil, link.Errorf(contract.CodeUnsupportedCapability, "this device has no microphone")
			}
			if s.speech == nil {
				return nil, speechDomainError("speech_unavailable")
			}
			return call(ctx, s, req, raw)
		}
	}
	session := func(method string) func(context.Context, *Server, *link.Request, json.RawMessage) (any, error) {
		return microphone(func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[speechSessionArgs](raw)
			if err != nil {
				return nil, err
			}
			return s.speechSessionCall(ctx, req.Principal.DeviceID, method, args)
		})
	}
	registerLinkDomain("speech", map[string]linkDomainMethod{
		"speech.status": {Call: func(ctx context.Context, s *Server, _ *link.Request, _ json.RawMessage) (any, error) {
			if s.speech == nil {
				return nil, speechDomainError("speech_unavailable")
			}
			return s.speech.Status(ctx), nil
		}},
		"speech.transcribe": {Mutation: true, Call: microphone(func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[speechTranscribeArgs](raw)
			if err != nil {
				return nil, err
			}
			return s.speechTranscribe(ctx, req.Principal.DeviceID, args)
		})},
		"speech.cancel": {Mutation: true, Call: microphone(func(_ context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[speechRequestArgs](raw)
			if err != nil {
				return nil, err
			}
			return s.speechCancelRecording(req.Principal.DeviceID, args)
		})},
		"speech.session.open": {Mutation: true, Call: microphone(func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[speechRequestArgs](raw)
			if err != nil {
				return nil, err
			}
			return s.speechOpen(ctx, req.Principal, args)
		})},
		// Frames are idempotent by sequence and digest, not by receipt.
		"speech.session.frame":  {Call: session("speech.session.frame")},
		"speech.session.finish": {Mutation: true, Call: session("speech.session.finish")},
		"speech.session.cancel": {Mutation: true, Call: session("speech.session.cancel")},
		"speech.session.events": {Call: session("speech.session.events")},
	})
}
