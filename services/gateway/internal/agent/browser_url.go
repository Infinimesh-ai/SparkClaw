package agent

import (
	"net/url"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// browserURLMode selects the canonicalization policy applied by
// canonicalBrowserURL. Every mode lowercases the scheme and host and defaults
// an empty path to "/"; the modes differ only in how they treat the query,
// the fragment, and input that does not parse as an absolute URL. Those
// differences are deliberate contracts, not drift — each constant documents
// its own policy.
type browserURLMode int

const (
	// browserURLNormalize freezes an owner-typed route target for
	// comparison: the fragment is dropped (it is not part of route identity
	// here), the query is kept verbatim (the owner typed it), and input that
	// does not parse as an absolute URL falls back to its trimmed form.
	browserURLNormalize browserURLMode = iota
	// browserURLNormalizeEvidence normalizes a live URL captured as workflow
	// evidence. Same as browserURLNormalize except the fragment is kept,
	// because in-page routes ("#/inbox") distinguish document states that
	// evidence comparison must see.
	browserURLNormalizeEvidence
	// browserURLSafeResult sanitizes a live URL before it is recorded as the
	// workflow result: the live query is dropped (provider-volatile queries
	// may carry session tokens) unless the owner supplied the target query,
	// in which case the frozen owner query is merged into the live URL.
	// Unparseable input falls back to the frozen target URL.
	browserURLSafeResult
	// browserURLSafeHandoff sanitizes a live URL stored on a login-handoff
	// block. Unlike browserURLSafeResult, an owner-supplied explicit target
	// short-circuits to the frozen target URL wholesale (live path and all);
	// otherwise the live query is always dropped, never merged.
	browserURLSafeHandoff
	// browserURLSafePersistence sanitizes URLs found inside persisted tool
	// arguments, results, and error text: like browserURLSafeResult, but
	// unparseable or non-http(s) input is returned verbatim because the
	// caller may be rewriting a URL embedded in free text that must
	// otherwise stay untouched.
	browserURLSafePersistence
)

// canonicalBrowserURL is the single implementation behind the browser URL
// helpers below. target is consulted only by the safe modes.
func canonicalBrowserURL(mode browserURLMode, target app.BrowserTargetDescriptor, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if mode == browserURLSafeHandoff &&
		target.TargetKind == app.BrowserTargetExplicitURL &&
		target.QueryProvenance == app.BrowserQueryOwnerSupplied {
		return target.CanonicalURL
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" ||
		(mode == browserURLSafePersistence && parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(mode != browserURLSafePersistence && parsed.Scheme == "") {
		switch mode {
		case browserURLNormalize, browserURLNormalizeEvidence:
			return trimmed
		case browserURLSafePersistence:
			return raw
		default:
			return target.CanonicalURL
		}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	if mode == browserURLSafePersistence {
		// Persisted records must never retain user:password@ credentials.
		parsed.User = nil
	}
	switch mode {
	case browserURLNormalize:
		parsed.Fragment = ""
	case browserURLNormalizeEvidence:
		// Keep query and fragment verbatim.
	default:
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		if mode != browserURLSafeHandoff && target.QueryProvenance == app.BrowserQueryOwnerSupplied {
			if frozen, frozenErr := url.Parse(strings.TrimSpace(target.CanonicalURL)); frozenErr == nil &&
				frozen.Scheme != "" && frozen.Host != "" {
				parsed.RawQuery = frozen.RawQuery
				parsed.ForceQuery = frozen.ForceQuery
			}
		}
	}
	return parsed.String()
}

func normalizeBrowserURL(raw string) string {
	return canonicalBrowserURL(browserURLNormalize, app.BrowserTargetDescriptor{}, raw)
}

func normalizeBrowserEvidenceURL(raw string) string {
	return canonicalBrowserURL(browserURLNormalizeEvidence, app.BrowserTargetDescriptor{}, raw)
}

func browserSafeResultURL(target app.BrowserTargetDescriptor, liveRaw string) string {
	return canonicalBrowserURL(browserURLSafeResult, target, liveRaw)
}

func browserSafeHandoffURL(target app.BrowserTargetDescriptor, liveRaw string) string {
	return canonicalBrowserURL(browserURLSafeHandoff, target, liveRaw)
}

func browserSafePersistenceURL(target app.BrowserTargetDescriptor, raw string) string {
	return canonicalBrowserURL(browserURLSafePersistence, target, raw)
}
