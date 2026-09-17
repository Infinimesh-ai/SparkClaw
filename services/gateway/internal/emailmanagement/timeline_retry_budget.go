package emailmanagement

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	timelineRetrySoftBytes   = 64 << 10
	timelineRetrySingleBytes = 160 << 10
)

// The Controller uses JSON.stringify, which does not HTML-escape '<', '>' or
// '&'. Match that wire shape rather than Go's substantially larger default.
// Descriptors occur twice in a page result and three times in a batch journal;
// the existing whole-result limit remains the final gate, not a smaller provider
// list limit (which would create artificial coverage gaps).
type timelineRetryBudget struct{ bytes, count int }

func (b *timelineRetryBudget) admit(target app.EmailCaptureTarget) (bool, error) {
	var wire bytes.Buffer
	encoder := json.NewEncoder(&wire)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(target); err != nil {
		return false, err
	}
	size := wire.Len() - 1 // Encoder's newline is not part of JSON.stringify.
	if size+2 > timelineRetrySingleBytes {
		return false, errors.New("email_retry_target_too_large")
	}
	if b.count == 0 {
		// A valid oversized head item gets one whole round, rather than being
		// silently skipped forever. Later items retain their open Store state.
		b.bytes, b.count = size+2, 1
		return true, nil
	}
	if b.bytes+size+1 > timelineRetrySoftBytes {
		return false, nil
	}
	b.bytes += size + 1
	b.count++
	return true, nil
}
