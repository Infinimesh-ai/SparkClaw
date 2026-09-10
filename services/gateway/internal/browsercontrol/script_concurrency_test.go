package browsercontrol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type concurrentScriptClient struct {
	ControllerClient
	entered         chan string
	release         chan struct{}
	validateEntered chan struct{}
	validateRelease chan struct{}
}

func (c *concurrentScriptClient) RunScript(ctx context.Context, input RunScriptRequest, _ []byte) (ScriptExecutionResult, error) {
	c.entered <- input.Provider
	select {
	case <-ctx.Done():
		return ScriptExecutionResult{}, ctx.Err()
	case <-c.release:
		return ScriptExecutionResult{State: "completed", CredentialGeneration: input.CredentialGeneration}, nil
	}
}
func (c *concurrentScriptClient) ValidateToken(ctx context.Context, _ string, _ []byte) (ValidationResult, error) {
	if c.validateEntered != nil {
		c.validateEntered <- struct{}{}
	}
	if c.validateRelease != nil {
		select {
		case <-ctx.Done():
			return ValidationResult{}, ctx.Err()
		case <-c.validateRelease:
		}
	}
	return validationResult(1, 1, 1), nil
}
func scriptConcurrencyFixture(t *testing.T) (*Service, *concurrentScriptClient, int64) {
	t.Helper()
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("x", 32)})
	if err := vault.ReplaceBinding(t.Context(), credentialBinding, credentialKind, []byte("test-browser-credential")); err != nil {
		t.Fatal(err)
	}
	client := &concurrentScriptClient{entered: make(chan string, 4), release: make(chan struct{})}
	service := New(vault, client, "default")
	service.Initialize(t.Context())
	return service, client, service.Status(t.Context()).CredentialGeneration
}
func concurrentScriptRequest(provider string, generation int64) RunScriptRequest {
	return RunScriptRequest{TaskID: "test-" + provider, Provider: provider, Operation: "probe", ScriptID: provider + ".probe", Revision: 1, CredentialGeneration: generation, Input: map[string]any{"schema_version": 1}}
}
func awaitScriptClient(t *testing.T, entered <-chan string) string {
	t.Helper()
	select {
	case provider := <-entered:
		return provider
	case <-time.After(time.Second):
		t.Fatal("script failed to reach controller concurrently")
		return ""
	}
}
func awaitScriptDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("operation did not finish within deadline")
		return nil
	}
}
func TestServiceScriptsOverlapAfterInitialize(t *testing.T) {
	service, client, generation := scriptConcurrencyFixture(t)
	done := make(chan error, 3)
	for _, provider := range []string{"qq", "gmail", "outlook"} {
		go func() {
			_, err := service.RunScript(t.Context(), concurrentScriptRequest(provider, generation))
			done <- err
		}()
	}
	seen := map[string]bool{}
	for range 3 {
		seen[awaitScriptClient(t, client.entered)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected distinct providers: %v", seen)
	}
	// Closing has an exclusive, bounded wait even with several scripts active.
	service.closeTimeout = 20 * time.Millisecond
	if err := service.Close(); ErrorCode(err) != CodeBusy {
		t.Fatalf("Close while scripts active: %v", err)
	}
	close(client.release)
	for range 3 {
		if err := awaitScriptDone(t, done); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestCredentialChangesWaitForScriptsAndFenceOldGeneration(t *testing.T) {
	for _, operation := range []string{"rotate", "remove"} {
		t.Run(operation, func(t *testing.T) {
			service, client, generation := scriptConcurrencyFixture(t)
			scripts := make(chan error, 2)
			for _, provider := range []string{"gmail", "outlook"} {
				go func() {
					_, err := service.RunScript(t.Context(), concurrentScriptRequest(provider, generation))
					scripts <- err
				}()
			}
			for range 2 {
				awaitScriptClient(t, client.entered)
			}
			mutate := func(ctx context.Context) error {
				if operation == "remove" {
					_, err := service.Remove(ctx)
					return err
				}
				_, err := service.SaveToken(ctx, []byte("replacement-browser-credential"))
				return err
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			err := mutate(ctx)
			cancel()
			if ErrorCode(err) != CodeBusy || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("mutation must honor waiting deadline: %v", err)
			}
			if status := service.Status(t.Context()); !status.Configured || status.CredentialGeneration != generation {
				t.Fatal("canceled mutation changed credentials")
			}
			mutated := make(chan error, 1)
			go func() { mutated <- mutate(t.Context()) }()
			select {
			case err := <-mutated:
				t.Fatalf("mutation passed active scripts: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(client.release)
			for range 2 {
				if err := awaitScriptDone(t, scripts); err != nil {
					t.Fatal(err)
				}
			}
			if err := awaitScriptDone(t, mutated); err != nil {
				t.Fatal(err)
			}
			_, err = service.RunScript(t.Context(), concurrentScriptRequest("gmail", generation))
			want := CodeCredentialStale
			if operation == "remove" {
				want = CodeNotConfigured
			}
			if ErrorCode(err) != want {
				t.Fatalf("old binding was not fenced: %v", err)
			}
			select {
			case <-client.entered:
				t.Fatal("stale script reached controller")
			default:
			}
		})
	}
}
func TestScriptWaitingForCredentialRotationCanCancel(t *testing.T) {
	service, client, generation := scriptConcurrencyFixture(t)
	client.validateEntered = make(chan struct{}, 1)
	client.validateRelease = make(chan struct{})
	changed := make(chan error, 1)
	go func() {
		_, err := service.SaveToken(t.Context(), []byte("replacement-browser-credential"))
		changed <- err
	}()
	select {
	case <-client.validateEntered:
	case <-time.After(time.Second):
		t.Fatal("rotation did not begin")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	_, err := service.RunScript(ctx, concurrentScriptRequest("gmail", generation))
	cancel()
	if ErrorCode(err) != CodeBusy || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("script wait failed cancellation: %v", err)
	}
	close(client.validateRelease)
	if err := awaitScriptDone(t, changed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.entered:
		t.Fatal("canceled script reached controller")
	default:
	}
}
