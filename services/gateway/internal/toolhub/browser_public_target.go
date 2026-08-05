package toolhub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const browserPublicTargetRedirectLimit = 5

func (h *ToolHub) identifyPublicBrowserTarget(ctx context.Context, _ map[string]any, sessionID, runID string) (Result, error) {
	searchCall, output, ok := h.latestStructuredWebSearch(sessionID, runID)
	if !ok {
		return Result{}, &app.CodedToolError{Code: app.ToolErrorPublicTargetProviderUnavailable, Err: errors.New("Info target identification has no completed structured web.search outcome")}
	}
	results := browserInteractionSlice(output["results"])
	if len(results) == 0 {
		return Result{}, &app.CodedToolError{Code: app.ToolErrorPublicTargetNotFound, Err: errors.New("Info target identification returned no structured result URLs")}
	}

	selection, unsafeCount, selected := selectInfoPublicBrowserTarget(ctx, results, h.validatePublicBrowserTarget)
	if selected {
		ownerTarget, surface := h.browserPublicTargetContext(runID)
		return Result{Output: map[string]any{
			"status":                  "resolved",
			"evidence_id":             app.NewID("browser_target"),
			"resolution_source":       "info_search",
			"owner_target_phrase":     ownerTarget,
			"requested_surface_kind":  surface,
			"info_request_id":         strings.TrimSpace(browserAutomationStringValue(output["request_id"])),
			"info_result_index":       selection.Index,
			"source_result_ref":       fmt.Sprintf("%s:results:%d", searchCall.ID, selection.Index),
			"canonical_entry_url":     normalizePublicTargetURL(selection.RawURL),
			"normalized_final_url":    selection.FinalURL,
			"observed_redirect_chain": selection.Redirects,
			"safety_gate_status":      "passed",
			"created_at":              time.Now().UTC().Format(time.RFC3339Nano),
			"untrusted":               true,
		}}, nil
	}
	code := app.ToolErrorPublicTargetNotFound
	message := "Info target identification returned no usable structured result URLs"
	if unsafeCount > 0 {
		code = app.ToolErrorPublicTargetUnsafe
		message = "all structured Info target URLs failed public HTTPS safety validation"
	}
	return Result{}, &app.CodedToolError{Code: code, Err: errors.New(message)}
}

type infoPublicBrowserTargetSelection struct {
	Index     int
	RawURL    string
	FinalURL  string
	Redirects []string
}

func selectInfoPublicBrowserTarget(
	ctx context.Context,
	results []any,
	validate func(context.Context, string) (string, []string, error),
) (infoPublicBrowserTargetSelection, int, bool) {
	unsafeCount := 0
	for index, raw := range results {
		item, ok := browserInteractionMap(raw)
		if !ok {
			continue
		}
		rawURL := strings.TrimSpace(browserAutomationStringValue(item["url"]))
		if rawURL == "" || rawURL == "<nil>" {
			continue
		}
		finalURL, redirects, err := validate(ctx, rawURL)
		if err != nil {
			unsafeCount++
			continue
		}
		return infoPublicBrowserTargetSelection{Index: index, RawURL: rawURL, FinalURL: finalURL, Redirects: redirects}, unsafeCount, true
	}
	return infoPublicBrowserTargetSelection{}, unsafeCount, false
}

func (h *ToolHub) latestStructuredWebSearch(sessionID, runID string) (app.ToolCall, map[string]any, bool) {
	calls := h.store.ListToolCalls(sessionID)
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call.RunID != runID || call.Tool != "web.search" || (call.Status != "completed" && call.Status != "completed_after_approval") {
			continue
		}
		output, ok := browserInteractionMap(call.Result)
		if ok {
			return call, output, true
		}
	}
	return app.ToolCall{}, nil, false
}

func (h *ToolHub) browserPublicTargetContext(runID string) (string, string) {
	run, ok := h.store.GetRun(runID)
	if !ok || run.Workflow == nil {
		return "", "official_home"
	}
	surface := "official_home"
	switch run.Workflow.Plan.ProfileID {
	case app.WorkflowBrowserFormDraft:
		surface = "web_app"
	case app.WorkflowBrowserPageRead:
		surface = "product_page"
	}
	return strings.TrimSpace(run.Workflow.Route.Slots.TargetRef), surface
}

func (h *ToolHub) validatePublicBrowserTarget(ctx context.Context, rawURL string) (string, []string, error) {
	parsed, err := validatePublicHTTPSURL(ctx, rawURL)
	if err != nil {
		return "", nil, err
	}
	redirects := []string{}
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > browserPublicTargetRedirectLimit {
				return errors.New("public target redirect limit exceeded")
			}
			if _, err := validatePublicHTTPSURL(req.Context(), req.URL.String()); err != nil {
				return err
			}
			redirects = append(redirects, normalizePublicTargetURL(req.URL.String()))
			return nil
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", nil, err
	}
	request.Header.Set("Range", "bytes=0-0")
	request.Header.Set("User-Agent", browserReadUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return "", nil, err
	}
	_, _ = io.CopyN(io.Discard, response.Body, 1)
	_ = response.Body.Close()
	if _, err := validatePublicHTTPSURL(ctx, response.Request.URL.String()); err != nil {
		return "", nil, err
	}
	return normalizePublicTargetURL(response.Request.URL.String()), redirects, nil
}

func validatePublicHTTPSURL(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("public browser target must be an HTTPS URL without userinfo")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" || host == "0.0.0.0" {
		return nil, errors.New("public browser target host is local")
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedPublicTargetIP(ip) {
			return nil, errors.New("public browser target IP is not public")
		}
		return parsed, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("public browser target DNS resolution failed")
	}
	for _, address := range addresses {
		if blockedPublicTargetIP(address.IP) {
			return nil, errors.New("public browser target DNS resolved to a non-public IP")
		}
	}
	return parsed, nil
}

func blockedPublicTargetIP(ip net.IP) bool {
	return blockedIP(ip) || ip.IsMulticast()
}

func normalizePublicTargetURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	parsed.Fragment = ""
	return parsed.String()
}
