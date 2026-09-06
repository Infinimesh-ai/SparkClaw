package browserautomation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (a *PlaywrightExtensionAdapter) pageInfoLocked(ctx context.Context, pageID string) (map[string]any, error) {
	output, err := a.executeControllerLocked(ctx, "page.info", optionalPageArgs(pageID))
	if err != nil {
		return nil, err
	}
	page := mapValue(output["page"])
	if page == nil {
		return nil, errors.New("playwright extension omitted page metadata")
	}
	return page, nil
}

func (a *PlaywrightExtensionAdapter) waitForReadyLocked(ctx context.Context, pageID string, timeoutMS int) error {
	timeoutMS = boundedBrowserSettleValue(timeoutMS, 500, 120000)
	readyCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	for {
		info, err := a.pageInfoLocked(readyCtx, pageID)
		if err == nil {
			state := strings.ToLower(strings.TrimSpace(firstStringValue(info, "ready_state")))
			if state == "interactive" || state == "complete" {
				return nil
			}
		} else if a.sessionLostLocked(err) {
			return err
		}
		select {
		case <-readyCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("browser_settle_timeout: page did not reach a ready state")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (a *PlaywrightExtensionAdapter) waitForStableStateLocked(ctx context.Context, args map[string]any) (map[string]any, error) {
	lease := a.session.Lease()
	if requested := uint64Value(args["session_generation"]); requested != 0 && requested != uint64(lease.SessionGeneration) {
		return nil, errors.New("browser_session_stale: requested session generation is no longer active")
	}
	adapterCfg := a.cfg.Adapters.BrowserAutomation
	timeoutMS := boundedBrowserSettleValue(intArg(args, "timeout_ms", adapterCfg.SettleTimeoutMS), 500, 120000)
	quietMS := boundedBrowserSettleValue(intArg(args, "quiet_period_ms", adapterCfg.SettleQuietPeriodMS), 100, 10000)
	pollMS := boundedBrowserSettleValue(intArg(args, "poll_interval_ms", adapterCfg.SettlePollIntervalMS), 25, quietMS)
	settleCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
	expectedURL := firstNonEmptyBrowserString(stringArg(args, "expected_url"), stringArg(args, "canonical_url"))
	targetKind := app.BrowserTargetKind(strings.TrimSpace(stringArg(args, "target_kind")))
	beforeDigest := strings.TrimSpace(stringArg(args, "before_digest"))
	allowNoChange := boolArg(args, "allow_no_change")
	requiredStable := maxInt(2, quietMS/pollMS+1)
	stableCount := 0
	observations := 0
	routeRebinds := 0
	var last browserStableObservation
	var stableSince time.Time
	for {
		read, err := a.executeControllerLocked(settleCtx, "page.read", mergePageArgs(pageID, map[string]any{"max_chars": 120000}))
		if err != nil && a.sessionLostLocked(err) {
			return nil, err
		}
		if err == nil {
			page := mapValue(read["page"])
			observation := browserStableObservation{
				URL: firstStringValue(page, "url"), Title: firstStringValue(page, "title"),
				Digest: digestBrowserStableContent(firstStringValue(page, "title"), firstStringValue(page, "text")),
			}
			observations++
			rebound := ""
			if expectedURL != "" {
				rebound, err = settleBrowserRoute(expectedURL, observation.URL, targetKind)
				if err != nil {
					return nil, err
				}
			}
			if observation == last {
				stableCount++
			} else {
				last = observation
				stableCount = 1
				stableSince = time.Now().UTC()
			}
			if rebound != "" && rebound != observation.URL && stableCount >= requiredStable && time.Since(stableSince) >= time.Duration(quietMS)*time.Millisecond {
				if routeRebinds >= adapterCfg.RouteRebindLimit {
					return nil, errors.New("browser_route_diverged: same-origin route rebind limit exceeded")
				}
				if _, err := a.executeControllerLocked(settleCtx, "page.navigate", mergePageArgs(pageID, map[string]any{"url": rebound})); err != nil {
					return nil, fmt.Errorf("browser_renderer_unavailable: rebind route: %w", err)
				}
				routeRebinds++
				stableCount = 0
				last = browserStableObservation{}
			} else if rebound == "" || rebound == observation.URL {
				changed := beforeDigest == "" || observation.Digest != beforeDigest
				if stableCount >= requiredStable && time.Since(stableSince) >= time.Duration(quietMS)*time.Millisecond && (changed || allowNoChange) {
					if pageID == "" {
						pageID = firstStringValue(page, "page_id")
					}
					return map[string]any{
						"status": "stable", "reason_code": "browser_target_settled",
						"text": "browser page reached a stable observable state", "page_id": pageID,
						"url": observation.URL, "title": observation.Title, "state_digest": observation.Digest,
						"state_changed": changed, "observations": observations, "quiet_period_ms": quietMS,
						"route_rebinds": routeRebinds, "session_generation": lease.SessionGeneration,
						"provider_session_ref": a.providerSessionRef,
					}, nil
				}
			}
		}
		select {
		case <-settleCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("browser_settle_timeout: required page signals did not remain stable")
		case <-time.After(time.Duration(pollMS) * time.Millisecond):
		}
	}
}
