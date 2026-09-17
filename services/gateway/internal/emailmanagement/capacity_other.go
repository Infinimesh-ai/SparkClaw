//go:build !linux

package emailmanagement

import "errors"

// Capacity probing is only implemented for the deployment platform; elsewhere
// the status projection reports the workspace capacity as unknown.
var statfs = func(string) (total, free int64, err error) {
	return 0, 0, errors.New("email_capacity_unsupported")
}
