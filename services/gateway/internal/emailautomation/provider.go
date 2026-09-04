package emailautomation

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type Script struct {
	ID       string
	Revision int
	Command  []string
	Timeout  time.Duration
}

type Provider struct {
	ID          string
	DisplayName string
	Aliases     []string
	LoginURL    string
	Origins     []string
	Probe       Script
	Send        Script
}

type Registry struct {
	providers map[string]Provider
	ordered   []string
}

func NewRegistry(providers []Provider) (Registry, error) {
	registry := Registry{providers: make(map[string]Provider, len(providers))}
	aliases := map[string]string{}
	for _, provider := range providers {
		provider.ID = strings.ToLower(strings.TrimSpace(provider.ID))
		provider.DisplayName = strings.TrimSpace(provider.DisplayName)
		provider.LoginURL = strings.TrimSpace(provider.LoginURL)
		if provider.ID == "" || provider.DisplayName == "" || provider.LoginURL == "" {
			return Registry{}, errors.New("email provider identity, display name, and login URL are required")
		}
		if provider.ID != app.EmailProviderQQMail && provider.ID != app.EmailProviderOutlook && provider.ID != app.EmailProviderGmail {
			return Registry{}, errors.New("email provider is not supported")
		}
		if _, exists := registry.providers[provider.ID]; exists {
			return Registry{}, errors.New("email provider is registered more than once")
		}
		if err := validateScript(provider.Probe); err != nil {
			return Registry{}, err
		}
		if err := validateScript(provider.Send); err != nil {
			return Registry{}, err
		}
		provider.Aliases = append([]string{provider.ID, provider.DisplayName}, provider.Aliases...)
		provider.Aliases = uniqueStrings(provider.Aliases)
		for _, alias := range provider.Aliases {
			key := normalizeAlias(alias)
			if key == "" {
				return Registry{}, errors.New("email provider alias is empty")
			}
			if prior := aliases[key]; prior != "" && prior != provider.ID {
				return Registry{}, errors.New("email provider alias is ambiguous")
			}
			aliases[key] = provider.ID
		}
		provider.Origins = uniqueStrings(provider.Origins)
		registry.providers[provider.ID] = cloneProvider(provider)
		registry.ordered = append(registry.ordered, provider.ID)
	}
	slices.Sort(registry.ordered)
	return registry, nil
}

func DefaultRegistry(scriptDir string) Registry {
	scriptDir = strings.TrimSpace(scriptDir)
	if scriptDir == "" {
		scriptDir = "/app/scripts"
	}
	script := func(id, name string, revision int, timeout time.Duration) Script {
		return Script{ID: id, Revision: revision, Command: []string{"node", filepath.Join(scriptDir, name)}, Timeout: timeout}
	}
	registry, err := NewRegistry([]Provider{
		{
			ID: app.EmailProviderQQMail, DisplayName: "QQ Mail",
			Aliases:  []string{"QQ 邮箱", "QQ邮箱", "QQMail", "腾讯邮箱"},
			LoginURL: "https://wx.mail.qq.com/", Origins: []string{"https://mail.qq.com", "https://wx.mail.qq.com"},
			Probe: script("qqmail.login_probe", "qqmail-login-probe.mjs", 1, 45*time.Second),
			Send:  script("qqmail.send", "qqmail-send.mjs", 1, 90*time.Second),
		},
		{
			ID: app.EmailProviderOutlook, DisplayName: "Outlook",
			Aliases:  []string{"Outlook Mail", "Outlook 邮箱", "微软邮箱", "Hotmail"},
			LoginURL: "https://outlook.live.com/mail/", Origins: []string{"https://outlook.live.com", "https://outlook.office.com", "https://outlook.office365.com"},
			Probe: script("outlook.login_probe", "outlook-login-probe.mjs", 1, 45*time.Second),
			Send:  script("outlook.send", "outlook-send.mjs", 1, 90*time.Second),
		},
		{
			ID: app.EmailProviderGmail, DisplayName: "Gmail",
			Aliases:  []string{"Google Mail", "谷歌邮箱", "Google 邮箱"},
			LoginURL: "https://mail.google.com/", Origins: []string{"https://mail.google.com", "https://accounts.google.com"},
			Probe: script("gmail.login_probe", "gmail-login-probe.mjs", 1, 45*time.Second),
			Send:  script("gmail.send", "gmail-send.mjs", 1, 90*time.Second),
		},
	})
	if err != nil {
		panic(err)
	}
	return registry
}

func (r Registry) Get(id string) (Provider, bool) {
	provider, ok := r.providers[strings.ToLower(strings.TrimSpace(id))]
	return cloneProvider(provider), ok
}

func (r Registry) List() []Provider {
	providers := make([]Provider, 0, len(r.ordered))
	for _, id := range r.ordered {
		providers = append(providers, cloneProvider(r.providers[id]))
	}
	return providers
}

func (r Registry) MatchRequest(value string) []Provider {
	normalized := normalizeAlias(value)
	matches := []Provider{}
	for _, id := range r.ordered {
		provider := r.providers[id]
		for _, alias := range provider.Aliases {
			if aliasMatchesRequest(normalized, normalizeAlias(alias)) {
				matches = append(matches, cloneProvider(provider))
				break
			}
		}
	}
	return matches
}

func validateScript(script Script) error {
	if strings.TrimSpace(script.ID) == "" || script.Revision <= 0 || len(script.Command) == 0 || script.Timeout <= 0 {
		return errors.New("email provider script registration is incomplete")
	}
	for _, part := range script.Command {
		if strings.TrimSpace(part) == "" || strings.ContainsRune(part, '\x00') {
			return errors.New("email provider script command is invalid")
		}
	}
	return nil
}

func cloneProvider(provider Provider) Provider {
	provider.Aliases = append([]string(nil), provider.Aliases...)
	provider.Origins = append([]string(nil), provider.Origins...)
	provider.Probe.Command = append([]string(nil), provider.Probe.Command...)
	provider.Send.Command = append([]string(nil), provider.Send.Command...)
	return provider
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	output := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		output = append(output, value)
	}
	return output
}

func normalizeAlias(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func aliasMatchesRequest(request, alias string) bool {
	if request == "" || alias == "" {
		return false
	}
	if strings.ContainsAny(alias, "邮箱邮件") {
		return strings.Contains(request, alias)
	}
	for offset := 0; ; {
		index := strings.Index(request[offset:], alias)
		if index < 0 {
			return false
		}
		index += offset
		leftOK := index == 0 || !isASCIIWord(request[index-1])
		right := index + len(alias)
		rightOK := right == len(request) || !isASCIIWord(request[right])
		if leftOK && rightOK {
			return true
		}
		offset = index + len(alias)
		if offset >= len(request) {
			return false
		}
	}
}

func isASCIIWord(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}
