package browsercontrol

import (
	"context"
	"fmt"
	"math"
)

const exclusiveOperationWeight = math.MaxInt64

// A script owns one shared lease until the controller returns. Credential and
// runtime lifecycle operations own all leases, so credentials cannot change
// beneath any script. The weighted semaphore queues writers ahead of later
// readers, preventing continuous polling from starving credential changes.
func (s *Service) acquireOperations(ctx context.Context, exclusive bool) (func(), error) {
	weight := int64(1)
	if exclusive {
		weight = exclusiveOperationWeight
	}
	if err := s.operations.Acquire(ctx, weight); err != nil {
		return nil, newError(CodeBusy, true, fmt.Errorf("browser controller operation wait canceled: %w", err))
	}
	return func() { s.operations.Release(weight) }, nil
}
