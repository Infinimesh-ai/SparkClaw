package config

import (
	"errors"
	"path/filepath"
	"strings"
)

func normalizeWorkbenchISCPConfig(cfg *GatewayConfig) error {
	cfg.WorkbenchISCPConfig = strings.TrimSpace(cfg.WorkbenchISCPConfig)
	if cfg.WorkbenchISCPConfig == "" {
		return nil
	}
	if !filepath.IsAbs(cfg.WorkbenchISCPConfig) {
		return errors.New("SPARKCLAW_WORKBENCH_ISCP_CONFIG must be an absolute path")
	}
	if !cfg.WorkbenchISCPLocalTest {
		return errors.New("ISCP local test issuer requires SPARKCLAW_WORKBENCH_ISCP_LOCAL_TEST=1")
	}
	if cfg.DeploymentID == "" {
		return errors.New("SPARKCLAW_WORKBENCH_ISCP_CONFIG requires SPARKCLAW_DEPLOYMENT_ID")
	}
	return nil
}
