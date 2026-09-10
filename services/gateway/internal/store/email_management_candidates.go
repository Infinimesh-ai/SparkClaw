package store

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailCandidates(e *emailEngine, q EmailCandidateQuery) (EmailCandidateSet, error) {
	out := EmailCandidateSet{OwnerEpoch: emailCounter(e, "assignment_epoch", false), Conversations: []app.EmailConversation{}, RelatedMails: []app.EmailMail{}}
	m, err := emailMail(e, q.MailID)
	if err != nil {
		return out, err
	}
	limit := emailLimit(q.Limit)
	seenMail := map[string]bool{m.ID: true}
	seenConv := map[string]bool{}
	addConversation := func(id string) {
		if id == "" || seenConv[id] || len(out.Conversations) >= limit {
			return
		}
		if c, ok := emailGet[app.EmailConversation](e, "conversation", id); ok {
			seenConv[id] = true
			c.Summary = emailProjectSummary(e, app.EmailJobConversationSummary, id)
			out.Conversations = append(out.Conversations, c)
		}
	}
	collect := func(query emailRowsQuery, matches func(app.EmailMail) bool) {
		if len(out.RelatedMails) >= limit && len(out.Conversations) >= limit {
			return
		}
		query.Kind = "mail"
		if !emailEvents(e) {
			query.Entry = "interaction"
		}
		// One extra row allows the source itself without consuming a candidate.
		query.Limit = limit + 1
		for _, other := range emailList[app.EmailMail](e, query) {
			if other.ID == m.ID || (matches != nil && !matches(other)) {
				continue
			}
			if !seenMail[other.ID] && len(out.RelatedMails) < limit {
				seenMail[other.ID] = true
				out.RelatedMails = append(out.RelatedMails, emailProjectMail(e, other))
			}
			addConversation(other.ConversationID)
		}
	}
	if m.ReplyMailID != "" && m.ReplyMailID != m.ID {
		if original, ok := emailGet[app.EmailMail](e, "mail", m.ReplyMailID); ok {
			seenMail[original.ID] = true
			out.RelatedMails = append(out.RelatedMails, emailProjectMail(e, original))
			addConversation(original.ConversationID)
		}
	}
	// Preserve evidence tiers and closest-parent ordering; canonical set sorting
	// would let an address or opaque thread ID displace an observed reply link.
	refs := append(app.EmailAnalysisReferences(m.ReplyReferences), m.MessageID)
	for _, ref := range emailCandidateUnique(refs) {
		collect(emailRowsQuery{Search: ref, MailMessageID: ref}, nil)
	}
	if m.ProviderThreadID != "" {
		collect(emailRowsQuery{Parent: m.MailboxID, Search: m.ProviderThreadID, MailThreadID: m.ProviderThreadID}, nil)
	}
	representation, _ := emailGet[app.EmailRepresentation](e, "representation", m.RepresentationID)
	// Shared senders often discuss many unrelated topics. Reserve the earlier
	// slots for matching content before a crowded counterpart consumes them.
	for _, term := range emailCandidateTopicTerms(m.Subject, representation.BodyText) {
		collect(emailRowsQuery{Search: term}, nil)
	}
	participants := append(append(append([]string{}, representation.From...), representation.To...), m.Participants...)
	participants = emailCandidateUnique(participants)
	if len(participants) > app.EmailAnalysisReferenceLimit {
		participants = participants[:app.EmailAnalysisReferenceLimit]
	}
	queried := 0
	for _, participant := range participants {
		if queried >= 8 {
			break
		}
		address, err := emailAddress(participant)
		if err != nil {
			continue
		}
		// Stable identity lookups include paused/historical receiving accounts,
		// without depending on an arbitrarily truncated mailbox listing.
		owned := false
		for _, provider := range app.EmailProviderIDs() {
			if _, found := emailGet[app.EmailMailbox](e, "mailbox", emailID(e.owner, provider, address)); found {
				owned = true
				break
			}
		}
		if owned {
			continue
		}
		queried++
		collect(emailRowsQuery{Search: address}, func(other app.EmailMail) bool {
			for _, p := range other.Participants {
				if normalized, err := emailAddress(p); err == nil && normalized == address {
					return true
				}
			}
			return false
		})
	}
	// Recent topics are the final, bounded semantic expansion, after observed
	// links, subject/content search and counterpart matches have had priority.
	for _, c := range emailList[app.EmailConversation](e, emailRowsQuery{Kind: "conversation", Limit: limit}) {
		addConversation(c.ID)
	}
	return out, e.err
}

func emailCandidateUnique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func emailCandidateTextPrefix(value string, limit int) string {
	n := 0
	for at := range value {
		if n == limit {
			return value[:at]
		}
		n++
	}
	return value
}

func emailCandidateTopicTerms(subject, body string) []string {
	subject = strings.ToLower(strings.TrimSpace(emailCandidateTextPrefix(subject, 256)))
	for i := 0; i < 8; i++ {
		at := strings.IndexAny(subject, ":：")
		if at < 0 || !slices.Contains([]string{"re", "fw", "fwd", "回复", "答复", "转发"}, strings.TrimSpace(subject[:at])) {
			break
		}
		_, width := utf8.DecodeRuneInString(subject[at:])
		subject = strings.TrimSpace(subject[at+width:])
	}
	var out []string
	if utf8.RuneCountInString(subject) >= 3 {
		out = append(out, emailCandidateTextPrefix(subject, 120))
	}
	words := func(value string) []string {
		return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	appendWords := func(value string, budget int) {
		for _, word := range emailCandidateUnique(words(value)) {
			if budget == 0 {
				break
			}
			if utf8.RuneCountInString(word) < 3 || slices.Contains([]string{"the", "and", "for", "that", "this", "with", "from", "your", "have", "please", "hello", "dear", "thanks", "regards"}, word) || slices.Contains(out, word) {
				continue
			}
			out = append(out, emailCandidateTextPrefix(word, 64))
			budget--
		}
	}
	appendWords(subject, 3)
	appendWords(emailCandidateTextPrefix(body, 512), 4)
	return emailCandidateUnique(out)
}
