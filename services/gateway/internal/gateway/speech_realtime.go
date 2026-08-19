package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/gorilla/websocket"
)

const maxSpeechRealtimeControlBytes = 4 << 10

type speechRealtimeTicket struct {
	id              string
	tokenHash       string
	ownerID         string
	sessionID       string
	requestID       string
	language        string
	maxAudioSeconds int
	expiresAt       time.Time
	session         speech.RealtimeSession
	timer           *time.Timer
}

type speechRealtimeSessionRequest struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Language  string `json:"language"`
}

func (s *Server) postSpeechRealtimeSession(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Speech.Enabled || s.cfg.Speech.Backend == "disabled" {
		writeSpeechError(w, http.StatusServiceUnavailable, speech.NewError(speech.CodeDisabled, "speech transcription is disabled", false, nil))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSpeechRealtimeControlBytes)
	var input speechRealtimeSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "invalid realtime speech request", false, err))
		return
	}
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Language = strings.TrimSpace(input.Language)
	if input.Language == "" {
		input.Language = s.cfg.Speech.DefaultLanguage
	}
	if input.SessionID == "" || !speechRequestIDPattern.MatchString(input.RequestID) ||
		(input.Language != "auto" && !speechLanguagePattern.MatchString(input.Language)) {
		writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "realtime speech request is invalid", false, nil))
		return
	}
	principal := principalForRequest(r)
	sessionRecord, ok := s.store.GetSession(input.SessionID)
	if !ok || sessionOwnerID(sessionRecord) != principal.OwnerID {
		writeSpeechError(w, http.StatusNotFound, speech.NewError(speech.CodeInvalidRequest, "session not found", false, nil))
		return
	}
	connectCtx, cancel := context.WithTimeout(r.Context(), time.Duration(speech.RealtimeConnectTimeout)*time.Second)
	defer cancel()
	realtime, err := s.speech.StartRealtime(connectCtx, speech.RealtimeRequest{
		RequestID: input.RequestID, SessionID: input.SessionID, Language: input.Language,
		MaxAudioSeconds: speechMaxAudioSeconds(s.cfg),
	})
	if err != nil {
		writeSpeechError(w, speechHTTPStatus(err), err)
		return
	}
	token, err := randomSecret(32)
	if err != nil {
		_ = realtime.Close()
		writeSpeechError(w, http.StatusInternalServerError, speech.NewError(speech.CodeInferenceFailed, "failed to create realtime speech ticket", false, err))
		return
	}
	now := time.Now().UTC()
	ticket := &speechRealtimeTicket{
		id: app.NewID("speech-rt"), tokenHash: hashSecret(token), ownerID: principal.OwnerID,
		sessionID: input.SessionID, requestID: input.RequestID, language: input.Language,
		maxAudioSeconds: speechMaxAudioSeconds(s.cfg),
		expiresAt:       now.Add(time.Duration(speech.RealtimeTicketTTL) * time.Second), session: realtime,
	}
	s.speechRealtimeMu.Lock()
	s.speechRealtimeTickets[ticket.tokenHash] = ticket
	s.speechRealtimeTicketIDs[ticket.id] = ticket.tokenHash
	ticket.timer = time.AfterFunc(time.Until(ticket.expiresAt), func() { s.expireSpeechRealtimeTicket(ticket.id, ticket.tokenHash) })
	s.speechRealtimeMu.Unlock()
	s.addSpeechAudit("speech.realtime.admitted", ticket.sessionID, ticket.requestID, "Realtime speech session admitted", map[string]any{
		"request_id": ticket.requestID, "model": s.cfg.Speech.Model, "protocol": speech.RealtimeProtocol,
	})
	ready := realtime.ReadyEvent()
	ready.Limits = &speech.RealtimeLimits{
		MaxAudioSeconds: ticket.maxAudioSeconds,
		MaxFrameSamples: speech.RealtimeFrameSamples,
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         ticket.id,
		"url":        "/api/speech/realtime?ticket=" + url.QueryEscape(token),
		"expires_at": ticket.expiresAt,
		"protocol":   speech.RealtimeProtocol,
		"format":     ready.Format,
		"limits":     ready.Limits,
	})
}

func (s *Server) deleteSpeechRealtimeSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	principal := principalForRequest(r)
	ticket := s.removeSpeechRealtimeTicketByID(id, principal.OwnerID)
	if ticket == nil {
		writeSpeechError(w, http.StatusNotFound, speech.NewError(speech.CodeInvalidRequest, "realtime speech session not found", false, nil))
		return
	}
	_ = ticket.session.Close()
	s.addSpeechAudit("speech.realtime.cancelled", ticket.sessionID, ticket.requestID, "Realtime speech session cancelled before connection", map[string]any{
		"request_id": ticket.requestID, "code": speech.CodeCancelled,
	})
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}

func (s *Server) getSpeechRealtime(w http.ResponseWriter, r *http.Request) {
	ticket := s.consumeSpeechRealtimeTicket(r.URL.Query().Get("ticket"))
	if ticket == nil {
		writeSpeechError(w, http.StatusUnauthorized, speech.NewError(speech.CodeInvalidRequest, "realtime speech ticket is invalid or expired", false, nil))
		return
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: time.Duration(speech.RealtimeConnectTimeout) * time.Second,
		CheckOrigin:      sameOriginWebSocket,
	}
	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		_ = ticket.session.Close()
		return
	}
	defer client.Close()
	defer ticket.session.Close()
	client.SetReadLimit(8 + speech.RealtimeFrameSamples*2)
	var writeMu sync.Mutex
	if err := writeRealtimeClientJSON(client, &writeMu, ticket.session.ReadyEvent()); err != nil {
		return
	}

	opCtx, cancel := context.WithTimeout(r.Context(), time.Duration(speechMaxAudioSeconds(s.cfg)+speech.RealtimeFinalTimeout+5)*time.Second)
	defer cancel()
	var acknowledged atomic.Int64
	clientResults := make(chan speechRealtimeRelayResult, 1)
	serverResults := make(chan speechRealtimeRelayResult, 1)
	go func() {
		clientResults <- relayRealtimeClient(opCtx, client, ticket.session, &acknowledged, ticket.maxAudioSeconds)
	}()
	go func() { serverResults <- relayRealtimeServer(opCtx, client, ticket.session, &writeMu, &acknowledged) }()

	result := waitSpeechRealtimeRelay(clientResults, serverResults, ticket.session)
	if result.code != "" && !result.alreadySent && result.kind != "cancelled" && result.kind != "disconnected" {
		event := "error"
		if result.retryable {
			event = "fallback"
		}
		_ = writeRealtimeClientJSON(client, &writeMu, speech.RealtimeEvent{Event: event, Code: result.code, Retryable: result.retryable})
	}
	_ = ticket.session.Close()
	_ = client.Close()
	if result.kind == "final" {
		s.addSpeechAudit("speech.realtime.completed", ticket.sessionID, ticket.requestID, "Realtime speech transcription completed", map[string]any{
			"request_id": ticket.requestID, "duration_ms": result.durationMS, "revisions": result.revision,
			"model": s.cfg.Speech.Model,
		})
	} else if result.kind != "disconnected" {
		s.addSpeechAudit("speech.realtime.failed", ticket.sessionID, ticket.requestID, "Realtime speech transcription ended", map[string]any{
			"request_id": ticket.requestID, "code": result.code, "retryable": result.retryable,
		})
	}
}

type speechRealtimeRelayResult struct {
	kind        string
	code        string
	retryable   bool
	alreadySent bool
	durationMS  int64
	revision    int64
}

func waitSpeechRealtimeRelay(
	clientResults <-chan speechRealtimeRelayResult,
	serverResults <-chan speechRealtimeRelayResult,
	session speech.RealtimeSession,
) speechRealtimeRelayResult {
	var finalTimer *time.Timer
	var finalTimeout <-chan time.Time
	for {
		select {
		case result := <-clientResults:
			if result.kind != "finish" {
				return result
			}
			finalTimer = time.NewTimer(time.Duration(speech.RealtimeFinalTimeout) * time.Second)
			finalTimeout = finalTimer.C
			clientResults = nil
		case result := <-serverResults:
			if finalTimer != nil {
				finalTimer.Stop()
			}
			return result
		case <-finalTimeout:
			_ = session.Close()
			return speechRealtimeRelayResult{kind: "fallback", code: speech.CodeTimeout, retryable: true}
		}
	}
}

func relayRealtimeClient(
	ctx context.Context,
	client *websocket.Conn,
	session speech.RealtimeSession,
	acknowledged *atomic.Int64,
	maxAudioSeconds int,
) speechRealtimeRelayResult {
	var expectedSequence uint32
	var totalSamples int64
	for {
		if deadline, ok := ctx.Deadline(); ok {
			_ = client.SetReadDeadline(deadline)
		}
		messageType, payload, err := client.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) && (closeErr.Code == websocket.CloseNormalClosure || closeErr.Code == websocket.CloseGoingAway) {
				return speechRealtimeRelayResult{kind: "disconnected"}
			}
			return speechRealtimeRelayResult{kind: "disconnected"}
		}
		switch messageType {
		case websocket.BinaryMessage:
			sequence, pcm, code := validateRealtimeAudioFrame(payload, expectedSequence)
			if code != "" {
				return speechRealtimeRelayResult{kind: "error", code: code}
			}
			if realtimeAudioOverrun(expectedSequence, acknowledged.Load()) {
				return speechRealtimeRelayResult{kind: "fallback", code: speech.CodeStreamOverrun, retryable: true}
			}
			if totalSamples+int64(len(pcm)/2) > int64(speech.RealtimeSampleRate*maxAudioSeconds) {
				return speechRealtimeRelayResult{kind: "error", code: speech.CodeTooLarge}
			}
			if err := session.WriteAudio(ctx, sequence, pcm); err != nil {
				code, retryable := speech.ErrorDetails(err)
				return speechRealtimeRelayResult{kind: "fallback", code: code, retryable: retryable}
			}
			totalSamples += int64(len(pcm) / 2)
			expectedSequence++
		case websocket.TextMessage:
			control, code := decodeRealtimeControl(payload)
			if code != "" {
				return speechRealtimeRelayResult{kind: "error", code: code}
			}
			lastSequence := uint32(0)
			if expectedSequence > 0 {
				lastSequence = expectedSequence - 1
			}
			if control.Event == "cancel" {
				_ = session.Cancel(ctx, lastSequence)
				return speechRealtimeRelayResult{kind: "cancelled", code: speech.CodeCancelled}
			}
			if expectedSequence == 0 || control.LastSequence != int64(lastSequence) ||
				absInt64(control.CapturedMS-samplesToMS(totalSamples)) > 10 {
				return speechRealtimeRelayResult{kind: "error", code: speech.CodeStreamProtocol}
			}
			if err := session.Finish(ctx, lastSequence, control.CapturedMS, control.Reason); err != nil {
				code, retryable := speech.ErrorDetails(err)
				return speechRealtimeRelayResult{kind: "fallback", code: code, retryable: retryable}
			}
			return speechRealtimeRelayResult{kind: "finish"}
		default:
			return speechRealtimeRelayResult{kind: "error", code: speech.CodeStreamProtocol}
		}
	}
}

func realtimeAudioOverrun(nextSequence uint32, acknowledgedCount int64) bool {
	return (int64(nextSequence)-acknowledgedCount)*speech.RealtimeFrameMS >= speech.RealtimeMaxUnackedMS
}

func relayRealtimeServer(
	ctx context.Context,
	client *websocket.Conn,
	session speech.RealtimeSession,
	writeMu *sync.Mutex,
	acknowledged *atomic.Int64,
) speechRealtimeRelayResult {
	var revision int64
	for {
		event, err := session.ReadEvent(ctx)
		if err != nil {
			code, retryable := speech.ErrorDetails(err)
			return speechRealtimeRelayResult{kind: "fallback", code: code, retryable: retryable}
		}
		switch event.Event {
		case "ack":
			if event.AcceptedSequence == nil {
				return speechRealtimeRelayResult{kind: "error", code: speech.CodeStreamProtocol}
			}
			acknowledged.Store(int64(*event.AcceptedSequence) + 1)
		case "partial":
			if event.Revision <= revision {
				return speechRealtimeRelayResult{kind: "error", code: speech.CodeStreamProtocol}
			}
			revision = event.Revision
		case "final":
			if event.Revision <= revision {
				return speechRealtimeRelayResult{kind: "error", code: speech.CodeStreamProtocol}
			}
			revision = event.Revision
		case "fallback", "error":
		default:
			return speechRealtimeRelayResult{kind: "error", code: speech.CodeStreamProtocol}
		}
		if err := writeRealtimeClientJSON(client, writeMu, event); err != nil {
			return speechRealtimeRelayResult{kind: "disconnected"}
		}
		switch event.Event {
		case "final":
			return speechRealtimeRelayResult{kind: "final", alreadySent: true, durationMS: event.DurationMS, revision: revision}
		case "fallback", "error":
			return speechRealtimeRelayResult{
				kind: event.Event, code: event.Code, retryable: event.Retryable, alreadySent: true,
			}
		}
	}
}

func validateRealtimeAudioFrame(payload []byte, expected uint32) (uint32, []byte, string) {
	if len(payload) < 8 {
		return 0, nil, speech.CodeStreamProtocol
	}
	sequence := binary.BigEndian.Uint32(payload[0:4])
	sampleCount := binary.BigEndian.Uint32(payload[4:8])
	if sequence != expected || sampleCount == 0 || sampleCount > speech.RealtimeFrameSamples ||
		len(payload) != 8+int(sampleCount)*2 {
		return 0, nil, speech.CodeStreamProtocol
	}
	return sequence, payload[8:], ""
}

type realtimeControl struct {
	Event        string `json:"event"`
	LastSequence int64  `json:"last_sequence"`
	CapturedMS   int64  `json:"captured_ms"`
	Reason       string `json:"reason"`
}

func decodeRealtimeControl(payload []byte) (realtimeControl, string) {
	if len(payload) > maxSpeechRealtimeControlBytes {
		return realtimeControl{}, speech.CodeStreamProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var control realtimeControl
	if err := decoder.Decode(&control); err != nil {
		return realtimeControl{}, speech.CodeStreamProtocol
	}
	if control.Event == "cancel" {
		return control, ""
	}
	if control.Event != "finish" || (control.Reason != "manual_stop" && control.Reason != "silence_stop" && control.Reason != "max_duration") {
		return realtimeControl{}, speech.CodeStreamProtocol
	}
	return control, ""
}

func writeRealtimeClientJSON(client *websocket.Conn, mu *sync.Mutex, event speech.RealtimeEvent) error {
	mu.Lock()
	defer mu.Unlock()
	_ = client.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return client.WriteJSON(event)
}

func sameOriginWebSocket(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}

func speechMaxAudioSeconds(cfg config.Config) int {
	if cfg.Speech.MaxAudioSeconds > 0 {
		return cfg.Speech.MaxAudioSeconds
	}
	return config.Default().Speech.MaxAudioSeconds
}

func samplesToMS(samples int64) int64 {
	return (samples*1000 + speech.RealtimeSampleRate/2) / speech.RealtimeSampleRate
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func (s *Server) consumeSpeechRealtimeTicket(raw string) *speechRealtimeTicket {
	if len(raw) < 32 || len(raw) > 128 {
		return nil
	}
	hash := hashSecret(raw)
	s.speechRealtimeMu.Lock()
	ticket := s.speechRealtimeTickets[hash]
	if ticket != nil {
		delete(s.speechRealtimeTickets, hash)
		delete(s.speechRealtimeTicketIDs, ticket.id)
		if ticket.timer != nil {
			ticket.timer.Stop()
		}
	}
	s.speechRealtimeMu.Unlock()
	if ticket == nil {
		return nil
	}
	if time.Now().UTC().After(ticket.expiresAt) {
		_ = ticket.session.Close()
		return nil
	}
	return ticket
}

func (s *Server) removeSpeechRealtimeTicketByID(id, ownerID string) *speechRealtimeTicket {
	s.speechRealtimeMu.Lock()
	defer s.speechRealtimeMu.Unlock()
	hash := s.speechRealtimeTicketIDs[id]
	ticket := s.speechRealtimeTickets[hash]
	if ticket == nil || ticket.ownerID != ownerID {
		return nil
	}
	delete(s.speechRealtimeTickets, hash)
	delete(s.speechRealtimeTicketIDs, id)
	if ticket.timer != nil {
		ticket.timer.Stop()
	}
	return ticket
}

func (s *Server) expireSpeechRealtimeTicket(id, tokenHash string) {
	s.speechRealtimeMu.Lock()
	ticket := s.speechRealtimeTickets[tokenHash]
	if ticket == nil || ticket.id != id {
		s.speechRealtimeMu.Unlock()
		return
	}
	delete(s.speechRealtimeTickets, tokenHash)
	delete(s.speechRealtimeTicketIDs, id)
	s.speechRealtimeMu.Unlock()
	_ = ticket.session.Close()
	s.addSpeechAudit("speech.realtime.expired", ticket.sessionID, ticket.requestID, "Realtime speech session expired before connection", map[string]any{
		"request_id": ticket.requestID, "code": speech.CodeTimeout,
	})
}
