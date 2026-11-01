package gitinternal

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// TokenName names the internal call's token among the internal tokens a migration writes into the database.
const TokenName = "git-server"

// Token is the internal call's token, which both ends read when they first need it and then keep: a workload may
// start before the migrate Job has written it. Rotating it restarts both ends.
type Token struct {
	read  func(ctx context.Context, name string) (string, error)
	mu    sync.Mutex
	value string
}

// NewToken returns the token that read reads by TokenName, such as from the database.
func NewToken(read func(ctx context.Context, name string) (string, error)) *Token {
	return &Token{read: read}
}

// Get returns the token, reading it the first time.
func (t *Token) Get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.value != "" {
		return t.value, nil
	}
	value, err := t.read(ctx, TokenName)
	if err != nil {
		return "", fmt.Errorf("reading the internal call's token: %w", err)
	}
	if value == "" {
		return "", errors.New("reading the internal call's token: it is empty")
	}
	t.value = value
	return value, nil
}
