package gateway

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// The resolver is deliberately stateless: every read uses the current request's
// verified installation/authorization binding, including after a draft restart.
type mailAttachmentObjects struct{}

var _ emailmanagement.AttachmentObjectReader = mailAttachmentObjects{}

func (mailAttachmentObjects) ReadAttachmentObject(ctx context.Context, owner string, object app.EmailAttachmentObject, limit int64) (app.EmailAttachmentObject, []byte, error) {
	unavailable := errors.New("mail attachment object unavailable")
	access, ok := ctx.Value(iscpObjectContextKey{}).(iscpObjectAccess)
	ref := iscpworkbench.ObjectReference(object)
	if !ok || owner != access.binding.OwnerID || ref.Purpose != app.EmailSendAttachmentPurpose || ref.Validate() != nil {
		return app.EmailAttachmentObject{}, nil, unavailable
	}
	actual, err := access.store.Describe(ctx, access.binding, ref.ObjectID, ref.Version)
	if err != nil || actual.Purpose != app.EmailSendAttachmentPurpose || actual.Size != ref.Size || actual.SHA256 != ref.SHA256 || (ref.Name != "" && ref.Name != actual.Name) || (ref.MediaType != "" && ref.MediaType != actual.MediaType) || (ref.ExpiresAt != "" && ref.ExpiresAt != actual.ExpiresAt) {
		return app.EmailAttachmentObject{}, nil, unavailable
	}
	raw, err := domainReadObject(ctx, actual, app.EmailSendAttachmentPurpose, limit)
	if err != nil {
		return app.EmailAttachmentObject{}, nil, unavailable
	}
	// A release or expiry during a multi-chunk read invalidates admission too.
	current, err := access.store.Describe(ctx, access.binding, actual.ObjectID, actual.Version)
	if err != nil || current != actual {
		return app.EmailAttachmentObject{}, nil, unavailable
	}
	return app.EmailAttachmentObject(actual), raw, nil
}
