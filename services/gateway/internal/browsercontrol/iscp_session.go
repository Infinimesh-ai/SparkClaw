package browsercontrol

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// This service is already bound by the Gateway to an authenticated device and
// task. It has no credential-vault or backend-profile fallback.
func (c *ISCPControllerClient) Status(ctx context.Context) Status {
	status := Status{Configured: true, ProfileID: ClientControllerProfile, CredentialGeneration: c.revision, State: app.IntegrationStateTemporarilyUnavailable}
	remote, err := c.ControllerStatus(ctx)
	if err != nil {
		status.ErrorCode = ErrorCode(err)
		return status
	}
	if remote.Available {
		status.State = app.IntegrationStateReady
	} else {
		status.ErrorCode = CodeControllerUnavailable
	}
	status.ControllerGeneration = remote.ControllerGeneration
	status.Versions = Versions{Client: "playwright-mcp", ClientVersion: remote.Versions["@playwright/mcp"], PlaywrightVersion: remote.Versions["playwright"], BrowserChannel: "electron"}
	return status
}

func (c *ISCPControllerClient) AcquireSession(ctx context.Context, taskID string, wait, ttl time.Duration) (Session, error) {
	return c.AcquireSessionWithLifetime(ctx, ctx, taskID, wait, ttl)
}
func (c *ISCPControllerClient) AcquireSessionWithLifetime(ctx, lifetime context.Context, taskID string, wait, ttl time.Duration) (Session, error) {
	if ttl == 0 {
		ttl = maxRuntimeSessionTTL
	}
	lease, err := c.Acquire(ctx, AcquireRequest{ProfileID: ClientControllerProfile, TaskID: taskID, CredentialGeneration: c.revision,
		WaitTimeoutMS: wait.Milliseconds(), SessionTTLMS: ttl.Milliseconds()}, nil)
	if err != nil {
		return nil, err
	}
	session := &RuntimeSession{client: c, lease: lease, ops: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		select {
		case <-lifetime.Done():
			ctx, cancel := context.WithTimeout(context.Background(), deferredReleaseTimeout)
			defer cancel()
			_ = session.Release(ctx)
		case <-session.done:
		}
	}()
	return session, nil
}
