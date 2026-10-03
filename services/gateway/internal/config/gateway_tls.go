package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxGatewayTLSFileBytes = 1 << 20

// GatewayTLSConfig loads the optional native Gateway TLS identity. An invalid
// or incomplete configured identity is a startup error, never an HTTP fallback.
// Loading again immediately before listening also detects changed key files.
func GatewayTLSConfig(gateway GatewayConfig) (*tls.Config, error) {
	certPath := strings.TrimSpace(gateway.TLSCertFile)
	keyPath := strings.TrimSpace(gateway.TLSKeyFile)
	if certPath == "" && keyPath == "" {
		return nil, nil
	}
	if certPath == "" || keyPath == "" {
		return nil, errors.New("gateway.tls_cert_file and gateway.tls_key_file must be configured together")
	}
	if !filepath.IsAbs(certPath) || !filepath.IsAbs(keyPath) {
		return nil, errors.New("gateway TLS certificate and key paths must be absolute")
	}
	certPEM, err := readGatewayTLSFile(certPath, false)
	if err != nil {
		return nil, fmt.Errorf("gateway TLS certificate: %w", err)
	}
	keyPEM, err := readGatewayTLSFile(keyPath, true)
	if err != nil {
		return nil, fmt.Errorf("gateway TLS key: %w", err)
	}
	defer clear(keyPEM)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("gateway TLS certificate/key pair: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, nil
}

func privateGatewayTLSKey(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0
}

func readGatewayTLSFile(path string, private bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if private && !privateGatewayTLSKey(info) {
		return nil, errors.New("must be an owner-only regular non-symlink file owned by the Gateway user")
	}
	if !private && info.Mode()&os.ModeSymlink != 0 {
		info, err = os.Stat(path)
		if err != nil {
			return nil, err
		}
	}
	if !info.Mode().IsRegular() || info.Size() > maxGatewayTLSFileBytes {
		return nil, errors.New("must be a regular PEM file no larger than 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || opened.Size() > maxGatewayTLSFileBytes {
		return nil, errors.New("must be a regular PEM file no larger than 1 MiB")
	}
	if private {
		current, err := os.Lstat(path)
		if err != nil || !privateGatewayTLSKey(opened) || !privateGatewayTLSKey(current) || !os.SameFile(info, opened) || !os.SameFile(opened, current) {
			return nil, errors.New("private key file changed while opening")
		}
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxGatewayTLSFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxGatewayTLSFileBytes {
		clear(raw)
		return nil, errors.New("PEM file exceeds 1 MiB")
	}
	return raw, nil
}
