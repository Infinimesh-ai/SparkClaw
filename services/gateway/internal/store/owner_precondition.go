package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// ErrOwnerProfileConflict means a conditional settings write observed a newer
// stored revision. No profile, audit or event is written on this path.
var ErrOwnerProfileConflict = errors.New("owner profile revision conflict")

type ownerProfilePreconditionKey struct{}
type ownerProfilePrecondition struct {
	Exists    bool
	UpdatedAt time.Time
}

// WithOwnerProfilePrecondition makes the existing repository write conditional
// within the backend transaction. The zero timestamp is only valid for creation.
// FileStore carries the condition into its serialized MemoryStore transaction.
func WithOwnerProfilePrecondition(ctx context.Context, exists bool, updatedAt time.Time) context.Context {
	return context.WithValue(ctx, ownerProfilePreconditionKey{}, ownerProfilePrecondition{exists, updatedAt})
}
func checkOwnerProfilePrecondition(ctx context.Context, current app.OwnerProfile, exists bool) error {
	expected, conditional := ctx.Value(ownerProfilePreconditionKey{}).(ownerProfilePrecondition)
	if conditional && (expected.Exists != exists || (exists && !expected.UpdatedAt.Equal(current.UpdatedAt))) {
		return ErrOwnerProfileConflict
	}
	return nil
}
