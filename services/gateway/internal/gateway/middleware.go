package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const webChatProxyTokenHeader = "X-SparkClaw-WebChat-Proxy"

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			// DNS-rebinding defense for the LAN MCP endpoint: a browser
			// context always sends an Origin header, which must match the
			// gateway's own loopback/bind origins or the operator allowlist
			// (mcp_access.allowed_origins). Requests without an Origin header
			// (curl, native MCP clients) pass through untouched — this is not
			// an authentication layer; /mcp still requires access tickets.
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" && !s.mcpOriginAllowed(origin) {
				writeError(w, http.StatusForbidden, errors.New("origin is not allowed on the MCP endpoint"))
				return
			}
			w.Header().Add("Vary", "Origin")
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id, MCP-Protocol-Version, Idempotency-Key")
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id, MCP-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, bridgeRoutePrefix) {
			// Bridge dispatch fails closed: its ISCP adapter surface includes
			// approval resolution, so it must never be reachable without a
			// credential just because the gateway runs in the default no-auth
			// posture. A configured gateway.bridge_token is the dedicated
			// (and then exclusive) bridge credential; otherwise the request
			// falls through to the standard gateway bearer validation below.
			if token := strings.TrimSpace(s.cfg.Gateway.BridgeToken); token != "" {
				presented := bearerCredential(r.Header.Get("Authorization"))
				if presented == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
					writeError(w, http.StatusUnauthorized, errors.New("valid bridge token required"))
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, defaultRequestPrincipal())))
				return
			}
			if !s.authRequired() {
				writeError(w, http.StatusServiceUnavailable, errors.New("bridge API requires Gateway authentication or a configured gateway.bridge_token"))
				return
			}
		}
		if isBrowserControlRoute(r.URL.Path) && !s.authRequired() {
			writeError(w, http.StatusServiceUnavailable, errors.New("browser control API requires Gateway authentication"))
			return
		}
		if s.isPublicRoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		if isTicketAuthenticatedRoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authRequired() {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, defaultRequestPrincipal())))
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if s.isPairingBootstrapRequest(r) && got == "" {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, defaultRequestPrincipal())))
			return
		}
		if got == "" {
			writeError(w, http.StatusUnauthorized, errors.New("valid bearer token required"))
			return
		}
		principal, ok, err := s.authenticateBearer(r.Context(), got)
		if err != nil {
			if store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
				writeError(w, http.StatusGatewayTimeout, errors.New("authentication request timed out"))
				return
			}
			writeError(w, http.StatusServiceUnavailable, errors.New("authentication is temporarily unavailable"))
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, errors.New("valid bearer token required"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, principal)))
	})
}

func (s *Server) withRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isPublicRoute(r) || s.limiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		allowed, retryAfter := s.limiter.allow(rateLimitKey(r))
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
			writeError(w, http.StatusTooManyRequests, errors.New("rate limit exceeded"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isTicketAuthenticatedRoute lists routes whose credential is a single-use
// ticket validated by the handler itself (consumeSpeechRealtimeTicket), so
// they bypass bearer auth. Keep this list explicit: a bare path-equality
// check inside withAuth would silently exempt any future handler mounted on
// the same path.
func isTicketAuthenticatedRoute(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/speech/realtime"
}

func (s *Server) isPublicRoute(r *http.Request) bool {
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/metrics":
			return true
		}
	}
	return false
}

func (s *Server) authRequired() bool {
	return s.cfg.Gateway.PairingRequired || strings.TrimSpace(s.cfg.Gateway.APIToken) != ""
}

func (s *Server) isPairingBootstrapRequest(r *http.Request) bool {
	if !s.cfg.Gateway.PairingRequired {
		return false
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/pairing/start" || r.URL.Path == "/api/pairing/claim") {
		return true
	}
	return false
}

func (s *Server) isTrustedPairingBootstrap(r *http.Request) bool {
	if isLocalRequest(r) {
		return true
	}
	expected := s.cfg.Gateway.WebChatProxyToken
	presented := r.Header.Get(webChatProxyTokenHeader)
	return expected != "" && presented != "" &&
		subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

func (s *Server) authenticateBearer(ctx context.Context, token string) (requestPrincipal, bool, error) {
	if configured := strings.TrimSpace(s.cfg.Gateway.APIToken); configured != "" {
		if subtle.ConstantTimeCompare([]byte(token), []byte(configured)) == 1 {
			return defaultRequestPrincipal(), true, nil
		}
	}
	client, ok, err := s.store.FindClientByTokenHash(ctx, hashSecret(token))
	if err != nil {
		return requestPrincipal{}, false, err
	}
	if !ok {
		return requestPrincipal{}, false, nil
	}
	client, ok, err = s.store.TouchClient(ctx, client.ID)
	if err != nil {
		return requestPrincipal{}, false, err
	}
	if !ok {
		return requestPrincipal{}, false, nil
	}
	ownerID := strings.TrimSpace(client.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	actorID := strings.TrimSpace(client.ActorID)
	if actorID == "" {
		actorID = ownerID
	}
	return requestPrincipal{OwnerID: ownerID, ActorID: actorID, ClientID: client.ID}, true, nil
}

type requestPrincipalContextKey struct{}

type requestPrincipal struct {
	OwnerID  string
	ActorID  string
	ClientID string
}

func defaultRequestPrincipal() requestPrincipal {
	return requestPrincipal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}
}

func principalForRequest(r *http.Request) requestPrincipal {
	if principal, ok := r.Context().Value(requestPrincipalContextKey{}).(requestPrincipal); ok && principal.OwnerID != "" && principal.ActorID != "" {
		return principal
	}
	return defaultRequestPrincipal()
}

type rateLimiter struct {
	mu       sync.Mutex
	enabled  bool
	rate     float64
	burst    float64
	buckets  map[string]rateLimitBucket
	rejected int
}

type rateLimitBucket struct {
	Tokens     float64
	LastRefill time.Time
}

func newRateLimiter(cfg config.RateLimitConfig) *rateLimiter {
	if !cfg.Enabled || cfg.RequestsPerMinute <= 0 || cfg.Burst <= 0 {
		return nil
	}
	return &rateLimiter{
		enabled: true,
		rate:    float64(cfg.RequestsPerMinute) / 60,
		burst:   float64(cfg.Burst),
		buckets: map[string]rateLimitBucket{},
	}
}

func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	if l == nil || !l.enabled {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	bucket := l.buckets[key]
	if bucket.LastRefill.IsZero() {
		bucket = rateLimitBucket{Tokens: l.burst, LastRefill: now}
	} else {
		elapsed := now.Sub(bucket.LastRefill).Seconds()
		bucket.Tokens = math.Min(l.burst, bucket.Tokens+elapsed*l.rate)
		bucket.LastRefill = now
	}
	if bucket.Tokens >= 1 {
		bucket.Tokens--
		l.buckets[key] = bucket
		return true, 0
	}
	l.rejected++
	l.buckets[key] = bucket
	waitSeconds := (1 - bucket.Tokens) / l.rate
	return false, time.Duration(math.Ceil(waitSeconds*1000)) * time.Millisecond
}

func (l *rateLimiter) rejectedCount() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rejected
}

func rateLimitKey(r *http.Request) string {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token != "" {
		return "token:" + hashSecret(token)
	}
	host := r.RemoteAddr
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		host = strings.Split(forwarded, ",")[0]
	}
	if parsed := strings.TrimSpace(host); parsed != "" {
		if h, _, err := net.SplitHostPort(parsed); err == nil {
			host = h
		} else {
			host = parsed
		}
	}
	return "remote:" + strings.Trim(host, "[]")
}
