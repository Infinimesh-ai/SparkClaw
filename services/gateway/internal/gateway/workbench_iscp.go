package gateway

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// textOnlyExecutionContextKey marks an execution that must not use tools.
type textOnlyExecutionContextKey struct{}

type workbenchISCPWriter struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	oversized bool
	limit     int
}

func (w *workbenchISCPWriter) Header() http.Header { return w.header }
func (w *workbenchISCPWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *workbenchISCPWriter) Write(raw []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	limit := w.limit
	if limit <= 0 {
		limit = iscpworkbench.MaxMessageBytes
	}
	if w.oversized || w.body.Len()+len(raw) > limit {
		w.oversized = true
		w.body.Reset()
		return 0, errors.New("ISCP response exceeds the text profile limit")
	}
	return w.body.Write(raw)
}
