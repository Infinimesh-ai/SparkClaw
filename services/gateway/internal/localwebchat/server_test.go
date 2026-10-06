package localwebchat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	server        *Server
	runtime       string
	assets        string
	credential    credential
	url           string
	client        *http.Client
	upstream      *http.Server
	identity      atomic.Pointer[identity]
	requests      atomic.Int32
	identityCalls atomic.Int32
}

func newFixture(t *testing.T, handler http.HandlerFunc) *fixture {
	t.Helper()
	// Keep the socket below sockaddr_un's small path limit on macOS too.
	temporary, err := os.MkdirTemp("", "lw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temporary) })
	base, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{runtime: base, assets: filepath.Join(base, "dist"), credential: credential{identity: identity{SchemaVersion: 1, DeploymentID: "deployment-test", ClientID: "local_webchat_test", OwnerID: "owner", ActorID: "owner"}, ClientName: "Local WebChat", Token: strings.Repeat("s", 48)}}
	for _, directory := range []string{filepath.Join(f.runtime, "local-webchat"), f.assets} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for filename, content := range map[string]string{"index.html": "<!doctype html><title>Workbench</title>", "app.js": "window.test=true", "secret.json": "SECRET", "app.js.map": "SECRET"} {
		if err := os.WriteFile(filepath.Join(f.assets, filename), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.saveCredential(t)
	socket := filepath.Join(f.runtime, "local-webchat", "workbench.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		t.Fatal(err)
	}
	f.identity.Store(&f.credential.identity)
	f.upstream = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(IngressHeader) != f.credential.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == IdentityPath {
			f.identityCalls.Add(1)
			_ = json.NewEncoder(w).Encode(f.identity.Load())
			return
		}
		f.requests.Add(1)
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Set-Cookie", "secret=hidden")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Security-Policy", "frame-ancestors *")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Path, "host": r.Host, "headers": r.Header})
	})}
	go func() { _ = f.upstream.Serve(listener) }()
	f.server, err = New(Config{RuntimeDir: f.runtime, AssetsDir: f.assets})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.url = "http://" + f.server.Addresses()[0]
	f.client = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	t.Cleanup(func() {
		f.client.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = f.server.Close(ctx)
		_ = f.upstream.Close()
	})
	return f
}

func (f *fixture) saveCredential(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(f.credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.runtime, "local-webchat.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) request(t *testing.T, route string, mutate func(*http.Request)) (*http.Response, string) {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, f.url+route, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set(ProofHeader, "1")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if mutate != nil {
		mutate(r)
	}
	response, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}

func TestAdmissionAndSecurityHeaders(t *testing.T) {
	f := newFixture(t, nil)
	cases := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"forged host", func(r *http.Request) { r.Host = "evil.example" }},
		{"numeric suffix", func(r *http.Request) { r.Host = "127.0.0.1.evil.example:" + fmt.Sprint(f.server.port) }},
		{"missing proof", func(r *http.Request) { r.Header.Del(ProofHeader) }},
		{"wrong proof", func(r *http.Request) { r.Header.Set(ProofHeader, "true") }},
		{"duplicate proof", func(r *http.Request) { r.Header.Add(ProofHeader, "1") }},
		{"no metadata", func(r *http.Request) { r.Header.Del("Sec-Fetch-Site") }},
		{"cross origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }},
		{"duplicate origin", func(r *http.Request) { r.Header.Add("Origin", f.url); r.Header.Add("Origin", f.url) }},
		{"same site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }},
		{"cross site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"none site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "none") }},
		{"preflight", func(r *http.Request) { r.Method = http.MethodOptions }},
		{"iframe", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "iframe") }},
		{"object", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "object") }},
		{"embed", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "embed") }},
		{"duplicate authorization", func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer old")
			r.Header.Add("Authorization", "Bearer forged")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := f.request(t, "/api/workbench/self", tc.mutate)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("status=%d", res.StatusCode)
			}
			assertSecurityHeaders(t, res.Header)
		})
	}
	if f.requests.Load() != 0 || f.identityCalls.Load() != 0 {
		t.Fatal("rejected requests reached private socket")
	}
	for _, origin := range []bool{false, true} {
		res, _ := f.request(t, "/api/workbench/self", func(r *http.Request) {
			if origin {
				r.Header.Set("Origin", f.url)
			}
		})
		if res.StatusCode != 200 {
			t.Fatalf("same-origin status=%d", res.StatusCode)
		}
		assertSecurityHeaders(t, res.Header)
	}
}

func assertSecurityHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for key, value := range map[string]string{"X-Frame-Options": "DENY", "Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff"} {
		if header.Get(key) != value {
			t.Errorf("%s=%q", key, header.Get(key))
		}
	}
	if !strings.Contains(strings.Join(header.Values("Content-Security-Policy"), ";"), "frame-ancestors 'none'") {
		t.Error("frame denial is missing")
	}
	if header.Get("Set-Cookie") != "" || header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("upstream browser trust headers leaked")
	}
}

func TestProxyStripsForgedHeadersAndPreservesExplicitCredential(t *testing.T) {
	f := newFixture(t, nil)
	res, body := f.request(t, "/api/workbench/self", func(r *http.Request) {
		r.Header.Set("Origin", f.url)
		r.Header.Set("Authorization", "Bearer revoked")
		r.Header.Set("Connection", "Authorization, Origin")
		for _, header := range []string{"Cookie", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", IngressHeader, "X-SparkClaw-Local-Owner"} {
			r.Header.Set(header, "forged")
		}
	})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var actual struct {
		Host    string      `json:"host"`
		Headers http.Header `json:"headers"`
	}
	if err := json.Unmarshal([]byte(body), &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Host != strings.TrimPrefix(f.url, "http://") || actual.Headers.Get(IngressHeader) != f.credential.Token || actual.Headers.Get("Authorization") != "Bearer revoked" || actual.Headers.Get("Origin") != f.url {
		t.Fatalf("identity headers changed: %+v", actual)
	}
	for _, header := range []string{"Cookie", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", ProofHeader, "X-SparkClaw-Local-Owner"} {
		if actual.Headers.Get(header) != "" {
			t.Errorf("forged header %s survived", header)
		}
	}
}

func TestOnlySocketPeerAndDestinationEstablishLocality(t *testing.T) {
	f := newFixture(t, nil)
	for _, tc := range []struct {
		name, peer, ip string
		port           int
	}{
		{"remote peer", "192.168.1.2:12345", "127.0.0.1", f.server.port},
		{"remote destination", "127.0.0.1:12345", "192.168.1.2", f.server.port},
		{"missing peer", "", "127.0.0.1", f.server.port},
		{"wrong local port", "127.0.0.1:12345", "127.0.0.1", f.server.port + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", f.url+"/healthz", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("Forwarded", "for=127.0.0.1;host=localhost")
			r.Header.Set(IngressHeader, f.credential.Token)
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP(tc.ip), Port: tc.port}))
			w := httptest.NewRecorder()
			f.server.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
	if f.identityCalls.Load() != 0 {
		t.Fatal("forged locality reached upstream")
	}
}

func TestIdentityAndCredentialChangesFailClosed(t *testing.T) {
	f := newFixture(t, nil)
	for _, field := range []string{"deployment", "owner", "actor", "client", "schema"} {
		t.Run(field, func(t *testing.T) {
			changed := f.credential.identity
			switch field {
			case "deployment":
				changed.DeploymentID = "other"
			case "owner":
				changed.OwnerID = "other"
			case "actor":
				changed.ActorID = "other"
			case "client":
				changed.ClientID = "other"
			case "schema":
				changed.SchemaVersion = 2
			}
			f.identity.Store(&changed)
			res, _ := f.request(t, "/api/workbench/self", nil)
			if res.StatusCode != 503 {
				t.Fatalf("status=%d", res.StatusCode)
			}
		})
	}
	f.identity.Store(&f.credential.identity)
	if err := os.Chmod(filepath.Join(f.runtime, "local-webchat.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := f.request(t, "/readyz", nil)
	if res.StatusCode != 503 {
		t.Fatalf("credential mode status=%d", res.StatusCode)
	}
	if f.requests.Load() != 0 {
		t.Fatal("identity mismatch reached business handler")
	}
}

func TestStaticAssetsAndHealthBoundaries(t *testing.T) {
	f := newFixture(t, nil)
	if err := os.Symlink(filepath.Join(f.assets, "app.js"), filepath.Join(f.assets, "link.js")); err != nil {
		t.Fatal(err)
	}
	for route, want := range map[string]int{"/": 200, "/settings": 200, "/app.js": 200, "/secret.json": 404, "/app.js.map": 404, "/.env": 404, "/link.js": 404, "/unknown.css": 404, "/api/local-webchat/identity": 403} {
		res, _ := f.request(t, route, nil)
		if res.StatusCode != want {
			t.Errorf("%s status=%d want=%d", route, res.StatusCode, want)
		}
		assertSecurityHeaders(t, res.Header)
	}
	res, _ := f.request(t, "/", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "iframe") })
	if res.StatusCode != 403 {
		t.Fatalf("iframe static status=%d", res.StatusCode)
	}
	res, _ = f.request(t, "/readyz", func(r *http.Request) { r.Header.Del(ProofHeader); r.Header.Del("Sec-Fetch-Site") })
	if res.StatusCode != 200 {
		t.Fatalf("health status=%d", res.StatusCode)
	}
	if f.requests.Load() != 1 {
		t.Fatalf("non-API path reached upstream: %d", f.requests.Load())
	}
}

func TestSpeechWebSocketRequiresExactOriginAndClosesOnShutdown(t *testing.T) {
	upstreamClosed := make(chan struct{})
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = buffer.Flush()
		_, _ = io.Copy(conn, conn)
		close(upstreamClosed)
	})
	for _, tc := range []struct{ route, origin, site string }{{"/api/speech/realtime", "", "same-origin"}, {"/api/speech/realtime", "http://evil.example", "same-origin"}, {"/api/speech/realtime", f.url, "cross-site"}, {"/api/workbench/self", f.url, "same-origin"}} {
		res, _ := f.request(t, tc.route, func(r *http.Request) {
			r.Header.Del(ProofHeader)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Connection", "Upgrade")
		})
		if res.StatusCode != 403 {
			t.Fatalf("bad WS admitted: %+v status=%d", tc, res.StatusCode)
		}
	}
	conn, err := net.DialTimeout("tcp", f.server.Addresses()[0], time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprintf(conn, "GET /api/speech/realtime?ticket=single-use HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nSec-Fetch-Site: same-origin\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n", strings.TrimPrefix(f.url, "http://"), f.url)
	if err != nil {
		t.Fatal(err)
	}
	buffer := bufio.NewReader(conn)
	response, err := http.ReadResponse(buffer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 {
		t.Fatalf("WS status=%d", response.StatusCode)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(buffer, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("WS echo=%q err=%v", echo, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.server.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.ReadByte(); err == nil {
		t.Fatal("hijacked browser connection survived shutdown")
	}
	select {
	case <-upstreamClosed:
	case <-time.After(time.Second):
		t.Fatal("hijacked upstream connection survived shutdown")
	}
}

func TestProxyFlushesSSEAndCancelsUpstream(t *testing.T) {
	upstreamClosed := make(chan struct{})
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(upstreamClosed)
	})
	r, err := http.NewRequest(http.MethodGet, f.url+"/api/workbench/events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set(ProofHeader, "1")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: ready\n" {
		t.Fatalf("SSE did not flush: %q %v", line, err)
	}
	_ = response.Body.Close()
	select {
	case <-upstreamClosed:
	case <-time.After(time.Second):
		t.Fatal("closed browser SSE left an upstream request running")
	}
}

func TestUpstreamRestartAndStartupOrder(t *testing.T) {
	f := newFixture(t, nil)
	if err := f.upstream.Close(); err != nil {
		t.Fatal(err)
	}
	response, _ := f.request(t, "/", nil)
	if response.StatusCode != 200 {
		t.Fatalf("public shell unavailable without Gateway: %d", response.StatusCode)
	}
	response, _ = f.request(t, "/readyz", nil)
	if response.StatusCode != 503 {
		t.Fatalf("readiness passed without Gateway: %d", response.StatusCode)
	}
	if err := os.Remove(filepath.Join(f.runtime, "local-webchat.json")); err != nil {
		t.Fatal(err)
	}
	response, _ = f.request(t, "/api/workbench/self", nil)
	if response.StatusCode != 503 {
		t.Fatalf("request admitted without credential: %d", response.StatusCode)
	}
}

func TestRejectsNonCanonicalPathsBeforeUpstream(t *testing.T) {
	f := newFixture(t, nil)
	for _, route := range []string{"/api/workbench/../local-webchat/identity", "/api//sessions", "/api/./sessions", "/api/sessions/..", "/api/%2e%2e/readyz"} {
		response, _ := f.request(t, route, nil)
		if response.StatusCode != 400 {
			t.Errorf("%s status=%d", route, response.StatusCode)
		}
	}
	if f.identityCalls.Load() != 0 || f.requests.Load() != 0 {
		t.Fatal("noncanonical paths reached private listener")
	}
}

func TestProxyPreservesUpstreamSandboxPolicy(t *testing.T) {
	const policy = "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'"
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", policy)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<p>untrusted email</p>")
	})
	response, _ := f.request(t, "/api/email/messages/mail/render-preview", nil)
	policies := response.Header.Values("Content-Security-Policy")
	if len(policies) != 2 || policies[0] != policy || policies[1] != "frame-ancestors 'none'" {
		t.Fatalf("sandbox policy lost or denial duplicated: %v", policies)
	}
	assertSecurityHeaders(t, response.Header)
}

func TestSecurityHeadersDoNotDuplicateExistingFrameDenial(t *testing.T) {
	header := http.Header{"Content-Security-Policy": []string{"sandbox; frame-ancestors 'none'; default-src 'none'"}}
	securityHeaders(header)
	securityHeaders(header)
	if len(header.Values("Content-Security-Policy")) != 1 {
		t.Fatalf("frame denial duplicated: %v", header)
	}
}
