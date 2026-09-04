package browserautomation

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
)

func TestAgentBrowserSessionCloseReclaimsDaemonBeforeStoppingMCP(t *testing.T) {
	requests, session := newAgentBrowserCloseTestSession(t)

	session.close()

	request := <-requests
	if firstStringValue(request, "method") != "tools/call" {
		t.Fatalf("unexpected MCP request: %#v", request)
	}
	params := mapValue(request["params"])
	if firstStringValue(params, "name") != "agent_browser_close" {
		t.Fatalf("normal close did not reclaim the agent-browser daemon: %#v", request)
	}
	arguments := mapValue(params["arguments"])
	if firstStringValue(arguments, "session") != session.sessionName ||
		firstStringValue(arguments, "namespace") != session.namespace {
		t.Fatalf("daemon close lost its invocation identity: %#v", arguments)
	}
	if timeout := intValue(arguments["timeoutMs"]); timeout <= 0 {
		t.Fatalf("daemon close did not carry a bounded timeout: %#v", arguments)
	}
}

func TestAgentBrowserSessionAbortDoesNotCallDaemonClose(t *testing.T) {
	reader, writer := io.Pipe()
	done := make(chan struct{})
	close(done)
	_, cancel := context.WithCancel(context.Background())
	session := &agentBrowserSession{
		cancel:    cancel,
		stdin:     writer,
		done:      done,
		closeOnce: sync.Once{},
	}

	read := make(chan []byte, 1)
	go func() {
		raw, _ := io.ReadAll(reader)
		read <- raw
	}()

	session.abort()
	if raw := <-read; len(raw) != 0 {
		t.Fatalf("abort must not issue MCP requests through an unhealthy transport: %s", raw)
	}
}

func newAgentBrowserCloseTestSession(t *testing.T) (<-chan map[string]any, *agentBrowserSession) {
	t.Helper()
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	done := make(chan struct{})
	close(done)
	_, cancel := context.WithCancel(context.Background())
	session := &agentBrowserSession{
		cancel:      cancel,
		stdin:       requestWriter,
		out:         bufio.NewScanner(responseReader),
		nextID:      1,
		done:        done,
		closeOnce:   sync.Once{},
		sessionName: "sc-test-session",
		namespace:   "sc-test-namespace",
		timeoutMS:   30000,
	}
	requests := make(chan map[string]any, 1)
	go func() {
		defer responseWriter.Close()
		scanner := bufio.NewScanner(requestReader)
		if !scanner.Scan() {
			return
		}
		var request map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return
		}
		requests <- request
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"response": map[string]any{"success": true, "data": map[string]any{}},
				},
			},
		}
		raw, _ := json.Marshal(response)
		_, _ = responseWriter.Write(append(raw, '\n'))
	}()
	t.Cleanup(func() {
		_ = requestReader.Close()
		_ = responseReader.Close()
	})
	return requests, session
}
