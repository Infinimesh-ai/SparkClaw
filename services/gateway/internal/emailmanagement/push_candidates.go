package emailmanagement

import (
	"log/slog"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Provider arrival notifications name candidate targets (QQ's list emailid,
// Outlook's ItemId and receipt time). Each candidate is matched against the
// members of the next committed rounds and the match kind and latency are
// logged; push_qualification.go turns those outcomes into the per-provider
// direct-fetch gate. Nothing here changes what a round captures, and no
// identifier is written to logs.
const (
	pushCandidateTTL         = 15 * time.Minute
	maxPushCandidatesPerMail = 64
)

type pushCandidate struct {
	provider   string
	target     browsercontrol.MailObserverTarget
	observedAt time.Time
}

// pushMatch is one candidate that named a committed round member.
type pushMatch struct {
	candidate pushCandidate
	member    store.EmailDiscoveryMember
	kind      string
}

type pushCandidates struct {
	mu        sync.Mutex
	byMailbox map[string][]pushCandidate
}

func pushCandidateKey(owner, mailbox string) string { return owner + "\x00" + mailbox }

// record keeps new candidates and returns the ones that expired unmatched.
func (p *pushCandidates) record(owner, mailbox, provider string, targets []browsercontrol.MailObserverTarget, now time.Time) []pushCandidate {
	if len(targets) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byMailbox == nil {
		p.byMailbox = map[string][]pushCandidate{}
	}
	key := pushCandidateKey(owner, mailbox)
	pending, expired := expirePushCandidates(p.byMailbox[key], now)
	for _, target := range targets {
		duplicate := false
		for _, existing := range pending {
			if existing.target.ProviderMessageID == target.ProviderMessageID && existing.target.ProviderItemID == target.ProviderItemID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			pending = append(pending, pushCandidate{provider: provider, target: target, observedAt: now})
		}
	}
	if len(pending) > maxPushCandidatesPerMail {
		pending = pending[len(pending)-maxPushCandidatesPerMail:]
	}
	p.byMailbox[key] = pending
	return expired
}

// match reports which candidates name a committed round member and drops
// them, together with the candidates that expired unmatched.
func (p *pushCandidates) match(owner, mailbox string, members []store.EmailDiscoveryMember, now time.Time) ([]pushMatch, []pushCandidate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := pushCandidateKey(owner, mailbox)
	pending, expired := expirePushCandidates(p.byMailbox[key], now)
	matches := []pushMatch{}
	remaining := pending[:0]
	for _, candidate := range pending {
		kind := ""
		var named store.EmailDiscoveryMember
		for _, member := range members {
			if kind = pushMatchKind(candidate.target, member); kind != "" {
				named = member
				break
			}
		}
		if kind == "" {
			remaining = append(remaining, candidate)
			continue
		}
		matches = append(matches, pushMatch{candidate: candidate, member: named, kind: kind})
		slog.Info("email push candidate matched", "provider", candidate.provider, "match", kind, "latency_ms", now.Sub(candidate.observedAt).Milliseconds())
	}
	if len(remaining) == 0 {
		delete(p.byMailbox, key)
	} else {
		p.byMailbox[key] = remaining
	}
	return matches, expired
}

func pushMatchKind(target browsercontrol.MailObserverTarget, member store.EmailDiscoveryMember) string {
	switch {
	case target.ProviderMessageID != "" && target.ProviderMessageID == member.ProviderMessageID:
		return "message_id"
	case target.ProviderItemID != "" && (target.ProviderItemID == member.ProviderMessageID || target.ProviderItemID == member.ProviderNativeID):
		return "item_id"
	case target.ReceivedAt != nil && !member.SourceTime.IsZero() && member.SourceTime.Equal(*target.ReceivedAt):
		return "received_at"
	}
	return ""
}

func expirePushCandidates(pending []pushCandidate, now time.Time) ([]pushCandidate, []pushCandidate) {
	kept := pending[:0]
	var expired []pushCandidate
	for _, candidate := range pending {
		if now.Sub(candidate.observedAt) < pushCandidateTTL {
			kept = append(kept, candidate)
			continue
		}
		kind := "item_id"
		if candidate.target.ProviderMessageID != "" {
			kind = "message_id"
		}
		slog.Info("email push candidate unmatched", "provider", candidate.provider, "candidate", kind)
		expired = append(expired, candidate)
	}
	return kept, expired
}
