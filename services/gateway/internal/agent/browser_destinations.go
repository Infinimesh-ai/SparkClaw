package agent

import (
	"net/url"
	"path"
	"strings"
)

type browserDestination struct {
	ID      string
	Name    string
	URL     string
	Aliases []string
}

// registeredBrowserDestinations is the allowlist for named browser targets.
// A matched name resolves to this frozen URL; the model never supplies one.
var registeredBrowserDestinations = []browserDestination{
	{
		ID:      "qq_mail",
		Name:    "QQ 邮箱",
		URL:     "https://mail.qq.com/",
		Aliases: []string{"QQ 邮箱", "QQ邮箱", "QQ Mail", "QQMail", "腾讯邮箱"},
	},
}

type browserDestinationMatch struct {
	Destination browserDestination
	Alias       string
}

func matchRegisteredBrowserDestination(content string) (browserDestinationMatch, bool) {
	lower := strings.ToLower(content)
	var best browserDestinationMatch
	for _, destination := range registeredBrowserDestinations {
		for _, alias := range destination.Aliases {
			if strings.Contains(lower, strings.ToLower(alias)) && len(alias) > len(best.Alias) {
				best = browserDestinationMatch{Destination: destination, Alias: alias}
			}
		}
	}
	return best, best.Alias != ""
}

func registeredBrowserDestinationMatchesURL(destinationID, rawURL string) bool {
	var destination *browserDestination
	for index := range registeredBrowserDestinations {
		if registeredBrowserDestinations[index].ID == destinationID {
			destination = &registeredBrowserDestinations[index]
			break
		}
	}
	if destination == nil {
		return false
	}

	target, targetErr := url.Parse(destination.URL)
	candidate, candidateErr := url.Parse(strings.TrimSpace(rawURL))
	if targetErr != nil || candidateErr != nil || target.Scheme == "" || candidate.Scheme == "" {
		return false
	}
	if !strings.EqualFold(target.Scheme, candidate.Scheme) || target.Port() != candidate.Port() {
		return false
	}
	targetHost := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	candidateHost := strings.TrimSuffix(strings.ToLower(candidate.Hostname()), ".")
	if targetHost == "" || candidateHost == "" ||
		(candidateHost != targetHost && !strings.HasSuffix(candidateHost, "."+targetHost)) {
		return false
	}

	targetPath := path.Clean("/" + strings.TrimPrefix(target.Path, "/"))
	if targetPath == "/" || targetPath == "." {
		return true
	}
	candidatePath := path.Clean("/" + strings.TrimPrefix(candidate.Path, "/"))
	return candidatePath == targetPath || strings.HasPrefix(candidatePath, targetPath+"/")
}

func browserTargetMatchesURL(targetURL, destinationID, candidateURL string) bool {
	if normalizeBrowserURL(candidateURL) == normalizeBrowserURL(targetURL) {
		return true
	}
	return destinationID != "" && registeredBrowserDestinationMatchesURL(destinationID, candidateURL)
}
