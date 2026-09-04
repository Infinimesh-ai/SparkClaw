package browserautomation

import (
	"context"
	"strings"
)

type browserEnvironmentPreflight struct {
	ok                    bool
	providerReady         bool
	providerVersionPinned bool
	endpointReady         bool
	profileMatched        bool
	browserVersion        string
	presentation          string
	generation            uint64
	reasonCodes           []string
}

func (p browserEnvironmentPreflight) output() map[string]any {
	status := "unavailable"
	if p.ok {
		status = "ok"
	}
	return map[string]any{
		"ok":                      p.ok,
		"status":                  status,
		"provider":                "agent-browser",
		"transport":               "host-cdp",
		"provider_ready":          p.providerReady,
		"provider_version":        agentBrowserVersion,
		"provider_version_pinned": p.providerVersionPinned,
		"endpoint_ready":          p.endpointReady,
		"profile_matched":         p.profileMatched,
		"browser_version":         p.browserVersion,
		"presentation":            p.presentation,
		"endpoint_generation":     p.generation,
		"reason_codes":            append([]string(nil), p.reasonCodes...),
	}
}

func inspectBrowserEnvironment(ctx context.Context, cfg agentBrowserAdapterConfig, commandPath string) browserEnvironmentPreflight {
	result := browserEnvironmentPreflight{}
	if err := validateAgentBrowserVersion(ctx, commandPath, cfg.StartupTimeoutMS); err != nil {
		result.reasonCodes = append(result.reasonCodes, "agent_browser_version_unavailable")
	} else {
		result.providerReady = true
		result.providerVersionPinned = true
	}
	endpoint, err := readHostCDPEndpoint(cfg.HostCDP)
	if err != nil {
		result.reasonCodes = append(result.reasonCodes, "browser_host_unavailable")
	} else {
		result.endpointReady = true
		result.profileMatched = endpoint.ProfileID == strings.TrimSpace(cfg.HostCDP.ProfileID)
		result.browserVersion = endpoint.BrowserVersion
		result.presentation = endpoint.Presentation
		result.generation = endpoint.Generation
	}
	result.ok = result.providerReady && result.providerVersionPinned && result.endpointReady && result.profileMatched
	return result
}
