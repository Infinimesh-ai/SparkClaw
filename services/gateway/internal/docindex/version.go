package docindex

import "context"

type structureVersionKey struct{}

// The index target pins parser behavior before extraction. In particular, a
// cold resume of a legacy workflow must not acquire new leaf boundaries.
func WithStructureVersion(ctx context.Context, version int) context.Context {
	return context.WithValue(ctx, structureVersionKey{}, version)
}

func StructureVersionFromContext(ctx context.Context) int {
	if version, ok := ctx.Value(structureVersionKey{}).(int); ok && version > 0 {
		return version
	}
	return 1
}
