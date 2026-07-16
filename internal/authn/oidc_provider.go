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
	"github.com/lestrrat-go/jwx/v3/jwk"
)

var errProviderResponseTooLarge = errors.New("provider response exceeds size limit")

type discoveryDocument struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

type keyCache struct {
	url   string
	cache *jwk.Cache
	set   jwk.CachedSet
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

	cache, err := jwk.NewCache(ctx, httprc.NewClient())
	if err != nil {
		return nil, ErrVerifierUnavailable
	}
	keepCache := false
	defer func() {
		if !keepCache {
			_ = cache.Shutdown(context.Background())
		}
	}()

	registrationCtx, cancelRegistration := context.WithCancel(ctx)
	defer cancelRegistration()
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

	keepCache = true
	return &OIDCVerifier{
		cfg:        cfg,
		discovery:  discovery,
		discovered: cfg.Clock(),
		keys: &keyCache{
			url:   jwksURL,
			cache: cache,
			set:   set,
		},
	}, nil
}

func (v *OIDCVerifier) Close(ctx context.Context) error {
	v.mu.Lock()
	keys := v.keys
	v.keys = nil
	v.mu.Unlock()

	if keys == nil || keys.cache == nil {
		return nil
	}
	return keys.cache.Shutdown(ctx)
}

func (v *OIDCVerifier) keyFor(_ context.Context, kid, _ string) (jwk.Key, error) {
	v.mu.RLock()
	keys := v.keys
	v.mu.RUnlock()
	if keys == nil || keys.set == nil || kid == "" {
		return nil, ErrVerifierUnavailable
	}

	for index := 0; index < keys.set.Len(); index++ {
		key, ok := keys.set.Key(index)
		if !ok {
			return nil, ErrVerifierUnavailable
		}
		keyID, ok := key.KeyID()
		if ok && keyID == kid {
			return key, nil
		}
	}
	return nil, ErrInvalidToken
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
