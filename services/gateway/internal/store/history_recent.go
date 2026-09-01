package store

import (
	"errors"
	"strings"
	"time"
)

const MaxRecentHistoryScanLimit = 4096

func validateRecentHistoryQuery(sessionID string, cutoff time.Time, scanLimit int) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("recent history session ID is required")
	}
	if cutoff.IsZero() {
		return errors.New("recent history cutoff is required")
	}
	if scanLimit <= 0 || scanLimit > MaxRecentHistoryScanLimit {
		return errors.New("recent history scan limit must be between 1 and 4096")
	}
	return nil
}
