package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestWorkbenchISCPStartupIsDisabledByDefaultAndFailsClosed(t *testing.T) {
	cfg := config.Default()
	service, err := startWorkbenchISCP(t.Context(), cfg, nil)
	if err != nil || service != nil {
		t.Fatalf("default startup enabled ISCP: %v", err)
	}
	cfg.Gateway.WorkbenchISCPConfig = filepath.Join(t.TempDir(), "responder.json")
	if _, err := startWorkbenchISCP(t.Context(), cfg, nil); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("test issuer lacked explicit runtime opt-in: %v", err)
	}
	cfg.Gateway.WorkbenchISCPLocalTest = true
	if _, err := startWorkbenchISCP(t.Context(), cfg, nil); err == nil || !strings.Contains(err.Error(), "invalid or unavailable") {
		t.Fatalf("missing private transport config did not fail closed: %v", err)
	}
}
