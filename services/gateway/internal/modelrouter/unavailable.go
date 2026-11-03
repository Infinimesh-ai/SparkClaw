package modelrouter

import (
	"context"
	"errors"
	"net/http"
)

// UnavailableError marks a model call that failed because the model service
// could not be reached, timed out, or refused service. It is distinct from an
// over-long request or an unusable answer, so callers can tell the owner that
// the model itself is unavailable.
type UnavailableError struct {
	Err error
}

func (e *UnavailableError) Error() string {
	if e == nil || e.Err == nil {
		return "model service is unavailable"
	}
	return e.Err.Error()
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// IsUnavailable reports whether err came from a model service outage.
func IsUnavailable(err error) bool {
	var unavailable *UnavailableError
	return errors.As(err, &unavailable)
}

// transportUnavailable wraps a failed HTTP round trip. A caller cancelling its
// own request is not a model outage.
func transportUnavailable(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	return &UnavailableError{Err: err}
}

// statusUnavailable wraps an HTTP failure whose status means the service did
// not serve the request (outage, overload, timeout, missing route or refused
// credentials). Request-shape rejections such as 400 or 413 stay unwrapped.
func statusUnavailable(status int, err error) error {
	switch {
	case status >= http.StatusInternalServerError,
		status == http.StatusTooManyRequests,
		status == http.StatusRequestTimeout,
		status == http.StatusUnauthorized,
		status == http.StatusForbidden,
		status == http.StatusNotFound:
		return &UnavailableError{Err: err}
	default:
		return err
	}
}
