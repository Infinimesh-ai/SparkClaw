package config

import (
	"path/filepath"
	"testing"
)

func TestLocalWebChatConfigurationFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, enabled, file, deployment string
		valid                           bool
	}{
		{"disabled", "false", "", "", true},
		{"typo", "tru", "", "", false},
		{"missing file", "true", "", "deployment", false},
		{"relative", "true", "local-webchat.json", "deployment", false},
		{"missing binding", "true", filepath.Join(t.TempDir(), "local-webchat.json"), "", false},
		{"bound", "true", filepath.Join(t.TempDir(), "local-webchat.json"), "deployment", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SPARKCLAW_LOCAL_WEBCHAT_ENABLED", test.enabled)
			t.Setenv("SPARKCLAW_LOCAL_WEBCHAT_FILE", test.file)
			t.Setenv("SPARKCLAW_DEPLOYMENT_ID", test.deployment)
			t.Setenv("SPARKCLAW_PAIRING_REQUIRED", "true")
			_, err := Load(repositoryPath("configs", "sparkclaw.default.json"))
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}

func TestLocalWebChatCannotEnableAnonymousNetworkFallback(t *testing.T) {
	t.Setenv("SPARKCLAW_LOCAL_WEBCHAT_ENABLED", "true")
	t.Setenv("SPARKCLAW_LOCAL_WEBCHAT_FILE", filepath.Join(t.TempDir(), "local-webchat.json"))
	t.Setenv("SPARKCLAW_DEPLOYMENT_ID", "deployment")
	t.Setenv("SPARKCLAW_PAIRING_REQUIRED", "false")
	t.Setenv("SPARKCLAW_API_TOKEN", "")
	if _, err := Load(repositoryPath("configs", "sparkclaw.default.json")); err == nil {
		t.Fatal("anonymous network fallback admitted with local access")
	}
}
