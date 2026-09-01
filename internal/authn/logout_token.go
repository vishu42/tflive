package authn

import (
	"context"
	"errors"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwt"
)

// backchannelLogoutEvent is the event identifier OIDC Back-Channel Logout 1.0
// §2.4 requires in the events claim.
const backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

var ErrInvalidLogoutToken = errors.New("invalid logout token")

// LogoutToken is a verified back-channel logout notification. At least one of
// its two fields is set.
type LogoutToken struct {
	Subject   string
	SessionID string
}

// VerifyLogoutToken authenticates a back-channel logout notification from the
// IdP. It reuses the JWKS and issuer the ID-token verifier already maintains,
// so there is one place a provider's signing keys are trusted.
func (v *OIDCVerifier) VerifyLogoutToken(ctx context.Context, raw string) (LogoutToken, error) {
	// verifiedPayload is the shared signature check extracted in Step 3a. It
	// must NOT be v.Verify: that also enforces ID-token claims (exp, name,
	// preferred_username, azp), none of which a logout token carries, so every
	// valid logout token would be rejected — and swallowing that rejection to
	// work around it would swallow signature failures too, since both return
	// ErrInvalidToken.
	payload, err := v.verifiedPayload(ctx, raw)
	if err != nil {
		return LogoutToken{}, err
	}

	v.mu.RLock()
	issuer := v.discovery.Issuer
	v.mu.RUnlock()

	token, err := jwt.Parse(payload,
		// The signature is already verified above; this parses claims from the
		// verified payload.
		jwt.WithVerify(false),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(v.cfg.Audience),
		jwt.WithRequiredClaim("events"),
		jwt.WithRequiredClaim("iat"),
		jwt.WithClock(jwt.ClockFunc(v.cfg.Clock)),
		jwt.WithAcceptableSkew(clockSkew),
	)
	if err != nil {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	// §2.4: a logout token MUST NOT contain a nonce. Its presence means an ID
	// token is being replayed here, which would let anyone holding one revoke
	// another user's sessions.
	if token.Has("nonce") {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	// §2.4: iat is required, and a logout notification is only meaningful
	// while it is fresh. The lower bound catches a stale token; the upper
	// bound catches one dated into the future by more than ordinary clock
	// skew, using the same clockSkew tolerance jwt.WithAcceptableSkew already
	// applies to the rest of this parse.
	issuedAt, ok := token.IssuedAt()
	now := v.cfg.Clock()
	if !ok || now.Sub(issuedAt) > logoutTokenMaxAge || issuedAt.Sub(now) > clockSkew {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	var events map[string]any
	if err := token.Get("events", &events); err != nil {
		return LogoutToken{}, ErrInvalidLogoutToken
	}
	if _, ok := events[backchannelLogoutEvent]; !ok {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	subject, _ := token.Subject()
	sessionID, ok := optionalStringClaim(token, "sid")
	if !ok {
		return LogoutToken{}, ErrInvalidLogoutToken
	}
	// §2.4: at least one of sub and sid must be present, or there is nothing
	// to revoke.
	if subject == "" && sessionID == "" {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	return LogoutToken{Subject: subject, SessionID: sessionID}, nil
}

// logoutTokenMaxAge bounds replay of a captured logout token.
const logoutTokenMaxAge = 2 * time.Minute
