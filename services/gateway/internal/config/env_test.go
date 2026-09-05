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
