package infinimeshinfo

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	errUnsupportedTokenType = errors.New("infinimesh info token type is not supported")
	errNoTokensIssued       = errors.New("infinimesh info token service returned no usable tokens")
)

type tokenWallet struct {
	mu        sync.Mutex
	issuer    TokenIssuer
	batchSize int
	now       func() time.Time
	tokens    map[TokenType][]Token
	issuing   map[TokenType]*issueCall
}

type issueCall struct {
	done chan struct{}
	err  error
}

func NewTokenWallet(issuer TokenIssuer, batchSize int) TokenWallet {
	return newTokenWallet(issuer, batchSize, time.Now)
}

func newTokenWallet(issuer TokenIssuer, batchSize int, now func() time.Time) *tokenWallet {
	if batchSize <= 0 {
		batchSize = 10
	}
	if now == nil {
		now = time.Now
	}
	return &tokenWallet{
		issuer:    issuer,
		batchSize: batchSize,
		now:       now,
		tokens:    map[TokenType][]Token{},
		issuing:   map[TokenType]*issueCall{},
	}
}

func (w *tokenWallet) Reserve(ctx context.Context, tokenType TokenType) (string, error) {
	if tokenType != TokenTypeBasic {
		return "", errUnsupportedTokenType
	}
	if w.issuer == nil {
		return "", errors.New("infinimesh info token issuer is not configured")
	}
	for {
		w.mu.Lock()
		w.pruneExpiredLocked(tokenType)
		if token := w.popLocked(tokenType); token != "" {
			w.mu.Unlock()
			return token, nil
		}
		if call := w.issuing[tokenType]; call != nil {
			w.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-call.done:
				if call.err != nil {
					return "", call.err
				}
				continue
			}
		}
		call := &issueCall{done: make(chan struct{})}
		w.issuing[tokenType] = call
		w.mu.Unlock()

		issued, err := w.issuer.Issue(ctx, tokenType, w.batchSize)
		usable := w.usableTokens(tokenType, issued)
		if err == nil && len(usable) == 0 {
			err = errNoTokensIssued
		}

		w.mu.Lock()
		if err == nil {
			w.tokens[tokenType] = append(w.tokens[tokenType], usable...)
		}
		call.err = err
		delete(w.issuing, tokenType)
		close(call.done)
		w.mu.Unlock()
		if err != nil {
			return "", err
		}
	}
}

func (w *tokenWallet) DiscardAll(tokenType TokenType) {
	w.mu.Lock()
	defer w.mu.Unlock()
	tokens := w.tokens[tokenType]
	for i := range tokens {
		tokens[i] = Token{}
	}
	delete(w.tokens, tokenType)
}

func (w *tokenWallet) usableTokens(tokenType TokenType, tokens []Token) []Token {
	now := w.now()
	usable := make([]Token, 0, len(tokens))
	for _, token := range tokens {
		if token.Type != tokenType || strings.TrimSpace(token.Value) == "" || !token.ExpiresAt.After(now) {
			continue
		}
		token.Value = strings.TrimSpace(token.Value)
		usable = append(usable, token)
	}
	return usable
}

func (w *tokenWallet) pruneExpiredLocked(tokenType TokenType) {
	now := w.now()
	tokens := w.tokens[tokenType]
	kept := tokens[:0]
	for _, token := range tokens {
		if strings.TrimSpace(token.Value) == "" || !token.ExpiresAt.After(now) {
			continue
		}
		kept = append(kept, token)
	}
	for i := len(kept); i < len(tokens); i++ {
		tokens[i] = Token{}
	}
	w.tokens[tokenType] = kept
}

func (w *tokenWallet) popLocked(tokenType TokenType) string {
	tokens := w.tokens[tokenType]
	if len(tokens) == 0 {
		return ""
	}
	last := len(tokens) - 1
	value := tokens[last].Value
	tokens[last] = Token{}
	w.tokens[tokenType] = tokens[:last]
	return value
}
