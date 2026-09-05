package browserautomation

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"crypto/sha256"
	"encoding/hex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func (a *PlaywrightExtensionAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.releaseLocked(context.Background())
}

func (a *PlaywrightExtensionAdapter) ReleaseSession(args map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil || a.scope != playwrightScope(a.cfg, args) {
		return nil
	}
	return a.releaseLocked(context.Background())
}

func (a *PlaywrightExtensionAdapter) acquireSessionLocked(ctx context.Context, args map[string]any) (browsercontrol.Session, error) {
	scope := playwrightScope(a.cfg, args)
	if a.session != nil {
		if a.scope != scope {
			return nil, errors.New("browser profile is busy with another SparkClaw scope")
		}
		return a.session, nil
	}
	if a.controller == nil {
		return nil, errors.New("playwright extension controller is unavailable")
	}
	a.nextTaskID++
	taskID := playwrightTaskID(scope, a.nextTaskID)
	sessionCtx, sessionCancel := context.WithCancel(context.WithoutCancel(ctx))
	acquireWait := time.Duration(a.cfg.Adapters.BrowserAutomation.StartupTimeoutMS) * time.Millisecond
	session, err := a.controller.AcquireSession(sessionCtx, taskID, acquireWait, playwrightExtensionSessionTTL)
	if err != nil {
		sessionCancel()
		return nil, err
	}
	a.session = session
	a.sessionCancel = sessionCancel
	a.scope = scope
	a.providerSessionRef = playwrightProviderSessionRef(session.Lease().SessionID)
	a.snapshots = map[string]*browserSnapshotState{}
	a.activeSnapshotPage = ""
	return session, nil
}

func (a *PlaywrightExtensionAdapter) releaseLocked(ctx context.Context) error {
	if a.session == nil {
		return nil
	}
	session := a.session
	sessionCancel := a.sessionCancel
	a.session = nil
	a.sessionCancel = nil
	a.scope = ""
	a.providerSessionRef = ""
	a.snapshots = map[string]*browserSnapshotState{}
	a.activeSnapshotPage = ""
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := session.Release(releaseCtx)
	if sessionCancel != nil {
		sessionCancel()
	}
	return err
}

func (a *PlaywrightExtensionAdapter) executeControllerLocked(ctx context.Context, operation string, args map[string]any) (map[string]any, error) {
	if a.session == nil {
		return nil, errors.New("playwright extension session is unavailable")
	}
	output, err := a.session.Execute(ctx, operation, args)
	if err != nil {
		switch browsercontrol.ErrorCode(err) {
		case browsercontrol.CodeControllerStale, browsercontrol.CodeSessionNotFound, browsercontrol.CodeSessionStale:
			_ = a.releaseLocked(context.Background())
		}
		return nil, err
	}
	return output, nil
}

// sessionLostLocked reports whether a polling loop must stop: the controller
// session is gone (a stale-session error already released it) or the
// controller answered with a typed failure that repeating the poll cannot fix.
func (a *PlaywrightExtensionAdapter) sessionLostLocked(err error) bool {
	return a.session == nil || browsercontrol.ErrorCode(err) != ""
}

func (a *PlaywrightExtensionAdapter) withSessionMetadataLocked(output map[string]any, metadata browserModeFields, args map[string]any) map[string]any {
	if output == nil {
		output = map[string]any{}
	} else {
		output = cloneArgs(output)
	}
	lease := a.session.Lease()
	ownerID, profileID := splitBrowserProfileKey(playwrightScope(a.cfg, args))
	output["session_generation"] = lease.SessionGeneration
	output["page_generation"] = lease.PageGeneration
	output["provider_session_ref"] = a.providerSessionRef
	output["presentation"] = metadata.Presentation
	output["owner_id"] = ownerID
	output["profile_id"] = profileID
	if pages := extractPages(output); len(pages) > 0 {
		annotated := make([]any, 0, len(pages))
		for _, raw := range pages {
			page := cloneArgs(mapValue(raw))
			page["session_generation"] = lease.SessionGeneration
			page["page_generation"] = lease.PageGeneration
			page["provider_session_ref"] = a.providerSessionRef
			page["presentation"] = metadata.Presentation
			page["owner_id"] = ownerID
			page["profile_id"] = profileID
			annotated = append(annotated, page)
		}
		output["pages"] = annotated
	}
	if snapshot := mapValue(output["snapshot"]); snapshot != nil {
		snapshot = cloneArgs(snapshot)
		snapshot["session_generation"] = lease.SessionGeneration
		snapshot["page_generation"] = lease.PageGeneration
		snapshot["provider_session_ref"] = a.providerSessionRef
		snapshot["presentation"] = metadata.Presentation
		snapshot["owner_id"] = ownerID
		snapshot["profile_id"] = profileID
		output["snapshot"] = snapshot
	}
	return output
}

func playwrightScope(cfg config.Config, args map[string]any) string {
	ownerID := strings.TrimSpace(stringArg(args, "owner_id"))
	if ownerID == "" {
		ownerID = "owner"
	}
	profileID := strings.TrimSpace(stringArg(args, "browser_profile_id"))
	if profileID == "" {
		profileID = strings.TrimSpace(cfg.Tools.BrowserAutomation.Profile)
	}
	if profileID == "" {
		profileID = "default"
	}
	return ownerID + "\x00" + profileID
}

func playwrightTaskID(scope string, sequence uint64) string {
	digest := sha256.Sum256([]byte(scope + "\x00" + strconv.FormatUint(sequence, 10)))
	return "pw-" + hex.EncodeToString(digest[:12])
}

func playwrightProviderSessionRef(sessionID string) string {
	digest := sha256.Sum256([]byte("playwright-extension\x00" + sessionID))
	return "pw-" + hex.EncodeToString(digest[:10])
}
