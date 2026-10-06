package localwebchat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialRequiresPrivateOwnedDeploymentFiles(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *fixture)
	}{
		{"wrong file mode", func(t *testing.T, f *fixture) {
			must(t, os.Chmod(filepath.Join(f.runtime, "local-webchat.json"), 0o640))
		}},
		{"wrong directory mode", func(t *testing.T, f *fixture) { must(t, os.Chmod(f.runtime, 0o750)) }},
		{"credential symlink", func(t *testing.T, f *fixture) {
			filename := filepath.Join(f.runtime, "local-webchat.json")
			must(t, os.Rename(filename, filename+".saved"))
			must(t, os.Symlink(filename+".saved", filename))
		}},
		{"management credential", func(t *testing.T, f *fixture) { f.credential.ClientID = "local_management_owner"; f.saveCredential(t) }},
		{"desktop credential", func(t *testing.T, f *fixture) { f.credential.ClientID = "desktop_owner"; f.saveCredential(t) }},
		{"old schema", func(t *testing.T, f *fixture) { f.credential.SchemaVersion = 0; f.saveCredential(t) }},
		{"short secret", func(t *testing.T, f *fixture) { f.credential.Token = "too-short"; f.saveCredential(t) }},
		{"header injection", func(t *testing.T, f *fixture) {
			f.credential.Token = strings.Repeat("x", 32) + "\r\nCookie: x"
			f.saveCredential(t)
		}},
		{"extra document", func(t *testing.T, f *fixture) {
			filename := filepath.Join(f.runtime, "local-webchat.json")
			data, err := os.ReadFile(filename)
			must(t, err)
			must(t, os.WriteFile(filename, append(data, []byte("{}")...), 0o600))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, nil)
			test.mutate(t, f)
			if _, err := readCredential(f.runtime); err == nil {
				t.Fatal("unsafe credential accepted")
			}
		})
	}
}

func TestSocketRequiresPrivateDeploymentOwnership(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *fixture)
	}{
		{"socket mode", func(t *testing.T, f *fixture) {
			must(t, os.Chmod(filepath.Join(f.runtime, "local-webchat", "workbench.sock"), 0o660))
		}},
		{"parent mode", func(t *testing.T, f *fixture) { must(t, os.Chmod(filepath.Join(f.runtime, "local-webchat"), 0o755)) }},
		{"socket symlink", func(t *testing.T, f *fixture) {
			filename := filepath.Join(f.runtime, "local-webchat", "workbench.sock")
			must(t, os.Rename(filename, filename+".saved"))
			must(t, os.Symlink(filename+".saved", filename))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, nil)
			test.mutate(t, f)
			res, _ := f.request(t, "/readyz", nil)
			if res.StatusCode != 503 {
				t.Fatalf("unsafe socket status=%d", res.StatusCode)
			}
		})
	}
}

func TestEnvironmentDefaultsAndValidation(t *testing.T) {
	cfg, enabled, err := LoadEnvironment(func(string) string { return "" })
	if err != nil || enabled || cfg.Port != 18794 || cfg.RuntimeDir != "/run/sparkclaw/runtime" || cfg.AssetsDir != "/usr/share/sparkclaw/webchat" {
		t.Fatalf("defaults: %+v %v %v", cfg, enabled, err)
	}
	for _, entry := range []struct{ key, value string }{{"SPARKCLAW_LOCAL_WEBCHAT_ENABLED", "sometimes"}, {"SPARKCLAW_LOCAL_WEBCHAT_ENABLED", "TRUE"}, {"SPARKCLAW_LOCAL_WEBCHAT_ENABLED", "1"}, {"SPARKCLAW_LOCAL_WEBCHAT_PORT", "0"}, {"SPARKCLAW_LOCAL_WEBCHAT_PORT", "65536"}, {"SPARKCLAW_LOCAL_WEBCHAT_PORT", "oops"}, {"SPARKCLAW_LOCAL_WEBCHAT_ASSETS", "relative"}, {"SPARKCLAW_LOCAL_WORKBENCH_RUNTIME_DIR", "/tmp/../runtime"}} {
		if _, _, err := LoadEnvironment(func(key string) string {
			if key == entry.key {
				return entry.value
			}
			return ""
		}); err == nil {
			t.Errorf("accepted %s=%s", entry.key, entry.value)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
