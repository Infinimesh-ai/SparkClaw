package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

// domainReply is a business function's answer: the HTTP status and JSON
// body the host WebChat has always returned. The HTTP handler writes it;
// the link domain method returns it as the domain result {status, body},
// which the desktop's original screens read as the same answer. Neither
// entry builds the other's request (docs/workbench-retirement-migration.md,
// target structure).
type domainReply struct {
	Status int
	Body   any
	// Object is a file the answer carries. The HTTP handler streams the
	// file itself; the link delivers it as a domain object.
	Object *domainReplyFile
	// Unknown marks a change whose effect cannot be told from the answer
	// (the provider may have acted before failing). The link keeps its
	// receipt fenced as outcome unknown; HTTP writes the answer.
	Unknown bool
}

// domainReplyFile is a file a domain answer delivers.
type domainReplyFile struct {
	Purpose   string
	Name      string
	MediaType string
	Data      []byte
}

func reply(status int, body any) domainReply { return domainReply{Status: status, Body: body} }

func replyError(status int, err error) domainReply {
	return domainReply{Status: status, Body: errorPayload(err)}
}

func (d domainReply) failed() bool { return d.Status >= http.StatusBadRequest }

// write sends the answer to the host WebChat.
func (d domainReply) write(w http.ResponseWriter) {
	writeJSON(w, d.Status, d.Body)
}

// replyCode is a failure named by its code alone, the shape of the
// settings and mail answers the desktop screens read.
func replyCode(status int, code string) domainReply {
	return reply(status, map[string]any{"code": code, "retryable": false})
}

// replyValue is a value with its revision: a change names the revision
// it was made against, so a change made from a stale screen is refused.
func replyValue(value any) domainReply {
	return reply(http.StatusOK, map[string]any{"revision": valueRevision(value), "value": value})
}

func valueRevision(value any) string {
	raw, _ := json.Marshal(value)
	return execution.Digest(raw)
}

// strictJSON decodes exactly one JSON value with only known fields.
func strictJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}
