package browserautomation

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const (
	agentBrowserVersion             = "0.32.3"
	agentBrowserProtocolVersion     = "2025-11-25"
	agentBrowserMCPToolsProfile     = "core,tabs"
	agentBrowserMaxMessageBytes     = 16 << 20
	hiddenBrowserViewport           = "1365x768"
	agentBrowserFallbackClose       = 10 * time.Second
	agentBrowserTransportHeadroomMS = 5000
)

var requiredAgentBrowserTools = map[string][]string{
	"agent_browser_open":          {},
	"agent_browser_reload":        {},
	"agent_browser_read":          {},
	"agent_browser_snapshot":      {},
	"agent_browser_click":         {"selector"},
	"agent_browser_fill":          {"selector", "text"},
	"agent_browser_type":          {"selector", "text"},
	"agent_browser_select":        {"selector", "values"},
	"agent_browser_wait_ms":       {"ms"},
	"agent_browser_wait_for_text": {"text"},
	"agent_browser_wait_for_load": {"state"},
	"agent_browser_screenshot":    {},
	"agent_browser_get_url":       {},
	"agent_browser_get_title":     {},
	"agent_browser_get_text":      {"selector"},
	"agent_browser_tab_new":       {},
	"agent_browser_tab_list":      {},
	"agent_browser_tab_switch":    {"tab"},
	"agent_browser_tab_close":     {},
	"agent_browser_close":         {},
}

type agentBrowserAdapterConfig = config.BrowserAutomationAdapterConfig

type agentBrowserSession struct {
	cancel       context.CancelFunc
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	out          *bufio.Scanner
	errs         *boundedBuffer
	nextID       int
	done         chan struct{}
	closeOnce    sync.Once
	sessionName  string
	namespace    string
	timeoutMS    int
	version      string
	commandPath  string
	environment  []string
	profileLease *browserProfileLease
}

type agentBrowserToolResult struct {
	Data    any
	Content []any
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type agentBrowserActionError struct {
	Tool    string
	Message string
}

func (e *agentBrowserActionError) Error() string {
	return fmt.Sprintf("agent-browser %s failed: %s", e.Tool, e.Message)
}

func newAgentBrowserNamespace() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		fallback := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())))
		copy(raw, fallback[:len(raw)])
	}
	return "sc-" + hex.EncodeToString(raw)
}

func agentBrowserSessionName(profileKey, presentation string) string {
	digest := sha256.Sum256([]byte(profileKey + "\x00" + presentation))
	return "sc-" + hex.EncodeToString(digest[:10])
}

func newAgentBrowserSession(ctx context.Context, cfg agentBrowserAdapterConfig, commandPath, namespace string, hidden bool, profileKey string) (*agentBrowserSession, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	profileDir, err := resolveSharedProfileDir(cfg.ProfileDir, profileKey)
	if err != nil {
		return nil, err
	}
	profileLease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		if errors.Is(err, errBrowserProfileBusy) {
			return nil, errBrowserProfileBusy
		}
		return nil, err
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			profileLease.release()
		}
	}()
	presentation := "visible"
	if hidden {
		presentation = "hidden"
	}
	sessionName := agentBrowserSessionName(profileKey, presentation)
	if _, err := profileLease.recoverStaleChromiumSingletons(profileDir); err != nil {
		if errors.Is(err, errBrowserProfileBusy) {
			err = reclaimLeakedBrowserProfile(ctx, profileLease, commandPath, profileDir, namespace, sessionName)
		}
		if err != nil {
			if errors.Is(err, errBrowserProfileBusy) {
				return nil, errBrowserProfileBusy
			}
			return nil, fmt.Errorf("prepare browser shared profile: %w", err)
		}
	}
	executable, err := resolveChromiumExecutable(cfg.ChromiumExecutable)
	if err != nil {
		return nil, err
	}
	var visibleEnvironment *visibleBrowserEnvironment
	if !hidden {
		resolved, resolveErr := resolveVisibleBrowserEnvironment()
		if resolveErr != nil {
			return nil, resolveErr
		}
		visibleEnvironment = &resolved
	}
	procCtx, cancel := context.WithCancel(context.Background())
	environment := agentBrowserEnvironmentResolved(cfg, namespace, sessionName, profileDir, executable, hidden, visibleEnvironment)
	cmd := exec.CommandContext(procCtx, commandPath, "mcp", "--tools", agentBrowserMCPToolsProfile)
	configureAdapterCommand(cmd)
	cmd.Env = environment
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
		return nil, fmt.Errorf("start agent-browser MCP server: %w", err)
	}
	recordBrowserDaemonOwner(profileDir, namespace, sessionName)
	errs := &boundedBuffer{limit: 8192}
	go func() { _, _ = io.Copy(errs, stderr) }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), agentBrowserMaxMessageBytes)
	session := &agentBrowserSession{
		cancel:       cancel,
		cmd:          cmd,
		stdin:        stdin,
		out:          scanner,
		errs:         errs,
		nextID:       1,
		done:         make(chan struct{}),
		sessionName:  sessionName,
		namespace:    namespace,
		timeoutMS:    adapterTimeoutMS(cfg.TimeoutMS),
		commandPath:  commandPath,
		environment:  environment,
		profileLease: profileLease,
	}
	releaseLease = false
	go func() {
		_ = cmd.Wait()
		close(session.done)
	}()
	return session, nil
}

func agentBrowserEnvironmentResolved(cfg agentBrowserAdapterConfig, namespace, sessionName, profileDir, executable string, hidden bool, visibleEnvironment *visibleBrowserEnvironment) []string {
	env := make([]string, 0, len(os.Environ())+10)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "AGENT_BROWSER_") {
			continue
		}
		if !hidden && visibleEnvironment != nil &&
			(strings.HasPrefix(entry, "DISPLAY=") || strings.HasPrefix(entry, "XAUTHORITY=")) {
			continue
		}
		env = append(env, entry)
	}
	values := map[string]string{
		"AGENT_BROWSER_NAMESPACE":       namespace,
		"AGENT_BROWSER_SESSION":         sessionName,
		"AGENT_BROWSER_PROFILE":         profileDir,
		"AGENT_BROWSER_HEADED":          strconv.FormatBool(!hidden),
		"AGENT_BROWSER_HIDE_SCROLLBARS": "true",
		"AGENT_BROWSER_MAX_OUTPUT":      strconv.Itoa(agentBrowserMaxMessageBytes / 2),
		"AGENT_BROWSER_ARGS":            "--window-size=" + hiddenBrowserViewport,
	}
	if hidden {
		values["AGENT_BROWSER_IDLE_TIMEOUT_MS"] = strconv.Itoa(adapterDaemonIdleTimeoutMS(cfg.DaemonIdleTimeoutMS))
	} else {
		values["AGENT_BROWSER_IDLE_TIMEOUT_MS"] = strconv.Itoa(visibleBrowserIdleTimeoutMS(cfg.DaemonIdleTimeoutMS))
		// A virtual X server is not a user-visible handoff surface.
		values["AGENT_BROWSER_NO_XVFB"] = "true"
		if visibleEnvironment != nil {
			values["DISPLAY"] = visibleEnvironment.display
			values["XAUTHORITY"] = visibleEnvironment.xauthority
		}
	}
	if executable != "" {
		values["AGENT_BROWSER_EXECUTABLE_PATH"] = executable
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func (s *agentBrowserSession) initialize(ctx context.Context) error {
	result, err := s.request(ctx, "initialize", map[string]any{
		"protocolVersion": agentBrowserProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "sparkclaw",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return err
	}
	values := mapValue(result)
	if firstStringValue(values, "protocolVersion") != agentBrowserProtocolVersion {
		return fmt.Errorf("agent-browser negotiated unsupported MCP protocol %q", firstStringValue(values, "protocolVersion"))
	}
	serverInfo := mapValue(values["serverInfo"])
	if firstStringValue(serverInfo, "name") != "agent-browser" {
		return fmt.Errorf("unexpected browser MCP server %q", firstStringValue(serverInfo, "name"))
	}
	s.version = firstStringValue(serverInfo, "version")
	if s.version != agentBrowserVersion {
		return fmt.Errorf("agent-browser MCP version mismatch: expected %s, got %s", agentBrowserVersion, s.version)
	}
	if err := s.notify("notifications/initialized", map[string]any{}); err != nil {
		return err
	}
	toolsResult, err := s.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return err
	}
	return validateAgentBrowserTools(toolsResult)
}

func validateAgentBrowserTools(result any) error {
	root := mapValue(result)
	tools, _ := root["tools"].([]any)
	available := map[string]map[string]any{}
	for _, raw := range tools {
		tool := mapValue(raw)
		name := firstStringValue(tool, "name")
		if name != "" {
			available[name] = tool
		}
	}
	for name, required := range requiredAgentBrowserTools {
		tool := available[name]
		if tool == nil {
			return fmt.Errorf("agent-browser MCP core profile is missing required tool %q", name)
		}
		properties := mapValue(mapValue(tool["inputSchema"])["properties"])
		for _, field := range required {
			if _, ok := properties[field]; !ok {
				return fmt.Errorf("agent-browser MCP tool %q is missing required field %q", name, field)
			}
		}
	}
	return nil
}

func (s *agentBrowserSession) callTool(ctx context.Context, name string, args map[string]any) (agentBrowserToolResult, error) {
	if _, ok := requiredAgentBrowserTools[name]; !ok {
		return agentBrowserToolResult{}, fmt.Errorf("agent-browser tool %q is outside the SparkClaw adapter allowlist", name)
	}
	callArgs := cloneArgs(args)
	callArgs["session"] = s.sessionName
	callArgs["namespace"] = s.namespace
	callArgs["timeoutMs"] = requestTimeoutMS(ctx, s.timeoutMS)
	result, err := s.request(ctx, "tools/call", map[string]any{"name": name, "arguments": callArgs})
	if err != nil {
		return agentBrowserToolResult{}, err
	}
	return decodeAgentBrowserToolResult(name, result)
}

func decodeAgentBrowserToolResult(name string, raw any) (agentBrowserToolResult, error) {
	result := mapValue(raw)
	if result == nil {
		return agentBrowserToolResult{}, fmt.Errorf("agent-browser %s returned an invalid MCP tool result", name)
	}
	content, _ := result["content"].([]any)
	structured := mapValue(result["structuredContent"])
	response := mapValue(structured["response"])
	if structured == nil || response == nil {
		return agentBrowserToolResult{}, fmt.Errorf(
			"agent-browser %s omitted structuredContent.response: %s",
			name,
			agentBrowserErrorMessage(structured["stderr"], content),
		)
	}
	if boolValue(result["isError"]) || !boolValue(response["success"]) {
		return agentBrowserToolResult{}, &agentBrowserActionError{Tool: name, Message: agentBrowserErrorMessage(response["error"], structured["stderr"], content)}
	}
	return agentBrowserToolResult{Data: response["data"], Content: content}, nil
}

func agentBrowserErrorMessage(values ...any) string {
	for _, value := range values {
		if obj := mapValue(value); obj != nil {
			if text := firstStringValue(obj, "message", "error", "code"); text != "" {
				return text
			}
		}
		if text := strings.TrimSpace(stringValue(value)); text != "" && text != "<nil>" {
			return text
		}
	}
	return "unknown action failure"
}

func (s *agentBrowserSession) request(ctx context.Context, method string, params map[string]any) (any, error) {
	id := s.nextID
	s.nextID++
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	type requestResult struct {
		response mcpResponse
		err      error
	}
	done := make(chan requestResult, 1)
	go func() {
		for s.out.Scan() {
			var response mcpResponse
			if err := json.Unmarshal(s.out.Bytes(), &response); err != nil {
				done <- requestResult{err: fmt.Errorf("decode agent-browser MCP response: %w", err)}
				return
			}
			if len(response.ID) == 0 {
				continue
			}
			var responseID int
			if err := json.Unmarshal(response.ID, &responseID); err != nil || responseID != id {
				done <- requestResult{err: fmt.Errorf("agent-browser MCP response id mismatch: expected %d, got %s", id, response.ID)}
				return
			}
			done <- requestResult{response: response}
			return
		}
		if err := s.out.Err(); err != nil {
			done <- requestResult{err: err}
			return
		}
		done <- requestResult{err: fmt.Errorf("agent-browser MCP returned no response: %s", s.errs.String())}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		if result.err != nil {
			return nil, result.err
		}
		if result.response.Error != nil {
			return nil, fmt.Errorf("agent-browser MCP %s failed (%d): %s", method, result.response.Error.Code, result.response.Error.Message)
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

func (s *agentBrowserSession) notify(method string, params map[string]any) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *agentBrowserSession) write(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = s.stdin.Write(raw)
	return err
}

func (s *agentBrowserSession) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *agentBrowserSession) close() {
	s.closeOnce.Do(func() {
		defer s.profileLease.release()
		var closeErr error
		if s.alive() {
			ctx, cancel := context.WithTimeout(context.Background(), adapterTimeout(s.timeoutMS))
			_, closeErr = s.callTool(ctx, "agent_browser_close", nil)
			cancel()
		} else {
			closeErr = errors.New("agent-browser MCP session already stopped")
		}
		s.stopMCP()
		if closeErr != nil {
			s.closeOwnedBrowser()
		}
	})
}

func (s *agentBrowserSession) abort() {
	s.closeOnce.Do(func() {
		defer s.profileLease.release()
		terminateAdapterProcess(s.cmd)
		s.stopMCP()
		s.closeOwnedBrowser()
	})
}

func (s *agentBrowserSession) stopMCP() {
	_ = s.stdin.Close()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		terminateAdapterProcess(s.cmd)
		s.cancel()
		<-s.done
	}
	s.cancel()
}

func (s *agentBrowserSession) closeOwnedBrowser() {
	if strings.TrimSpace(s.commandPath) == "" || strings.TrimSpace(s.sessionName) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentBrowserFallbackClose)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.commandPath, "close", "--session", s.sessionName, "--json")
	configureAdapterCommand(cmd)
	cmd.Env = append([]string(nil), s.environment...)
	_ = cmd.Run()
}

func resolveAgentBrowserCommand(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		configured = "agent-browser"
	}
	if configured == "agent-browser" {
		if local := findWorkspaceAgentBrowser(); local != "" {
			return local, nil
		}
	}
	if strings.ContainsRune(configured, os.PathSeparator) || filepath.IsAbs(configured) {
		return validateAgentBrowserCommand(configured)
	}
	path, err := exec.LookPath(configured)
	if err != nil {
		return "", fmt.Errorf("resolve agent-browser command %q: %w; run npm install", configured, err)
	}
	return validateAgentBrowserCommand(path)
}

func findWorkspaceAgentBrowser() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for current := wd; ; current = filepath.Dir(current) {
		candidates := []string{filepath.Join(current, "node_modules", ".bin", "agent-browser")}
		for _, candidate := range candidates {
			if path, pathErr := validateAgentBrowserCommand(candidate); pathErr == nil {
				return path
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
	}
}

func validateAgentBrowserCommand(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("agent-browser command %q is a directory", abs)
	}
	return abs, nil
}

func validateAgentBrowserVersion(ctx context.Context, command string, startupTimeoutMS int) error {
	timeout := time.Duration(adapterStartupTimeoutMS(startupTimeoutMS)) * time.Millisecond
	versionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(versionCtx, command, "--version")
	configureAdapterCommand(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("read agent-browser version: %w: %s", err, strings.TrimSpace(string(output)))
	}
	got := strings.TrimSpace(string(output))
	if got != "agent-browser "+agentBrowserVersion {
		return fmt.Errorf("agent-browser version mismatch: expected %s, got %q", agentBrowserVersion, got)
	}
	return nil
}

func resolveChromiumExecutable(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return validateChromiumExecutable(configured)
	}

	candidates := []string{"/usr/bin/chromium", "/usr/bin/chromium-browser", "/snap/bin/chromium"}
	for _, name := range []string{"chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	return resolveChromiumExecutableFromCandidates(candidates)
}

func resolveChromiumExecutableFromCandidates(candidates []string) (string, error) {
	for _, candidate := range candidates {
		executable, err := validateChromiumExecutable(candidate)
		if err == nil {
			return executable, nil
		}
	}
	return "", errors.New("system Chromium was not found; install Chromium or configure adapters.browserAutomation.chromiumExecutable")
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

func requestTimeoutMS(ctx context.Context, fallback int) int {
	limit := adapterTimeoutMS(fallback)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline).Milliseconds()
		if remaining > 0 && remaining < int64(limit) {
			limit = int(remaining)
		}
	}
	if limit > agentBrowserTransportHeadroomMS {
		return limit - agentBrowserTransportHeadroomMS
	}
	if limit > 1 {
		return limit / 2
	}
	return 1
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

func adapterStartupTimeoutMS(timeoutMS int) int {
	if timeoutMS <= 0 {
		return 10000
	}
	return timeoutMS
}

func adapterDaemonIdleTimeoutMS(timeoutMS int) int {
	if timeoutMS <= 0 {
		return config.DefaultBrowserDaemonIdleTimeoutMS
	}
	return timeoutMS
}

// visibleBrowserIdleTimeoutMultiplier scales the hidden-session idle timeout
// for visible sessions. They are user-facing handoff surfaces, so the bound is
// generous (two hours with the default configuration) — but it must stay
// finite, or an abandoned visible Chromium lives until gateway exit.
const visibleBrowserIdleTimeoutMultiplier = 6

func visibleBrowserIdleTimeoutMS(timeoutMS int) int {
	return adapterDaemonIdleTimeoutMS(timeoutMS) * visibleBrowserIdleTimeoutMultiplier
}

type boundedBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	limit := b.limit
	if limit <= 0 {
		limit = 4096
	}
	b.buf = append(b.buf, p...)
	if len(b.buf) > limit {
		b.buf = b.buf[len(b.buf)-limit:]
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func isAgentBrowserActionError(err error) bool {
	var actionErr *agentBrowserActionError
	return errors.As(err, &actionErr)
}
