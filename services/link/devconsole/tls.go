package devconsole

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

const (
	certFile = "tls-cert.pem"
	keyFile  = "tls-key.pem"
)

// EnsureCertificate loads the console's self-signed TLS certificate from
// the data directory, creating it on first start (ECDSA P-256, ten years,
// the given hosts as IP or DNS SANs). It returns the certificate and its
// pin (relay.CertificatePin of the leaf DER).
func EnsureCertificate(dir string, hosts []string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	if _, err := os.Stat(certPath); err == nil {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		return cert, relay.CertificatePin(cert.Certificate[0]), nil
	}
	certPEM, keyPEM, err := newSelfSigned(hosts, time.Now())
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writeFileAtomic(keyPath, keyPEM); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writeFileAtomic(certPath, certPEM); err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, relay.CertificatePin(cert.Certificate[0]), nil
}

// CertificateHosts returns the SANs of a certificate, for diagnostics.
func CertificateHosts(cert tls.Certificate) []string {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil
	}
	out := append([]string(nil), leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		out = append(out, ip.String())
	}
	return out
}

func newSelfSigned(hosts []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "SparkClaw dev console"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	if len(tpl.IPAddresses)+len(tpl.DNSNames) == 0 {
		return nil, nil, errors.New("certificate needs at least one SAN")
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}
