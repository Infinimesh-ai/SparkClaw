package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// EmailSentMatch is a bounded original-message predicate, not a provider query.
// Provider identities and account addresses remain Runtime-owned.
type EmailSentMatch struct {
	Folder        string `json:"folder"`
	Subject       string `json:"subject"`
	Recipient     string `json:"recipient"`
	IntervalStart string `json:"interval_start"`
	IntervalEnd   string `json:"interval_end"`
}

const EmailSentMatchMaxSubjectBytes = 512

var sentMatchAddress = regexp.MustCompile(`^[A-Za-z0-9]+(?:[A-Za-z0-9._%+\-]*[A-Za-z0-9])?@[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?)+$`)

// ValidEmailSentMatchAddress intentionally accepts only ordinary ASCII bare
// addresses. Quoted local parts and search delimiters have no qualified query
// escaping semantics in the provider adapter.
func ValidEmailSentMatchAddress(value string) bool {
	at := strings.IndexByte(value, '@')
	return len(value) <= 254 && at > 0 && at <= 64 && !strings.Contains(value[:at], "..") && sentMatchAddress.MatchString(value)
}

func (m EmailSentMatch) Validate() error {
	invalid := errors.New("invalid bounded Sent match")
	if m.Folder != "sent" || !ValidEmailSentMatchAddress(m.Recipient) || len(m.Subject) < 1 || len(m.Subject) > EmailSentMatchMaxSubjectBytes || !utf8.ValidString(m.Subject) || strings.ContainsAny(m.Subject, "\"\\") {
		return invalid
	}
	for _, r := range m.Subject {
		if r < 32 || r == 127 {
			return invalid
		}
	}
	start, err := time.Parse(time.RFC3339, m.IntervalStart)
	end, endErr := time.Parse(time.RFC3339, m.IntervalEnd)
	if err != nil || endErr != nil || start.Format(time.RFC3339) != m.IntervalStart || end.Format(time.RFC3339) != m.IntervalEnd || !strings.HasSuffix(m.IntervalStart, "Z") || !strings.HasSuffix(m.IntervalEnd, "Z") || !start.Before(end) || end.Sub(start) > 24*time.Hour {
		return invalid
	}
	return nil
}

// DecodeEmailSentMatch rejects omitted, duplicate and unknown fields as well as
// a trailing JSON value. It does not normalize literals into a match.
func DecodeEmailSentMatch(raw []byte) (*EmailSentMatch, error) {
	invalid := errors.New("invalid bounded Sent match")
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	fields := map[string]string{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, invalid
		}
		switch key {
		case "folder", "subject", "recipient", "interval_start", "interval_end":
		default:
			return nil, invalid
		}
		if _, exists := fields[key]; exists {
			return nil, invalid
		}
		var value string
		if err = d.Decode(&value); err != nil {
			return nil, invalid
		}
		fields[key] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || len(fields) != 5 {
		return nil, invalid
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, invalid
	}
	m := &EmailSentMatch{fields["folder"], fields["subject"], fields["recipient"], fields["interval_start"], fields["interval_end"]}
	return m, m.Validate()
}
