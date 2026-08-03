package browserautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type browserStableObservation struct {
	URL    string
	Title  string
	Digest string
}

func (e *agentBrowserSessionEntry) waitForStableStateLocked(ctx context.Context, args map[string]any) (map[string]any, error) {
	if requested := uint64Value(args["session_generation"]); requested != 0 && requested != e.generation {
		return nil, errors.New("browser_session_stale: requested session generation is no longer active")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.adapter.observeStableState == nil && (e.session == nil || !e.session.alive()) {
		return nil, errors.New("browser_renderer_unavailable: agent-browser session is not active")
	}
	adapterCfg := e.adapter.cfg.Adapters.BrowserAutomation
	timeoutMS := boundedBrowserSettleValue(
		intArg(args, "timeout_ms", adapterCfg.SettleTimeoutMS),
		500,
		120000,
	)
	quietMS := boundedBrowserSettleValue(
		intArg(args, "quiet_period_ms", adapterCfg.SettleQuietPeriodMS),
		100,
		10000,
	)
	pollMS := boundedBrowserSettleValue(
		intArg(args, "poll_interval_ms", adapterCfg.SettlePollIntervalMS),
		25,
		quietMS,
	)
	settleCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	if e.adapter.observeStableState == nil {
		_, _ = e.callAgentToolLocked(settleCtx, "agent_browser_wait_for_load", map[string]any{"state": "domcontentloaded"})
	}
	expectedURL := firstNonEmptyAgentBrowserString(stringArg(args, "expected_url"), stringArg(args, "canonical_url"))
	targetKind := app.BrowserTargetKind(strings.TrimSpace(stringArg(args, "target_kind")))
	beforeDigest := strings.TrimSpace(stringArg(args, "before_digest"))
	allowNoChange := boolArg(args, "allow_no_change")
	requiredStable := maxInt(2, quietMS/pollMS+1)
	stableCount := 0
	observations := 0
	routeRebinds := 0
	var last browserStableObservation
	var stableSince time.Time
	awaitNextPoll := func() error {
		select {
		case <-settleCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("browser_settle_timeout: required page signals did not remain stable")
		case <-time.After(time.Duration(pollMS) * time.Millisecond):
			return nil
		}
	}

	for {
		observation, err := e.observeStableBrowserState(settleCtx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(settleCtx.Err(), context.DeadlineExceeded) {
				return nil, errors.New("browser_settle_timeout: page did not become observable before the configured deadline")
			}
			if isAgentBrowserActionError(err) {
				stableCount = 0
				last = browserStableObservation{}
				if err := awaitNextPoll(); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("browser_renderer_unavailable: %w", err)
		}
		observations++
		rebound := ""
		if expectedURL != "" {
			var routeErr error
			rebound, routeErr = settleBrowserRoute(expectedURL, observation.URL, targetKind)
			if routeErr != nil {
				return nil, routeErr
			}
		}
		if observation == last {
			stableCount++
		} else {
			last = observation
			stableCount = 1
			stableSince = time.Now().UTC()
		}
		if rebound != "" && rebound != observation.URL {
			// SPA bootstraps can briefly expose their default route before
			// restoring the requested fragment. Only spend a bounded rebind
			// attempt after the divergent same-origin state has itself settled.
			if stableCount >= requiredStable && time.Since(stableSince) >= time.Duration(quietMS)*time.Millisecond {
				if routeRebinds >= adapterCfg.RouteRebindLimit {
					return nil, errors.New("browser_route_diverged: same-origin route rebind limit exceeded")
				}
				if _, err := e.callAgentToolLocked(settleCtx, "agent_browser_open", map[string]any{"url": rebound}); err != nil {
					return nil, fmt.Errorf("browser_renderer_unavailable: rebind route: %w", err)
				}
				routeRebinds++
				stableCount = 0
				last = browserStableObservation{}
			}
			if err := awaitNextPoll(); err != nil {
				return nil, err
			}
			continue
		}
		changed := beforeDigest == "" || observation.Digest != beforeDigest
		if stableCount >= requiredStable && time.Since(stableSince) >= time.Duration(quietMS)*time.Millisecond && (changed || allowNoChange) {
			pageID := strings.TrimSpace(stringArg(args, "page_id"))
			if pageID == "" {
				pageID = e.currentPageIDLocked(settleCtx)
			}
			return map[string]any{
				"status":               "stable",
				"reason_code":          "browser_target_settled",
				"text":                 "browser page reached a stable observable state",
				"page_id":              pageID,
				"url":                  observation.URL,
				"title":                observation.Title,
				"state_digest":         observation.Digest,
				"state_changed":        changed,
				"observations":         observations,
				"quiet_period_ms":      quietMS,
				"route_rebinds":        routeRebinds,
				"session_generation":   e.generation,
				"provider_session_ref": e.session.sessionName,
			}, nil
		}
		if err := awaitNextPoll(); err != nil {
			return nil, err
		}
	}
}

func (e *agentBrowserSessionEntry) observeStableBrowserState(ctx context.Context) (browserStableObservation, error) {
	if e.adapter.observeStableState != nil {
		return e.adapter.observeStableState(ctx, e.session)
	}
	return e.observeStableBrowserStateLocked(ctx)
}

func (e *agentBrowserSessionEntry) observeStableBrowserStateLocked(ctx context.Context) (browserStableObservation, error) {
	pageURL, err := e.currentURLLocked(ctx)
	if err != nil {
		return browserStableObservation{}, err
	}
	parsed, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return browserStableObservation{}, fmt.Errorf("page URL %q is not a usable HTTP(S) document", pageURL)
	}
	title, err := e.currentTitleLocked(ctx)
	if err != nil {
		return browserStableObservation{}, err
	}
	textResult, err := e.callAgentToolLocked(ctx, "agent_browser_get_text", map[string]any{"selector": "body"})
	if err != nil {
		return browserStableObservation{}, err
	}
	body := firstStringValue(mapValue(textResult.Data), "text", "value", "result")
	return browserStableObservation{URL: pageURL, Title: title, Digest: digestBrowserStableContent(title, body)}, nil
}

func digestBrowserStableContent(title, body string) string {
	raw := sha256.Sum256([]byte(title + "\x00" + body))
	return hex.EncodeToString(raw[:])
}

func settleBrowserRoute(expectedRaw, currentRaw string, targetKind app.BrowserTargetKind) (string, error) {
	expected, expectedErr := url.Parse(strings.TrimSpace(expectedRaw))
	current, currentErr := url.Parse(strings.TrimSpace(currentRaw))
	if expectedErr != nil || currentErr != nil || expected.Scheme == "" || expected.Host == "" || current.Scheme == "" || current.Host == "" {
		return "", errors.New("browser_route_diverged: expected or current route is invalid")
	}
	if !browserURLsShareOrigin(expected, current) &&
		(targetKind != app.BrowserTargetRegisteredDestination || !registeredDestinationAllowsOrigin(expected, current)) {
		return "", errors.New("browser_route_diverged: browser left the frozen target origin")
	}
	if (expected.Path != "" && expected.Path != "/" && current.Path != expected.Path) ||
		(expected.Fragment != "" && current.Fragment != expected.Fragment) {
		rebound := *current
		if expected.Path != "" {
			rebound.Path = expected.Path
			rebound.RawPath = expected.RawPath
		}
		if expected.Fragment != "" {
			rebound.Fragment = expected.Fragment
			rebound.RawFragment = expected.RawFragment
		}
		return rebound.String(), nil
	}
	return "", nil
}

func registeredDestinationAllowsOrigin(expected, current *url.URL) bool {
	if expected == nil || current == nil ||
		!strings.EqualFold(expected.Scheme, current.Scheme) || expected.Port() != current.Port() {
		return false
	}
	expectedHost := strings.TrimSuffix(strings.ToLower(expected.Hostname()), ".")
	currentHost := strings.TrimSuffix(strings.ToLower(current.Hostname()), ".")
	return expectedHost != "" && currentHost != "" &&
		(currentHost == expectedHost || strings.HasSuffix(currentHost, "."+expectedHost))
}

func boundedBrowserSettleValue(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func uint64Value(value any) uint64 {
	switch typed := value.(type) {
	case uint64:
		return typed
	case uint:
		return uint64(typed)
	case int:
		if typed > 0 {
			return uint64(typed)
		}
	case int64:
		if typed > 0 {
			return uint64(typed)
		}
	case float64:
		if typed > 0 {
			return uint64(typed)
		}
	}
	return 0
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
