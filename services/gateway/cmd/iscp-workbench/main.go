// iscp-workbench is a private-pipe SDK helper. It never opens a listening
// socket and accepts only the fixed workbench operation registry.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

const ipcVersion = 1

type callFrame struct {
	OperationID      string                 `json:"operation_id,omitempty"`
	ExpectedRevision uint64                 `json:"expected_revision,omitempty"`
	IPCVersion       int                    `json:"ipc_version"`
	Type             string                 `json:"type"`
	ID               string                 `json:"id,omitempty"`
	Request          iscpworkbench.Request  `json:"request,omitempty"`
	Response         iscpworkbench.Response `json:"response,omitempty"`
	BodyBase64       string                 `json:"body_base64,omitempty"`
}

type ipcWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *ipcWriter) send(value any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return json.NewEncoder(w.writer).Encode(value)
}

func main() {
	configPath := flag.String("config", "", "private workbench configuration")
	check := flag.Bool("check", false, "validate private profile without contacting Relay")
	flag.Parse()
	if *configPath == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: iscp-workbench -config <path> [-check]")
		os.Exit(2)
	}
	cfg, err := iscpworkbench.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ISCP workbench profile validation failed:", err)
		os.Exit(1)
	}
	writer := &ipcWriter{writer: os.Stdout}
	publicIdentity, err := cfg.PublicIdentity()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ISCP workbench identity validation failed")
		os.Exit(1)
	}
	if *check {
		_ = writer.send(map[string]any{"valid": true, "schema_version": 1, "mode": cfg.Mode, "role": cfg.Role, "profile": iscpworkbench.Profile, "identity": publicIdentity, "operations": iscpworkbench.Operations(), "max_request_bytes": iscpworkbench.MaxRequestBytes, "max_response_bytes": iscpworkbench.MaxResponseBytes})
		return
	}
	if cfg.Role != iscpworkbench.RoleInitiator {
		fmt.Fprintln(os.Stderr, "ISCP workbench helper requires desktop initiator role")
		os.Exit(1)
	}
	bridge := newReverseBridge(writer)
	endpoint, err := iscpworkbench.NewEndpoint(cfg, bridge.handle, func(state string) {
		_ = writer.send(map[string]any{"ipc_version": ipcVersion, "type": "state", "state": state})
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "ISCP workbench initialization failed:", err)
		os.Exit(1)
	}
	endpoint.SetCapabilitiesHandler(func(c iscpworkbench.TransportCapabilities) {
		if c.Profile == iscpworkbench.ProfileV2 {
			_ = writer.send(map[string]any{"ipc_version": ipcVersion, "type": "capabilities", "capabilities": c})
		}
	})
	defer endpoint.Close()
	if err := writer.send(map[string]any{"ipc_version": ipcVersion, "type": "hello", "identity": publicIdentity, "operations": iscpworkbench.Operations(), "max_request_bytes": iscpworkbench.MaxRequestBytes, "max_response_bytes": iscpworkbench.MaxResponseBytes}); err != nil {
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := serveWithReverse(ctx, os.Stdin, writer, endpoint, bridge); err != nil {
		fmt.Fprintln(os.Stderr, "ISCP workbench helper stopped:", err)
		os.Exit(1)
	}
}

type caller interface {
	DeleteAuthorization(context.Context, string, uint64) (iscpauth.DeletionReceipt, error)
	AuthorizationDeletionReceipt(context.Context, string, uint64) (iscpauth.DeletionReceipt, error)
	Run(context.Context) error
	Call(context.Context, iscpworkbench.Request) (iscpworkbench.Response, error)
	Close() error
}

func serve(ctx context.Context, input io.Reader, writer *ipcWriter, endpoint caller) error {
	return serveWithReverse(ctx, input, writer, endpoint, nil)
}
func serveWithReverse(ctx context.Context, input io.Reader, writer *ipcWriter, endpoint caller, bridge *reverseBridge) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = endpoint.Run(ctx) }()
	defer func() { cancel(); _ = endpoint.Close(); <-runDone }()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 128<<10)
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		readErr <- scanner.Err()
		close(lines)
	}()
	var calls sync.WaitGroup
	defer func() { cancel(); calls.Wait() }()
	slots := make(chan struct{}, iscpworkbench.MaxConcurrent)
	controls := make(chan struct{}, 1)
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				cancel()
				return <-readErr
			}
			frame, err := decodeCall(line)
			if err != nil {
				return err
			}
			if frame.Type == "authorization_delete" || frame.Type == "authorization_delete_receipt" {
				select {
				case controls <- struct{}{}:
				default:
					_ = writer.send(map[string]any{"ipc_version": ipcVersion, "type": "authorization_receipt", "id": frame.ID, "error": "authorization control busy", "retryable": true})
					continue
				}
				calls.Add(1)
				go func(frame callFrame) {
					defer calls.Done()
					defer func() { <-controls }()
					var r iscpauth.DeletionReceipt
					var err error
					if frame.Type == "authorization_delete" {
						r, err = endpoint.DeleteAuthorization(ctx, frame.OperationID, frame.ExpectedRevision)
					} else {
						r, err = endpoint.AuthorizationDeletionReceipt(ctx, frame.OperationID, frame.ExpectedRevision)
					}
					out := map[string]any{"ipc_version": ipcVersion, "type": "authorization_receipt", "id": frame.ID}
					if err != nil {
						out["error"] = "authorization control unavailable"
					} else {
						out["receipt"] = r
					}
					_ = writer.send(out)
				}(frame)
				continue
			}
			if frame.Type == "reverse_response" {
				if bridge == nil {
					return errors.New("unexpected reverse response")
				}
				bridge.accept(frame.ID, frame.Response)
				continue
			}
			if frame.Type == "shutdown" {
				cancel()
				return nil
			}
			select {
			case slots <- struct{}{}:
			default:
				_ = writeResponse(writer, frame.ID, iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: iscpworkbench.Profile, ID: frame.Request.ID, Status: 429, Error: "workbench concurrency limit exceeded"})
				continue
			}
			calls.Add(1)
			go func(frame callFrame) {
				defer calls.Done()
				defer func() { <-slots }()
				response, err := endpoint.Call(ctx, frame.Request)
				if err != nil {
					response = iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: iscpworkbench.Profile, ID: frame.Request.ID, Status: 503, Error: "ISCP workbench call failed"}
				}
				_ = writeResponse(writer, frame.ID, response)
			}(frame)
		}
	}
}

func writeResponse(writer *ipcWriter, id string, response iscpworkbench.Response) error {
	return writer.send(map[string]any{"ipc_version": ipcVersion, "type": "response", "id": id, "response": response})
}

func decodeCall(line []byte) (callFrame, error) {
	var frame callFrame
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&frame); err != nil {
		return frame, errors.New("invalid private IPC frame")
	}
	if decoder.Decode(new(any)) != io.EOF || frame.IPCVersion != ipcVersion {
		return frame, errors.New("unsupported private IPC version or trailing data")
	}
	if frame.Type == "shutdown" {
		return frame, nil
	}
	if frame.Type == "authorization_delete" || frame.Type == "authorization_delete_receipt" {
		if frame.ID == "" || len(frame.ID) > 200 || frame.OperationID == "" || len(frame.OperationID) > 128 || frame.ExpectedRevision == 0 {
			return frame, errors.New("invalid authorization control frame")
		}
		return frame, nil
	}
	if frame.Type == "reverse_response" {
		if frame.ID == "" || len(frame.ID) > 200 || frame.Response.Validate() != nil {
			return frame, errors.New("invalid reverse response")
		}
		return frame, nil
	}
	if frame.Type != "call" || frame.ID == "" || len(frame.ID) > 200 {
		return frame, errors.New("invalid private IPC call")
	}
	if frame.BodyBase64 != "" {
		if len(frame.Request.Body) != 0 {
			return frame, errors.New("IPC call supplies two bodies")
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(frame.BodyBase64)
		if err != nil || len(raw) > iscpworkbench.MaxRequestBytes || !json.Valid(raw) {
			return frame, errors.New("invalid private IPC encoded body")
		}
		frame.Request.Body = json.RawMessage(raw)
	}
	if err := frame.Request.Validate(); err != nil {
		return frame, errors.New("invalid private IPC workbench request")
	}
	return frame, nil
}
