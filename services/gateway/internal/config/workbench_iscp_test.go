package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkbenchISCPMigrationUsesIndependentExplicitProfiles(t *testing.T) {
	dir := t.TempDir()
	cfg := GatewayConfig{DeploymentID: "deployment", WorkbenchISCPConfig: filepath.Join(dir, "old.json"), WorkbenchISCPLocalTest: true, WorkbenchISCPNodeConfig: filepath.Join(dir, "node.json"), WorkbenchISCPNodeManaged: true}
	if err := normalizeWorkbenchISCPConfig(&cfg); err != nil {
		t.Fatal("independent migration profiles rejected", err)
	}
	for _, change := range []func(*GatewayConfig){func(c *GatewayConfig) { c.WorkbenchISCPNodeConfig = c.WorkbenchISCPConfig }, func(c *GatewayConfig) { c.WorkbenchISCPNodeConfig = "" }, func(c *GatewayConfig) { c.WorkbenchISCPNodeManaged = false }, func(c *GatewayConfig) { c.WorkbenchISCPNodeConfig = "relative.json" }} {
		bad := cfg
		change(&bad)
		if err := normalizeWorkbenchISCPConfig(&bad); err == nil {
			t.Fatal("overlapping/implicit migration profile accepted")
		}
	}
}

func TestWorkbenchISCPConfigurationRequiresExplicitIsolatedOptIn(t *testing.T) {
	private := filepath.Join(t.TempDir(), "responder.json")
	for _, test := range []struct{ name, path, deployment, optIn, wantError string }{
		{name: "disabled by default"},
		{name: "explicit path requires opt in", path: private, deployment: "deployment-test", wantError: "LOCAL_TEST=1"},
		{name: "relative path", path: "responder.json", deployment: "deployment-test", optIn: "1", wantError: "absolute path"},
		{name: "deployment required", path: private, optIn: "1", wantError: "DEPLOYMENT_ID"},
		{name: "invalid opt in", path: private, deployment: "deployment-test", optIn: "true", wantError: "0 or 1"},
		{name: "isolated test", path: private, deployment: "deployment-test", optIn: "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SPARKCLAW_WORKBENCH_ISCP_CONFIG", test.path)
			t.Setenv("SPARKCLAW_WORKBENCH_ISCP_LOCAL_TEST", test.optIn)
			t.Setenv("SPARKCLAW_DEPLOYMENT_ID", test.deployment)
			cfg, err := Load("")
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error=%v want %s", err, test.wantError)
				}
				return
			}
			if err != nil || cfg.Gateway.WorkbenchISCPConfig != test.path {
				t.Fatalf("path=%q error=%v", cfg.Gateway.WorkbenchISCPConfig, err)
			}
			raw, err := json.Marshal(cfg.Gateway)
			if err != nil || bytesContainsPrivatePath(raw, private) {
				t.Fatalf("public config leaked ISCP private configuration: %s %v", raw, err)
			}
		})
	}
}

func bytesContainsPrivatePath(raw []byte, path string) bool {
	return strings.Contains(string(raw), path) || strings.Contains(string(raw), "workbench_iscp")
}
