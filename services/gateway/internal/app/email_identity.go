package app

import (
	"net/mail"
	"strings"
	"unicode/utf8"
)

// ValidEmailDeliveryAddresses checks the closed metadata contract. Provider
// adapters must additionally prove these addresses belong to the same native
// account; syntactic validity alone does not establish mailbox ownership.
func ValidEmailDeliveryAddresses(address string, aliases []string) bool {
	if address == "" || len(address) > 320 || !utf8.ValidString(address) || strings.ContainsAny(address, "<> \t\r\n\x00") || len(aliases) == 0 || len(aliases) > 100 {
		return false
	}
	seen, found := map[string]bool{}, false
	for _, alias := range aliases {
		if len(alias) > 320 || !utf8.ValidString(alias) || strings.ContainsAny(alias, "<> \t\r\n\x00") {
			return false
		}
		parsed, err := mail.ParseAddress(alias)
		if err != nil || parsed.Name != "" || parsed.Address != alias {
			return false
		}
		key := strings.ToLower(alias)
		if seen[key] {
			return false
		}
		seen[key] = true
		found = found || strings.EqualFold(address, alias)
	}
	return found
}

// ContactAddress is communication metadata, never the Reader/Store identity.
// Older QQ/Gmail records used their actual mailbox address as account identity.
// Outlook may instead expose a non-deliverable internal account namespace.
func (m EmailMailbox) ContactAddress() string {
	if m.DeliveryAddress != "" {
		return m.DeliveryAddress
	}
	if m.Provider == EmailProviderOutlook {
		return ""
	}
	return m.Address
}
