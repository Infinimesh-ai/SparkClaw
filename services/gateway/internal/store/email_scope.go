package store

import (
	"encoding/base64"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"strings"
	"time"
)

func emailVerificationValidity(m app.EmailMail, at time.Time) string {
	if m.Classification == nil || m.Classification.NotificationSubtype != "verification" {
		return ""
	}
	if m.Verification == nil || m.Verification.ExpiresAt == nil {
		return "validity_unknown"
	}
	if !m.Verification.ExpiresAt.After(at) {
		return "expired"
	}
	return "not_expired"
}

type emailScopeCursor struct {
	Scope string    `json:"scope"`
	After string    `json:"after"`
	At    time.Time `json:"at"`
}

func emailScopeKey(q EmailQuery) string {
	q.After = ""
	q.AsOf = time.Time{}
	q.Limit = 0
	return emailID(string(emailJSON(q)))
}
func emailScopedCursor(q EmailQuery, now time.Time) (EmailQuery, error) {
	if q.Validity != "" && q.Validity != "expired" && q.Validity != "not_expired" && q.Validity != "validity_unknown" {
		return q, errEmailInvalid
	}
	if q.AsOf.IsZero() {
		q.AsOf = now
	}
	if q.Entry == "" && q.Direction == "" {
		return q, nil
	}
	if q.After != "" {
		if !strings.HasPrefix(q.After, "v2.") {
			return q, errEmailInvalid
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(q.After, "v2."))
		if err != nil {
			return q, errEmailInvalid
		}
		var c emailScopeCursor
		if json.Unmarshal(raw, &c) != nil || c.Scope != emailScopeKey(q) || c.At.IsZero() {
			return q, errEmailInvalid
		}
		q.After = c.After
		q.AsOf = c.At
	}
	return q, nil
}
func emailEncodeScopeCursor(q EmailQuery, after string) string {
	if q.Entry == "" && q.Direction == "" {
		return after
	}
	return "v2." + base64.RawURLEncoding.EncodeToString(emailJSON(emailScopeCursor{Scope: emailScopeKey(q), After: after, At: q.AsOf}))
}
