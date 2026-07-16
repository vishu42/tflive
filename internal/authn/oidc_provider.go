package authn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/httprc/v3/errsink"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
)

var (
	errProviderResponseTooLarge = errors.New("provider response exceeds size limit")
	errRefreshCooldown          = errors.New("oidc refresh cooldown")
	errJWKSCacheExpired         = errors.New("oidc JWKS cache expired")
)

type discoveryDocument struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

type keyCache struct {
	url        string
	cache      *jwk.Cache
	set        jwk.CachedSet
	freshUntil time.Time
}

type OIDCVerifier struct {
	cfg         OIDCVerifierConfig
	mu          sync.RWMutex
	discovery   discoveryDocument
	discovered  time.Time
	keys        *keyCache
	refreshMu   sync.Mutex
	refreshedAt time.Time
}

func NewOIDCVerifier(ctx context.Context, cfg OIDCVerifierConfig) (*OIDCVerifier, error) {
	cfg = cfg.withDefaults()
	if !validOIDCVerifierConfig(cfg) {
		return nil, ErrVerifierUnavailable
	}

	providerClient := cfg.HTTPClient
	hardenedClient := hardenedProviderClient(providerClient, nil)
	cfg.HTTPClient = hardenedClient
	discovery, err := fetchDiscovery(ctx, cfg.IssuerURL, hardenedClient)
	if err != nil {
		return nil, ErrVerifierUnavailable
	}
	if discovery.Issuer != cfg.IssuerURL.String() {
		return nil, ErrVerifierUnavailable
	}

	jwksURL, err := validJWKSURL(discovery.JWKSURI)
	if err != nil {
		return nil, ErrVerifierUnavailable
	}

	registrationCtx, cancelRegistration := context.WithCancel(ctx)
	defer cancelRegistration()
	cache, err := jwk.NewCache(ctx, httprc.NewClient(
		httprc.WithErrorSink(errsink.NewFunc(func(context.Context, error) {
			cancelRegistration()
		})),
	))
	if err != nil {
		return nil, ErrVerifierUnavailable
	}
	keepCache := false
	defer func() {
		if !keepCache {
			_ = cache.Shutdown(context.Background())
		}
	}()

	cacheClient := hardenedProviderClient(providerClient, cancelRegistration)
	if err := cache.Register(
		registrationCtx,
		jwksURL,
		jwk.WithHTTPClient(cacheClient),
		jwk.WithMinInterval(cfg.JWKSMinRefreshInterval),
		jwk.WithMaxInterval(cfg.JWKSMaxRefreshInterval),
	); err != nil {
		return nil, ErrVerifierUnavailable
	}

	set, err := cache.CachedSet(jwksURL)
	if err != nil || hasDuplicateKeyIDs(set) {
		return nil, ErrVerifierUnavailable
	}
	freshUntil, err := jwksFreshUntil(cfg.Clock(), cache, jwksURL)
	if err != nil {
		return nil, ErrVerifierUnavailable
	}

	keepCache = true
	return &OIDCVerifier{
		cfg:        cfg,
		discovery:  discovery,
		discovered: cfg.Clock(),
		keys: &keyCache{
			url:        jwksURL,
			cache:      cache,
			set:        set,
			freshUntil: freshUntil,
		},
	}, nil
}

func (v *OIDCVerifier) Close(ctx context.Context) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()

	v.mu.Lock()
	keys := v.keys
	v.keys = nil
	v.mu.Unlock()

	if keys == nil || keys.cache == nil {
		return nil
	}
	return keys.cache.Shutdown(ctx)
}

func (v *OIDCVerifier) keyFor(ctx context.Context, kid string, algorithm jwa.SignatureAlgorithm) (any, error) {
	publicKey, found, err := v.cachedKeyFor(kid, algorithm)
	if err != nil && !errors.Is(err, errJWKSCacheExpired) {
		return nil, err
	}
	if found {
		return publicKey, nil
	}

	refreshErr := v.refreshKeys(ctx, true)
	publicKey, found, err = v.cachedKeyFor(kid, algorithm)
	if err != nil {
		if errors.Is(err, errJWKSCacheExpired) {
			return nil, ErrVerifierUnavailable
		}
		return nil, err
	}
	if found {
		return publicKey, nil
	}
	if errors.Is(refreshErr, errRefreshCooldown) {
		return nil, ErrVerifierUnavailable
	}
	if refreshErr != nil {
		return nil, ErrVerifierUnavailable
	}
	return nil, ErrInvalidToken
}

func (v *OIDCVerifier) cachedKeyFor(kid string, algorithm jwa.SignatureAlgorithm) (any, bool, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	keys := v.keys
	if keys == nil || keys.set == nil {
		return nil, false, ErrVerifierUnavailable
	}
	if !keys.freshUntil.After(v.cfg.Clock()) {
		return nil, false, errJWKSCacheExpired
	}
	if kid == "" {
		return nil, false, ErrInvalidToken
	}

	setLength := keys.set.Len()
	if setLength < 0 {
		return nil, false, ErrVerifierUnavailable
	}
	keyIDs := make(map[string]struct{}, setLength)
	var selected jwk.Key
	for index := 0; index < setLength; index++ {
		key, ok := keys.set.Key(index)
		if !ok {
			return nil, false, ErrVerifierUnavailable
		}
		keyID, ok := key.KeyID()
		if !ok || keyID == "" {
			continue
		}
		if _, duplicate := keyIDs[keyID]; duplicate {
			return nil, false, ErrInvalidToken
		}
		keyIDs[keyID] = struct{}{}
		if keyID == kid {
			selected = key
		}
	}
	if selected == nil {
		return nil, false, nil
	}
	if usage, present := selected.KeyUsage(); present && usage != "sig" {
		return nil, false, ErrInvalidToken
	}
	if configuredAlgorithm, present := selected.Algorithm(); present && configuredAlgorithm.String() != algorithm.String() {
		return nil, false, ErrInvalidToken
	}

	var publicKey any
	if err := jwk.Export(selected, &publicKey); err != nil || publicKey == nil {
		return nil, false, ErrInvalidToken
	}
	return publicKey, true, nil
}

func (v *OIDCVerifier) refreshKeys(ctx context.Context, force bool) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()

	if force {
		now := v.cfg.Clock()
		if !v.refreshedAt.IsZero() && now.Sub(v.refreshedAt) < v.cfg.RefreshCooldown {
			return errRefreshCooldown
		}
		v.refreshedAt = now
	}

	if err := v.refreshDiscoveryIfDue(ctx); err != nil {
		return err
	}

	v.mu.RLock()
	keys := v.keys
	v.mu.RUnlock()
	if keys == nil || keys.cache == nil || keys.set == nil {
		return errors.New("OIDC key cache is unavailable")
	}

	set, err := keys.cache.Refresh(ctx, keys.url)
	if err != nil {
		return err
	}
	if hasDuplicateKeyIDs(set) {
		return errors.New("OIDC JWKS contains duplicate key IDs")
	}
	freshUntil, err := jwksFreshUntil(v.cfg.Clock(), keys.cache, keys.url)
	if err != nil {
		return err
	}
	v.mu.Lock()
	if v.keys == keys {
		keys.freshUntil = freshUntil
	}
	v.mu.Unlock()
	return nil
}

func (v *OIDCVerifier) refreshDiscoveryIfDue(ctx context.Context) error {
	now := v.cfg.Clock()
	v.mu.RLock()
	discovered := v.discovered
	v.mu.RUnlock()
	if now.Sub(discovered) < v.cfg.DiscoveryTTL {
		return nil
	}

	discovery, err := fetchDiscovery(ctx, v.cfg.IssuerURL, v.cfg.HTTPClient)
	if err != nil {
		return err
	}
	if discovery.Issuer != v.cfg.IssuerURL.String() {
		return errors.New("OIDC discovery issuer mismatch")
	}
	jwksURL, err := validJWKSURL(discovery.JWKSURI)
	if err != nil {
		return err
	}

	v.mu.RLock()
	current := v.keys
	v.mu.RUnlock()
	if current == nil {
		return errors.New("OIDC key cache is unavailable")
	}
	if current.url != jwksURL {
		if err := v.replaceKeyCache(ctx, jwksURL); err != nil {
			return err
		}
	}

	v.mu.Lock()
	v.discovery = discovery
	v.discovered = now
	v.mu.Unlock()
	return nil
}

func (v *OIDCVerifier) replaceKeyCache(ctx context.Context, jwksURI string) error {
	registrationCtx, cancelRegistration := context.WithCancel(ctx)
	defer cancelRegistration()

	cache, err := jwk.NewCache(ctx, httprc.NewClient(
		httprc.WithErrorSink(errsink.NewFunc(func(context.Context, error) {
			cancelRegistration()
		})),
	))
	if err != nil {
		return err
	}
	keepCache := false
	defer func() {
		if !keepCache {
			_ = cache.Shutdown(context.Background())
		}
	}()

	cacheClient := hardenedProviderClient(v.cfg.HTTPClient, cancelRegistration)
	if err := cache.Register(
		registrationCtx,
		jwksURI,
		jwk.WithHTTPClient(cacheClient),
		jwk.WithMinInterval(v.cfg.JWKSMinRefreshInterval),
		jwk.WithMaxInterval(v.cfg.JWKSMaxRefreshInterval),
	); err != nil {
		return err
	}
	set, err := cache.CachedSet(jwksURI)
	if err != nil {
		return err
	}
	if hasDuplicateKeyIDs(set) {
		return errors.New("OIDC JWKS contains duplicate key IDs")
	}
	freshUntil, err := jwksFreshUntil(v.cfg.Clock(), cache, jwksURI)
	if err != nil {
		return err
	}

	newKeys := &keyCache{url: jwksURI, cache: cache, set: set, freshUntil: freshUntil}
	v.mu.Lock()
	oldKeys := v.keys
	v.keys = newKeys
	v.mu.Unlock()
	keepCache = true

	if oldKeys != nil && oldKeys.cache != nil {
		return oldKeys.cache.Shutdown(context.Background())
	}
	return nil
}

func jwksFreshUntil(now time.Time, cache *jwk.Cache, jwksURI string) (time.Time, error) {
	resource, err := cache.LookupResource(context.Background(), jwksURI)
	if err != nil {
		return time.Time{}, err
	}
	lifetime := resource.Next().Sub(time.Now())
	if lifetime < defaultJWKSMinRefreshInterval {
		lifetime = defaultJWKSMinRefreshInterval
	}
	if lifetime > defaultJWKSMaxRefreshInterval {
		lifetime = defaultJWKSMaxRefreshInterval
	}
	return now.Add(lifetime), nil
}

func validOIDCVerifierConfig(cfg OIDCVerifierConfig) bool {
	if cfg.IssuerURL == nil || cfg.Audience == "" ||
		cfg.DiscoveryTTL <= 0 || cfg.JWKSMinRefreshInterval <= 0 ||
		cfg.JWKSMaxRefreshInterval <= 0 || cfg.RefreshCooldown <= 0 ||
		cfg.JWKSMinRefreshInterval > cfg.JWKSMaxRefreshInterval {
		return false
	}
	return validProviderURL(cfg.IssuerURL)
}

func fetchDiscovery(ctx context.Context, issuerURL *url.URL, client *http.Client) (discoveryDocument, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		issuerURL.String()+"/.well-known/openid-configuration",
		nil,
	)
	if err != nil {
		return discoveryDocument{}, err
	}

	response, err := client.Do(request)
	if err != nil {
		return discoveryDocument{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return discoveryDocument{}, errors.New("unexpected discovery status")
	}

	var discovery discoveryDocument
	if err := json.NewDecoder(response.Body).Decode(&discovery); err != nil {
		return discoveryDocument{}, err
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return discoveryDocument{}, err
	}
	return discovery, nil
}

func validJWKSURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || !validProviderURL(parsed) || parsed.User != nil {
		return "", errors.New("invalid JWKS URL")
	}
	return parsed.String(), nil
}

func validProviderURL(value *url.URL) bool {
	return value != nil && value.IsAbs() && value.Hostname() != "" &&
		(value.Scheme == "http" || value.Scheme == "https")
}

func hasDuplicateKeyIDs(set jwk.Set) bool {
	keyIDs := make(map[string]struct{}, set.Len())
	for index := 0; index < set.Len(); index++ {
		key, ok := set.Key(index)
		if !ok {
			return true
		}
		keyID, ok := key.KeyID()
		if !ok || keyID == "" {
			continue
		}
		if _, exists := keyIDs[keyID]; exists {
			return true
		}
		keyIDs[keyID] = struct{}{}
	}
	return false
}

func hardenedProviderClient(client *http.Client, onFailure func()) *http.Client {
	hardened := *client
	if hardened.Timeout == 0 {
		hardened.Timeout = defaultHTTPTimeout
	}
	hardened.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	transport := hardened.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	hardened.Transport = providerResponseLimitTransport{base: transport, onFailure: onFailure}
	return &hardened
}

type providerResponseLimitTransport struct {
	base      http.RoundTripper
	onFailure func()
}

func (t providerResponseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		t.fail()
		return response, err
	}
	if response.StatusCode != http.StatusOK {
		t.fail()
	}
	response.Body = &providerResponseBody{
		ReadCloser: response.Body,
		remaining:  maxProviderResponseBytes,
		onFailure:  t.onFailure,
	}
	return response, nil
}

func (t providerResponseLimitTransport) fail() {
	if t.onFailure != nil {
		t.onFailure()
	}
}

type providerResponseBody struct {
	io.ReadCloser
	remaining int64
	exceeded  bool
	onFailure func()
}

func (b *providerResponseBody) Read(buffer []byte) (int, error) {
	if b.exceeded {
		return 0, errProviderResponseTooLarge
	}
	if b.remaining == 0 {
		if len(buffer) == 0 {
			return 0, nil
		}
		read, err := b.ReadCloser.Read(buffer[:1])
		if read == 0 {
			return 0, err
		}
		b.exceeded = true
		if b.onFailure != nil {
			b.onFailure()
		}
		return 0, errProviderResponseTooLarge
	}
	if int64(len(buffer)) > b.remaining {
		buffer = buffer[:b.remaining]
	}

	read, err := b.ReadCloser.Read(buffer)
	b.remaining -= int64(read)
	return read, err
}
