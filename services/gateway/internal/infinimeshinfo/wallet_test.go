package infinimeshinfo

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeTokenIssuer struct {
	mu    sync.Mutex
	calls int
	issue func(call int, tokenType TokenType, count int) ([]Token, error)
}

func (f *fakeTokenIssuer) Issue(_ context.Context, tokenType TokenType, count int) ([]Token, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	return f.issue(call, tokenType, count)
}

func (f *fakeTokenIssuer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestTokenWalletConcurrentReservationsAreUnique(t *testing.T) {
	const workers = 24
	now := time.Date(2026, 7, 14, 2, 0, 0, 0, time.UTC)
	issuer := &fakeTokenIssuer{issue: func(_ int, tokenType TokenType, count int) ([]Token, error) {
		time.Sleep(10 * time.Millisecond)
		tokens := make([]Token, 0, count)
		for index := 0; index < count; index++ {
			tokens = append(tokens, Token{
				Value:     fmt.Sprintf("token-%d", index),
				Type:      tokenType,
				ExpiresAt: now.Add(time.Hour),
			})
		}
		return tokens, nil
	}}
	wallet := newTokenWallet(issuer, workers, func() time.Time { return now })

	start := make(chan struct{})
	values := make(chan string, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			value, err := wallet.Reserve(context.Background(), TokenTypeBasic)
			if err != nil {
				errs <- err
				return
			}
			values <- value
		}()
	}
	close(start)
	wait.Wait()
	close(values)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal("concurrent reservation failed")
		}
	}
	seen := map[string]bool{}
	for value := range values {
		if seen[value] {
			t.Fatal("wallet returned the same token more than once")
		}
		seen[value] = true
	}
	if len(seen) != workers {
		t.Fatalf("reserved %d unique tokens, want %d", len(seen), workers)
	}
	if issuer.callCount() != 1 {
		t.Fatalf("issuer calls = %d, want 1", issuer.callCount())
	}
}

func TestTokenWalletPrunesExpiredTokens(t *testing.T) {
	now := time.Date(2026, 7, 14, 2, 0, 0, 0, time.UTC)
	issuer := &fakeTokenIssuer{issue: func(call int, tokenType TokenType, _ int) ([]Token, error) {
		return []Token{
			{Value: fmt.Sprintf("expired-%d", call), Type: tokenType, ExpiresAt: now.Add(-time.Minute)},
			{Value: fmt.Sprintf("valid-%d", call), Type: tokenType, ExpiresAt: now.Add(time.Hour)},
		}, nil
	}}
	wallet := newTokenWallet(issuer, 2, func() time.Time { return now })

	first, err := wallet.Reserve(context.Background(), TokenTypeBasic)
	if err != nil {
		t.Fatal(err)
	}
	second, err := wallet.Reserve(context.Background(), TokenTypeBasic)
	if err != nil {
		t.Fatal(err)
	}
	if first != "valid-1" || second != "valid-2" {
		t.Fatalf("unexpected reserved tokens: %q, %q", first, second)
	}
	if issuer.callCount() != 2 {
		t.Fatalf("issuer calls = %d, want 2", issuer.callCount())
	}
}

func TestTokenWalletDiscardAllForcesNewBatch(t *testing.T) {
	now := time.Date(2026, 7, 14, 2, 0, 0, 0, time.UTC)
	issuer := &fakeTokenIssuer{issue: func(call int, tokenType TokenType, count int) ([]Token, error) {
		tokens := make([]Token, 0, count)
		for index := 0; index < count; index++ {
			tokens = append(tokens, Token{
				Value:     fmt.Sprintf("batch-%d-token-%d", call, index),
				Type:      tokenType,
				ExpiresAt: now.Add(time.Hour),
			})
		}
		return tokens, nil
	}}
	wallet := newTokenWallet(issuer, 2, func() time.Time { return now })

	first, err := wallet.Reserve(context.Background(), TokenTypeBasic)
	if err != nil {
		t.Fatal(err)
	}
	wallet.DiscardAll(TokenTypeBasic)
	second, err := wallet.Reserve(context.Background(), TokenTypeBasic)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || issuer.callCount() != 2 {
		t.Fatalf("discard did not force a new batch: first=%q second=%q calls=%d", first, second, issuer.callCount())
	}
}
