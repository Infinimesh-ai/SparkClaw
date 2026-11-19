// Package contract defines sparkclaw.link.v1, the transport-neutral message
// contract shared by clients, connectors and the Gateway. It has no
// dependency on any transport, so the same frames travel over the LAN
// WebSocket, the ISCP Relay and the connector's Unix socket.
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Version is the only protocol version this package speaks.
const Version = "sparkclaw.link.v1"

// Frame kinds.
const (
	FrameHello    = "hello"
	FrameRequest  = "request"
	FrameResponse = "response"
	FrameEvent    = "event"
	FrameCancel   = "cancel"
)

// MaxFrameBytes bounds one encoded frame. Large objects use chunked
// operations and never exceed this in one frame.
const MaxFrameBytes = 600 * 1024

// MaxChunkBytes bounds the raw bytes of one object chunk (base64 grows it by
// a third, which still fits in MaxFrameBytes).
const MaxChunkBytes = 256 * 1024

// Frame is the single envelope for every message. Fields that do not apply to
// a kind are omitted.
type Frame struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	// hello
	Hello *Hello `json:"hello,omitempty"`

	// request
	Op     string          `json:"op,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`

	// response
	OK     *bool           `json:"ok,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`

	// event: Topic names the stream; Subscription correlates it with the
	// request that opened it.
	Topic        string          `json:"topic,omitempty"`
	Subscription string          `json:"subscription,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
}

// Hello is exchanged once at the start of every connection. The client sends
// its conditions; the server answers with the intersection and the features
// available to this device.
type Hello struct {
	Versions   []string   `json:"versions,omitempty"`
	Version    string     `json:"version,omitempty"`
	Profile    string     `json:"profile,omitempty"` // duplex | pull
	Device     *Device    `json:"device,omitempty"`
	Features   []string   `json:"features,omitempty"`
	Principal  *Principal `json:"principal,omitempty"`
	ServerTime string     `json:"server_time,omitempty"`
	// Reverse lists the reverse operations the client serves (device
	// capabilities such as the SparkX built-in browser).
	Reverse []string `json:"reverse,omitempty"`
}

// Access profiles (design 3.7).
const (
	ProfileDuplex = "duplex"
	ProfilePull   = "pull"
)

// Principal is what a connector hands to the Gateway after verifying the
// device: owner, device and device class.
type Principal struct {
	OwnerID  string `json:"owner_id,omitempty"`
	DeviceID string `json:"device_id"`
	Class    string `json:"class"`
	Name     string `json:"name,omitempty"`
}

// Device classes, equal to the console device types.
const (
	ClassDesktop = "desktop"
	ClassPhone   = "phone"
	ClassWatch   = "watch"
)

// ValidClass reports whether c is one of the three client classes.
func ValidClass(c string) bool {
	return c == ClassDesktop || c == ClassPhone || c == ClassWatch
}

// Error is the structured failure carried by a response.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// Error codes.
const (
	CodeUnsupportedCapability = "unsupported_capability"
	CodeInvalidRequest        = "invalid_request"
	CodeNotFound              = "not_found"
	CodeConflict              = "conflict"
	CodeUnauthorized          = "unauthorized"
	CodeRevoked               = "revoked"
	CodeBackendUnavailable    = "backend_unavailable"
	CodeTooLarge              = "too_large"
	CodeBusy                  = "busy"
	CodeCancelled             = "cancelled"
	CodeInternal              = "internal"
	CodeQuotaExceeded         = "quota_exceeded"
	CodeTimeout               = "timeout"
)

// Errorf builds an Error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// AsError extracts a contract Error from err, wrapping unknown errors as
// internal failures.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// Request builds a request frame.
func Request(id, op string, params any) (Frame, error) {
	raw, err := marshalOptional(params)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: FrameRequest, ID: id, Op: op, Params: raw}, nil
}

// Success builds a successful response frame.
func Success(id string, result any) (Frame, error) {
	raw, err := marshalOptional(result)
	if err != nil {
		return Frame{}, err
	}
	ok := true
	return Frame{Type: FrameResponse, ID: id, OK: &ok, Result: raw}, nil
}

// Failure builds a failed response frame.
func Failure(id string, e *Error) Frame {
	ok := false
	if e == nil {
		e = &Error{Code: CodeInternal}
	}
	return Frame{Type: FrameResponse, ID: id, OK: &ok, Error: e}
}

// Event builds an event frame.
func Event(id, subscription, topic string, data any) (Frame, error) {
	raw, err := marshalOptional(data)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: FrameEvent, ID: id, Subscription: subscription, Topic: topic, Data: raw}, nil
}

func marshalOptional(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(v)
}

// Encode serializes a frame and enforces the size bound.
func Encode(f Frame) ([]byte, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFrameBytes {
		return nil, Errorf(CodeTooLarge, "frame is %d bytes", len(b))
	}
	return b, nil
}

// Decode parses and validates a frame.
func Decode(b []byte) (Frame, error) {
	var f Frame
	if len(b) > MaxFrameBytes {
		return f, Errorf(CodeTooLarge, "frame is %d bytes", len(b))
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, Errorf(CodeInvalidRequest, "frame is not JSON: %v", err)
	}
	return f, f.Validate()
}

// Validate checks the structural rules of a frame.
func (f Frame) Validate() error {
	if f.ID == "" || len(f.ID) > 128 || strings.ContainsAny(f.ID, "\x00\n") {
		return Errorf(CodeInvalidRequest, "frame id is missing or invalid")
	}
	switch f.Type {
	case FrameHello:
		if f.Hello == nil {
			return Errorf(CodeInvalidRequest, "hello frame without hello")
		}
	case FrameRequest:
		if f.Op == "" || len(f.Op) > 96 {
			return Errorf(CodeInvalidRequest, "request without op")
		}
	case FrameResponse:
		if f.OK == nil {
			return Errorf(CodeInvalidRequest, "response without ok")
		}
		if !*f.OK && f.Error == nil {
			return Errorf(CodeInvalidRequest, "failed response without error")
		}
	case FrameEvent:
		if f.Topic == "" {
			return Errorf(CodeInvalidRequest, "event without topic")
		}
	case FrameCancel:
	default:
		// Unknown kinds are rejected structurally; unknown *ops* are
		// answered with unsupported_capability and keep the connection.
		return Errorf(CodeInvalidRequest, "unknown frame type %q", f.Type)
	}
	return nil
}

// DecodeParams unmarshals request params. Unknown fields are ignored so a
// newer client keeps working against an older backend.
func DecodeParams(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return Errorf(CodeInvalidRequest, "params: %v", err)
	}
	return nil
}
