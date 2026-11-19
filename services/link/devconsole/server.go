// Package devconsole is the SparkClaw self-hosted development console
// (console design 3.1–3.6): domains, device binding, one-time SparkClaw
// access credentials, Grants with bounded automatic renewal, the device
// directory, audit and the pages. It never relays or stores messages.
// Devices reach it only through consoleapi.Client.
package devconsole

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

// Options configures a Server.
type Options struct {
	// DataDir holds state.json, account.json, signer.json and the TLS files.
	DataDir string
	// PublicURL is the address devices use; it goes into issued
	// credentials, and devices accept only https consoles. TLSPin is the pin of the certificate served there.
	PublicURL string
	TLSPin    string
	// Name is shown to devices in the Trust response.
	Name string
	// Relay is the Relay devices are registered at; RelayAdminToken is its
	// operator token. RelayHTTPClient overrides the pinned client built
	// from Relay.TLSCertificatePEM.
	Relay           relay.Endpoint
	RelayAdminToken string
	RelayHTTPClient *http.Client
	// AdminPassword initializes the account when none exists.
	AdminPassword string
	// SecureCookies marks the session cookie Secure (on under TLS).
	SecureCookies bool
	// Location formats times on the pages (default Asia/Shanghai, else Local).
	Location *time.Location
	// Now is the console's clock for credentials, Grants and renewal; ProofNow
	// is the clock device proofs are checked against. Both default to
	// time.Now; tests move Now without breaking fresh proofs.
	Now      func() time.Time
	ProofNow func() time.Time
	Logger   *log.Logger
}

// Server is the dev console.
type Server struct {
	opts      Options
	provider  crypto.Provider
	signer    identity.Device
	endpoint  relay.Endpoint
	relayHTTP *http.Client
	consoleID string
	logger    *log.Logger

	mu sync.Mutex // guards st
	st *state

	joinMu sync.Mutex // serializes joins across the Relay call

	nonceMu sync.Mutex
	nonces  map[string]time.Time

	sessMu     sync.Mutex
	sessions   map[string]*session
	loginFails []time.Time

	pages map[string]*template.Template
	login *template.Template
}

// Proof lifetimes.
const (
	joinProofTTL   = 5 * time.Minute
	signedProofTTL = 2 * time.Minute
)

// New opens (or initializes) the console in opts.DataDir.
func New(opts Options) (*Server, error) {
	if err := EnsureDataDir(opts.DataDir); err != nil {
		return nil, err
	}
	if opts.Relay.DomainID == "" || opts.Relay.RelayID == "" || opts.Relay.BaseURL == "" {
		return nil, errors.New("relay endpoint is incomplete")
	}
	if opts.RelayAdminToken == "" {
		return nil, errors.New("relay admin token is required")
	}
	if !strings.HasPrefix(opts.PublicURL, "https://") && !strings.HasPrefix(opts.PublicURL, "http://") {
		return nil, errors.New("public URL must be an absolute http(s) URL")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ProofNow == nil {
		opts.ProofNow = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = log.New(io.Discard, "", 0)
	}
	if opts.Location == nil {
		if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
			opts.Location = loc
		} else {
			opts.Location = time.Local
		}
	}
	if opts.Name == "" {
		opts.Name = "SparkClaw dev console"
	}
	acct, err := loadAccount(opts.DataDir)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		if opts.AdminPassword == "" {
			return nil, errors.New("no administrator account: give an admin password to initialize it")
		}
		if err := SetPassword(opts.DataDir, opts.AdminPassword); err != nil {
			return nil, err
		}
	}
	st, err := loadState(opts.DataDir)
	if err != nil {
		return nil, err
	}
	if err := saveState(opts.DataDir, st); err != nil {
		return nil, err
	}
	provider := crypto.NewProvider()
	signer, err := loadOrCreateSigner(opts.DataDir, st.ConsoleID, opts.Relay.DomainID, provider)
	if err != nil {
		return nil, err
	}
	relayHTTP := opts.RelayHTTPClient
	if relayHTTP == nil {
		cfg, err := relay.PinnedTLSConfig(opts.Relay.TLSCertificatePEM)
		if err != nil {
			return nil, fmt.Errorf("relay certificate: %w", err)
		}
		relayHTTP = relay.HTTPClient(cfg, 20*time.Second)
	}
	s := &Server{
		opts:      opts,
		provider:  provider,
		signer:    signer,
		endpoint:  opts.Relay,
		relayHTTP: relayHTTP,
		consoleID: st.ConsoleID,
		logger:    opts.Logger,
		st:        st,
		nonces:    map[string]time.Time{},
		sessions:  map[string]*session{},
	}
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

// ConsoleID returns the console's identifier (the device proof audience).
func (s *Server) ConsoleID() string { return s.consoleID }

// Signer returns the console's trust-root identity.
func (s *Server) Signer() identity.DeviceIdentity { return s.signer.Identity }

func (s *Server) now() time.Time { return s.opts.Now().UTC() }

// Handler returns the console's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "console_id": s.consoleID})
	})
	mux.HandleFunc("GET /assets/console.css", s.handleCSS)

	// Device-facing interface (consoleapi.Client).
	mux.HandleFunc("GET /v1/link/trust", s.handleTrust)
	mux.HandleFunc("POST /v1/link/join", s.handleJoin)
	mux.HandleFunc("POST /v1/link/grant", s.handleGrant)
	mux.HandleFunc("POST /v1/link/relay-access", s.handleRelayAccess)
	mux.HandleFunc("POST /v1/link/directory", s.handleDirectory)
	mux.HandleFunc("POST /v1/link/host", s.handleHost)
	mux.HandleFunc("POST /v1/link/leave", s.handleLeave)

	// Administrator JSON interface.
	mux.HandleFunc("POST /v1/accounts/login", s.apiLogin)
	mux.HandleFunc("POST /v1/accounts/logout", s.apiLogout)
	mux.HandleFunc("GET /v1/me", s.api(s.apiMe))
	mux.HandleFunc("GET /v1/domains", s.api(s.apiListDomains))
	mux.HandleFunc("POST /v1/domains", s.api(s.apiCreateDomain))
	mux.HandleFunc("GET /v1/domains/{d}", s.api(s.apiGetDomain))
	mux.HandleFunc("PATCH /v1/domains/{d}", s.api(s.apiPatchDomain))
	mux.HandleFunc("GET /v1/domains/{d}/devices", s.api(s.apiListDevices))
	mux.HandleFunc("GET /v1/domains/{d}/devices/{dev}", s.api(s.apiGetDevice))
	mux.HandleFunc("PATCH /v1/domains/{d}/devices/{dev}", s.api(s.apiPatchDevice))
	mux.HandleFunc("POST /v1/domains/{d}/devices/{dev}/revoke", s.api(s.apiRevokeDevice))
	mux.HandleFunc("POST /v1/domains/{d}/devices/{dev}/renewal-authorizations", s.api(s.apiRenewalAuthorization))
	mux.HandleFunc("POST /v1/domains/{d}/access-credentials", s.api(s.apiIssueCredential))
	mux.HandleFunc("GET /v1/domains/{d}/access-credentials", s.api(s.apiListCredentials))
	mux.HandleFunc("GET /v1/domains/{d}/audit", s.api(s.apiAudit))

	// Pages.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/domains", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /login", s.pageLogin)
	mux.HandleFunc("POST /login", s.pageLoginSubmit)
	mux.HandleFunc("POST /logout", s.pageLogout)
	mux.HandleFunc("GET /domains", s.page(s.pageDomains))
	mux.HandleFunc("POST /domains", s.form(s.formCreateDomain))
	mux.HandleFunc("GET /domains/{d}", s.page(s.pageOverview))
	mux.HandleFunc("POST /domains/{d}/rename", s.form(s.formRenameDomain))
	mux.HandleFunc("GET /domains/{d}/devices", s.page(s.pageDevices))
	mux.HandleFunc("GET /domains/{d}/devices/{dev}", s.page(s.pageDevice))
	mux.HandleFunc("POST /domains/{d}/devices/{dev}/rename", s.form(s.formRenameDevice))
	mux.HandleFunc("POST /domains/{d}/devices/{dev}/revoke", s.form(s.formRevokeDevice))
	mux.HandleFunc("POST /domains/{d}/devices/{dev}/renewal", s.form(s.formRenewal))
	mux.HandleFunc("GET /domains/{d}/pairing", s.page(s.pagePairing))
	mux.HandleFunc("POST /domains/{d}/pairing", s.form(s.formIssueCredential))
	mux.HandleFunc("GET /domains/{d}/audit", s.page(s.pageAudit))

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// --- JSON helpers ----------------------------------------------------------

// apiError is a console error answered as {"error":{"code","message"}}.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func errf(status int, code, format string, args ...any) *apiError {
	return &apiError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// Codes beyond those consoleapi names.
const (
	codeInvalidRequest   = "invalid_request"
	codeNotOwnerDevice   = "not_owner_device"
	codeDomainSuspended  = "domain_suspended"
	codeDeviceExists     = "device_exists"
	codeRelayUnavailable = "relay_unavailable"
	codeInternal         = "internal_error"
	codeUnauthorized     = "unauthorized"
	codeCSRF             = "csrf_invalid"
	codeNotFound         = "not_found"
	codeTooManyRequests  = "rate_limited"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, e *apiError) {
	writeJSON(w, e.Status, map[string]any{"error": consoleapi.Error{Code: e.Code, Message: e.Message}})
}

const maxBody = 1 << 20

func decodeJSON(r *http.Request, v any) *apiError {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		return errf(http.StatusBadRequest, codeInvalidRequest, "request body is not valid JSON")
	}
	return nil
}

// --- proof nonces ------------------------------------------------------------

// useNonce records a proof nonce and reports false when it was seen before.
func (s *Server) useNonce(deviceID, nonce string, until time.Time) bool {
	if nonce == "" {
		return false
	}
	now := time.Now()
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	if len(s.nonces) > 1024 {
		for k, exp := range s.nonces {
			if now.After(exp) {
				delete(s.nonces, k)
			}
		}
	}
	key := deviceID + "\x00" + nonce
	if _, seen := s.nonces[key]; seen {
		return false
	}
	// Keep the nonce at least as long as the proof could still verify.
	keep := until
	if min := now.Add(10 * time.Minute); keep.Before(min) {
		keep = min
	}
	s.nonces[key] = keep
	return true
}

// verifyProof checks a device proof and consumes its nonce.
func (s *Server) verifyProof(id identity.DeviceIdentity, proof identity.DeviceProof, challenge string, ttl time.Duration) *apiError {
	if err := identity.VerifyProof(s.provider, id, proof, s.consoleID, challenge, s.opts.ProofNow().UTC(), ttl); err != nil {
		return errf(http.StatusUnauthorized, consoleapi.ErrCodeProofInvalid, "device proof rejected: %v", err)
	}
	if !s.useNonce(id.DeviceID, proof.Nonce, proof.IssuedAt.Add(ttl)) {
		return errf(http.StatusUnauthorized, consoleapi.ErrCodeProofInvalid, "device proof was already used")
	}
	return nil
}

// --- relay -------------------------------------------------------------------

func (s *Server) relayBind(ctx context.Context, id identity.DeviceIdentity, proof identity.DeviceProof) (relay.Credentials, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return relay.Bind(ctx, s.relayHTTP, s.endpoint.BaseURL, s.opts.RelayAdminToken, id, proof)
}

func (s *Server) relayRevoke(deviceID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return relay.Revoke(ctx, s.relayHTTP, s.endpoint.BaseURL, s.opts.RelayAdminToken, deviceID)
}

// save persists the state; the caller holds s.mu.
func (s *Server) saveLocked() error {
	if err := saveState(s.opts.DataDir, s.st); err != nil {
		s.logger.Printf("dev-console: save state: %v", err)
		return err
	}
	return nil
}
