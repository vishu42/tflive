package authn

import (
	"context"
	"errors"
	"strings"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

func (v *OIDCVerifier) Verify(ctx context.Context, raw string) (VerifiedToken, error) {
	if len(raw) == 0 || len(raw) > maxTokenBytes || strings.Count(raw, ".") != 2 {
		return VerifiedToken{}, ErrInvalidToken
	}

	header, err := protectedHeader(raw)
	if err != nil {
		return VerifiedToken{}, ErrInvalidToken
	}
	algorithm, keyID, ok := allowedHeader(header)
	if !ok {
		return VerifiedToken{}, ErrInvalidToken
	}
	key, err := v.keyFor(ctx, keyID, algorithm)
	if err != nil {
		return VerifiedToken{}, err
	}
	payload, err := jws.Verify([]byte(raw), jws.WithKey(algorithm, key), jws.WithCompact())
	if err != nil {
		return VerifiedToken{}, ErrInvalidToken
	}
	return v.validatedToken(payload)
}

func protectedHeader(raw string) (jws.Headers, error) {
	message, err := jws.Parse([]byte(raw), jws.WithCompact())
	if err != nil {
		return nil, err
	}
	signatures := message.Signatures()
	if len(signatures) != 1 || signatures[0] == nil {
		return nil, errors.New("invalid compact JWS signature")
	}
	protected := signatures[0].ProtectedHeaders()
	if protected == nil {
		return nil, errors.New("ambiguous JWS headers")
	}
	return protected, nil
}

func allowedHeader(header jws.Headers) (jwa.SignatureAlgorithm, string, bool) {
	if header == nil {
		return jwa.EmptySignatureAlgorithm(), "", false
	}
	provided, ok := header.Algorithm()
	if !ok {
		return jwa.EmptySignatureAlgorithm(), "", false
	}
	keyID, ok := header.KeyID()
	if !ok || keyID == "" {
		return jwa.EmptySignatureAlgorithm(), "", false
	}

	algorithmName := provided.String()
	switch algorithmName {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA":
	default:
		return jwa.EmptySignatureAlgorithm(), "", false
	}
	algorithm, ok := jwa.LookupSignatureAlgorithm(algorithmName)
	if !ok {
		return jwa.EmptySignatureAlgorithm(), "", false
	}
	return algorithm, keyID, true
}

func (v *OIDCVerifier) validatedToken(payload []byte) (VerifiedToken, error) {
	token, err := jwt.Parse(
		payload,
		jwt.WithVerify(false),
		jwt.WithIssuer(v.discovery.Issuer),
		jwt.WithAudience(v.cfg.Audience),
		jwt.WithRequiredClaim(jwt.ExpirationKey),
		jwt.WithClock(jwt.ClockFunc(v.cfg.Clock)),
		jwt.WithAcceptableSkew(clockSkew),
	)
	if err != nil {
		return VerifiedToken{}, ErrInvalidToken
	}

	subject, ok := token.Subject()
	if !ok || subject == "" {
		return VerifiedToken{}, ErrInvalidToken
	}
	tokenType, ok := stringClaim(token, "typ")
	if !ok || tokenType != "Bearer" {
		return VerifiedToken{}, ErrInvalidToken
	}
	name, ok := stringClaim(token, "name")
	if !ok {
		return VerifiedToken{}, ErrInvalidToken
	}
	preferredUsername, ok := stringClaim(token, "preferred_username")
	if !ok {
		return VerifiedToken{}, ErrInvalidToken
	}
	email, ok := stringClaim(token, "email")
	if !ok {
		return VerifiedToken{}, ErrInvalidToken
	}
	roles, ok := realmRoles(token)
	if !ok {
		return VerifiedToken{}, ErrInvalidToken
	}

	return VerifiedToken{
		Subject:           subject,
		Name:              name,
		PreferredUsername: preferredUsername,
		Email:             email,
		RealmRoles:        roles,
	}, nil
}

func stringClaim(token jwt.Token, name string) (string, bool) {
	if !token.Has(name) {
		return "", true
	}
	var value string
	if err := token.Get(name, &value); err != nil {
		return "", false
	}
	return value, true
}

func realmRoles(token jwt.Token) ([]string, bool) {
	if !token.Has("realm_access") {
		return nil, true
	}

	var value any
	if err := token.Get("realm_access", &value); err != nil {
		return nil, false
	}
	realmAccess, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	rolesValue, present := realmAccess["roles"]
	if !present {
		return nil, true
	}
	values, ok := rolesValue.([]any)
	if !ok {
		return nil, false
	}
	roles := make([]string, len(values))
	for index, value := range values {
		role, ok := value.(string)
		if !ok {
			return nil, false
		}
		roles[index] = role
	}
	return roles, true
}
