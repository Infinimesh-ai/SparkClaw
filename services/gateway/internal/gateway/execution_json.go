package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// workbench control requests are closed, bounded objects. Keep legacy JSON parsing
// unchanged because existing public clients may send additional fields.
func readExecutionJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("missing workbench request body")
	}
	defer r.Body.Close()
	const limit = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || len(raw) > limit {
		return errors.New("workbench request body exceeds limit")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return errors.New("workbench request must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing workbench request data")
	}
	return nil
}

func emptyExecutionRequest(w http.ResponseWriter, r *http.Request) bool {
	if err := readExecutionJSON(r, &struct{}{}); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid empty workbench request"))
		return false
	}
	return true
}
