package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvBindingsAreUniqueAndDocumented(t *testing.T) {
	seen := map[string]bool{}
	for _, binding := range envBindings {
		if binding.name == "" || binding.apply == nil || binding.doc == "" {
			t.Fatalf("env binding %q must have a name, apply, and doc", binding.name)
		}
		for _, name := range append([]string{binding.name}, binding.aliases...) {
			if !environmentNamePattern.MatchString(name) {
				t.Fatalf("env binding name %q is not a valid environment variable name", name)
			}
			if seen[name] {
				t.Fatalf("env binding name %q is declared twice", name)
			}
			seen[name] = true
		}
	}
	for _, retired := range retiredEnvironment {
		if seen[retired.name] {
			t.Fatalf("retired variable %q is also a live binding", retired.name)
		}
	}
}

func TestEnvBindingAliasesOnlyFillUnsetName(t *testing.T) {
	values := map[string]string{
		"SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES": "1234",
		"SPARKCLAW_REACT_MAX_OBSERVATION_BYTES":         "999",
	}
	lookup := func(name string) string { return values[name] }

	cfg := Default()
	if err := applyEnvBindings(&cfg, lookup); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.RunMaxObservationBytes != 1234 {
		t.Fatalf("first deprecated alias should win over the second: %d", cfg.Runtime.RunMaxObservationBytes)
	}

	values["SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES"] = "not-a-number"
	cfg = Default()
	if err := applyEnvBindings(&cfg, lookup); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.RunMaxObservationBytes != Default().Runtime.RunMaxObservationBytes {
		t.Fatalf("an unparsable new name must not fall back to deprecated aliases: %d", cfg.Runtime.RunMaxObservationBytes)
	}
}

func TestEnvBindingsSkipEmptyValues(t *testing.T) {
	cfg := Default()
	empty := func(string) string { return "" }
	if err := applyEnvBindings(&cfg, empty); err != nil {
		t.Fatal(err)
	}
	want := Default()
	// applyEnvBindings resolves these two paths even without overrides.
	want.State.CredentialKeyFile = cfg.State.CredentialKeyFile
	want.Storage.ArtifactDir = cfg.Storage.ArtifactDir
	if cfg.Gateway != want.Gateway || cfg.Runtime != want.Runtime || cfg.Model.Fast.BaseURL != want.Model.Fast.BaseURL || cfg.State != want.State {
		t.Fatalf("empty environment changed the defaults: %#v", cfg)
	}
}

func TestLoadRejectsRetiredModelModeAndJSONMock(t *testing.T) {
	t.Setenv("SPARKCLAW_MODEL_MODE", "external")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "SPARKCLAW_MODEL_MODE is retired") {
		t.Fatalf("SPARKCLAW_MODEL_MODE should be rejected: %v", err)
	}
	t.Setenv("SPARKCLAW_MODEL_MODE", "")

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"model":{"mock":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "model.mock is not allowed") {
		t.Fatalf("model.mock should be rejected: %v", err)
	}
}

func TestLoadPopulatesModelAPIKeyAndWarnsOnRemoteEndpointsWithoutIt(t *testing.T) {
	t.Setenv("SPARKCLAW_FAST_BASE_URL", "https://models.example.com/v1")
	t.Setenv("SPARKCLAW_DEEP_BASE_URL", "http://sparkclaw-deep:8002/v1")
	t.Setenv("OPENAI_API_KEY", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.APIKey != "" {
		t.Fatalf("api key should be empty: %q", cfg.Model.APIKey)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "model.fast.base_url") || !strings.Contains(cfg.Warnings[0], "OPENAI_API_KEY") {
		t.Fatalf("expected one remote-endpoint warning for the fast lane, got %v", cfg.Warnings)
	}

	t.Setenv("OPENAI_API_KEY", " secret-token ")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.APIKey != "secret-token" {
		t.Fatalf("api key should be trimmed from OPENAI_API_KEY: %q", cfg.Model.APIKey)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("no warnings expected once the key is set: %v", cfg.Warnings)
	}
}

func TestMockProfileNeverWarnsAboutMissingAPIKey(t *testing.T) {
	t.Setenv("SPARKCLAW_MODEL_CAPACITY_PROFILE", "mock")
	t.Setenv("SPARKCLAW_FAST_BASE_URL", "https://models.example.com/v1")
	t.Setenv("OPENAI_API_KEY", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Model.Mock || len(cfg.Warnings) != 0 {
		t.Fatalf("mock profile should not warn: mock=%v warnings=%v", cfg.Model.Mock, cfg.Warnings)
	}
}
