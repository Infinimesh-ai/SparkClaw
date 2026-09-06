package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJingSiRuntimeConfigLoadsSecretOnlyBearerAndRequiresLoopback(t *testing.T) {
	t.Setenv("SPARKCLAW_JINGSI_RUNTIME_V1_ENABLED", "true")
	t.Setenv("SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN", "runtime-secret-value-123")
	t.Setenv("SPARKCLAW_JINGSI_RUNTIME_V1_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.JingSiRuntime.Enabled || cfg.JingSiRuntime.BearerToken != "runtime-secret-value-123" || !filepath.IsAbs(cfg.JingSiRuntime.StateDir) {
		t.Fatalf("runtime config not normalized: %#v", cfg.JingSiRuntime)
	}

	t.Setenv("SPARKCLAW_BIND", "0.0.0.0")
	if _, err := Load(""); err == nil {
		t.Fatal("enabled Runtime v1 accepted a non-loopback gateway bind")
	}
}

func TestJingSiRuntimeConfigRequiresOwnerOnlyTokenFile(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "runtime.token")
	if err := os.WriteFile(tokenPath, []byte("runtime-secret-value-456\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPARKCLAW_JINGSI_RUNTIME_V1_ENABLED", "true")
	t.Setenv("SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN_FILE", tokenPath)
	if _, err := Load(""); err == nil {
		t.Fatal("group-readable Runtime token file was accepted")
	}
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() owner-only token error = %v", err)
	}
	if cfg.JingSiRuntime.BearerToken != "runtime-secret-value-456" {
		t.Fatal("owner-only token file was not loaded")
	}
}

func TestJingSiRuntimeConfigRetentionDaysDefaultsAndValidates(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.JingSiRuntime.RetentionDays != 30 {
		t.Fatalf("retention_days default = %d, want 30", cfg.JingSiRuntime.RetentionDays)
	}
	t.Setenv("SPARKCLAW_JINGSI_RUNTIME_V1_RETENTION_DAYS", "0")
	cfg, err = Load("")
	if err != nil || cfg.JingSiRuntime.RetentionDays != 0 {
		t.Fatalf("explicit zero (keep forever) rejected: %v %d", err, cfg.JingSiRuntime.RetentionDays)
	}
	for _, invalid := range []string{"-1", "3651"} {
		t.Setenv("SPARKCLAW_JINGSI_RUNTIME_V1_RETENTION_DAYS", invalid)
		if _, err := Load(""); err == nil {
			t.Fatalf("retention_days=%s was accepted", invalid)
		}
	}
}

func TestJingSiRuntimeRejectsRetiredEffectSwitch(t *testing.T) {
	for _, value := range []string{"false", "true", "null"} {
		t.Run("config_"+value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"jingsi_runtime_v1":{"enforce_effect_scopes":`+value+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "enforce_effect_scopes is retired") {
				t.Fatalf("retired config was not rejected: %v", err)
			}
		})
	}
	for _, value := range []string{"false", "true", ""} {
		t.Run("env_"+value, func(t *testing.T) {
			t.Setenv("SPARKCLAW_JINGSI_RUNTIME_V1_ENFORCE_EFFECT_SCOPES", value)
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "ENFORCE_EFFECT_SCOPES is retired") {
				t.Fatalf("retired env was not rejected: %v", err)
			}
		})
	}
}
