package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMailSendLoginReuseDefaultsOverridesAndBounds(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Adapters.BrowserAutomation.PlaywrightExtension.MailSendLoginReuseSeconds; got != 120 {
		t.Fatalf("default reuse window = %d, want 120", got)
	}
	t.Setenv("SPARKCLAW_MAIL_SEND_LOGIN_REUSE_SECONDS", "0")
	if cfg, err = Load(""); err != nil || cfg.Adapters.BrowserAutomation.PlaywrightExtension.MailSendLoginReuseSeconds != 0 {
		t.Fatalf("explicit 0 must disable reuse: %v", err)
	}
	t.Setenv("SPARKCLAW_MAIL_SEND_LOGIN_REUSE_SECONDS", "")
	path := filepath.Join(t.TempDir(), "sparkclaw.json")
	for value, valid := range map[string]bool{"45": true, "600": true, "601": false, "-1": false} {
		raw := `{"adapters":{"browserAutomation":{"playwrightExtension":{"mailSendLoginReuseSeconds":` + value + `}}}}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if valid && (err != nil || cfg.Adapters.BrowserAutomation.PlaywrightExtension.MailSendLoginReuseSeconds == 120) {
			t.Fatalf("%s: %v", value, err)
		}
		if !valid && (err == nil || !strings.Contains(err.Error(), "mailSendLoginReuseSeconds")) {
			t.Fatalf("%s accepted: %v", value, err)
		}
	}
}
