package integrationconfig

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"testing"
)

func TestCredentialMutationsCheckStatusRevisionBeforeEffects(t *testing.T) {
	controller := newController(config.Default(), nil, nil, nil, nil)
	ctx := WithStatusPrecondition(t.Context(), "stale")
	operations := []func() error{
		func() error { _, err := controller.AddInfoCredential(ctx, AddInfoCredentialInput{}); return err },
		func() error {
			_, err := controller.AddLocalMindCredential(ctx, AddLocalMindCredentialInput{})
			return err
		},
		func() error { _, err := controller.Activate(ctx, InfoID, "", false); return err },
		func() error { _, err := controller.Check(ctx, InfoID, "missing"); return err },
		func() error { _, err := controller.Delete(ctx, InfoID, "missing"); return err },
	}
	for _, operation := range operations {
		if code := ErrorCode(operation()); code != "credential_revision_conflict" {
			t.Fatalf("unexpected revision code %q", code)
		}
	}
	status, _ := controller.Get(t.Context(), InfoID)
	if _, err := controller.Delete(WithStatusPrecondition(t.Context(), StatusRevision(status)), InfoID, "missing"); ErrorCode(err) != "credential_not_found" {
		t.Fatalf("fresh precondition rejected: %v", err)
	}
}
