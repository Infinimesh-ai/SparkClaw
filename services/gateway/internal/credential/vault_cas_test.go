package credential

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"testing"
)

func TestBindingCASCannotRestoreARevokedGeneration(t *testing.T) {
	v := New(store.NewMemoryStore(), Options{Key: testKey(31)})
	ctx := t.Context()
	if err := v.CompareAndReplaceBinding(ctx, "lease", "lease-v1", []byte("active"), 0); err != nil {
		t.Fatal(err)
	}
	_, gen, _, err := v.OpenBindingVersion(ctx, "lease", "lease-v1")
	if err != nil {
		t.Fatal(err)
	}
	if err = v.CompareAndReplaceBinding(ctx, "lease", "lease-v1", []byte("revoked"), gen); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []int64{0, gen} {
		if err = v.CompareAndReplaceBinding(ctx, "lease", "lease-v1", []byte("active"), stale); ErrorCode(err) != CodeInvalid {
			t.Fatal("stale CAS succeeded", err)
		}
	}
	raw, _, _, err := v.OpenBindingVersion(ctx, "lease", "lease-v1")
	if err != nil || string(raw) != "revoked" {
		t.Fatal("revocation overwritten", err)
	}
}
