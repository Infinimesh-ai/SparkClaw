package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// R3 control requests are closed, bounded objects. Keep legacy JSON parsing
// unchanged because existing public clients may send additional fields.
func readR3JSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("missing R3 request body")
	}
	defer r.Body.Close()
	const limit = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || len(raw) > limit {
		return errors.New("R3 request body exceeds limit")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return errors.New("R3 request must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing R3 request data")
	}
	return nil
}

func emptyR3Request(w http.ResponseWriter, r *http.Request) bool {
	if err := readR3JSON(r, &struct{}{}); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid empty R3 request"))
		return false
	}
	return true
}
