package browserautomation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var observationHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var observationPage = regexp.MustCompile(`^page_[a-f0-9]{32}$`)

// Content sampling stays beside the browser. Only a frozen target route rebind
// can cause another bounded request; all requests share the original deadline.
func (a *PlaywrightExtensionAdapter) waitForClientStableStateLocked(ctx context.Context, args map[string]any) (map[string]any, error) {
	lease := a.session.Lease()
	if requested := uint64Value(args["session_generation"]); requested != 0 && requested != uint64(lease.SessionGeneration) {
		return nil, errors.New("browser_session_stale: requested session generation is no longer active")
	}
	cfg := a.cfg.Adapters.BrowserAutomation
	timeoutMS := boundedBrowserSettleValue(intArg(args, "timeout_ms", cfg.SettleTimeoutMS), 500, 20000)
	quietMS := boundedBrowserSettleValue(intArg(args, "quiet_period_ms", cfg.SettleQuietPeriodMS), 100, 10000)
	pollMS := boundedBrowserSettleValue(intArg(args, "poll_interval_ms", cfg.SettlePollIntervalMS), 25, quietMS)
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	a.settledObservation = nil
	pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
	expectedURL := firstNonEmptyBrowserString(stringArg(args, "expected_url"), stringArg(args, "canonical_url"))
	for rebinds := 0; ; rebinds++ {
		deadline, _ := waitCtx.Deadline()
		remaining := int(time.Until(deadline).Milliseconds()) - min(1000, timeoutMS/5)
		if remaining < 1 {
			return nil, errors.New("browser_settle_timeout: observation deadline exhausted")
		}
		input := mergePageArgs(pageID, map[string]any{
			"timeout_ms": remaining, "quiet_period_ms": quietMS, "poll_interval_ms": pollMS,
			"minimum_samples": 2, "signals": []string{"url", "ready_state", "rendered_content", "frames"},
			"allow_no_change": boolArg(args, "allow_no_change"),
		})
		before := stringArg(args, "before_digest")
		if before != "" {
			input["before_digest"] = before
		}
		output, err := a.executeControllerLocked(waitCtx, "page.observe", input)
		if err != nil {
			return nil, err
		}
		observed := mapValue(output["observation"])
		if firstStringValue(observed, "status") == "timeout" {
			return nil, errors.New("browser_settle_timeout: client observation deadline exhausted")
		}
		if firstStringValue(observed, "status") == "failed" && firstStringValue(observed, "reason_code") == "browser_observation_unavailable" {
			return nil, errors.New("browser_observation_unavailable: client could not observe all required page signals")
		}
		if err := validateClientObservation(observed, pageID, quietMS, remaining, before, boolArg(args, "allow_no_change")); err != nil {
			return nil, err
		}
		url := firstStringValue(observed, "url")
		rebound := ""
		if expectedURL != "" {
			rebound, err = settleBrowserRoute(expectedURL, url, app.BrowserTargetKind(stringArg(args, "target_kind")))
			if err != nil {
				return nil, err
			}
		}
		pageID = firstStringValue(observed, "page_id")
		if rebound != "" && rebound != url {
			if rebinds >= cfg.RouteRebindLimit {
				return nil, errors.New("browser_route_diverged: same-origin route rebind limit exceeded")
			}
			if _, err := a.executeControllerLocked(waitCtx, "page.navigate", mergePageArgs(pageID, map[string]any{"url": rebound})); err != nil {
				return nil, fmt.Errorf("browser_renderer_unavailable: rebind route: %w", err)
			}
			continue
		}
		a.settledObservation = cloneArgs(observed)
		result := cloneArgs(observed)
		result["text"] = "browser page reached a stable observable state"
		result["route_rebinds"] = rebinds
		result["session_generation"] = lease.SessionGeneration
		result["provider_session_ref"] = a.providerSessionRef
		return result, nil
	}
}

func validateClientObservation(o map[string]any, pageID string, quietMS, timeoutMS int, before string, allowNoChange bool) error {
	invalid := errors.New("browser_observation_invalid: client did not provide bound stable evidence")
	if o == nil || observationInt(o, "schema_version") != 1 || firstStringValue(o, "status") != "stable" ||
		firstStringValue(o, "reason_code") != "browser_target_settled" || firstStringValue(o, "page_id") == "" ||
		firstStringValue(o, "url") == "" || o["loading"] != false ||
		pageID != "" && firstStringValue(o, "page_id") != pageID || !observationPage.MatchString(firstStringValue(o, "page_ref")) ||
		!observationHash.MatchString(firstStringValue(o, "document_version")) || !observationHash.MatchString(firstStringValue(o, "state_digest")) ||
		!observationHash.MatchString(firstStringValue(o, "frames_digest")) || o["frames_ready"] != true ||
		(firstStringValue(o, "ready_state") != "interactive" && firstStringValue(o, "ready_state") != "complete") {
		return invalid
	}
	var signals []string
	raw, _ := json.Marshal(o["signals"])
	if json.Unmarshal(raw, &signals) != nil || len(signals) != 4 {
		return invalid
	}
	for _, signal := range []string{"url", "ready_state", "rendered_content", "frames"} {
		found := false
		for _, item := range signals {
			found = found || item == signal
		}
		if !found {
			return invalid
		}
	}
	observedMS, stableMS := observationInt(o, "observed_ms"), observationInt(o, "stable_for_ms")
	if observationInt(o, "quiet_period_ms") != quietMS || stableMS < quietMS || observedMS < stableMS || observedMS > timeoutMS ||
		observationInt(o, "stable_samples") < 2 || observationInt(o, "observations") < observationInt(o, "stable_samples") ||
		observationInt(o, "frame_count") < 0 || observationInt(o, "frame_count") > 63 {
		return invalid
	}
	changed := before == "" || firstStringValue(o, "state_digest") != before
	if o["state_changed"] != changed || !changed && !allowNoChange {
		return invalid
	}
	return nil
}

// Evidence counters accept JSON integers (including zero), never coercible strings.
func observationInt(o map[string]any, key string) int {
	raw, err := json.Marshal(o[key])
	if err != nil || string(raw) == "null" {
		return -1
	}
	var value int64
	if json.Unmarshal(raw, &value) != nil || value < 0 || value > 1<<31-1 {
		return -1
	}
	return int(value)
}
