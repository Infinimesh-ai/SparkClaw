package browserautomation

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const hiddenBrowserViewport = "1365x768"

//go:embed scripts/playwright_driver.cjs
var playwrightDriverScript string

type playwrightDriverConfig struct {
	ExecutablePath string `json:"executablePath"`
	UserDataDir    string `json:"userDataDir"`
	Headless       bool   `json:"headless"`
	TimeoutMS      int    `json:"timeoutMS"`
}

type driverRequest struct {
	ID     int            `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type driverResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *driverError    `json:"error,omitempty"`
}

type driverError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type playwrightActionError struct {
	Method  string
	Code    string
	Message string
}

func (e *playwrightActionError) Error() string {
	return fmt.Sprintf("Playwright %s failed (%s): %s", e.Method, e.Code, e.Message)
}

type playwrightSession struct {
	cancel    context.CancelFunc
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	out       *bufio.Scanner
	errs      *safeBuffer
	nextID    int
	done      chan struct{}
	closeOnce sync.Once
}

func (a *PlaywrightAdapter) health(ctx context.Context) (any, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	hidden := a.activePresentation != "visible"
	profileKey := a.activeProfile
	if profileKey == "" {
		profileKey = a.browserProfileKey(nil)
	}
	session, err := a.ensureSessionLocked(ctx, hidden, profileKey)
	if err != nil {
		return nil, hidden, err
	}
	out, err := session.request(ctx, "health", nil)
	if err != nil {
		a.resetSessionLocked()
	}
	return out, hidden, err
}

func (a *PlaywrightAdapter) callToolWithSession(ctx context.Context, hidden bool, profileKey, name string, args map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, err := a.ensureSessionLocked(ctx, hidden, profileKey)
	if err != nil {
		return nil, err
	}
	out, err := session.request(ctx, name, args)
	if err != nil {
		var actionErr *playwrightActionError
		if !errors.As(err, &actionErr) {
			a.resetSessionLocked()
		}
		return nil, err
	}
	return out, nil
}

func (a *PlaywrightAdapter) ensureSessionLocked(ctx context.Context, hidden bool, profileKey string) (*playwrightSession, error) {
	presentation := "visible"
	if hidden {
		presentation = "hidden"
	}
	if a.session != nil && a.session.alive() && a.activeProfile == profileKey && a.activePresentation == presentation {
		return a.session, nil
	}
	a.resetSessionLocked()
	session, err := a.newSession(ctx, hidden, profileKey)
	if err != nil {
		return nil, err
	}
	startupCtx, cancel := context.WithTimeout(ctx, adapterTimeout(a.cfg.Adapters.BrowserAutomation.TimeoutMS))
	defer cancel()
	if _, err := session.request(startupCtx, "health", nil); err != nil {
		session.close()
		return nil, fmt.Errorf("start Playwright browser session: %w", err)
	}
	a.session = session
	a.activeProfile = profileKey
	a.activePresentation = presentation
	return session, nil
}

func (a *PlaywrightAdapter) resetSessionLocked() {
	if a.session != nil {
		a.session.close()
		a.session = nil
	}
	a.activeProfile = ""
	a.activePresentation = ""
}

func (a *PlaywrightAdapter) newSession(ctx context.Context, hidden bool, profileKey string) (*playwrightSession, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	adapterCfg := a.cfg.Adapters.BrowserAutomation
	nodeCommand := strings.TrimSpace(adapterCfg.NodeCommand)
	if nodeCommand == "" {
		nodeCommand = "node"
	}
	nodePath, err := exec.LookPath(nodeCommand)
	if err != nil {
		return nil, fmt.Errorf("resolve Playwright Node command %q: %w", nodeCommand, err)
	}
	executable, err := resolveChromiumExecutable(adapterCfg.ChromiumExecutable)
	if err != nil {
		return nil, err
	}
	profileDir, err := resolveSharedProfileDir(adapterCfg.ProfileDir, profileKey)
	if err != nil {
		return nil, err
	}
	runtimeDir, err := resolvePlaywrightRuntimeDir()
	if err != nil {
		return nil, err
	}
	launchConfig, err := json.Marshal(playwrightDriverConfig{
		ExecutablePath: executable,
		UserDataDir:    profileDir,
		Headless:       hidden,
		TimeoutMS:      adapterTimeoutMS(adapterCfg.TimeoutMS),
	})
	if err != nil {
		return nil, err
	}
	execCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(execCtx, nodePath, "-e", playwrightDriverScript)
	configureDriverCommand(cmd)
	cmd.Dir = runtimeDir
	cmd.Env = append(os.Environ(), "SPARKCLAW_PLAYWRIGHT_DRIVER_CONFIG="+string(launchConfig))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	errs := &safeBuffer{}
	go func() { _, _ = io.Copy(errs, stderr) }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	session := &playwrightSession{
		cancel: cancel,
		cmd:    cmd,
		stdin:  stdin,
		out:    scanner,
		errs:   errs,
		nextID: 1,
		done:   make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(session.done)
	}()
	return session, nil
}

func adapterTimeout(timeoutMS int) time.Duration {
	return time.Duration(adapterTimeoutMS(timeoutMS)) * time.Millisecond
}

func adapterTimeoutMS(timeoutMS int) int {
	if timeoutMS <= 0 {
		return 30000
	}
	return timeoutMS
}

func resolvePlaywrightRuntimeDir() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("SPARKCLAW_BROWSER_RUNTIME_DIR")); configured != "" {
		return validatePlaywrightRuntimeDir(configured)
	}
	wd, err := os.Getwd()
	if err == nil {
		for current := wd; ; current = filepath.Dir(current) {
			if dir, dirErr := validatePlaywrightRuntimeDir(current); dirErr == nil {
				return dir, nil
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
		}
	}
	return "", errors.New("Playwright runtime was not found; run npm install at the SparkClaw repository root or set SPARKCLAW_BROWSER_RUNTIME_DIR")
}

func validatePlaywrightRuntimeDir(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	manifest := filepath.Join(abs, "node_modules", "playwright", "package.json")
	if info, statErr := os.Stat(manifest); statErr != nil || info.IsDir() {
		return "", fmt.Errorf("Playwright package is unavailable under %q", abs)
	}
	return abs, nil
}

func resolveChromiumExecutable(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return "", nil
	}
	return validateChromiumExecutable(configured)
}

func validateChromiumExecutable(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve Chromium executable: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("browser shared profile Chromium executable %q: %w", abs, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("browser shared profile Chromium executable %q is a directory", abs)
	}
	return abs, nil
}

func resolveSharedProfileDir(configured, profileKey string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		configured = "./data/browser-profiles"
	}
	root, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("resolve browser shared profile directory: %w", err)
	}
	ownerID, profileID, _ := strings.Cut(strings.TrimSpace(profileKey), "\x00")
	if ownerID == "" {
		ownerID = "owner"
	}
	if profileID == "" {
		profileID = "default"
	}
	ownerDigest := sha256.Sum256([]byte(ownerID))
	profileDigest := sha256.Sum256([]byte(profileID))
	abs := filepath.Join(root, fmt.Sprintf("%x", ownerDigest[:12]), fmt.Sprintf("%x", profileDigest[:12]), "user-data")
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", fmt.Errorf("create browser shared profile directory: %w", err)
	}
	return abs, nil
}

func (a *PlaywrightAdapter) browserProfileKey(args map[string]any) string {
	ownerID := strings.TrimSpace(stringArg(args, "owner_id"))
	if ownerID == "" {
		ownerID = "owner"
	}
	profileID := strings.TrimSpace(stringArg(args, "browser_profile_id"))
	if profileID == "" {
		profileID = strings.TrimSpace(a.cfg.Tools.BrowserAutomation.Profile)
	}
	if profileID == "" {
		profileID = "default"
	}
	return ownerID + "\x00" + profileID
}

func (s *playwrightSession) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *playwrightSession) request(ctx context.Context, method string, params map[string]any) (any, error) {
	id := s.nextID
	s.nextID++
	if err := s.write(driverRequest{ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	type requestResult struct {
		response driverResponse
		err      error
	}
	done := make(chan requestResult, 1)
	go func() {
		for s.out.Scan() {
			var response driverResponse
			if err := json.Unmarshal(s.out.Bytes(), &response); err != nil || response.ID != id {
				continue
			}
			done <- requestResult{response: response}
			return
		}
		if err := s.out.Err(); err != nil {
			done <- requestResult{err: err}
			return
		}
		done <- requestResult{err: fmt.Errorf("Playwright driver returned no response: %s", s.errs.String())}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		if result.err != nil {
			return nil, result.err
		}
		if result.response.Error != nil {
			return nil, &playwrightActionError{Method: method, Code: result.response.Error.Code, Message: result.response.Error.Message}
		}
		var decoded any
		if len(result.response.Result) > 0 {
			if err := json.Unmarshal(result.response.Result, &decoded); err != nil {
				return nil, err
			}
		}
		return decoded, nil
	}
}

func (s *playwrightSession) write(request driverRequest) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = s.stdin.Write(raw)
	return err
}

func (s *playwrightSession) close() {
	s.closeOnce.Do(func() {
		_ = s.write(driverRequest{ID: 0, Method: "close"})
		_ = s.stdin.Close()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			terminateDriverProcess(s.cmd)
			s.cancel()
			<-s.done
		}
		s.cancel()
	})
}

type safeBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > 4096 {
		b.buf = b.buf[len(b.buf)-4096:]
	}
	return len(p), nil
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
