package authn

// Endpoints are the provider URLs the authorization-code flow needs, read from
// the same discovery document the verifier validates tokens against. Serving
// them from the verifier means one fetch, one TTL, and no possibility of the
// flow and the verifier disagreeing about which provider they are talking to.
type Endpoints struct {
	Authorization string
	Token         string
	// EndSession is empty when the provider does not advertise one.
	EndSession string
}

// Endpoints returns the currently discovered provider endpoints.
func (v *OIDCVerifier) Endpoints() Endpoints {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return Endpoints{
		Authorization: v.discovery.AuthorizationEndpoint,
		Token:         v.discovery.TokenEndpoint,
		EndSession:    v.discovery.EndSessionEndpoint,
	}
}
