package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

func (s *Server) startPairing(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Gateway.PairingRequired {
		writeError(w, http.StatusBadRequest, errors.New("pairing is not required"))
		return
	}
	if !isLocalRequest(r) {
		writeError(w, http.StatusForbidden, errors.New("pairing can only be started locally"))
		return
	}
	if err := s.pairing.gate.Acquire(r.Context(), 1); err != nil {
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	defer s.pairing.gate.Release(1)
	principal := principalForRequest(r)
	fingerprint := pairingStartFingerprint(principal.OwnerID)
	if pending := s.pairing.pending; pending != nil {
		if pending.kind != pairingPendingStart || pending.fingerprint != fingerprint {
			writeError(w, http.StatusConflict, errors.New("another pairing request is pending"))
			return
		}
		response, status, err := s.reconcilePendingStartLocked(r.Context(), pending)
		if err != nil {
			writeError(w, status, err)
			return
		}
		writeJSON(w, status, response)
		return
	}
	code, err := randomSecret(8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := s.pairing.currentTime()
	pairing := app.PairingCode{
		ID:        app.NewID("pair"),
		CodeHash:  hashSecret(code),
		Status:    "pending",
		ExpiresAt: now.Add(5 * time.Minute),
	}
	pending := &pairingPending{
		kind: pairingPendingStart, fingerprint: fingerprint, expiresAt: pairing.ExpiresAt,
		plaintextCode: code, attemptedPairing: pairing,
	}
	s.installPairingPendingLocked(pending)
	saved, err := s.store.SavePairingCode(r.Context(), pairing)
	if saved.ID != "" {
		pending.pairingCandidate = &saved
	}
	if err != nil {
		if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
			response, status, reconcileErr := s.reconcilePendingStartLocked(r.Context(), pending)
			if reconcileErr != nil {
				writeError(w, status, reconcileErr)
				return
			}
			writeJSON(w, status, response)
			return
		}
		s.clearPairingPendingLocked()
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	response, status, err := s.completePendingStartLocked(pending, saved)
	if err != nil {
		writeError(w, status, err)
		return
	}
	writeJSON(w, status, response)
}

func (s *Server) claimPairing(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Gateway.PairingRequired {
		writeError(w, http.StatusBadRequest, errors.New("pairing is not required"))
		return
	}
	var input struct {
		PairingID  string `json:"pairing_id"`
		Code       string `json:"code"`
		ClientName string `json:"client_name"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid pairing request"))
		return
	}
	if err := s.pairing.gate.Acquire(r.Context(), 1); err != nil {
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	defer s.pairing.gate.Release(1)
	pairingID := strings.TrimSpace(input.PairingID)
	clientName := strings.TrimSpace(input.ClientName)
	if clientName == "" {
		clientName = "SparkClaw Client"
	}
	principal := principalForRequest(r)
	submittedHash := hashSecret(input.Code)
	fingerprint := pairingClaimFingerprint(principal.OwnerID, pairingID, submittedHash, clientName)
	if pending := s.pairing.pending; pending != nil {
		if pending.kind != pairingPendingClaim {
			writeError(w, http.StatusConflict, errors.New("another pairing request is pending"))
			return
		}
		if !pairingCodesMatch(pending.submittedCodeHash, submittedHash) {
			writeError(w, http.StatusUnauthorized, errors.New("invalid pairing code"))
			return
		}
		if pending.fingerprint != fingerprint {
			writeError(w, http.StatusConflict, errors.New("another pairing request is pending"))
			return
		}
		response, status, err := s.reconcilePendingClaimLocked(r.Context(), pending)
		if err != nil {
			writeError(w, status, err)
			return
		}
		writeJSON(w, status, response)
		return
	}
	pairing, ok, err := s.store.GetPairingCode(r.Context(), pairingID)
	if err != nil {
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("pairing code not found"))
		return
	}
	if pairing.Status != "pending" || !pairing.ExpiresAt.After(s.pairing.currentTime()) {
		writeError(w, http.StatusBadRequest, errors.New("pairing code is not active"))
		return
	}
	if !pairingCodesMatch(pairing.CodeHash, submittedHash) {
		writeError(w, http.StatusUnauthorized, errors.New("invalid pairing code"))
		return
	}
	token, err := randomSecret(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	client := app.Client{
		ID:        app.NewID("client"),
		OwnerID:   principal.OwnerID,
		ActorID:   principal.ActorID,
		Name:      clientName,
		TokenHash: hashSecret(token),
	}
	pending := &pairingPending{
		kind: pairingPendingClaim, fingerprint: fingerprint, expiresAt: pairing.ExpiresAt,
		plaintextToken: token, preClaimPairing: pairing, attemptedClient: client, submittedCodeHash: pairing.CodeHash,
	}
	s.installPairingPendingLocked(pending)
	claimedPairing, claimedClient, err := s.store.ClaimPairingCode(r.Context(), pairing.ID, client)
	if claimedPairing.ID != "" {
		pending.pairingCandidate = &claimedPairing
	}
	if claimedClient.ID != "" {
		pending.clientCandidate = &claimedClient
	}
	if err != nil {
		if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
			response, status, reconcileErr := s.reconcilePendingClaimLocked(r.Context(), pending)
			if reconcileErr != nil {
				writeError(w, status, reconcileErr)
				return
			}
			writeJSON(w, status, response)
			return
		}
		s.clearPairingPendingLocked()
		if store.StoreErrorCodeOf(err) == store.StoreErrorConflict || store.StoreErrorCodeOf(err) == store.StoreErrorNotFound {
			writeError(w, http.StatusConflict, errors.New("pairing state changed"))
			return
		}
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	response, status, err := s.completePendingClaimLocked(pending, claimedPairing, claimedClient)
	if err != nil {
		writeError(w, status, err)
		return
	}
	writeJSON(w, status, response)
}

func (s *Server) listNotificationBindings(w http.ResponseWriter, r *http.Request) {
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	principal := principalForRequest(r)
	visible := []app.NotificationBinding{}
	bindings, err := s.store.ListNotificationBindings(r.Context(), channel, status)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	for _, binding := range bindings {
		actorID := strings.TrimSpace(binding.ActorID)
		if actorID == "" {
			actorID = binding.OwnerID
		}
		if binding.OwnerID == principal.OwnerID && actorID == principal.ActorID {
			visible = append(visible, binding)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bindings": publicNotificationBindings(visible),
	})
}

func (s *Server) listConnectors(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	principal := principalForRequest(r)
	statuses, err := s.connectors.ListStatus(r.Context(), principal.OwnerID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": statuses})
}

func (s *Server) updateConnector(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	channel := strings.ToLower(strings.TrimSpace(r.PathValue("channel")))
	if channel == "" {
		writeError(w, http.StatusBadRequest, errors.New("channel is required"))
		return
	}
	var input struct {
		Enabled         *bool  `json:"enabled"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := readJSON(r, &input); err != nil || input.Enabled == nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 {
		writeError(w, http.StatusBadRequest, errors.New("enabled and a non-negative expected_version are required"))
		return
	}
	principal := principalForRequest(r)
	status, err := s.connectors.SetEnabled(r.Context(), principal.OwnerID, principal.ActorID, channel, *input.Enabled, *input.ExpectedVersion)
	if err != nil {
		httpStatus := http.StatusBadRequest
		if errors.Is(err, store.ErrConnectorSettingConflict) {
			httpStatus = http.StatusConflict
		}
		writeError(w, httpStatus, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) startNotificationBinding(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	channel := strings.ToLower(strings.TrimSpace(r.PathValue("channel")))
	if channel == "" {
		writeError(w, http.StatusBadRequest, errors.New("channel is required"))
		return
	}
	var input struct {
		DefaultForChannel bool   `json:"default_for_channel"`
		CredentialSecret  string `json:"credential_secret"`
		BotToken          string `json:"bot_token"`
	}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := readJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, errors.New("invalid notification binding request"))
			return
		}
	}
	principal := principalForRequest(r)
	requested := app.NotificationBinding{
		OwnerID:           principal.OwnerID,
		ActorID:           principal.ActorID,
		Channel:           channel,
		DefaultForChannel: input.DefaultForChannel,
		Scopes:            app.DefaultMessagingBindingScopes(),
	}
	credentialSecret := strings.TrimSpace(input.CredentialSecret)
	if credentialSecret == "" {
		credentialSecret = input.BotToken
	}
	started, err := s.connectors.StartNotificationBinding(r.Context(), requested, binding.StartOptions{CredentialSecret: credentialSecret})
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, publicNotificationBinding(started, true))
}

func (s *Server) getNotificationBinding(w http.ResponseWriter, r *http.Request) {
	bindingID := strings.TrimSpace(r.PathValue("id"))
	binding, ok, err := s.store.GetNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !ok || binding.OwnerID != principal.OwnerID || firstEndpointActor(binding) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	writeJSON(w, http.StatusOK, publicNotificationBinding(binding))
}

func (s *Server) pollNotificationBinding(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	current, ok, err := s.store.GetNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !ok || current.OwnerID != principal.OwnerID || firstEndpointActor(current) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	updated, err := s.connectors.PollNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	if !isPendingNotificationBinding(updated.Status) {
		s.closeNotificationBindingBrowser(r.Context(), updated)
	}
	writeJSON(w, http.StatusOK, publicNotificationBinding(updated))
}

func (s *Server) openNotificationBindingBrowser(w http.ResponseWriter, r *http.Request) {
	bindingID := strings.TrimSpace(r.PathValue("id"))
	binding, ok, err := s.store.GetNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !ok || binding.OwnerID != principal.OwnerID || firstEndpointActor(binding) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	if binding.Channel != "weixin" || !weixinproto.IsQRLoginProvider(binding.Provider) || !isPendingNotificationBinding(binding.Status) ||
		(binding.ExpiresAt != nil && !binding.ExpiresAt.After(time.Now().UTC())) {
		writeError(w, http.StatusConflict, errors.New("notification binding has no pending Weixin login"))
		return
	}
	if !weixinproto.IsQRLoginURL(binding.QRCodeURL) {
		writeError(w, http.StatusBadRequest, errors.New("notification binding has no valid Weixin login URL"))
		return
	}
	if s.managedBrowserWindows == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("managed Chromium is unavailable"))
		return
	}
	bindingExpiresAt := time.Time{}
	if binding.ExpiresAt != nil {
		bindingExpiresAt = *binding.ExpiresAt
	}
	if err := s.managedBrowserWindows.OpenManagedBrowserWindow(r.Context(), binding.OwnerID, binding.ID, binding.QRCodeURL, bindingExpiresAt); err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("open managed Chromium: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": true})
}

func isPendingNotificationBinding(status string) bool {
	return status == app.NotificationBindingWaitingScan || status == app.NotificationBindingWaitingConfirm
}

func (s *Server) closeNotificationBindingBrowser(ctx context.Context, binding app.NotificationBinding) {
	if s.managedBrowserWindows == nil || binding.Channel != "weixin" || !weixinproto.IsQRLoginProvider(binding.Provider) {
		return
	}
	if err := s.managedBrowserWindows.CloseManagedBrowserWindow(ctx, binding.OwnerID, binding.ID); err != nil {
		slog.Warn("failed to close managed Weixin login window", "binding_id", binding.ID, "error", err)
	}
}

func (s *Server) revokeNotificationBinding(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	binding, ok, err := s.store.GetNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !ok || binding.OwnerID != principal.OwnerID || firstEndpointActor(binding) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	revoked, err := s.connectors.RevokeNotificationBinding(r.Context(), bindingID)
	if err != nil {
		s.closeNotificationBindingBrowser(r.Context(), binding)
		writeConnectorError(w, err)
		return
	}
	s.closeNotificationBindingBrowser(r.Context(), binding)
	writeJSON(w, http.StatusOK, publicNotificationBinding(revoked))
}

func connectorStartStatus(code string) int {
	switch code {
	case binding.CodeOperatorDisabled:
		return http.StatusForbidden
	case binding.CodeUserDisabled:
		return http.StatusConflict
	case binding.CodeBindingInProgress, binding.CodeBindingActive:
		return http.StatusConflict
	case binding.CodeConnectorUnavailable:
		return http.StatusServiceUnavailable
	case credential.CodeInvalid:
		return http.StatusBadRequest
	case credential.CodeCanceled:
		return http.StatusRequestTimeout
	case credential.CodeUnavailable, credential.CodeKeyUnavailable, credential.CodeUnsealFailed:
		return http.StatusServiceUnavailable
	case binding.CodeInvalidBotToken:
		return http.StatusBadRequest
	case binding.CodeTelegramRateLimited:
		return http.StatusTooManyRequests
	case binding.CodeTelegramUnavailable:
		return http.StatusServiceUnavailable
	case binding.CodeTelegramUnreachable, binding.CodeTelegramVerifyFailed:
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func writeConnectorError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("notification binding changed"))
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("notification binding request is invalid"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, &credential.Error{Code: credential.CodeCanceled})
	case store.StoreErrorTimeout, store.StoreErrorUnavailable, store.StoreErrorDurability,
		store.StoreErrorUnknownOutcome, store.StoreErrorCorrupt, store.StoreErrorInternal:
		writeError(w, http.StatusServiceUnavailable, &binding.BindingError{Code: binding.CodeConnectorUnavailable})
	default:
		writeError(w, connectorStartStatus(errorCode(err)), err)
	}
}
