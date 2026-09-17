package main

import (
	"log/slog"
	"os"
	"path/filepath"
)

// warnDivergentEmailWorkspace turns a silent misconfiguration into a named one.
// The Browser Controller writes captures under SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT
// while the gateway reads them under its own workspace root. Nothing validates
// that the two agree, and when they do not every capture fails as a missing
// source with no indication of why.
func warnDivergentEmailWorkspace(gatewayRoot string) {
	controllerRoot := os.Getenv("SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT")
	if controllerRoot == "" {
		return
	}
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		return absolute
	}
	if resolve(controllerRoot) != resolve(gatewayRoot) {
		slog.Warn("email workspace root divergent; captures will not be readable",
			"gateway_root", gatewayRoot, "controller_root", controllerRoot)
	}
}
