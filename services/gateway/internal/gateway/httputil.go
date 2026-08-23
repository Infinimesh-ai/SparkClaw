package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func readJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func queryOwnerID(r *http.Request) string {
	ownerID := strings.TrimSpace(r.URL.Query().Get("owner_id"))
	if ownerID == "" {
		return app.DefaultOwnerID
	}
	return ownerID
}

func sessionOwnerID(session app.Session) string {
	if strings.TrimSpace(session.OwnerID) == "" {
		return app.DefaultOwnerID
	}
	return strings.TrimSpace(session.OwnerID)
}

func (s *Server) sessionIDVisibleToOwner(ctx context.Context, sessionID, ownerID string) (bool, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ownerID == app.DefaultOwnerID, nil
	}
	session, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if !ok {
		return ownerID == app.DefaultOwnerID, nil
	}
	return sessionOwnerID(session) == ownerID, nil
}

func (s *Server) artifactVisibleToOwner(ctx context.Context, object app.ArtifactObject, ownerID string) (bool, error) {
	return s.sessionIDVisibleToOwner(ctx, object.SessionID, ownerID)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeSSEEvent(w http.ResponseWriter, event app.Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		return err
	}
	return nil
}

func writeNamedSSE(w http.ResponseWriter, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", name); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		return err
	}
	return nil
}

func writeSSEHeartbeat(w io.Writer) error {
	_, err := io.WriteString(w, ": ping\n\n")
	return err
}

func lastEventID(events []app.Event) string {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].ID
}

func streamVisibleEvent(typ string) bool {
	return strings.HasPrefix(typ, "tool_call.") || strings.HasPrefix(typ, "approval.")
}

func isLocalRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if parsed := strings.TrimSpace(host); parsed != "" {
		if h, _, err := net.SplitHostPort(parsed); err == nil {
			host = h
		} else {
			host = parsed
		}
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || host == "localhost" || host == ""
}

func randomSecret(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func writeError(w http.ResponseWriter, status int, err error) {
	payload := map[string]any{"error": err.Error()}
	if code := errorCode(err); code != "" {
		payload["code"] = code
	}
	var retryable interface{ Retryable() bool }
	if errors.As(err, &retryable) {
		payload["retryable"] = retryable.Retryable()
	}
	writeJSON(w, status, payload)
}

func errorCode(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return ""
}
