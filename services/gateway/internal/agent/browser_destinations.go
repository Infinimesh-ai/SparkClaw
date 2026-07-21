package agent

import (
	"strings"
	"unicode"
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

func registeredBrowserDestinationHasInteractionGoal(content string, match browserDestinationMatch) bool {
	lower := strings.ToLower(content)
	lower = strings.Replace(lower, strings.ToLower(match.Alias), " ", 1)
	for _, phrase := range []string{
		"please", "help me", "could you", "would you", "in chromium", "in the browser",
		"open", "visit", "launch", "focus", "go to",
		"麻烦", "请", "帮我", "给我", "现在", "在chromium中", "在浏览器中", "在浏览器里",
		"浏览器", "chromium", "打开", "访问", "进入", "前往", "去", "一下", "页面", "网页", "的", "在", "中", "里",
	} {
		lower = strings.ReplaceAll(lower, phrase, " ")
	}
	lower = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return r
	}, lower)
	return lower != ""
}
