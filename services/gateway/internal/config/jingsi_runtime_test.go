package config

import (
	"os"
	"path/filepath"
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
