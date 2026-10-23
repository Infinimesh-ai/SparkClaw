package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmailSentMatchClosedBoundedLiterals(t *testing.T) {
	base := EmailSentMatch{"sent", "验收进度汇报 [original-3]", "sink+case@example.test", "2026-10-23T13:00:00Z", "2026-10-24T13:00:00Z"}
	for name, mutate := range map[string]func(*EmailSentMatch){
		"valid":               func(*EmailSentMatch) {},
		"literal punctuation": func(m *EmailSentMatch) { m.Subject = " [Case]: (常规汇报) " },
		"max bytes":           func(m *EmailSentMatch) { m.Subject = strings.Repeat("a", 512) },
		"folder":              func(m *EmailSentMatch) { m.Folder = "all" },
		"empty subject":       func(m *EmailSentMatch) { m.Subject = "" },
		"utf8 bytes":          func(m *EmailSentMatch) { m.Subject = strings.Repeat("中", 171) },
		"quote":               func(m *EmailSentMatch) { m.Subject += `" in:anywhere` },
		"slash":               func(m *EmailSentMatch) { m.Subject += `\` },
		"control":             func(m *EmailSentMatch) { m.Subject += "\n" },
		"delete":              func(m *EmailSentMatch) { m.Subject += "\x7f" },
		"utf8 invalid":        func(m *EmailSentMatch) { m.Subject += "\xff" },
		"display address":     func(m *EmailSentMatch) { m.Recipient = "Other <sink@example.test>" },
		"two addresses":       func(m *EmailSentMatch) { m.Recipient = "a@example.test,b@example.test" },
		"local dots":          func(m *EmailSentMatch) { m.Recipient = "a..b@example.test" },
		"local too long":      func(m *EmailSentMatch) { m.Recipient = strings.Repeat("a", 65) + "@example.test" },
		"domain delimiter":    func(m *EmailSentMatch) { m.Recipient = "a@example.test in:all" },
		"reverse":             func(m *EmailSentMatch) { m.IntervalEnd = "2026-10-23T12:59:59Z" },
		"equal":               func(m *EmailSentMatch) { m.IntervalEnd = m.IntervalStart },
		"too wide":            func(m *EmailSentMatch) { m.IntervalEnd = "2026-10-24T13:00:01Z" },
		"offset":              func(m *EmailSentMatch) { m.IntervalStart = "2026-10-23T13:00:00+00:00" },
		"fraction":            func(m *EmailSentMatch) { m.IntervalStart = "2026-10-23T13:00:00.000Z" },
		"invalid date":        func(m *EmailSentMatch) { m.IntervalStart = "2026-02-30T13:00:00Z" },
	} {
		t.Run(name, func(t *testing.T) {
			m := base
			mutate(&m)
			err := m.Validate()
			valid := name == "valid" || name == "literal punctuation" || name == "max bytes"
			if (err == nil) != valid {
				t.Fatalf("accepted=%v", err == nil)
			}
		})
	}
	raw, _ := json.Marshal(base)
	for _, malformed := range []string{"null", "[]", "{}", string(raw) + "{}", strings.Replace(string(raw), `"folder":"sent",`, "", 1), strings.Replace(string(raw), `"folder":"sent"`, `"folder":"sent","folder":"sent"`, 1), strings.Replace(string(raw), `"folder":"sent"`, `"folder":"sent","account_address":"other@example.test"`, 1), strings.Replace(string(raw), `"folder":"sent"`, `"folder":1`, 1)} {
		if _, err := DecodeEmailSentMatch([]byte(malformed)); err == nil {
			t.Fatal("malformed selector accepted")
		}
	}
	decoded, err := DecodeEmailSentMatch(raw)
	if err != nil || *decoded != base {
		t.Fatal("literals were not preserved")
	}
}
