package emailautomation

import (
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// providerScriptContract is generated from the Controller provider registry
// (tools/browser-controller/src/provider-scripts.mjs) by
// `npm run sync:provider-contract --prefix tools/browser-controller`. The
// Controller test suite fails when the two drift, so the script identity,
// revision, and budget the gateway binds to are never restated by hand here.
//
//go:embed provider_scripts.json
var providerScriptContract []byte

// providerAliases are the gateway-side request phrasings that select a
// provider; they are a routing concern and have no Controller counterpart.
var providerAliases = map[string][]string{
	app.EmailProviderQQMail:  {"QQ 邮箱", "QQ邮箱", "QQMail", "腾讯邮箱"},
	app.EmailProviderOutlook: {"Outlook Mail", "Outlook 邮箱", "微软邮箱", "Hotmail"},
	app.EmailProviderGmail:   {"Google Mail", "谷歌邮箱", "Google 邮箱"},
}

type providerScriptContractFile struct {
	SchemaVersion int                           `json:"schema_version"`
	Scripts       []providerScriptContractEntry `json:"scripts"`
}

type providerScriptContractEntry struct {
	Provider  string `json:"provider"`
	Operation string `json:"operation"`
	ScriptID  string `json:"script_id"`
	Revision  int    `json:"revision"`
	TimeoutMS int    `json:"timeout_ms"`
}

// Script identifies a provider script by the ID and revision the browser
// controller resolves in its own registry; Timeout is the controller-side
// budget for that script and bounds the gateway's wait for it.
type Script struct {
	ID       string
	Revision int
	Timeout  time.Duration
}

// Provider is the gateway's view of one Controller-registered mail provider.
// Login URL and allowed origins live only in the Controller registry, which
// resolves them from the provider ID at run time.
type Provider struct {
	ID          string
	DisplayName string
	Aliases     []string
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
		if provider.ID == "" || provider.DisplayName == "" {
			return Registry{}, errors.New("email provider identity and display name are required")
		}
		if !app.KnownEmailProvider(provider.ID) {
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
		registry.providers[provider.ID] = cloneProvider(provider)
		registry.ordered = append(registry.ordered, provider.ID)
	}
	slices.Sort(registry.ordered)
	return registry, nil
}

// DefaultRegistry binds every app.EmailProviderIDs entry to the probe and
// send scripts the Controller contract declares for it.
func DefaultRegistry() Registry {
	registry, err := registryFromContract(providerScriptContract)
	if err != nil {
		panic(err)
	}
	return registry
}

func registryFromContract(raw []byte) (Registry, error) {
	var contract providerScriptContractFile
	if err := decodeStrictJSON(raw, &contract); err != nil {
		return Registry{}, fmt.Errorf("email provider script contract: %w", err)
	}
	if contract.SchemaVersion != 1 {
		return Registry{}, fmt.Errorf("email provider script contract schema %d is unsupported", contract.SchemaVersion)
	}
	scripts := map[string]Script{}
	for _, entry := range contract.Scripts {
		if !app.KnownEmailProvider(entry.Provider) || entry.Operation != "probe" && entry.Operation != "send" {
			return Registry{}, fmt.Errorf("email provider script contract lists unknown %s %s", entry.Provider, entry.Operation)
		}
		key := entry.Provider + ":" + entry.Operation
		if _, exists := scripts[key]; exists {
			return Registry{}, fmt.Errorf("email provider script contract repeats %s", key)
		}
		scripts[key] = Script{ID: entry.ScriptID, Revision: entry.Revision, Timeout: time.Duration(entry.TimeoutMS) * time.Millisecond}
	}
	providers := make([]Provider, 0, len(providerAliases))
	for _, id := range app.EmailProviderIDs() {
		probe, probeOK := scripts[id+":probe"]
		send, sendOK := scripts[id+":send"]
		if !probeOK || !sendOK {
			return Registry{}, fmt.Errorf("email provider script contract has no probe and send scripts for %s", id)
		}
		providers = append(providers, Provider{
			ID: id, DisplayName: app.EmailProviderDisplayName(id), Aliases: providerAliases[id],
			Probe: probe, Send: send,
		})
	}
	if len(scripts) != 2*len(providers) {
		return Registry{}, errors.New("email provider script contract lists scripts for an unregistered provider")
	}
	return NewRegistry(providers)
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
	if strings.TrimSpace(script.ID) == "" || script.Revision <= 0 || script.Timeout <= 0 {
		return errors.New("email provider script registration is incomplete")
	}
	return nil
}

func cloneProvider(provider Provider) Provider {
	provider.Aliases = append([]string(nil), provider.Aliases...)
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
