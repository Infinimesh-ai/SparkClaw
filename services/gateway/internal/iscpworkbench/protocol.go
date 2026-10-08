// Package iscpworkbench implements the bounded desktop workbench application
// profile over authenticated, end-to-end encrypted ISCP sessions.
package iscpworkbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
)

const (
	Profile           = "sparkclaw.workbench.transport.v1"
	Permission        = "sparkclaw.workbench.v1"
	RequestType       = "task.invoke"
	ResponseType      = "task.result"
	OperationIdentity = "workbench.identity"
	OperationBind     = "installation.bind"
	OperationConfig   = "presentation.config"
	OperationOwner    = "presentation.owner"
	OperationReady    = "presentation.ready"
	OperationSubmit   = "execution.submit"
	OperationLookup   = "execution.lookup"
	OperationCancel   = "execution.cancel"
	OperationAck      = "execution.ack"
	MaxRequestBytes   = 64 << 10
	MaxResponseBytes  = 64 << 10
	MaxMessageBytes   = MaxRequestBytes
	MaxBodyBytes      = MaxMessageBytes - 2048
	MaxConcurrent     = 4
	RoleInitiator     = "initiator"
	RoleResponder     = "responder"
)

// Operations is the single registry for the first-stage application profile.
// Returning a copy prevents adapters from changing advertised permissions.
func Operations() []string {
	return []string{OperationIdentity, OperationBind, OperationConfig, OperationOwner, OperationReady, OperationSubmit, OperationLookup, OperationCancel, OperationAck}
}

type Request struct {
	Type           string          `json:"type"`
	Profile        string          `json:"profile"`
	ID             string          `json:"id"`
	Operation      string          `json:"operation"`
	InstallationID string          `json:"installation_id,omitempty"`
	InputDigest    string          `json:"input_digest,omitempty"`
	Body           json.RawMessage `json:"body,omitempty"`
	RequestID      string          `json:"request_id,omitempty"`
}

type Response struct {
	Type    string          `json:"type"`
	Profile string          `json:"profile"`
	ID      string          `json:"id"`
	Status  int             `json:"status"`
	Body    json.RawMessage `json:"body,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type Handler func(context.Context, Request) Response

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (r Request) Validate() error {
	if r.Type != RequestType || r.Profile != Profile || !uuidPattern.MatchString(r.ID) {
		return errors.New("invalid workbench request identity or profile")
	}
	if !slices.Contains(Operations(), r.Operation) {
		return errors.New("unsupported workbench operation")
	}
	if len(r.InstallationID) > 200 || len(r.InputDigest) > 128 || len(r.RequestID) > 200 {
		return errors.New("workbench request field is too large")
	}
	if len(r.Body) > 0 && !json.Valid(r.Body) {
		return errors.New("invalid workbench request body")
	}
	if r.Operation == OperationSubmit || r.Operation == OperationLookup || r.Operation == OperationCancel || r.Operation == OperationAck {
		if !uuidPattern.MatchString(r.RequestID) {
			return errors.New("execution request ID must be a UUID")
		}
	}
	return nil
}

func (r Response) Validate() error {
	if r.Type != ResponseType || r.Profile != Profile || !uuidPattern.MatchString(r.ID) || r.Status < 100 || r.Status > 599 {
		return errors.New("invalid workbench response")
	}
	if len(r.Error) > 1024 || (len(r.Body) > 0 && !json.Valid(r.Body)) {
		return errors.New("invalid workbench response body")
	}
	return nil
}

// encodeRequest deliberately embeds the original body bytes. encoding/json
// compacts RawMessage during Marshal, changing execution's durable input digest.
func encodeRequest(r Request) ([]byte, error) {
	body := r.Body
	r.Body = nil
	raw, err := json.Marshal(r)
	if err != nil || len(body) == 0 {
		return raw, err
	}
	if !json.Valid(body) {
		return nil, errors.New("invalid workbench body")
	}
	raw = append(raw[:len(raw)-1], []byte(`,"body":`)...)
	raw = append(raw, body...)
	return append(raw, '}'), nil
}

func strictDecode(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}
