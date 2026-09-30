package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
)

// LocalManagementHandler is mounted only on a private Unix-domain listener by
// the deployment process. Its credential has no authority on Handler's network
// listener and is independent of revocable user Clients.
func (s *Server) LocalManagementHandler(ownerID, actorID, clientID, tokenHash string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workbench/identity", s.getWorkbenchIdentity)
	mux.HandleFunc("GET /api/clients", s.listClients)
	mux.HandleFunc("POST /api/clients", s.issueClient)
	mux.HandleFunc("POST /api/clients/{id}/revoke", s.revokeClient)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := bearerCredential(r.Header.Get("Authorization"))
		if got == "" || tokenHash == "" || subtle.ConstantTimeCompare([]byte(hashSecret(got)), []byte(tokenHash)) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("local management credential required"))
			return
		}
		principal := requestPrincipal{OwnerID: ownerID, ActorID: actorID, ClientID: clientID, Authenticated: true}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, principal)))
	})
}
