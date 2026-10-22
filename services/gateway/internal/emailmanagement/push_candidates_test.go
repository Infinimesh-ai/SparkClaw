package emailmanagement

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestPushCandidatesMatchCommittedMembersByEachProviderIdentity(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	received := now.Add(-2 * time.Second)
	var p pushCandidates
	p.record("owner", "box", app.EmailProviderQQMail, []browsercontrol.MailObserverTarget{{ProviderMessageID: "ZC0001"}, {ProviderMessageID: "ZC0001"}}, now)
	p.record("owner", "box", app.EmailProviderOutlook, []browsercontrol.MailObserverTarget{{ProviderItemID: "item-native"}, {ProviderItemID: "item-moved", ReceivedAt: &received}}, now)
	if got := len(p.byMailbox[pushCandidateKey("owner", "box")]); got != 3 {
		t.Fatalf("duplicates must collapse: %d", got)
	}
	members := []store.EmailDiscoveryMember{
		{ProviderMessageID: "ZC0001"},
		{ProviderMessageID: "immutable", ProviderNativeID: "item-native"},
		{ProviderMessageID: "other", SourceTime: received},
	}
	matches, _ := p.match("owner", "box", members, now.Add(3*time.Second))
	if len(matches) != 3 || matches[0].kind != "message_id" || matches[1].kind != "item_id" || matches[2].kind != "received_at" || matches[1].member.ProviderNativeID != "item-native" {
		t.Fatalf("matches: %v", matches)
	}
	if _, left := p.byMailbox[pushCandidateKey("owner", "box")]; left {
		t.Fatal("matched candidates must be removed")
	}
}

func TestPushCandidatesStayIsolatedExpireAndAreBounded(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	var p pushCandidates
	p.record("owner", "box", app.EmailProviderQQMail, []browsercontrol.MailObserverTarget{{ProviderMessageID: "a"}}, now)
	if matches, _ := p.match("other-owner", "box", []store.EmailDiscoveryMember{{ProviderMessageID: "a"}}, now); len(matches) != 0 {
		t.Fatal("candidates must not cross owners")
	}
	if matches, expired := p.match("owner", "box", []store.EmailDiscoveryMember{{ProviderMessageID: "a"}}, now.Add(pushCandidateTTL)); len(matches) != 0 || len(expired) != 1 {
		t.Fatal("expired candidates must not match and must be reported")
	}
	for i := 0; i < maxPushCandidatesPerMail+10; i++ {
		p.record("owner", "box", app.EmailProviderQQMail, []browsercontrol.MailObserverTarget{{ProviderMessageID: string(rune('A'+i%26)) + string(rune('a'+i/26))}}, now)
	}
	if got := len(p.byMailbox[pushCandidateKey("owner", "box")]); got != maxPushCandidatesPerMail {
		t.Fatalf("candidates must be bounded: %d", got)
	}
}

func TestSignalWakesIdleWorkersImmediately(t *testing.T) {
	s, _, _ := newFixtureService(t, store.NewMemoryStore())
	waiting := s.workSignal()
	s.signal()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("signal did not wake idle workers")
	}
	next := s.workSignal()
	select {
	case <-next:
		t.Fatal("a fresh wait must not be pre-woken")
	default:
	}
}
