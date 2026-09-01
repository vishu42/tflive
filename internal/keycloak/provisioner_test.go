package keycloak

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestProvisionWithBackendIsRepeatableAndUsesApprovedDesiredState(t *testing.T) {
	t.Parallel()

	cfg := configForServer(t, "http://keycloak.example.test")
	backend := newFakeProvisionBackend()

	for run := 1; run <= 2; run++ {
		result, err := provisionWithBackend(context.Background(), cfg, backend)
		if err != nil {
			t.Fatalf("provisionWithBackend() run %d error = %v", run, err)
		}
		if result.Realm != "tflive" || result.APIClientID != "tflive-api" {
			t.Fatalf("result = %#v", result)
		}
	}

	last, err := provisionWithBackend(context.Background(), cfg, backend)
	if err != nil {
		t.Fatalf("provisionWithBackend() final run error = %v", err)
	}
	if last.Realm != "tflive" || last.APIClientID != "tflive-api" || last.PlatformAdminUsername != cfg.PlatformAdminUsername {
		t.Fatalf("final result = %#v", last)
	}

	if got, want := backend.createdRealms, 1; got != want {
		t.Fatalf("created realms = %d, want %d", got, want)
	}
	// Keycloak is identity-only: no global realm roles are provisioned, because
	// the platform tiers they carried are OpenFGA tuples now.
	if got, want := backend.createdRoles, 0; got != want {
		t.Fatalf("created roles = %d, want %d", got, want)
	}
	// No audience scope, no protocol mapper: this task's whole point is that
	// an ID token's aud is the client ID by construction, so the workaround
	// that forced an aud into an access token is gone. The backend has no way
	// to create either any more -- provisionBackend no longer has the methods.
	//
	// One client, not two: the directory reader service account is gone with
	// the Keycloak directory it existed to read.
	if got, want := backend.createdClients, 1; got != want {
		t.Fatalf("created clients = %d, want %d", got, want)
	}
	// One user: the platform administrator. The directory reader's service
	// account went with its client.
	if got, want := backend.createdUsers, 1; got != want {
		t.Fatalf("created users = %d, want %d", got, want)
	}
	platformUser := backend.users[cfg.PlatformAdminUsername]
	if platformUser.Email != cfg.PlatformAdminEmail || platformUser.FirstName != cfg.PlatformAdminFirstName || platformUser.LastName != cfg.PlatformAdminLastName || !platformUser.EmailVerified {
		t.Fatalf("platform user profile = %#v", platformUser)
	}

	realm := backend.realms[cfg.Realm]
	if !realm.Enabled || realm.AccessTokenLifespan != 300 || realm.SSLRequired != "external" || realm.RegistrationAllowed {
		t.Fatalf("realm spec = %#v", realm)
	}
	for _, role := range []string{"platform-admin", "stack-creator"} {
		if _, provisioned := backend.roles[role]; provisioned {
			t.Fatalf("realm role %s was provisioned; authorization is OpenFGA's now", role)
		}
	}
	if len(backend.realmRoleMappings) != 0 {
		t.Fatalf("realm role mappings = %#v, want none", backend.realmRoleMappings)
	}

	api := backend.clients[cfg.APIClientID]
	if !api.StandardFlowEnabled || api.PublicClient || api.BearerOnly || api.ImplicitFlowEnabled || api.DirectAccessGrantsEnabled || api.ServiceAccountsEnabled {
		t.Fatalf("API client flow spec = %#v", api)
	}

	// The seeded administrator carries no global realm role at all. Its
	// platform tier is a tuple #212 writes; the realm-management roles below
	// are different -- they are Keycloak's own, needed to administer the realm.
	wantAdminRoles := []string{"manage-users", "query-users", "view-realm", "view-users"}
	for _, role := range wantAdminRoles {
		if !backend.clientRoleMappings[cfg.PlatformAdminUsername]["realm-management"][role] {
			t.Fatalf("platform user realm-management roles = %#v, missing %q", backend.clientRoleMappings[cfg.PlatformAdminUsername]["realm-management"], role)
		}
	}
	if backend.clientRoleMappings[cfg.PlatformAdminUsername]["realm-management"]["realm-admin"] {
		t.Fatal("platform user must not receive realm-admin")
	}

	// No directory reader client is provisioned any more. Display names come
	// from the local identity projection, so nothing needs query-users or
	// view-users on the realm -- which is the whole point: that was the
	// elevated permission a customer's security team would have to approve.
	for clientID := range backend.clients {
		if strings.Contains(clientID, "directory-reader") {
			t.Fatalf("provisioned a directory reader client %q, want none", clientID)
		}
	}
	if len(backend.clientScopeMappings) != 0 {
		t.Fatalf("client scope mappings = %#v, want none", backend.clientScopeMappings)
	}
}

func TestProvisionCreatesOneConfidentialClient(t *testing.T) {
	t.Parallel()

	cfg := configForServer(t, "http://keycloak.example.test")
	backend := newFakeProvisionBackend()

	if _, err := provisionWithBackend(context.Background(), cfg, backend); err != nil {
		t.Fatalf("provisionWithBackend returned error: %v", err)
	}

	if _, exists := backend.clients["tflive-web"]; exists {
		t.Fatal("the public browser client still exists")
	}

	api := backend.clients[cfg.APIClientID]
	if api.PublicClient || api.BearerOnly {
		t.Fatalf("api client = %#v, want confidential and not bearer-only", api)
	}
	if !api.StandardFlowEnabled {
		t.Fatal("api client cannot run the authorization-code flow")
	}
	if api.Secret != cfg.APIClientSecret {
		t.Fatalf("api client secret = %q", api.Secret)
	}
	if len(api.RedirectURIs) != 1 || api.RedirectURIs[0] != cfg.CallbackURI {
		t.Fatalf("redirect URIs = %v, want [%s]", api.RedirectURIs, cfg.CallbackURI)
	}
	if len(api.WebOrigins) != 0 {
		t.Fatalf("web origins = %v, want none — the browser never calls the API cross-origin", api.WebOrigins)
	}
	if api.Attributes["post.logout.redirect.uris"] != cfg.PostLogoutRedirectURI {
		t.Fatalf("post-logout redirect = %q", api.Attributes["post.logout.redirect.uris"])
	}
	if api.Attributes["pkce.code.challenge.method"] != "S256" {
		t.Fatal("PKCE is not enforced on the confidential client")
	}
}

func TestProvisionRegistersBackchannelLogout(t *testing.T) {
	t.Parallel()

	cfg := configForServer(t, "http://keycloak.example.test")
	backend := newFakeProvisionBackend()

	if _, err := provisionWithBackend(context.Background(), cfg, backend); err != nil {
		t.Fatalf("provisionWithBackend returned error: %v", err)
	}

	api := backend.clients[cfg.APIClientID]
	if api.Attributes["backchannel.logout.url"] != cfg.BackchannelLogoutURI {
		t.Fatalf("backchannel.logout.url = %q, want %q", api.Attributes["backchannel.logout.url"], cfg.BackchannelLogoutURI)
	}
	if api.Attributes["backchannel.logout.session.required"] != "true" {
		t.Fatalf("backchannel.logout.session.required = %q, want true — without it Keycloak omits sid, and a signed-out device would sign out every session the user has", api.Attributes["backchannel.logout.session.required"])
	}
}

func TestProvisionWithBackendRequiresKeycloakBuiltins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*fakeProvisionBackend)
		wantErr string
	}{
		{name: "realm management client", mutate: func(b *fakeProvisionBackend) { delete(b.clients, "realm-management") }, wantErr: "required client realm-management"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := configForServer(t, "http://keycloak.example.test")
			backend := newFakeProvisionBackend()
			tt.mutate(backend)

			_, err := provisionWithBackend(context.Background(), cfg, backend)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("provisionWithBackend() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

type fakeProvisionBackend struct {
	realms              map[string]RealmSpec
	roles               map[string]RoleSpec
	clients             map[string]ClientSpec
	users               map[string]UserSpec
	realmRoleMappings   map[string]map[string]bool
	clientRoleMappings  map[string]map[string]map[string]bool
	clientScopeMappings map[string]map[string]map[string]bool

	createdRealms  int
	createdRoles   int
	createdClients int
	createdUsers   int
}

func newFakeProvisionBackend() *fakeProvisionBackend {
	return &fakeProvisionBackend{
		realms: map[string]RealmSpec{}, roles: map[string]RoleSpec{},
		clients: map[string]ClientSpec{"realm-management": {ClientID: "realm-management"}},
		users:   map[string]UserSpec{}, realmRoleMappings: map[string]map[string]bool{},
		clientRoleMappings:  map[string]map[string]map[string]bool{},
		clientScopeMappings: map[string]map[string]map[string]bool{},
	}
}

func (f *fakeProvisionBackend) EnsureRealm(_ context.Context, spec RealmSpec) error {
	if _, ok := f.realms[spec.Name]; !ok {
		f.createdRealms++
	}
	f.realms[spec.Name] = spec
	return nil
}

func (f *fakeProvisionBackend) EnsureRole(_ context.Context, _ string, spec RoleSpec) (ResourceRef, error) {
	if _, ok := f.roles[spec.Name]; !ok {
		f.createdRoles++
	}
	f.roles[spec.Name] = spec
	return ResourceRef{ID: "role-" + spec.Name, Name: spec.Name}, nil
}

func (f *fakeProvisionBackend) EnsureClient(_ context.Context, _ string, spec ClientSpec) (ResourceRef, error) {
	if _, ok := f.clients[spec.ClientID]; !ok {
		f.createdClients++
	}
	f.clients[spec.ClientID] = spec
	return ResourceRef{ID: "client-" + spec.ClientID, Name: spec.ClientID}, nil
}

func (f *fakeProvisionBackend) LookupClient(_ context.Context, _ string, clientID string) (ResourceRef, error) {
	if _, ok := f.clients[clientID]; !ok {
		return ResourceRef{}, fmt.Errorf("required client %s was not found", clientID)
	}
	return ResourceRef{ID: "client-" + clientID, Name: clientID}, nil
}

func (f *fakeProvisionBackend) EnsureUser(_ context.Context, _ string, spec UserSpec) (ResourceRef, error) {
	if _, ok := f.users[spec.Username]; !ok {
		f.createdUsers++
	}
	f.users[spec.Username] = spec
	return ResourceRef{ID: "user-" + spec.Username, Name: spec.Username}, nil
}

func (f *fakeProvisionBackend) ClientRole(_ context.Context, _ string, _ ResourceRef, roleName string) (ResourceRef, error) {
	return ResourceRef{ID: "client-role-" + roleName, Name: roleName}, nil
}

func (f *fakeProvisionBackend) EnsureClientRoleMapping(_ context.Context, _ string, user, client ResourceRef, roles []ResourceRef) error {
	if f.clientRoleMappings[user.Name] == nil {
		f.clientRoleMappings[user.Name] = map[string]map[string]bool{}
	}
	if f.clientRoleMappings[user.Name][client.Name] == nil {
		f.clientRoleMappings[user.Name][client.Name] = map[string]bool{}
	}
	for _, role := range roles {
		f.clientRoleMappings[user.Name][client.Name][role.Name] = true
	}
	return nil
}

func (f *fakeProvisionBackend) EnsureClientScopeMapping(_ context.Context, _ string, client, roleClient ResourceRef, roles []ResourceRef) error {
	if f.clientScopeMappings[client.Name] == nil {
		f.clientScopeMappings[client.Name] = map[string]map[string]bool{}
	}
	if f.clientScopeMappings[client.Name][roleClient.Name] == nil {
		f.clientScopeMappings[client.Name][roleClient.Name] = map[string]bool{}
	}
	for _, role := range roles {
		f.clientScopeMappings[client.Name][roleClient.Name][role.Name] = true
	}
	return nil
}
