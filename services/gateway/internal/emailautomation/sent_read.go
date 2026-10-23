package emailautomation

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func validateSentRead(request ReadRequest) error {
	if request.SentMatch == nil {
		return nil
	}
	if request.SentMatch.Validate() != nil || request.Target != nil || request.Discovery != nil {
		return codedError(app.ToolErrorEmailInvalidInput, "Sent match cannot be combined with another capture selector")
	}
	if request.Provider != app.EmailProviderGmail {
		return codedError(app.ToolErrorEmailProviderUnavailable, "Sent original matching is unavailable for this provider")
	}
	return nil
}

func (c *Controller) sentReadMailbox(ctx context.Context, owner, provider string) (app.EmailMailbox, error) {
	reader, ok := c.store.(mailboxSignalReader)
	if !ok {
		return app.EmailMailbox{}, codedError(app.ToolErrorEmailProviderUnavailable, "Verified mailbox lookup is unavailable")
	}
	mailboxes, err := reader.ListEmailMailboxes(ctx, owner)
	if err != nil {
		return app.EmailMailbox{}, err
	}
	var selected app.EmailMailbox
	for _, mailbox := range mailboxes {
		if !mailbox.Active || mailbox.Provider != provider {
			continue
		}
		if mailbox.OwnerID != owner || selected.Address != "" || mailbox.ID == "" || mailbox.BindingGeneration < 1 || !app.ValidEmailSentMatchAddress(mailbox.Address) {
			return app.EmailMailbox{}, codedError(app.ToolErrorEmailProviderUnavailable, "A unique verified mailbox is required")
		}
		selected = mailbox
	}
	if selected.Address == "" {
		return app.EmailMailbox{}, codedError(app.ToolErrorEmailProviderUnavailable, "A unique verified mailbox is required")
	}
	return selected, nil
}
