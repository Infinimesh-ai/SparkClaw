package localwebchat

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"strconv"
	"strings"
	"time"
)

func securityHeaders(headers http.Header) {
	// CSP policies intersect. Preserve the Gateway's stronger policies (notably
	// sandbox/default-src on untrusted email previews) and add only frame denial.
	frameDenied := false
	for _, policy := range headers.Values("Content-Security-Policy") {
		for _, directive := range strings.Split(policy, ";") {
			if strings.TrimSpace(directive) == "frame-ancestors 'none'" {
				frameDenied = true
			}
		}
	}
	if !frameDenied {
		headers.Add("Content-Security-Policy", "frame-ancestors 'none'")
	}
	headers.Set("X-Frame-Options", "DENY")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Cache-Control", "no-store")
	for key := range headers {
		lower := strings.ToLower(key)
		if lower == "set-cookie" || strings.HasPrefix(lower, "access-control-") {
			headers.Del(key)
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w.Header())
	if !s.localRequest(r) || r.Method == http.MethodOptions || embeddedRequest(r) || len(r.Header.Values("Authorization")) > 1 {
		http.Error(w, "local WebChat request denied", http.StatusForbidden)
		return
	}
	clean := path.Clean(r.URL.Path)
	if clean != r.URL.Path || strings.Contains(r.URL.Path, "\\") {
		http.Error(w, "non-canonical workbench path", http.StatusBadRequest)
		return
	}
	private := strings.HasPrefix(r.URL.Path, "/api/")
	health := r.URL.Path == "/readyz" || r.URL.Path == "/healthz"
	if !private && !health {
		s.serveAsset(w, r)
		return
	}
	if r.URL.Path == IdentityPath || (health && r.Method != http.MethodGet && r.Method != http.MethodHead) || (private && !browserProof(r)) {
		http.Error(w, "local WebChat request denied", http.StatusForbidden)
		return
	}
	credential, err := readCredential(s.cfg.RuntimeDir)
	if err != nil || !s.verifyIdentity(r.Context(), r.Host, credential) {
		http.Error(w, "local WebChat upstream is unavailable", http.StatusServiceUnavailable)
		return
	}
	// Streams and upgrades have a finite lifetime; browser reconnects repeat all
	// admission checks. Cancellation also closes hijacked connections.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	if conn, ok := r.Context().Value(connectionContextKey{}).(net.Conn); ok {
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
	}
	proxy := &httputil.ReverseProxy{
		Transport: s.transport, FlushInterval: -1,
		Rewrite: func(proxy *httputil.ProxyRequest) {
			proxy.Out.URL.Scheme, proxy.Out.URL.Host = "http", "local-webchat.internal"
			proxy.Out.Host = r.Host
			stripUntrustedHeaders(proxy.Out.Header)
			// Hop-by-hop sanitization must not silently erase an explicit credential
			// or the browser origin: either would change authentication semantics.
			for _, name := range []string{"Authorization", "Origin"} {
				if values := r.Header.Values(name); len(values) == 1 {
					proxy.Out.Header.Set(name, values[0])
				}
			}
			proxy.Out.Header.Set(IngressHeader, credential.Token)
		},
		ModifyResponse: func(response *http.Response) error {
			securityHeaders(response.Header)
			// ReverseProxy appends upstream fields to these initial response headers.
			// Its now-validated CSP includes denial, so avoid adding it twice.
			w.Header().Del("Content-Security-Policy")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "local WebChat upstream is unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) localRequest(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(peer).IsLoopback() {
		return false
	}
	destination, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok || !destination.IP.IsLoopback() {
		return false
	}
	s.mu.Lock()
	port := s.port
	s.mu.Unlock()
	if destination.Port != port {
		return false
	}
	for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
		if r.Host == net.JoinHostPort(host, strconv.Itoa(port)) {
			return true
		}
	}
	return false
}

func embeddedRequest(r *http.Request) bool {
	for _, value := range r.Header.Values("Sec-Fetch-Dest") {
		if value == "iframe" || value == "frame" || value == "object" || value == "embed" {
			return true
		}
	}
	return false
}

func singleHeader(header http.Header, name, expected string) bool {
	values := header.Values(name)
	return len(values) == 1 && values[0] == expected
}

func browserProof(r *http.Request) bool {
	if !singleHeader(r.Header, "Sec-Fetch-Site", "same-origin") {
		return false
	}
	origin := r.Header.Values("Origin")
	if len(origin) > 0 && (len(origin) != 1 || origin[0] != "http://"+r.Host) {
		return false
	}
	if r.Header.Get("Upgrade") != "" {
		return r.Method == http.MethodGet && r.URL.Path == "/api/speech/realtime" && len(r.Header.Values("Upgrade")) == 1 && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && len(origin) == 1
	}
	return singleHeader(r.Header, ProofHeader, "1")
}

func stripUntrustedHeaders(headers http.Header) {
	for key := range headers {
		lower := strings.ToLower(key)
		if lower == "cookie" || lower == "forwarded" || lower == "x-real-ip" || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-sparkclaw-local-") {
			headers.Del(key)
		}
	}
}

func (s *Server) verifyIdentity(ctx context.Context, host string, expected credential) bool {
	if expected.Token == "" {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://local-webchat.internal"+IdentityPath, nil)
	if err != nil {
		return false
	}
	request.Host = host
	request.Header.Set(IngressHeader, expected.Token)
	response, err := s.client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<10))
	decoder.DisallowUnknownFields()
	var actual identity
	return decoder.Decode(&actual) == nil && decoder.Decode(&struct{}{}) == io.EOF && actual == expected.identity
}
