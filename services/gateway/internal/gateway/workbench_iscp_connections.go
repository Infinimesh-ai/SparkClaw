package gateway

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

// connectionBindings uses only the existing owner/actor-scoped binding handlers.
// The desktop chooses a typed operation, never a destination URL or HTTP route.
func (a *iscpDomainAdapter) connectionBindings(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	switch request.Operation {
	case iscpworkbench.OperationSettingsBindingsList:
		if !connectionDomainParams(request.Params, "channel", "status") || !connectionDomainEmpty(request.Body) {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		channel, status := request.Params["channel"], request.Params["status"]
		if channel != "" && channel != "weixin" && channel != "telegram" || !connectionBindingStatus(status) {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
			query := url.Values{}
			if channel != "" {
				query.Set("channel", channel)
			}
			if status != "" {
				query.Set("status", status)
			}
			r.URL.RawQuery = query.Encode()
			a.server.listNotificationBindings(w, r)
		}, nil)
	case iscpworkbench.OperationSettingsBindingsStart:
		channel := request.Params["channel"]
		var input struct {
			DefaultForChannel bool   `json:"default_for_channel"`
			CredentialSecret  string `json:"credential_secret"`
			BotToken          string `json:"bot_token"`
		}
		if !connectionDomainParams(request.Params, "channel") || channel != "weixin" && channel != "telegram" ||
			len(request.Body) > 4096 || connectionDomainDecode(request.Body, &input) != nil {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		if channel == "weixin" && (input.CredentialSecret != "" || input.BotToken != "") {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPost, a.server.startNotificationBinding, map[string]string{"channel": channel})
	case iscpworkbench.OperationSettingsBindingsGet, iscpworkbench.OperationSettingsBindingsPoll,
		iscpworkbench.OperationSettingsBindingsRevoke, iscpworkbench.OperationSettingsBindingsLogin:
		id := request.Params["binding_id"]
		if !connectionDomainParams(request.Params, "binding_id") || !connectionBindingID(id) || !connectionDomainEmpty(request.Body) {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		params := map[string]string{"id": id}
		switch request.Operation {
		case iscpworkbench.OperationSettingsBindingsGet:
			return domainHTTP(ctx, request, http.MethodGet, a.server.getNotificationBinding, params)
		case iscpworkbench.OperationSettingsBindingsPoll:
			return domainHTTP(ctx, request, http.MethodPost, a.server.pollNotificationBinding, params)
		case iscpworkbench.OperationSettingsBindingsRevoke:
			return domainHTTP(ctx, request, http.MethodDelete, a.server.revokeNotificationBinding, params)
		default:
			request.Body = nil
			return domainHTTP(ctx, request, http.MethodGet, a.server.getNotificationBindingLoginURL, params)
		}
	}
	return domainError(http.StatusNotImplemented, "capability_unavailable")
}

func (a *iscpDomainAdapter) browserSettings(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if len(request.Params) != 0 || len(request.Body) > maxBrowserControlRequestBytes {
		return domainError(http.StatusBadRequest, "invalid_input")
	}
	if request.Operation == iscpworkbench.OperationSettingsBrowserSave {
		var input struct {
			Token string `json:"token"`
		}
		if connectionDomainDecode(request.Body, &input) != nil || input.Token == "" {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPut, a.server.putBrowserExtensionToken, nil)
	}
	if !connectionDomainEmpty(request.Body) {
		return domainError(http.StatusBadRequest, "invalid_input")
	}
	switch request.Operation {
	case iscpworkbench.OperationSettingsBrowserGet:
		return domainHTTP(ctx, request, http.MethodGet, a.server.getBrowserExtension, nil)
	case iscpworkbench.OperationSettingsBrowserCheck:
		return domainHTTP(ctx, request, http.MethodPost, a.server.checkBrowserExtension, nil)
	case iscpworkbench.OperationSettingsBrowserDelete:
		return domainHTTP(ctx, request, http.MethodDelete, a.server.deleteBrowserExtensionToken, nil)
	}
	return domainError(http.StatusNotImplemented, "capability_unavailable")
}

// getNotificationBindingLoginURL lets a desktop display the provider's QR page
// locally. It never copies a browser session or launches the backend browser.
func (s *Server) getNotificationBindingLoginURL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" || r.ContentLength > 0 {
		writeError(w, http.StatusBadRequest, errors.New("invalid notification binding login request"))
		return
	}
	record, found, err := s.store.GetNotificationBinding(r.Context(), r.PathValue("id"))
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !found || record.OwnerID != principal.OwnerID || firstEndpointActor(record) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	if record.Channel != "weixin" || !weixinproto.IsQRLoginProvider(record.Provider) ||
		!isPendingNotificationBinding(record.Status) || record.ExpiresAt == nil || !record.ExpiresAt.After(time.Now().UTC()) {
		writeError(w, http.StatusConflict, errors.New("notification binding has no pending Weixin login"))
		return
	}
	if !weixinproto.IsQRLoginURL(record.QRCodeURL) {
		writeError(w, http.StatusBadRequest, errors.New("notification binding has no valid Weixin login URL"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": record.QRCodeURL, "expires_at": record.ExpiresAt})
}

func connectionDomainParams(params map[string]string, allowed ...string) bool {
	for key := range params {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}

func connectionDomainDecode(raw []byte, output any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] != '{' {
		return errors.New("request must be a JSON object")
	}
	return domainDecode(raw, output)
}

func connectionDomainEmpty(raw []byte) bool {
	var empty struct{}
	return connectionDomainDecode(raw, &empty) == nil
}

func connectionBindingID(id string) bool {
	return id != "" && len(id) <= 256 && strings.TrimSpace(id) == id &&
		!strings.ContainsAny(id, "/\\?#\x00\r\n\t")
}

func connectionBindingStatus(status string) bool {
	switch status {
	case "", app.NotificationBindingStarting, app.NotificationBindingWaitingScan, app.NotificationBindingWaitingConfirm,
		app.NotificationBindingCredentialPending, app.NotificationBindingActive, app.NotificationBindingRevoking,
		app.NotificationBindingFailed, app.NotificationBindingExpired, app.NotificationBindingRevoked:
		return true
	default:
		return false
	}
}
