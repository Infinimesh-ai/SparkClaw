package config

import (
	"errors"
	"path/filepath"
	"strings"
)

func normalizeWorkbenchISCPConfig(cfg *GatewayConfig) error {
	cfg.WorkbenchISCPConfig = strings.TrimSpace(cfg.WorkbenchISCPConfig)
	cfg.WorkbenchISCPNodeConfig = strings.TrimSpace(cfg.WorkbenchISCPNodeConfig)
	if cfg.WorkbenchISCPNodeConfig != "" {
		if !filepath.IsAbs(cfg.WorkbenchISCPNodeConfig) || cfg.WorkbenchISCPNodeConfig == cfg.WorkbenchISCPConfig || !cfg.WorkbenchISCPNodeManaged {
			return errors.New("separate node responder requires distinct absolute configuration and explicit node-managed opt-in")
		}
		if cfg.DeploymentID == "" {
			return errors.New("node responder requires SPARKCLAW_DEPLOYMENT_ID")
		}
	}
	if cfg.WorkbenchISCPConfig == "" {
		return nil
	}
	if !filepath.IsAbs(cfg.WorkbenchISCPConfig) {
		return errors.New("SPARKCLAW_WORKBENCH_ISCP_CONFIG must be an absolute path")
	}
	if cfg.WorkbenchISCPLocalTest && cfg.WorkbenchISCPNodeManaged && cfg.WorkbenchISCPNodeConfig == "" {
		return errors.New("ISCP node-managed and local-test modes are mutually exclusive")
	}
	if !cfg.WorkbenchISCPLocalTest && !cfg.WorkbenchISCPNodeManaged {
		return errors.New("ISCP local test issuer requires SPARKCLAW_WORKBENCH_ISCP_LOCAL_TEST=1")
	}
	if cfg.DeploymentID == "" {
		return errors.New("SPARKCLAW_WORKBENCH_ISCP_CONFIG requires SPARKCLAW_DEPLOYMENT_ID")
	}
	return nil
}
