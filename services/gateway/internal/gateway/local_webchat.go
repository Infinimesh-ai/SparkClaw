package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"path"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const localWebChatIngressHeader = "X-SparkClaw-Local-Ingress"

type localWebChatEntranceKey struct{}
type localWebChatEntrance struct {
	ID     string
	Origin string
}

// handleWorkbench is an explicit opt-in at the existing route registration.
// New network handlers have no local authority until enrolled here.
func (s *Server) handleWorkbench(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, handler)
	s.localWorkbenchMux.HandleFunc(pattern, handler)
}

// LocalWebChatHandler is mounted only on its separately provisioned Unix socket.
// The ingress secret authenticates this transport, never Handler's TCP surface.
// Its principal is deliberately not a persisted, revocable device Client.
func (s *Server) LocalWebChatHandler(ownerID, actorID, accessID, tokenHash string) http.Handler {
	workbench := s.withRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := principalForRequest(r)
		_, pattern := s.localWorkbenchMux.Handler(r)
		// Store/Runtime own the resulting business audit. Record authenticated
		// local provenance once at mutation admission too, without persisting
		// bodies, URL queries, credentials, or making read requests write state.
		if principal.LocalAccessID != "" && pattern != "" {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				s.addAudit(r.Context(), app.AuditEvent{
					ID: app.NewID("audit"), Time: time.Now().UTC(), Type: "local_webchat.mutation_admitted",
					Actor: principal.ActorID, Summary: "Local WebChat mutation admitted",
					Fields: map[string]any{"route": pattern},
				})
			}
		}
		s.localWorkbenchMux.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		got := r.Header.Get(localWebChatIngressHeader)
		if got == "" || tokenHash == "" || ownerID == "" || actorID == "" || accessID == "" ||
			subtle.ConstantTimeCompare([]byte(hashSecret(got)), []byte(tokenHash)) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("private local WebChat ingress required"))
			return
		}
		// Only the private caller can attest this entrance. No request header on
		// the network listener can populate this context value.
		entrance := localWebChatEntrance{ID: accessID, Origin: "http://" + r.Host}
		ctx := context.WithValue(r.Context(), localWebChatEntranceKey{}, entrance)
		principal := requestPrincipal{OwnerID: ownerID, ActorID: actorID, LocalAccessID: accessID, Authenticated: true}
		// Presence (including an empty/malformed header) is intentional. Never
		// hide an explicit authentication failure behind local Owner authority.
		if _, present := r.Header["Authorization"]; present {
			token := bearerCredential(r.Header.Get("Authorization"))
			var ok bool
			var err error
			if token != "" {
				principal, ok, err = s.authenticateBearer(ctx, token)
			}
			if err != nil {
				status := http.StatusServiceUnavailable
				if store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
					status = http.StatusGatewayTimeout
				}
				writeError(w, status, errors.New("authentication is temporarily unavailable"))
				return
			}
			if !ok {
				writeError(w, http.StatusUnauthorized, errors.New("valid bearer token required"))
				return
			}
			connected, release, err := s.clientConnectionContext(ctx, principal.ClientID)
			if err != nil {
				writeError(w, http.StatusUnauthorized, errors.New("valid client credential required"))
				return
			}
			defer release()
			ctx = connected
		}
		r = r.WithContext(context.WithValue(ctx, requestPrincipalContextKey{}, principal))
		if r.Method == http.MethodGet && r.URL.Path == "/api/local-webchat/identity" {
			writeJSON(w, http.StatusOK, map[string]any{
				"schema_version": 1, "deployment_id": s.cfg.Gateway.DeploymentID,
				"client_id": accessID, "owner_id": ownerID, "actor_id": actorID,
			})
			return
		}
		// Never redirect a protected request as part of ServeMux path cleaning.
		// The ingress has the same check; keep the privileged socket fail-closed.
		if path.Clean(r.URL.Path) != r.URL.Path {
			writeError(w, http.StatusBadRequest, errors.New("non-canonical workbench path"))
			return
		}
		workbench.ServeHTTP(w, r)
	})
}
