// Package relay is a minimal client for the ISCP Relay duplex transport
// (iscp.relay.duplex.v1): authentication, reauthentication, credential
// refresh, envelope submission with receipts and delivery.
package relay

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"time"
)

// PinnedTLSConfig accepts exactly the given leaf certificate. With an empty
// PEM it returns nil, meaning the system trust store applies.
func PinnedTLSConfig(certificatePEM string) (*tls.Config, error) {
	if certificatePEM == "" {
		return nil, nil
	}
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("pinned certificate is not PEM")
	}
	want := block.Bytes
	return PinnedDERConfig(want), nil
}

// PinnedDERConfig accepts exactly the leaf certificate with this DER.
func PinnedDERConfig(der []byte) *tls.Config {
	want := append([]byte(nil), der...)
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // replaced by the exact pin below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || !bytes.Equal(raw[0], want) {
				return errors.New("server certificate does not match the pinned certificate")
			}
			return nil
		},
	}
}

// PinnedSHA256Config accepts exactly the leaf certificate whose DER SHA-256
// (base64url, no padding) equals pin.
func PinnedSHA256Config(pin string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // replaced by the exact pin below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || CertificatePin(raw[0]) != pin {
				return errors.New("server certificate does not match the pinned fingerprint")
			}
			return nil
		},
	}
}

// CertificatePin is the base64url SHA-256 of a DER certificate.
func CertificatePin(der []byte) string {
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// PEMPin returns CertificatePin for a PEM certificate.
func PEMPin(certificatePEM string) (string, error) {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("certificate is not PEM")
	}
	return CertificatePin(block.Bytes), nil
}

// HTTPClient builds a client for a pinned origin (nil config = system roots).
// Proxies are never used for pinned origins.
func HTTPClient(cfg *tls.Config, timeout time.Duration) *http.Client {
	tr := &http.Transport{
		TLSClientConfig:     cfg,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     60 * time.Second,
		MaxIdleConnsPerHost: 4,
	}
	if cfg == nil {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}
