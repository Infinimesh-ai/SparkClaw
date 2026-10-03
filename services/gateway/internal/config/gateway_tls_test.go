package config

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func gatewayTLSFixture(t *testing.T) GatewayConfig {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "temporary-gateway-tls-fixture"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := GatewayConfig{TLSCertFile: filepath.Join(root, "cert.pem"), TLSKeyFile: filepath.Join(root, "key.pem")}
	if err := os.WriteFile(cfg.TLSCertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.TLSKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestGatewayTLSOptionalConfigAndEnvironment(t *testing.T) {
	t.Setenv("SPARKCLAW_GATEWAY_TLS_CERT_FILE", "")
	t.Setenv("SPARKCLAW_GATEWAY_TLS_KEY_FILE", "")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig, err := GatewayTLSConfig(cfg.Gateway); err != nil || tlsConfig != nil {
		t.Fatalf("default HTTP changed: TLS=%v err=%v", tlsConfig, err)
	}
	fromFile := gatewayTLSFixture(t)
	fromFile.Bind, fromFile.Port = "127.0.0.1", 18789
	raw, _ := json.Marshal(map[string]any{"gateway": fromFile})
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.Gateway.TLSCertFile != fromFile.TLSCertFile || cfg.Gateway.TLSKeyFile != fromFile.TLSKeyFile {
		t.Fatalf("config TLS pair not applied: %+v %v", cfg.Gateway, err)
	}
	fromEnv := gatewayTLSFixture(t)
	t.Setenv("SPARKCLAW_GATEWAY_TLS_CERT_FILE", fromEnv.TLSCertFile)
	t.Setenv("SPARKCLAW_GATEWAY_TLS_KEY_FILE", fromEnv.TLSKeyFile)
	cfg, err = Load(path)
	if err != nil || cfg.Gateway.TLSCertFile != fromEnv.TLSCertFile || cfg.Gateway.TLSKeyFile != fromEnv.TLSKeyFile {
		t.Fatalf("environment TLS override not applied: %+v %v", cfg.Gateway, err)
	}
	tlsConfig, err := GatewayTLSConfig(cfg.Gateway)
	if err != nil || tlsConfig.MinVersion != tls.VersionTLS12 || len(tlsConfig.Certificates) != 1 {
		t.Fatalf("native TLS configuration: %+v %v", tlsConfig, err)
	}
	certPEM, _ := os.ReadFile(fromEnv.TLSCertFile)
	block, _ := pem.Decode(certPEM)
	if !bytes.Equal(tlsConfig.Certificates[0].Certificate[0], block.Bytes) {
		t.Fatal("loaded identity does not match environment-selected certificate")
	}
	t.Setenv("SPARKCLAW_GATEWAY_TLS_KEY_FILE", "")
	// Load without a file proves the environment alone must supply a complete pair.
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("incomplete environment TLS identity accepted: %v", err)
	}
}

func TestGatewayTLSRejectsInvalidIdentity(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *GatewayConfig)
		want   string
	}{
		{"certificate_only", func(_ *testing.T, c *GatewayConfig) { c.TLSKeyFile = "" }, "configured together"},
		{"key_only", func(_ *testing.T, c *GatewayConfig) { c.TLSCertFile = "" }, "configured together"},
		{"relative_certificate", func(_ *testing.T, c *GatewayConfig) { c.TLSCertFile = "cert.pem" }, "absolute"},
		{"relative_key", func(_ *testing.T, c *GatewayConfig) { c.TLSKeyFile = "key.pem" }, "absolute"},
		{"missing_key", func(_ *testing.T, c *GatewayConfig) { c.TLSKeyFile += ".missing" }, "no such file"},
		{"directory_certificate", func(_ *testing.T, c *GatewayConfig) { c.TLSCertFile = filepath.Dir(c.TLSCertFile) }, "regular"},
		{"public_key_permissions", func(t *testing.T, c *GatewayConfig) { mustTLSFileChange(t, os.Chmod(c.TLSKeyFile, 0o644)) }, "owner-only"},
		{"group_key_permissions", func(t *testing.T, c *GatewayConfig) { mustTLSFileChange(t, os.Chmod(c.TLSKeyFile, 0o640)) }, "owner-only"},
		{"symlink_key", func(t *testing.T, c *GatewayConfig) {
			path := c.TLSKeyFile + ".link"
			mustTLSFileChange(t, os.Symlink(c.TLSKeyFile, path))
			c.TLSKeyFile = path
		}, "non-symlink"},
		{"directory_key", func(_ *testing.T, c *GatewayConfig) { c.TLSKeyFile = filepath.Dir(c.TLSKeyFile) }, "regular"},
		{"malformed_certificate", func(t *testing.T, c *GatewayConfig) {
			mustTLSFileChange(t, os.WriteFile(c.TLSCertFile, []byte("invalid PEM"), 0o644))
		}, "certificate/key pair"},
		{"malformed_key", func(t *testing.T, c *GatewayConfig) {
			mustTLSFileChange(t, os.WriteFile(c.TLSKeyFile, []byte("invalid PEM"), 0o600))
		}, "certificate/key pair"},
		{"mismatched_key", func(t *testing.T, c *GatewayConfig) { c.TLSKeyFile = gatewayTLSFixture(t).TLSKeyFile }, "certificate/key pair"},
		{"oversized_key", func(t *testing.T, c *GatewayConfig) {
			mustTLSFileChange(t, os.WriteFile(c.TLSKeyFile, bytes.Repeat([]byte("x"), maxGatewayTLSFileBytes+1), 0o600))
		}, "1 MiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := gatewayTLSFixture(t)
			tc.change(t, &cfg)
			if _, err := GatewayTLSConfig(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid TLS identity accepted: %v; want %q", err, tc.want)
			}
		})
	}
}

func mustTLSFileChange(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type gatewayTLSForeignFileInfo struct{ os.FileInfo }

func (f gatewayTLSForeignFileInfo) Sys() any { return &syscall.Stat_t{Uid: uint32(os.Geteuid()) + 1} }

func TestGatewayTLSKeyOwnerMustMatchProcess(t *testing.T) {
	fixture := gatewayTLSFixture(t)
	info, err := os.Lstat(fixture.TLSKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if privateGatewayTLSKey(gatewayTLSForeignFileInfo{info}) {
		t.Fatal("private key owned by another user accepted")
	}
	if err := os.Chmod(fixture.TLSKeyFile, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := GatewayTLSConfig(fixture); err != nil {
		t.Fatalf("read-only private owner key rejected: %v", err)
	}
}
