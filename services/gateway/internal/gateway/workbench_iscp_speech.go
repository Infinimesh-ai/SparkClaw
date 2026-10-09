package gateway

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
)

type iscpSpeechEvent struct {
	Sequence uint64               `json:"sequence"`
	Event    speech.RealtimeEvent `json:"event"`
}
type iscpSpeechSession struct {
	mu               sync.Mutex
	id, scope        string
	session          speech.RealtimeSession
	ctx              context.Context
	cancel           context.CancelFunc
	last             uint32
	samples          int64
	digests          map[uint32]string
	providerAck      uint32
	sequence, ack    uint64
	events           []iscpSpeechEvent
	finished, closed bool
}

func (a *iscpDomainAdapter) speech(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if a.server.speech == nil {
		return domainError(503, "speech_unavailable")
	}
	if request.Operation == iscpworkbench.OperationSpeechStatus {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainJSON(200, a.server.speech.Status(ctx))
	}
	principal, _, err := a.executionIdentity(ctx, request)
	if err != nil {
		return domainError(403, "installation_required")
	}
	if request.Operation == iscpworkbench.OperationSpeechTranscribe {
		return a.transcribe(ctx, request)
	}
	if request.Operation == iscpworkbench.OperationSpeechSessionOpen {
		return a.openSpeech(ctx, request, principal)
	}
	var input struct {
		SessionID    string `json:"session_id"`
		Sequence     uint32 `json:"sequence,omitempty"`
		PCM16        string `json:"pcm16,omitempty"`
		LastSequence uint32 `json:"last_sequence,omitempty"`
		TotalSamples int64  `json:"total_samples,omitempty"`
		Reason       string `json:"reason,omitempty"`
		After        uint64 `json:"after,omitempty"`
	}
	if domainDecode(request.Body, &input) != nil || input.SessionID == "" {
		return domainError(400, "invalid_input")
	}
	a.speechMu.Lock()
	session := a.speechSessions[input.SessionID]
	a.speechMu.Unlock()
	if session == nil || session.scope != a.receiptScope(principal, request.InstallationID) {
		return domainError(404, "speech_session_not_found")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	switch request.Operation {
	case iscpworkbench.OperationSpeechSessionFrame:
		if session.closed || session.finished || session.ctx.Err() != nil {
			return domainError(409, "speech_session_closed")
		}
		if len(input.PCM16) > base64.StdEncoding.EncodedLen(speech.RealtimeFrameSamples*2) {
			return domainError(413, "speech_frame_too_large")
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(input.PCM16)
		if err != nil || len(raw) == 0 || len(raw)%2 != 0 {
			return domainError(400, "speech_invalid_frame")
		}
		digest := execution.Digest(raw)
		if input.Sequence <= session.last {
			if session.digests[input.Sequence] != digest {
				return domainError(409, "speech_frame_conflict")
			}
			return domainJSON(200, map[string]any{"accepted_sequence": input.Sequence, "received_audio_ms": session.samples * 1000 / speech.RealtimeSampleRate})
		}
		if input.Sequence != session.last+1 || input.Sequence-session.providerAck > speech.RealtimeMaxUnackedMS/speech.RealtimeFrameMS {
			return domainError(409, "speech_stream_overrun")
		}
		if session.samples+int64(len(raw)/2) > int64(speechMaxAudioSeconds(a.server.cfg)*speech.RealtimeSampleRate) {
			return domainError(413, "speech_audio_too_long")
		}
		if err = session.session.WriteAudio(ctx, input.Sequence-1, raw); err != nil {
			return domainSpeechError(err)
		}
		session.last = input.Sequence
		session.samples += int64(len(raw) / 2)
		session.digests[input.Sequence] = digest
		if input.Sequence > 64 {
			delete(session.digests, input.Sequence-64)
		}
		return domainJSON(200, map[string]any{"accepted_sequence": input.Sequence, "received_audio_ms": session.samples * 1000 / speech.RealtimeSampleRate})
	case iscpworkbench.OperationSpeechSessionFinish:
		if session.closed || session.ctx.Err() != nil {
			return domainError(409, "speech_session_closed")
		}
		if session.finished {
			return domainJSON(200, map[string]bool{"finishing": true})
		}
		if input.LastSequence == 0 || input.LastSequence != session.last || input.TotalSamples != session.samples {
			return domainError(409, "speech_frame_conflict")
		}
		if err := session.session.Finish(ctx, input.LastSequence-1, input.TotalSamples*1000/speech.RealtimeSampleRate, input.Reason); err != nil {
			return domainSpeechError(err)
		}
		session.finished = true
		return domainJSON(200, map[string]bool{"finishing": true})
	case iscpworkbench.OperationSpeechCancel, iscpworkbench.OperationSpeechSessionCancel:
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
		return domainJSON(200, map[string]bool{"cancelled": true})
	case iscpworkbench.OperationSpeechSessionEvents:
		if input.After < session.ack || input.After > session.sequence {
			return domainError(409, "cursor_gap")
		}
		session.ack = input.After
		for len(session.events) > 0 && session.events[0].Sequence <= input.After {
			session.events = session.events[1:]
		}
		events := append([]iscpSpeechEvent{}, session.events...)
		return domainJSON(200, map[string]any{"session_id": session.id, "events": events, "closed": session.closed})
	default:
		return domainError(501, "capability_unavailable")
	}
}
func (a *iscpDomainAdapter) transcribe(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	var input struct {
		SessionID   string                        `json:"session_id"`
		RequestID   string                        `json:"request_id"`
		Language    string                        `json:"language"`
		AudioObject iscpworkbench.ObjectReference `json:"audio_object"`
	}
	if domainDecode(request.Body, &input) != nil || !execution.UUID(input.SessionID) || !speechRequestIDPattern.MatchString(input.RequestID) {
		return domainError(400, "invalid_input")
	}
	language := strings.TrimSpace(input.Language)
	if language == "" {
		language = a.server.cfg.Speech.DefaultLanguage
	}
	if language != "auto" && !speechLanguagePattern.MatchString(language) {
		return domainError(400, "invalid_language")
	}
	if !a.server.cfg.Speech.Enabled || a.server.cfg.Speech.Backend == "disabled" {
		return domainError(503, "speech_disabled")
	}
	purpose := input.AudioObject.Purpose
	if purpose != "speech_recording" && purpose != "speech_audio" {
		return domainError(400, "invalid_audio_object")
	}
	raw, err := domainReadObject(ctx, input.AudioObject, purpose, a.server.cfg.Speech.MaxUploadBytes)
	if err != nil {
		return domainError(409, "speech_audio_unavailable")
	}
	info, err := speech.ValidatePCM16WAV(raw, speechMaxAudioSeconds(a.server.cfg))
	if err != nil {
		return domainSpeechError(err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(a.server.cfg.Speech.TimeoutSeconds)*time.Second)
	defer cancel()
	result, err := a.server.speech.Transcribe(requestCtx, speech.Request{RequestID: input.RequestID, SessionID: input.SessionID, Language: language, PCM16WAV: raw, DurationMS: info.DurationMS})
	if err != nil {
		return domainSpeechError(err)
	}
	// The object service retains the recording only until its declared expiry or
	// an explicit release. Do not claim immediate deletion while its object exists.
	return domainJSON(200, map[string]any{"id": app.NewID("stt"), "request_id": input.RequestID, "session_id": input.SessionID, "text": result.Text, "language": result.Language, "duration_ms": info.DurationMS, "inference_ms": result.InferenceMS, "model": result.Model, "audio_retained": true, "audio_expires_at": input.AudioObject.ExpiresAt})
}
func (a *iscpDomainAdapter) openSpeech(ctx context.Context, request iscpworkbench.Request, principal requestPrincipal) iscpDomainResult {
	var input struct {
		SessionID string `json:"session_id"`
		RequestID string `json:"request_id"`
		Language  string `json:"language"`
	}
	if domainDecode(request.Body, &input) != nil || !execution.UUID(input.SessionID) || !speechRequestIDPattern.MatchString(input.RequestID) {
		return domainError(400, "invalid_input")
	}
	if input.Language == "" {
		input.Language = a.server.cfg.Speech.DefaultLanguage
	}
	if input.Language != "auto" && !speechLanguagePattern.MatchString(input.Language) {
		return domainError(400, "invalid_language")
	}
	status := a.server.speech.Status(ctx)
	if !status.Ready || !status.SupportsStreaming {
		return domainError(503, "speech_stream_unavailable")
	}
	scope := a.receiptScope(principal, request.InstallationID)
	a.speechMu.Lock()
	defer a.speechMu.Unlock()
	if len(a.speechSessions) >= 32 {
		return domainError(429, "speech_session_capacity")
	}
	count := 0
	for _, existing := range a.speechSessions {
		if existing.scope == scope {
			count++
		}
	}
	if count >= 2 {
		return domainError(429, "speech_session_capacity")
	}
	connectCtx, cancel := context.WithTimeout(ctx, speech.RealtimeConnectTimeout*time.Second)
	defer cancel()
	stream, err := a.server.speech.StartRealtime(connectCtx, speech.RealtimeRequest{RequestID: input.RequestID, SessionID: input.SessionID, Language: input.Language, MaxAudioSeconds: speechMaxAudioSeconds(a.server.cfg)})
	if err != nil {
		return domainSpeechError(err)
	}
	connected, release, err := a.server.clientConnectionContext(a.server.executionContext(), principal.ClientID)
	if err != nil {
		stream.Close()
		return domainError(401, "authorization_closed")
	}
	connected, stop := context.WithTimeout(connected, time.Duration(speechMaxAudioSeconds(a.server.cfg)+speech.RealtimeFinalTimeout+30)*time.Second)
	session := &iscpSpeechSession{id: app.NewID("iscp_speech"), scope: scope, session: stream, ctx: connected, cancel: stop, digests: map[uint32]string{}}
	a.speechSessions[session.id] = session
	go a.readSpeechEvents(session, release)
	return domainJSON(200, map[string]any{"session_id": session.id, "ready": stream.ReadyEvent()})
}
func (a *iscpDomainAdapter) readSpeechEvents(session *iscpSpeechSession, release func()) {
	defer release()
	defer session.cancel()
	defer session.session.Close()
	defer func() {
		session.mu.Lock()
		session.closed = true
		session.mu.Unlock()
		time.AfterFunc(time.Minute, func() { a.speechMu.Lock(); delete(a.speechSessions, session.id); a.speechMu.Unlock() })
	}()
	for {
		event, err := session.session.ReadEvent(session.ctx)
		if err != nil {
			session.mu.Lock()
			if !session.closed && len(session.events) < 64 {
				session.sequence++
				session.events = append(session.events, iscpSpeechEvent{session.sequence, speech.RealtimeEvent{Event: "error", Code: "speech_stream_closed"}})
			}
			session.mu.Unlock()
			return
		}
		session.mu.Lock()
		if len(session.events) >= 63 {
			session.sequence++
			session.events = append(session.events, iscpSpeechEvent{session.sequence, speech.RealtimeEvent{Event: "error", Code: "speech_stream_overrun"}})
			session.closed = true
			session.mu.Unlock()
			return
		}
		if event.AcceptedSequence != nil {
			shifted := *event.AcceptedSequence + 1
			event.AcceptedSequence = &shifted
		}
		session.sequence++
		session.events = append(session.events, iscpSpeechEvent{session.sequence, event})
		if event.AcceptedSequence != nil && *event.AcceptedSequence > session.providerAck {
			session.providerAck = *event.AcceptedSequence
		}
		terminal := event.Event == "final" || event.Event == "error" || event.Event == "cancelled"
		session.mu.Unlock()
		if terminal {
			return
		}
	}
}
func domainSpeechError(err error) iscpDomainResult {
	code, _ := speech.ErrorDetails(err)
	return domainError(speechHTTPStatus(err), code)
}
