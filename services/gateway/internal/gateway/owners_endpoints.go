package gateway

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) getOwnerProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.store.GetOwnerProfile(r.Context())
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (s *Server) updateOwnerProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string            `json:"display_name"`
		Email       string            `json:"email"`
		Preferences map[string]string `json:"preferences"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	current, err := s.store.GetOwnerProfile(r.Context())
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	profile, err := normalizeOwnerProfileInput(current, input.DisplayName, input.Email, input.Preferences)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.store.UpdateOwnerProfile(r.Context(), profile)
	updated, err = store.ReconcileOwnerProfileWrite(r.Context(), s.store, updated, err)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) listOwnerProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.ListOwnerProfiles(r.Context())
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (s *Server) getOwnerProfileByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("owner_id"))
	profile, ok, err := s.store.GetOwnerProfileByID(r.Context(), id)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("profile not found"))
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (s *Server) patchOwnerProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("owner_id"))
	current, ok, err := s.store.GetOwnerProfileByID(r.Context(), id)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("profile not found"))
		return
	}
	var input struct {
		DisplayName      string            `json:"display_name"`
		Email            string            `json:"email"`
		Preferences      map[string]string `json:"preferences"`
		Source           string            `json:"source"`
		ExternalRef      string            `json:"external_ref"`
		WorkspaceRoot    string            `json:"workspace_root"`
		DefaultChannel   string            `json:"default_channel"`
		DefaultBindingID string            `json:"default_binding_id"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	profile, err := normalizeOwnerProfileInput(current, input.DisplayName, input.Email, input.Preferences)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	profile.Source = strings.TrimSpace(input.Source)
	profile.ExternalRef = strings.TrimSpace(input.ExternalRef)
	profile.WorkspaceRoot = strings.TrimSpace(input.WorkspaceRoot)
	profile.DefaultChannel = strings.TrimSpace(input.DefaultChannel)
	profile.DefaultBindingID = strings.TrimSpace(input.DefaultBindingID)
	updated, err := s.store.SaveOwnerProfile(r.Context(), profile)
	updated, err = store.ReconcileOwnerProfileWrite(r.Context(), s.store, updated, err)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func writeOwnerStoreError(w http.ResponseWriter, err error) {
	if store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
		writeError(w, http.StatusGatewayTimeout, errors.New("owner profile request timed out"))
		return
	}
	writeError(w, http.StatusServiceUnavailable, errors.New("owner profiles are temporarily unavailable"))
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.store.ListClients(r.Context())
	if err != nil {
		if store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
			writeError(w, http.StatusGatewayTimeout, errors.New("client list request timed out"))
			return
		}
		writeError(w, http.StatusServiceUnavailable, errors.New("clients are temporarily unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": clients})
}

func (s *Server) revokeClient(w http.ResponseWriter, r *http.Request) {
	client, err := s.store.RevokeClient(r.Context(), r.PathValue("id"))
	if err != nil {
		switch store.StoreErrorCodeOf(err) {
		case store.StoreErrorNotFound:
			writeError(w, http.StatusNotFound, errors.New("client not found"))
		case store.StoreErrorTimeout:
			writeError(w, http.StatusGatewayTimeout, errors.New("client revoke request timed out"))
		default:
			writeError(w, http.StatusServiceUnavailable, errors.New("clients are temporarily unavailable"))
		}
		return
	}
	writeJSON(w, http.StatusOK, client)
}

var ownerEmailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func normalizeOwnerProfileInput(current app.OwnerProfile, displayName, email string, preferences map[string]string) (app.OwnerProfile, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return app.OwnerProfile{}, errors.New("display_name is required")
	}
	if len([]rune(displayName)) > 80 {
		return app.OwnerProfile{}, errors.New("display_name must be 80 characters or fewer")
	}
	email = strings.TrimSpace(email)
	if len(email) > 254 {
		return app.OwnerProfile{}, errors.New("email must be 254 characters or fewer")
	}
	if email != "" && !ownerEmailPattern.MatchString(email) {
		return app.OwnerProfile{}, errors.New("email must be a valid address")
	}
	normalizedPreferences := map[string]string{}
	for key, value := range preferences {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return app.OwnerProfile{}, errors.New("preference keys must be non-empty")
		}
		if strings.HasPrefix(key, "_") {
			return app.OwnerProfile{}, errors.New("preference keys must not start with underscore")
		}
		if len([]rune(key)) > 80 {
			return app.OwnerProfile{}, errors.New("preference keys must be 80 characters or fewer")
		}
		if len([]rune(value)) > 500 {
			return app.OwnerProfile{}, errors.New("preference values must be 500 characters or fewer")
		}
		normalizedPreferences[key] = value
	}
	if len(normalizedPreferences) > 50 {
		return app.OwnerProfile{}, errors.New("preferences must include 50 entries or fewer")
	}
	if current.ID == "" {
		current = app.DefaultOwnerProfile()
	}
	current.DisplayName = displayName
	current.Email = email
	current.Preferences = normalizedPreferences
	return current, nil
}
