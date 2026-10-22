package emailmanagement

import (
	"log/slog"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Provider arrival notifications name candidate targets (QQ's mid, Outlook's
// ItemId and receipt time). Until a qualification run proves that a provider's
// candidates name the Reader's targets, they are only measured: each candidate
// is matched against the members of the next committed rounds and the match
// kind and latency are logged. Nothing here changes what a round captures, and
// no identifier is written to logs.
const (
	pushCandidateTTL         = 15 * time.Minute
	maxPushCandidatesPerMail = 64
)

type pushCandidate struct {
	provider   string
	target     browsercontrol.MailObserverTarget
	observedAt time.Time
}

type pushCandidates struct {
	mu        sync.Mutex
	byMailbox map[string][]pushCandidate
}

func pushCandidateKey(owner, mailbox string) string { return owner + "\x00" + mailbox }

func (p *pushCandidates) record(owner, mailbox, provider string, targets []browsercontrol.MailObserverTarget, now time.Time) {
	if len(targets) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byMailbox == nil {
		p.byMailbox = map[string][]pushCandidate{}
	}
	key := pushCandidateKey(owner, mailbox)
	pending := expirePushCandidates(p.byMailbox[key], now)
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
}

// match reports which candidates name a committed round member and drops them.
func (p *pushCandidates) match(owner, mailbox string, members []store.EmailDiscoveryMember, now time.Time) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := pushCandidateKey(owner, mailbox)
	pending := expirePushCandidates(p.byMailbox[key], now)
	kinds := []string{}
	remaining := pending[:0]
	for _, candidate := range pending {
		kind := ""
		for _, member := range members {
			if kind = pushMatchKind(candidate.target, member); kind != "" {
				break
			}
		}
		if kind == "" {
			remaining = append(remaining, candidate)
			continue
		}
		kinds = append(kinds, kind)
		slog.Info("email push candidate matched", "provider", candidate.provider, "match", kind, "latency_ms", now.Sub(candidate.observedAt).Milliseconds())
	}
	if len(remaining) == 0 {
		delete(p.byMailbox, key)
	} else {
		p.byMailbox[key] = remaining
	}
	return kinds
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

func expirePushCandidates(pending []pushCandidate, now time.Time) []pushCandidate {
	kept := pending[:0]
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
	}
	return kept
}
