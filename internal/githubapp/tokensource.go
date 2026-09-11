package githubapp

import (
	"context"
	"sync"
	"time"
)

// expiryMargin is how long before a token's stated expiry it stops being
// handed out. A token with seconds left is a clone that fails partway through
// for no reason a user could diagnose.
const expiryMargin = 5 * time.Minute

// TokenSource hands out installation tokens for repositories, minting on demand
// and caching until each is close to expiring.
//
// A nil TokenSource is valid and yields no credential: that is how a deployment
// with no GitHub App configured keeps cloning public repositories unchanged.
type TokenSource struct {
	client *Client
	now    func() time.Time

	mu     sync.Mutex
	tokens map[string]Token
}

type TokenSourceOption func(*TokenSource)

// WithTokenSourceClock replaces the time source so cache expiry can be tested
// without sleeping.
func WithTokenSourceClock(now func() time.Time) TokenSourceOption {
	return func(source *TokenSource) {
		if now != nil {
			source.now = now
		}
	}
}

func NewTokenSource(client *Client, options ...TokenSourceOption) *TokenSource {
	source := &TokenSource{
		client: client,
		now:    time.Now,
		tokens: make(map[string]Token),
	}
	for _, option := range options {
		option(source)
	}
	return source
}

// Token returns an installation token that can read owner/repo, or an empty
// string when no GitHub App is configured.
//
// The cache is keyed by repository rather than by installation because the
// minted token is itself repository-scoped -- one repo's token cannot serve
// another even when the same installation covers both.
func (source *TokenSource) Token(ctx context.Context, owner string, repo string) (string, error) {
	if source == nil || source.client == nil {
		return "", nil
	}

	key := owner + "/" + repo
	if token, ok := source.cached(key); ok {
		return token, nil
	}

	installationID, err := source.client.InstallationForRepo(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	token, err := source.client.MintToken(ctx, installationID, repo)
	if err != nil {
		return "", err
	}

	source.mu.Lock()
	source.tokens[key] = token
	source.mu.Unlock()
	return token.Value, nil
}

func (source *TokenSource) cached(key string) (string, bool) {
	source.mu.Lock()
	defer source.mu.Unlock()

	token, ok := source.tokens[key]
	if !ok {
		return "", false
	}
	if !source.now().Add(expiryMargin).Before(token.ExpiresAt) {
		delete(source.tokens, key)
		return "", false
	}
	return token.Value, true
}
