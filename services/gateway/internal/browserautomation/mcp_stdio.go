package browserautomation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
	return a.listToolsWithSession(ctx, false)
}

func (a *ChromeDevToolsAdapter) listToolsWithSession(ctx context.Context, hidden bool) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, err := a.ensureSessionLocked(ctx, hidden)
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
	return a.callToolWithSession(ctx, false, name, args)
}

func (a *ChromeDevToolsAdapter) callToolWithSession(ctx context.Context, hidden bool, name string, args map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, err := a.ensureSessionLocked(ctx, hidden)
	if err != nil {
		return nil, err
	}
	response, err := session.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		a.resetSessionLocked(hidden)
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

func (a *ChromeDevToolsAdapter) ensureSessionLocked(ctx context.Context, hidden bool) (*stdioSession, error) {
	if session := a.sessionForModeLocked(hidden); session != nil && session.alive() {
		return session, nil
	}
	session, err := a.newSession(ctx, hidden)
	if err != nil {
		return nil, err
	}
	if err := session.initialize(); err != nil {
		session.close()
		return nil, err
	}
	a.setSessionForModeLocked(hidden, session)
	return session, nil
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

func (a *ChromeDevToolsAdapter) newSession(ctx context.Context, hidden bool) (*stdioSession, error) {
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
	if hidden {
		if unsafeFlag := hiddenMCPArgsUnsafeFlag(args); unsafeFlag != "" {
			return nil, fmt.Errorf("hidden browser session refuses configured %s", unsafeFlag)
		}
		args = hiddenMCPArgs(args)
	}
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

func hiddenMCPArgs(args []string) []string {
	out := cloneStringSlice(args)
	if !hasMCPFlag(out, "--headless") {
		out = append(out, "--headless")
	}
	if !hasMCPFlag(out, "--isolated") {
		out = append(out, "--isolated")
	}
	if !hasMCPFlag(out, "--viewport") {
		out = append(out, "--viewport="+hiddenBrowserViewport)
	}
	if !hasMCPFlag(out, "--no-usage-statistics") && !hasMCPFlag(out, "--usage-statistics") {
		out = append(out, "--no-usage-statistics")
	}
	return out
}

func hiddenMCPArgsUnsafeFlag(args []string) string {
	for _, names := range [][]string{
		{"--browserUrl", "--browser-url"},
		{"--userDataDir", "--user-data-dir"},
	} {
		if hasMCPFlag(args, names...) {
			return names[0]
		}
	}
	return ""
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
