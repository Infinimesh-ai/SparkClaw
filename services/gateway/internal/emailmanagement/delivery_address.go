package emailmanagement

import (
	"errors"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func pageDeliveryIdentity(parts []timelinePagePart) (string, []string, error) {
	var address string
	var aliases, normalized []string
	missing := false
	for _, part := range parts {
		v := part.page.Discovery
		if v.DeliveryAddress == "" && len(v.AccountAliases) == 0 {
			missing = true
			continue
		}
		if !app.ValidEmailDeliveryAddresses(v.DeliveryAddress, v.AccountAliases) {
			return "", nil, errors.New("email_delivery_identity_invalid")
		}
		keys := make([]string, len(v.AccountAliases))
		for i, alias := range v.AccountAliases {
			keys[i] = strings.ToLower(alias)
		}
		slices.Sort(keys)
		if address != "" && (!strings.EqualFold(address, v.DeliveryAddress) || !slices.Equal(normalized, keys)) {
			// Stable account identity is checked before this point. A native
			// default/alias change withdraws only the communication projection;
			// it must not strand already captured source behind its durable page.
			missing = true
		}
		address, aliases, normalized = v.DeliveryAddress, slices.Clone(v.AccountAliases), keys
	}
	if missing {
		return "", nil, nil
	}
	return address, aliases, nil
}
