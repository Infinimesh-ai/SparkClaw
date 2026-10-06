// Package localwebchat owns the host-loopback browser ingress. It has no access
// to the Gateway's public listener, management credentials, or desktop Client.
package localwebchat

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	IngressHeader = "X-SparkClaw-Local-Ingress"
	ProofHeader   = "X-SparkClaw-Local-WebChat"
	IdentityPath  = "/api/local-webchat/identity"
)

type Config struct {
	RuntimeDir string
	AssetsDir  string
	Port       int
}

func LoadEnvironment(getenv func(string) string) (Config, bool, error) {
	cfg := Config{RuntimeDir: "/run/sparkclaw/runtime", AssetsDir: "/usr/share/sparkclaw/webchat", Port: 18794}
	enabled := false
	if raw := strings.TrimSpace(getenv("SPARKCLAW_LOCAL_WEBCHAT_ENABLED")); raw != "" {
		if raw != "true" && raw != "false" {
			return cfg, false, errors.New("SPARKCLAW_LOCAL_WEBCHAT_ENABLED must be true or false")
		}
		enabled = raw == "true"
	}
	if raw := strings.TrimSpace(getenv("SPARKCLAW_LOCAL_WEBCHAT_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return cfg, false, errors.New("SPARKCLAW_LOCAL_WEBCHAT_PORT must be between 1 and 65535")
		}
		cfg.Port = port
	}
	if raw := strings.TrimSpace(getenv("SPARKCLAW_LOCAL_WORKBENCH_RUNTIME_DIR")); raw != "" {
		cfg.RuntimeDir = raw
	}
	if raw := strings.TrimSpace(getenv("SPARKCLAW_LOCAL_WEBCHAT_ASSETS")); raw != "" {
		cfg.AssetsDir = raw
	}
	return cfg, enabled, cfg.validate()
}

func (cfg Config) validate() error {
	for name, directory := range map[string]string{"runtime": cfg.RuntimeDir, "assets": cfg.AssetsDir} {
		if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
			return fmt.Errorf("local WebChat %s directory must be an absolute clean path", name)
		}
	}
	// Port zero is reserved for callers that need an ephemeral loopback listener.
	if cfg.Port < 0 || cfg.Port > 65535 {
		return errors.New("local WebChat port is out of range")
	}
	return nil
}
