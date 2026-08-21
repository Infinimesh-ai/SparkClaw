package gateway

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
)

var (
	speechRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	speechLanguagePattern  = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)
)

func (s *Server) getSpeechStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.speech.Status(r.Context()))
}

func (s *Server) postSpeechTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Speech.Enabled || s.cfg.Speech.Backend == "disabled" {
		writeSpeechError(w, http.StatusServiceUnavailable, speech.NewError(speech.CodeDisabled, "speech transcription is disabled", false, nil))
		return
	}
	timeoutSeconds := s.cfg.Speech.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = config.Default().Speech.TimeoutSeconds
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	r = r.WithContext(requestCtx)

	maxUploadBytes := s.cfg.Speech.MaxUploadBytes
	if maxUploadBytes <= 0 {
		maxUploadBytes = config.Default().Speech.MaxUploadBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		if requestSizeStatus(err) == http.StatusRequestEntityTooLarge {
			writeSpeechError(w, http.StatusRequestEntityTooLarge, speech.NewError(speech.CodeTooLarge, "speech upload exceeds the configured limit", false, err))
		} else {
			writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "invalid multipart speech request", false, err))
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "multipart field \"file\" is required", false, err))
		return
	}
	defer file.Close()
	if r.MultipartForm != nil && len(r.MultipartForm.File["file"]) != 1 {
		writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "exactly one speech file is required", false, nil))
		return
	}
	for name, files := range r.MultipartForm.File {
		if name != "file" && len(files) > 0 {
			writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "unexpected speech file field", false, nil))
			return
		}
	}
	contentType := strings.ToLower(strings.TrimSpace(header.Header.Get("Content-Type")))
	if contentType != "audio/wav" && contentType != "audio/x-wav" {
		writeSpeechError(w, http.StatusUnsupportedMediaType, speech.NewError(speech.CodeUnsupported, "speech input must use audio/wav", false, nil))
		return
	}

	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	if sessionID == "" {
		writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "session_id is required", false, nil))
		return
	}
	session, ok, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeSpeechError(w, http.StatusServiceUnavailable, speech.NewError(speech.CodeUnavailable, "session service is unavailable", true, nil))
		return
	}
	if !ok || sessionOwnerID(session) != principalForRequest(r).OwnerID {
		writeSpeechError(w, http.StatusNotFound, speech.NewError(speech.CodeInvalidRequest, "session not found", false, nil))
		return
	}
	requestID := strings.TrimSpace(r.FormValue("request_id"))
	if !speechRequestIDPattern.MatchString(requestID) {
		writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "request_id is invalid", false, nil))
		return
	}
	language := strings.TrimSpace(r.FormValue("language"))
	if language == "" {
		language = s.cfg.Speech.DefaultLanguage
	}
	if language != "auto" && !speechLanguagePattern.MatchString(language) {
		writeSpeechError(w, http.StatusBadRequest, speech.NewError(speech.CodeInvalidRequest, "language must be auto or a BCP-47 tag", false, nil))
		return
	}

	audio, err := readBoundedSpeechFile(file, maxUploadBytes)
	if err != nil {
		writeSpeechError(w, http.StatusRequestEntityTooLarge, err)
		return
	}
	maxAudioSeconds := s.cfg.Speech.MaxAudioSeconds
	if maxAudioSeconds <= 0 {
		maxAudioSeconds = config.Default().Speech.MaxAudioSeconds
	}
	wavInfo, err := speech.ValidatePCM16WAV(audio, maxAudioSeconds)
	if err != nil {
		writeSpeechError(w, speechHTTPStatus(err), err)
		return
	}

	s.addSpeechAudit(requestCtx, "speech.transcription.started", sessionID, requestID, "Speech transcription started", map[string]any{
		"request_id":  requestID,
		"bytes":       len(audio),
		"duration_ms": wavInfo.DurationMS,
		"language":    language,
		"model":       s.cfg.Speech.Model,
	})
	result, err := s.speech.Transcribe(requestCtx, speech.Request{
		RequestID:  requestID,
		SessionID:  sessionID,
		Language:   language,
		PCM16WAV:   audio,
		DurationMS: wavInfo.DurationMS,
	})
	if err != nil {
		code, retryable := speech.ErrorDetails(err)
		eventType := "speech.transcription.failed"
		summary := "Speech transcription failed"
		if code == speech.CodeCancelled {
			eventType = "speech.transcription.cancelled"
			summary = "Speech transcription cancelled"
		}
		s.addSpeechAudit(requestCtx, eventType, sessionID, requestID, summary, map[string]any{
			"request_id":  requestID,
			"duration_ms": wavInfo.DurationMS,
			"code":        code,
			"retryable":   retryable,
		})
		writeSpeechError(w, speechHTTPStatus(err), err)
		return
	}
	s.addSpeechAudit(requestCtx, "speech.transcription.completed", sessionID, requestID, "Speech transcription completed", map[string]any{
		"request_id":   requestID,
		"duration_ms":  wavInfo.DurationMS,
		"inference_ms": result.InferenceMS,
		"language":     result.Language,
		"model":        result.Model,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id":             app.NewID("stt"),
		"request_id":     requestID,
		"session_id":     sessionID,
		"text":           result.Text,
		"language":       result.Language,
		"duration_ms":    wavInfo.DurationMS,
		"inference_ms":   result.InferenceMS,
		"model":          result.Model,
		"audio_retained": false,
	})
}

func readBoundedSpeechFile(file multipart.File, maxBytes int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, speech.NewError(speech.CodeInvalidRequest, "failed to read speech upload", false, err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, speech.NewError(speech.CodeTooLarge, "speech upload exceeds the configured limit", false, nil)
	}
	return raw, nil
}

func (s *Server) addSpeechAudit(ctx context.Context, eventType, sessionID, requestID, summary string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["request_id"] = requestID
	s.addAudit(ctx, app.AuditEvent{
		ID:        app.NewID("audit"),
		Time:      time.Now().UTC(),
		Type:      eventType,
		SessionID: sessionID,
		Actor:     "speech",
		Summary:   summary,
		Fields:    fields,
	})
}

func writeSpeechError(w http.ResponseWriter, status int, err error) {
	code, retryable := speech.ErrorDetails(err)
	if code == speech.CodeBusy {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, status, map[string]any{
		"error":     err.Error(),
		"code":      code,
		"retryable": retryable,
	})
}

func speechHTTPStatus(err error) int {
	code, _ := speech.ErrorDetails(err)
	switch code {
	case speech.CodeInvalidRequest, speech.CodeTooShort:
		return http.StatusBadRequest
	case speech.CodeTooLarge:
		return http.StatusRequestEntityTooLarge
	case speech.CodeUnsupported:
		return http.StatusUnsupportedMediaType
	case speech.CodeBusy:
		return http.StatusTooManyRequests
	case speech.CodeCancelled:
		return http.StatusRequestTimeout
	case speech.CodeDisabled, speech.CodeUnavailable:
		return http.StatusServiceUnavailable
	case speech.CodeTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

func requestSizeStatus(err error) int {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
