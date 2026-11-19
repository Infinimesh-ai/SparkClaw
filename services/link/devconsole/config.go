package devconsole

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

// Config is the dev console's configuration file (JSON). Command-line flags
// override its fields.
type Config struct {
	Listen    string   `json:"listen"`
	PublicURL string   `json:"public_url"`
	SANs      []string `json:"sans,omitempty"`
	DataDir   string   `json:"data_dir"`
	Name      string   `json:"name,omitempty"`
	TLS       *bool    `json:"tls,omitempty"`
	TimeZone  string   `json:"time_zone,omitempty"`

	AdminPasswordFile string `json:"admin_password_file,omitempty"`

	Relay RelayConfig `json:"relay"`
}

// RelayConfig names the ISCP Relay devices are registered at.
type RelayConfig struct {
	DomainID           string `json:"domain_id"`
	RelayID            string `json:"relay_id"`
	BaseURL            string `json:"base_url"`
	WebSocketURL       string `json:"ws_url"`
	CertificatePEMFile string `json:"tls_certificate_pem_file,omitempty"`
	SignerIdentityFile string `json:"signer_identity_file,omitempty"`
	AdminTokenFile     string `json:"admin_token_file"`
}

// LoadConfig reads a JSON configuration file.
func LoadConfig(path string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

// TLSEnabled reports whether the console serves HTTPS (the default).
func (c Config) TLSEnabled() bool { return c.TLS == nil || *c.TLS }

// CertificateHosts returns the SANs for the self-signed certificate: the
// configured ones plus the public URL's host.
func (c Config) CertificateHosts() []string {
	hosts := append([]string(nil), c.SANs...)
	if u, err := url.Parse(c.PublicURL); err == nil && u.Hostname() != "" {
		hosts = append(hosts, u.Hostname())
	}
	if len(hosts) == 0 {
		hosts = []string{"localhost", "127.0.0.1"}
	}
	return hosts
}

// RelayEndpoint reads the Relay files and returns the endpoint devices are
// told about and the Relay admin token.
func (c Config) RelayEndpoint() (relay.Endpoint, string, error) {
	r := c.Relay
	ep := relay.Endpoint{DomainID: r.DomainID, RelayID: r.RelayID, BaseURL: strings.TrimRight(r.BaseURL, "/"), WebSocketURL: r.WebSocketURL}
	if ep.DomainID == "" || ep.RelayID == "" || ep.BaseURL == "" || ep.WebSocketURL == "" {
		return ep, "", errors.New("relay domain_id, relay_id, base_url and ws_url are required")
	}
	if r.CertificatePEMFile != "" {
		b, err := os.ReadFile(r.CertificatePEMFile)
		if err != nil {
			return ep, "", fmt.Errorf("relay certificate: %w", err)
		}
		if _, err := relay.PEMPin(string(b)); err != nil {
			return ep, "", fmt.Errorf("relay certificate: %w", err)
		}
		ep.TLSCertificatePEM = string(b)
	}
	if r.SignerIdentityFile != "" {
		b, err := os.ReadFile(r.SignerIdentityFile)
		if err != nil {
			return ep, "", fmt.Errorf("relay signer: %w", err)
		}
		var id identity.DeviceIdentity
		if err := json.Unmarshal(b, &id); err != nil || id.PublicKey.Public == "" {
			return ep, "", fmt.Errorf("relay signer identity file is not a device identity")
		}
		ep.Signer = &id
	}
	if r.AdminTokenFile == "" {
		return ep, "", errors.New("relay admin_token_file is required")
	}
	b, err := os.ReadFile(r.AdminTokenFile)
	if err != nil {
		return ep, "", fmt.Errorf("relay admin token: %w", err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return ep, "", errors.New("relay admin token file is empty")
	}
	return ep, token, nil
}

// Validate checks the fields the server needs.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address is required")
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen address: %w", err)
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("public_url must be an absolute http(s) URL")
	}
	if c.TLSEnabled() && u.Scheme != "https" {
		return errors.New("public_url must use https when TLS is on")
	}
	if c.DataDir == "" {
		return errors.New("data_dir is required")
	}
	return nil
}
