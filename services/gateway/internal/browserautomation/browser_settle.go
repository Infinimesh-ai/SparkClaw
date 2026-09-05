package browserautomation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type browserStableObservation struct {
	URL    string
	Title  string
	Digest string
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
