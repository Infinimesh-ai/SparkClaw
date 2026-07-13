package browserautomation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const hiddenBrowserViewport = "1365x768"

type jsonRPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (a *ChromeDevToolsAdapter) listTools(ctx context.Context) ([]string, error) {
	hidden := true
	a.mu.Lock()
	if a.session != nil && a.session.alive() {
		hidden = false
	}
	a.mu.Unlock()
	return a.listToolsWithSession(ctx, hidden, a.browserProfileKey(nil))
}

func (a *ChromeDevToolsAdapter) listToolsWithSession(ctx context.Context, hidden bool, profileKey string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, err := a.ensureSessionLocked(ctx, hidden, profileKey)
	if err != nil {
		return nil, err
	}
	response, err := session.request(ctx, "tools/list", nil)
	if err != nil {
		a.resetSessionLocked(hidden)
		return nil, err
	}
	var decoded struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(response.Result, &decoded)
	out := make([]string, 0, len(decoded.Tools))
	for _, tool := range decoded.Tools {
		if tool.Name != "" {
			out = append(out, tool.Name)
		}
	}
	return out, nil
}

func (a *ChromeDevToolsAdapter) callTool(ctx context.Context, name string, args map[string]any) (any, error) {
	return a.callToolWithSession(ctx, false, a.browserProfileKey(args), name, args)
}

func (a *ChromeDevToolsAdapter) callToolWithSession(ctx context.Context, hidden bool, profileKey, name string, args map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	startupURL := ""
	if name == "new_page" {
		startupURL = chromiumStartupURL(args)
	}
	session, started, err := a.ensureSessionLockedWithStartupURL(ctx, hidden, profileKey, startupURL)
	if err != nil {
		return nil, err
	}
	if name == "new_page" && startupURL != "" {
		if started {
			pages, listErr := callSessionTool(ctx, session, "list_pages", nil)
			if listErr != nil {
				a.resetSessionLocked(hidden)
				return nil, listErr
			}
			if out, reused, navigateErr := navigateReusableBlankPage(ctx, session, pages, startupURL, args); navigateErr != nil {
				a.resetSessionLocked(hidden)
				return nil, navigateErr
			} else if reused {
				a.cleanupAboutBlankPagesLocked(ctx, session)
				if cleaned, listErr := callSessionTool(ctx, session, "list_pages", nil); listErr == nil {
					return cleaned, nil
				}
				return out, nil
			}
			if !selectedPageMatchesURL(pages, startupURL) {
				out, openErr := callSessionTool(ctx, session, "new_page", args)
				if openErr != nil {
					a.resetSessionLocked(hidden)
					return nil, openErr
				}
				a.cleanupAboutBlankPagesLocked(ctx, session)
				return out, nil
			}
			a.cleanupAboutBlankPagesLocked(ctx, session)
			return callSessionTool(ctx, session, "list_pages", nil)
		}
		pages, listErr := callSessionTool(ctx, session, "list_pages", nil)
		if listErr == nil {
			out, reused, navigateErr := navigateReusableBlankPage(ctx, session, pages, startupURL, args)
			if navigateErr != nil {
				a.resetSessionLocked(hidden)
				return nil, navigateErr
			}
			if reused {
				a.cleanupAboutBlankPagesLocked(ctx, session)
				if cleaned, cleanedErr := callSessionTool(ctx, session, "list_pages", nil); cleanedErr == nil {
					return cleaned, nil
				}
				return out, nil
			}
		}
	}
	decoded, err := callSessionTool(ctx, session, name, args)
	if err != nil {
		a.resetSessionLocked(hidden)
		return nil, err
	}
	if name == "new_page" {
		a.cleanupAboutBlankPagesLocked(ctx, session)
	}
	return decoded, nil
}

func navigateSelectedPage(ctx context.Context, session *stdioSession, targetURL string, openArgs map[string]any) (any, error) {
	navigateArgs := map[string]any{"url": targetURL, "type": "url"}
	if timeout, ok := openArgs["timeout"]; ok {
		navigateArgs["timeout"] = timeout
	}
	return callSessionTool(ctx, session, "navigate_page", navigateArgs)
}

func navigateReusableBlankPage(ctx context.Context, session *stdioSession, pages any, targetURL string, openArgs map[string]any) (any, bool, error) {
	entries := mcpPageEntries(pages)
	blankID := 0
	selected := false
	for _, entry := range entries {
		if !strings.EqualFold(strings.TrimSpace(entry.URL), "about:blank") {
			continue
		}
		if blankID == 0 || entry.Selected {
			blankID = entry.ID
			selected = entry.Selected
		}
		if entry.Selected {
			break
		}
	}
	if blankID == 0 {
		return nil, false, nil
	}
	if !selected {
		selectedOutput, err := callSessionTool(ctx, session, "select_page", map[string]any{"pageId": blankID, "bringToFront": true})
		if err != nil {
			return nil, true, err
		}
		if err := mcpToolError("select_page", selectedOutput); err != nil {
			return nil, true, err
		}
	}
	out, err := navigateSelectedPage(ctx, session, targetURL, openArgs)
	return out, true, err
}

func callSessionTool(ctx context.Context, session *stdioSession, name string, args map[string]any) (any, error) {
	if args == nil {
		args = map[string]any{}
	}
	response, err := session.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}
	var decoded any
	if len(response.Result) > 0 {
		if err := json.Unmarshal(response.Result, &decoded); err != nil {
			return nil, err
		}
	}
	return decoded, nil
}

func chromiumStartupURL(args map[string]any) string {
	raw := strings.TrimSpace(stringArg(args, "url"))
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

type mcpPageEntry struct {
	ID       int
	URL      string
	Selected bool
}

func mcpPageEntries(output any) []mcpPageEntry {
	result, ok := output.(map[string]any)
	if !ok {
		return nil
	}
	pages := extractPages(result)
	entries := make([]mcpPageEntry, 0, len(pages))
	for _, raw := range pages {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		line := strings.TrimSpace(stringValue(item["text"]))
		idText := ""
		for _, key := range []string{"pageId", "page_id", "id"} {
			if value := strings.TrimSpace(stringValue(item[key])); value != "" && value != "<nil>" {
				idText = value
				break
			}
		}
		if idText == "" && line != "" {
			if before, _, found := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "-")), ":"); found {
				idText = strings.TrimSpace(before)
			}
		}
		id, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(idText), "page_"))
		if err != nil {
			continue
		}
		pageURL := strings.TrimSpace(stringValue(item["url"]))
		if pageURL == "" || pageURL == "<nil>" {
			if strings.Contains(strings.ToLower(line), "about:blank") {
				pageURL = "about:blank"
			} else {
				pageURL = firstHTTPURL(line)
			}
		}
		selected := boolValue(item["selected"]) || strings.Contains(strings.ToLower(line), "[selected]")
		entries = append(entries, mcpPageEntry{ID: id, URL: pageURL, Selected: selected})
	}
	return entries
}

func firstHTTPURL(text string) string {
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "()[]<>,\"")
		parsed, err := url.Parse(candidate)
		if err == nil && parsed.Hostname() != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			return parsed.String()
		}
	}
	return ""
}

func selectedPageMatchesURL(output any, targetURL string) bool {
	targetURL = strings.TrimRight(strings.TrimSpace(targetURL), "/")
	for _, entry := range mcpPageEntries(output) {
		if entry.Selected {
			return strings.EqualFold(strings.TrimRight(strings.TrimSpace(entry.URL), "/"), targetURL)
		}
	}
	return false
}

func (a *ChromeDevToolsAdapter) cleanupAboutBlankPagesLocked(ctx context.Context, session *stdioSession) {
	output, err := callSessionTool(ctx, session, "list_pages", nil)
	if err != nil {
		return
	}
	entries := mcpPageEntries(output)
	nonBlank := 0
	for _, entry := range entries {
		if !strings.EqualFold(strings.TrimSpace(entry.URL), "about:blank") {
			nonBlank++
		}
	}
	if nonBlank == 0 {
		return
	}
	for _, entry := range entries {
		if !strings.EqualFold(strings.TrimSpace(entry.URL), "about:blank") {
			continue
		}
		closed, err := callSessionTool(ctx, session, "close_page", map[string]any{"pageId": entry.ID})
		if err != nil || mcpToolError("close_page", closed) != nil {
			continue
		}
	}
}

func (a *ChromeDevToolsAdapter) ensureSessionLocked(ctx context.Context, hidden bool, profileKey string) (*stdioSession, error) {
	session, _, err := a.ensureSessionLockedWithStartupURL(ctx, hidden, profileKey, "")
	return session, err
}

func (a *ChromeDevToolsAdapter) ensureSessionLockedWithStartupURL(ctx context.Context, hidden bool, profileKey, startupURL string) (*stdioSession, bool, error) {
	if a.activeProfile != "" && a.activeProfile != profileKey {
		a.resetAllSessionsLocked()
	}
	a.resetSessionLocked(!hidden)
	if session := a.sessionForModeLocked(hidden); session != nil && session.alive() {
		return session, false, nil
	}
	session, err := a.newSession(ctx, hidden, profileKey, startupURL)
	if err != nil {
		return nil, false, err
	}
	if err := session.initialize(); err != nil {
		session.close()
		return nil, false, err
	}
	a.setSessionForModeLocked(hidden, session)
	a.activeProfile = profileKey
	return session, true, nil
}

func (a *ChromeDevToolsAdapter) sessionForModeLocked(hidden bool) *stdioSession {
	if hidden {
		return a.hiddenSession
	}
	return a.session
}

func (a *ChromeDevToolsAdapter) setSessionForModeLocked(hidden bool, session *stdioSession) {
	if hidden {
		a.hiddenSession = session
		return
	}
	a.session = session
}

func (a *ChromeDevToolsAdapter) resetSessionLocked(hidden bool) {
	if hidden {
		if a.hiddenSession != nil {
			a.hiddenSession.close()
			a.hiddenSession = nil
		}
		return
	}
	if a.session != nil {
		a.session.close()
		a.session = nil
	}
}

func (a *ChromeDevToolsAdapter) resetAllSessionsLocked() {
	a.resetSessionLocked(false)
	a.resetSessionLocked(true)
	a.activeProfile = ""
}

type stdioSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Scanner
	errs   *safeBuffer
	nextID int
}

func (a *ChromeDevToolsAdapter) newSession(ctx context.Context, hidden bool, profileKey, startupURL string) (*stdioSession, error) {
	adapterCfg := a.cfg.Adapters.BrowserAutomation
	if adapterCfg.MCPCommand == "" {
		return nil, errors.New("browser automation MCP command is empty")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	args := cloneStringSlice(adapterCfg.MCPArgs)
	if unsafeFlag := sharedProfileMCPArgsUnsafeFlag(args); unsafeFlag != "" {
		return nil, fmt.Errorf("shared Chromium profile refuses configured %s", unsafeFlag)
	}
	executable, err := resolveChromiumExecutable(adapterCfg.ChromiumExecutable)
	if err != nil {
		return nil, err
	}
	profileDir, err := resolveSharedProfileDir(adapterCfg.ProfileDir, profileKey)
	if err != nil {
		return nil, err
	}
	args = sharedProfileMCPArgs(args, hidden, executable, profileDir, startupURL)
	execCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(execCtx, adapterCfg.MCPCommand, args...)
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
	go func() {
		_, _ = io.Copy(errs, stderr)
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &stdioSession{
		ctx:    execCtx,
		cancel: cancel,
		cmd:    cmd,
		stdin:  stdin,
		out:    scanner,
		errs:   errs,
		nextID: 1,
	}, nil
}

func sharedProfileMCPArgs(args []string, hidden bool, executable, profileDir, startupURL string) []string {
	out := cloneStringSlice(args)
	out = append(out,
		"--executablePath="+executable,
		"--userDataDir="+profileDir,
	)
	if hidden {
		out = append(out, "--headless")
		if !hasMCPFlag(out, "--viewport") {
			out = append(out, "--viewport="+hiddenBrowserViewport)
		}
	}
	if startupURL != "" {
		out = append(out, "--chromeArg="+startupURL)
	}
	if !hasMCPFlag(out, "--no-usage-statistics") && !hasMCPFlag(out, "--usage-statistics") {
		out = append(out, "--no-usage-statistics")
	}
	return out
}

func sharedProfileMCPArgsUnsafeFlag(args []string) string {
	for _, names := range [][]string{
		{"--browserUrl", "--browser-url"},
		{"--wsEndpoint", "--ws-endpoint"},
		{"--autoConnect", "--auto-connect"},
		{"--userDataDir", "--user-data-dir"},
		{"--executablePath", "--executable-path", "-e"},
		{"--isolated"},
		{"--headless"},
		{"--chromeArg", "--chrome-arg"},
	} {
		if hasMCPFlag(args, names...) {
			return names[0]
		}
	}
	return ""
}

func resolveChromiumExecutable(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return validateChromiumExecutable(configured)
	}
	for _, name := range []string{"chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			return validateChromiumExecutable(path)
		}
	}
	candidates := []string{}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, "/Applications/Chromium.app/Contents/MacOS/Chromium")
	case "linux":
		candidates = append(candidates, "/usr/bin/chromium", "/usr/bin/chromium-browser")
	case "windows":
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "Chromium", "Application", "chrome.exe"))
		}
	}
	for _, candidate := range candidates {
		if path, err := validateChromiumExecutable(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("browser shared profile Chromium executable was not found; set SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE")
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

func (a *ChromeDevToolsAdapter) browserProfileKey(args map[string]any) string {
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

func hasMCPFlag(args []string, names ...string) bool {
	want := map[string]bool{}
	for _, name := range names {
		want[name] = true
	}
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if want[arg] {
			return true
		}
		if idx := strings.IndexByte(arg, '='); idx > 0 && want[arg[:idx]] {
			return true
		}
	}
	return false
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func (s *stdioSession) alive() bool {
	select {
	case <-s.ctx.Done():
		return false
	default:
		return s.cmd.Process != nil
	}
}

func (s *stdioSession) initialize() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.request(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "sparkclaw",
			"version": "0.1",
		},
	}); err != nil {
		return err
	}
	return s.notify("notifications/initialized", nil)
}

func (s *stdioSession) request(ctx context.Context, method string, params map[string]any) (jsonRPCResponse, error) {
	id := s.nextID
	s.nextID++
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := s.write(req); err != nil {
		return jsonRPCResponse{}, err
	}
	type requestResult struct {
		response jsonRPCResponse
		err      error
	}
	done := make(chan requestResult, 1)
	go func() {
		for s.out.Scan() {
			line := s.out.Bytes()
			if len(line) == 0 {
				continue
			}
			var response jsonRPCResponse
			if err := json.Unmarshal(line, &response); err != nil {
				continue
			}
			if response.ID == nil || fmt.Sprint(response.ID) != fmt.Sprint(id) {
				continue
			}
			if response.Error != nil {
				done <- requestResult{err: fmt.Errorf("mcp %s error %d: %s", method, response.Error.Code, response.Error.Message)}
				return
			}
			done <- requestResult{response: response}
			return
		}
		if err := s.out.Err(); err != nil {
			done <- requestResult{err: err}
			return
		}
		done <- requestResult{err: fmt.Errorf("mcp %s returned no response: %s", method, s.errs.String())}
	}()
	select {
	case <-ctx.Done():
		s.close()
		return jsonRPCResponse{}, ctx.Err()
	case result := <-done:
		return result.response, result.err
	}
}

func (s *stdioSession) notify(method string, params map[string]any) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return s.write(req)
}

func (s *stdioSession) write(req jsonRPCRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = s.stdin.Write(raw)
	return err
}

func (s *stdioSession) close() {
	_ = s.stdin.Close()
	s.cancel()
	_ = s.cmd.Wait()
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
