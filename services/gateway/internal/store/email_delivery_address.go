package store

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

// Address refreshes change communication metadata only. They must not create a
// new mailbox, reset a checkpoint, rebind a draft or alter a capture namespace.
func emailSetDelivery(box *app.EmailMailbox, address string, aliases []string) error {
	if address == "" && len(aliases) == 0 {
		// A successful fresh observation without metadata no longer proves an
		// earlier delivery address. Retain identity/history, withdraw the projection.
		box.DeliveryAddress, box.AccountAliases = "", nil
		return nil
	}
	if !app.ValidEmailDeliveryAddresses(address, aliases) {
		return errEmailInvalid
	}
	box.DeliveryAddress = address
	box.AccountAliases = append([]string(nil), aliases...)
	return nil
}
