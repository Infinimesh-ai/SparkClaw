package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalManagementConfigurationIsOptionalAndRequiresBoundAbsolutePath(t *testing.T) {
	for _, test := range []struct{ name, path, deployment, wantError string }{
		{name: "default disabled"},
		{name: "relative path", path: "runtime/local-management.json", deployment: "deployment-test", wantError: "absolute path"},
		{name: "no deployment", path: filepath.Join(t.TempDir(), "local-management.json"), wantError: "SPARKCLAW_DEPLOYMENT_ID"},
		{name: "bound path", path: filepath.Join(t.TempDir(), "local-management.json"), deployment: "deployment-test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SPARKCLAW_LOCAL_MANAGEMENT_FILE", test.path)
			t.Setenv("SPARKCLAW_DEPLOYMENT_ID", test.deployment)
			cfg, err := Load("")
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("load error=%v want %s", err, test.wantError)
				}
				return
			}
			if err != nil || cfg.Gateway.LocalManagementFile != test.path {
				t.Fatalf("management path=%s error=%v", cfg.Gateway.LocalManagementFile, err)
			}
		})
	}
}
