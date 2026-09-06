package main

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
)

func TestProductionBrowserBackendsArePlaywrightOnly(t *testing.T) {
	cfg := config.Default()
	controller := &browsercontrol.Service{}
	if _, ok := browserautomation.NewPlaywrightExtensionAdapter(cfg, controller).(*browserautomation.PlaywrightExtensionAdapter); !ok {
		t.Fatal("browser adapter is not Playwright Extension")
	}
	login := emailautomation.NewPlaywrightRunner(controller)
	var loginBrowser emailautomation.LoginBrowser = login
	var scriptRunner emailautomation.ScriptRunner = login
	if loginBrowser != login || scriptRunner != login {
		t.Fatal("email automation did not share one Playwright runner")
	}
}
