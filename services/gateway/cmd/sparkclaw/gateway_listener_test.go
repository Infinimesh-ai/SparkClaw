package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3browser"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/gorilla/websocket"
)

func nativeGatewayTLSFixture(t *testing.T) (config.GatewayConfig, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "temporary-native-gateway-fixture"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := config.GatewayConfig{TLSCertFile: filepath.Join(root, "cert.pem"), TLSKeyFile: filepath.Join(root, "key.pem")}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(files.TLSCertFile, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files.TLSKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("could not trust temporary fixture certificate")
	}
	return files, roots
}

// This starts the same ListenAndServe(TLS) helper used by main, with an OS
// allocated loopback port. BaseContext publishes the actual listener address.
func startNativeGatewayListener(t *testing.T, handler http.Handler, cfg config.GatewayConfig) (string, *http.Server) {
	t.Helper()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	server := &http.Server{Addr: "127.0.0.1:0", Handler: handler, ReadHeaderTimeout: time.Second, BaseContext: func(listener net.Listener) context.Context { ready <- listener.Addr().String(); return t.Context() }}
	go func() { done <- serveGatewayHTTP(server, cfg) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = server.Close()
		select {
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("native Gateway listener close: %v", err)
			}
		case <-ctx.Done():
			t.Error("native Gateway listener did not stop")
		}
	})
	select {
	case address := <-ready:
		return address, server
	case err := <-done:
		t.Fatalf("native Gateway listener failed to start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("native Gateway listener did not start")
	}
	return "", nil
}

func installedGatewayFixture(t *testing.T) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "native-tls-fixture"
	cfg.Gateway.RateLimit.Enabled = false
	cfg.State.Backend = "memory"
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
	cfg.Storage.TraceDir, cfg.Storage.ArtifactDir = filepath.Join(root, "traces"), filepath.Join(root, "artifacts")
	backend := store.NewMemoryStore()
	const token = "synthetic-native-gateway-tls-installed-client-token"
	if _, err := backend.RegisterClient(t.Context(), app.Client{ID: "tls-client", OwnerID: "tls-owner", Name: "temporary fixture", TokenHash: desktopTokenHash(token)}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, backend)
	t.Cleanup(func() { _ = tools.Close() })
	instance := gateway.New(cfg, backend, tools, agent.Runtime{}, gateway.WithR3Executions(filepath.Join(root, "r3"), nil))
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	instance.BindLifecycleContext(ctx)
	return instance.Handler(), token
}

const nativeTLSInstallation = "11111111-1111-4111-8111-111111111111"

func nativeTLSGrant(t *testing.T, client *http.Client, baseURL, token string) r3browser.Grant {
	t.Helper()
	nativeTLSRequest(t, client, baseURL, token, "/api/r3/installations", `{"schema_version":1,"installation_id":"`+nativeTLSInstallation+`"}`, "", nil)
	var grant r3browser.Grant
	nativeTLSRequest(t, client, baseURL, token, "/api/r3/hosts/grants", `{}`, nativeTLSInstallation, &grant)
	if grant.HostID == "" || grant.Token == "" {
		t.Fatal("real Gateway host grant missing")
	}
	return grant
}

func nativeTLSRequest(t *testing.T, client *http.Client, baseURL, token, route, body, installation string, output any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+route, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SparkClaw-Installation", installation)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("real Gateway route %s returned %d", route, res.StatusCode)
	}
	if strings.HasPrefix(baseURL, "https://") && (res.TLS == nil || res.TLS.Version < tls.VersionTLS12) {
		t.Fatal("HTTPS request did not use the configured native TLS listener")
	}
	if strings.HasPrefix(baseURL, "http://") && res.TLS != nil {
		t.Fatal("default HTTP route unexpectedly required TLS")
	}
	if output != nil {
		if err := json.NewDecoder(res.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	} else {
		_, _ = io.Copy(io.Discard, res.Body)
	}
}

func nativeTLSHostHeaders(token string, grant r3browser.Grant) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}, "X-Sparkclaw-Installation": {nativeTLSInstallation}, "X-Sparkclaw-Host-Id": {grant.HostID}, "X-Sparkclaw-Host-Grant": {grant.Token}, "X-Sparkclaw-Runtime": {"temporary-tls-generation"}}
}

func TestNativeGatewayHTTPSInstalledHostWSSAndRevocation(t *testing.T) {
	files, roots := nativeGatewayTLSFixture(t)
	handler, token := installedGatewayFixture(t)
	address, server := startNativeGatewayListener(t, handler, files)
	tlsClient := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	transport := &http.Transport{TLSClientConfig: tlsClient}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	baseURL := "https://" + address
	grant := nativeTLSGrant(t, client, baseURL, token)
	if server.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("native listener did not enforce TLS >= 1.2")
	}
	// Certificate trust and hostname verification stay active, never InsecureSkipVerify.
	dialer := websocket.Dialer{TLSClientConfig: tlsClient, HandshakeTimeout: 3 * time.Second}
	conn, response, err := dialer.Dial("wss://"+address+"/api/r3/hosts/connect", nativeTLSHostHeaders(token, grant))
	if err != nil {
		t.Fatalf("native Gateway WSS host upgrade: %v", err)
	}
	defer conn.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatal("host did not upgrade over real native TLS")
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var welcome struct {
		Type   string `json:"type"`
		HostID string `json:"host_id"`
		Epoch  string `json:"connection_epoch"`
	}
	if err := conn.ReadJSON(&welcome); err != nil || welcome.Type != "welcome" || welcome.HostID != grant.HostID || welcome.Epoch == "" {
		t.Fatalf("real Broker welcome over native Gateway TLS: %+v %v", welcome, err)
	}
	if err := conn.WriteJSON(r3browser.Message{SchemaVersion: 1, Type: "heartbeat"}); err != nil {
		t.Fatal(err)
	}
	var renew struct {
		Type  string `json:"type"`
		Epoch string `json:"connection_epoch"`
	}
	if err := conn.ReadJSON(&renew); err != nil || renew.Type != "renew" || renew.Epoch != welcome.Epoch {
		t.Fatalf("real Broker heartbeat over native Gateway TLS: %+v %v", renew, err)
	}
	tls12, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("native Gateway did not support TLS 1.2: %v", err)
	}
	tls12.Close()
	oldTLS := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
	if oldConn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, oldTLS); err == nil {
		oldConn.Close()
		t.Fatal("native Gateway accepted TLS 1.1")
	}
	untrusted := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}, HandshakeTimeout: time.Second}
	if badConn, _, err := untrusted.Dial("wss://"+address+"/api/r3/hosts/connect", nativeTLSHostHeaders(token, grant)); err == nil {
		badConn.Close()
		t.Fatal("native Gateway identity accepted without fixture certificate trust")
	}
	nativeTLSRequest(t, client, baseURL, token, "/api/r3/hosts/"+grant.HostID+"/revoke", `{}`, nativeTLSInstallation, nil)
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("Gateway host revocation left the WSS channel executable")
	}
	if revoked, res, err := dialer.Dial("wss://"+address+"/api/r3/hosts/connect", nativeTLSHostHeaders(token, grant)); err == nil {
		revoked.Close()
		t.Fatal("revoked grant reconnected")
	} else if res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked grant did not fail closed: %v", err)
	} else {
		res.Body.Close()
	}
}

func TestNativeGatewayDefaultHTTPCannotForgeTLSHostAdmission(t *testing.T) {
	handler, token := installedGatewayFixture(t)
	address, _ := startNativeGatewayListener(t, handler, config.GatewayConfig{})
	client := &http.Client{Timeout: 3 * time.Second}
	baseURL := "http://" + address
	grant := nativeTLSGrant(t, client, baseURL, token)
	headers := nativeTLSHostHeaders(token, grant)
	headers.Set("X-Forwarded-Proto", "https")
	headers.Set("Forwarded", "proto=https")
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	conn, res, err := dialer.Dial("ws://"+address+"/api/r3/hosts/connect", headers)
	if err == nil {
		conn.Close()
		t.Fatal("plain HTTP upstream admitted host on forged proxy headers")
	}
	if res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("HTTP host admission should remain denied: %v", err)
	}
	res.Body.Close()
}

func TestNativeGatewayTLSInvalidIdentityNeverOpensHTTPListener(t *testing.T) {
	files, _ := nativeGatewayTLSFixture(t)
	if _, err := config.GatewayTLSConfig(files); err != nil {
		t.Fatal(err)
	}
	// A private key becoming readable after config.Load is rejected before bind.
	if err := os.Chmod(files.TLSKeyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:0", BaseContext: func(net.Listener) context.Context {
		t.Error("invalid TLS identity opened a listener")
		return t.Context()
	}}
	if err := serveGatewayHTTP(server, files); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("changed key did not stop listener startup: %v", err)
	}
	files.TLSKeyFile = ""
	if err := serveGatewayHTTP(server, files); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("partial TLS configuration fell back to HTTP: %v", err)
	}
}
