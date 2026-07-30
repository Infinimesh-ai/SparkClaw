package browserautomation

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// browserDaemonOwnerFileName records which agent-browser daemon (namespace +
// session) last launched Chromium against a shared profile. It lives next to
// the profile lease file so a later gateway process — whose own namespace is
// freshly randomized — can still address a daemon leaked by an unclean exit.
const browserDaemonOwnerFileName = ".sparkclaw-daemon-owner.json"

type browserDaemonOwner struct {
	Namespace string `json:"namespace"`
	Session   string `json:"session"`
}

func browserDaemonOwnerPath(profileDir string) string {
	return filepath.Join(filepath.Dir(profileDir), browserDaemonOwnerFileName)
}

// recordBrowserDaemonOwner is best-effort: the marker only speeds up reclaim
// after an unclean exit, so failing to write it must not fail the launch.
func recordBrowserDaemonOwner(profileDir, namespace, session string) {
	raw, err := json.Marshal(browserDaemonOwner{Namespace: namespace, Session: session})
	if err != nil {
		return
	}
	_ = os.WriteFile(browserDaemonOwnerPath(profileDir), raw, 0o600)
}

func readBrowserDaemonOwner(profileDir string) (browserDaemonOwner, bool) {
	raw, err := os.ReadFile(browserDaemonOwnerPath(profileDir))
	if err != nil {
		return browserDaemonOwner{}, false
	}
	var owner browserDaemonOwner
	if err := json.Unmarshal(raw, &owner); err != nil ||
		strings.TrimSpace(owner.Namespace) == "" || strings.TrimSpace(owner.Session) == "" {
		return browserDaemonOwner{}, false
	}
	return owner, true
}

// reclaimLeakedBrowserProfile handles the profile-busy verdict that follows an
// unclean gateway exit: the kernel released the profile lease with the dead
// process, but the leaked agent-browser daemon keeps Chromium (and therefore
// its SingletonSocket) alive, so recoverStaleChromiumSingletons reports busy on
// every startup. Ask the daemon recorded as the profile's last owner to close
// its browser, then re-probe the singleton artifacts once. Must run while the
// caller exclusively holds the profile lease.
func reclaimLeakedBrowserProfile(ctx context.Context, lease *browserProfileLease, commandPath, profileDir, fallbackNamespace, fallbackSession string) error {
	owner, ok := readBrowserDaemonOwner(profileDir)
	if !ok {
		owner = browserDaemonOwner{Namespace: fallbackNamespace, Session: fallbackSession}
	}
	closeLeakedBrowserDaemon(ctx, commandPath, owner)
	_, err := lease.recoverStaleChromiumSingletons(profileDir)
	return err
}

func closeLeakedBrowserDaemon(ctx context.Context, commandPath string, owner browserDaemonOwner) {
	if strings.TrimSpace(commandPath) == "" || strings.TrimSpace(owner.Session) == "" {
		return
	}
	closeCtx, cancel := context.WithTimeout(ctx, agentBrowserFallbackClose)
	defer cancel()
	cmd := exec.CommandContext(closeCtx, commandPath, "close", "--session", owner.Session, "--json")
	configureAdapterCommand(cmd)
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "AGENT_BROWSER_") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"AGENT_BROWSER_NAMESPACE="+owner.Namespace,
		"AGENT_BROWSER_SESSION="+owner.Session,
	)
	cmd.Env = env
	_ = cmd.Run()
}
