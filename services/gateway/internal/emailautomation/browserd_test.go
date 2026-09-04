package emailautomation

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestBrowserdClientRequiresSuccessfulMatchingPresentationAndGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("browserd control uses Unix sockets")
	}
	runtimeDir := t.TempDir()
	endpointPath := filepath.Join(runtimeDir, "cdp-endpoint")
	writeBrowserdEndpoint(t, endpointPath, testBrowserdEndpoint("headed", 4))
	serverErrors := serveBrowserdControlOnce(t, runtimeDir, func(request browserdControlRequest) browserdControlResponse {
		if request.Operation != "ensure-hidden" || request.URL != "" {
			t.Fatalf("control request = %#v", request)
		}
		writeBrowserdEndpoint(t, endpointPath, testBrowserdEndpoint("headless", 5))
		return testBrowserdResponse(true, "headless", 5)
	})

	client := NewBrowserdClient(config.HostCDPConfig{EndpointFile: endpointPath, ProfileID: "default", ConnectTimeoutMS: 1000})
	endpoint, err := client.EnsureHeadless(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Presentation != "headless" || endpoint.Generation != 5 {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestBrowserdClientAcceptsRestartUniqueNonMonotonicGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("browserd control uses Unix sockets")
	}
	runtimeDir := t.TempDir()
	endpointPath := filepath.Join(runtimeDir, "cdp-endpoint")
	writeBrowserdEndpoint(t, endpointPath, testBrowserdEndpoint("headed", 99))
	serverErrors := serveBrowserdControlOnce(t, runtimeDir, func(browserdControlRequest) browserdControlResponse {
		writeBrowserdEndpoint(t, endpointPath, testBrowserdEndpoint("headless", 7))
		return testBrowserdResponse(true, "headless", 7)
	})

	client := NewBrowserdClient(config.HostCDPConfig{EndpointFile: endpointPath, ProfileID: "default", ConnectTimeoutMS: 1000})
	endpoint, err := client.EnsureHeadless(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Generation != 7 {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestBrowserdClientAcceptsManualLoginWithoutCDPEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("browserd control uses Unix sockets")
	}
	runtimeDir := t.TempDir()
	serverErrors := serveBrowserdControlOnce(t, runtimeDir, func(request browserdControlRequest) browserdControlResponse {
		if request.Operation != "open-login" || request.URL != "https://mail.google.com/" {
			t.Fatalf("control request = %#v", request)
		}
		return testBrowserdResponse(true, "manual-login", 10)
	})

	client := NewBrowserdClient(config.HostCDPConfig{
		EndpointFile: filepath.Join(runtimeDir, "cdp-endpoint"), ProfileID: "default", ConnectTimeoutMS: 1000,
	})
	if err := client.OpenLogin(t.Context(), "https://mail.google.com/"); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestBrowserdClientRejectsNegativeOrMismatchedControlResponses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("browserd control uses Unix sockets")
	}
	for _, test := range []struct {
		name     string
		response browserdControlResponse
	}{
		{name: "negative without error text", response: testBrowserdResponse(false, "headless", 9)},
		{name: "wrong presentation", response: testBrowserdResponse(true, "headed", 9)},
		{name: "zero generation", response: testBrowserdResponse(true, "headless", 0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			endpointPath := filepath.Join(runtimeDir, "cdp-endpoint")
			writeBrowserdEndpoint(t, endpointPath, testBrowserdEndpoint("headless", 9))
			serverErrors := serveBrowserdControlOnce(t, runtimeDir, func(browserdControlRequest) browserdControlResponse {
				return test.response
			})
			client := NewBrowserdClient(config.HostCDPConfig{EndpointFile: endpointPath, ProfileID: "default", ConnectTimeoutMS: 1000})
			if _, err := client.EnsureHeadless(t.Context()); ErrorCode(err) != CodeProviderUnavailable {
				t.Fatalf("error = %v code=%q", err, ErrorCode(err))
			}
			if err := <-serverErrors; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func serveBrowserdControlOnce(t *testing.T, runtimeDir string, handle func(browserdControlRequest) browserdControlResponse) <-chan error {
	t.Helper()
	path := filepath.Join(runtimeDir, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	errors := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			errors <- err
			return
		}
		defer connection.Close()
		var request browserdControlRequest
		if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&request); err != nil {
			errors <- err
			return
		}
		_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
		errors <- json.NewEncoder(connection).Encode(handle(request))
	}()
	return errors
}

func writeBrowserdEndpoint(t *testing.T, path string, endpoint browserautomation.HostCDPEndpoint) {
	t.Helper()
	raw, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testBrowserdEndpoint(presentation string, generation uint64) browserautomation.HostCDPEndpoint {
	endpoint := testHeadlessEndpoint(generation)
	endpoint.Presentation = presentation
	return endpoint
}

func testBrowserdResponse(ok bool, presentation string, generation uint64) browserdControlResponse {
	return browserdControlResponse{
		OK: ok, BrowserPID: 42, Presentation: presentation, ProfileID: "default",
		BrowserVersion: "148.0.7778.0", Generation: generation,
	}
}
