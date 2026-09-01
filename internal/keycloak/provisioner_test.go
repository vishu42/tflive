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
	if got, want := backend.createdClients, 2; got != want {
		t.Fatalf("created clients = %d, want %d", got, want)
	}
	if got, want := backend.createdUsers, 2; got != want {
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
	// are different -- they are Keycloak's own, and the directory reads need
	// them.
	wantAdminRoles := []string{"manage-users", "query-users", "view-realm", "view-users"}
	for _, role := range wantAdminRoles {
		if !backend.clientRoleMappings[cfg.PlatformAdminUsername]["realm-management"][role] {
			t.Fatalf("platform user realm-management roles = %#v, missing %q", backend.clientRoleMappings[cfg.PlatformAdminUsername]["realm-management"], role)
		}
	}
	if backend.clientRoleMappings[cfg.PlatformAdminUsername]["realm-management"]["realm-admin"] {
		t.Fatal("platform user must not receive realm-admin")
	}

	directoryReader := backend.clients[directoryReaderClientID]
	if !directoryReader.ServiceAccountsEnabled || directoryReader.PublicClient || directoryReader.BearerOnly {
		t.Fatalf("directory reader client = %#v", directoryReader)
	}
	if got, want := last.DirectoryReaderClientID, directoryReaderClientID; got != want {
		t.Fatalf("result.DirectoryReaderClientID = %q, want %q", got, want)
	}
	if got, want := last.DirectoryReaderClientSecret, cfg.DirectoryReaderClientSecret; got != want {
		t.Fatalf("result.DirectoryReaderClientSecret = %q, want %q", got, want)
	}
	saUser := "service-account-" + directoryReaderClientID
	if !backend.clientRoleMappings[saUser]["realm-management"]["query-users"] {
		t.Fatalf("directory reader realm-management roles = %#v", backend.clientRoleMappings[saUser]["realm-management"])
	}
	if !backend.clientRoleMappings[saUser]["realm-management"]["view-users"] {
		t.Fatalf("directory reader missing view-users role")
	}
	if !backend.clientRoleMappings[saUser]["realm-management"]["view-realm"] {
		t.Fatalf("directory reader missing view-realm role")
	}
	if !backend.clientScopeMappings[directoryReaderClientID]["realm-management"]["query-users"] {
		t.Fatalf("directory reader client scope roles = %#v", backend.clientScopeMappings[directoryReaderClientID]["realm-management"])
	}
	if !backend.clientScopeMappings[directoryReaderClientID]["realm-management"]["view-users"] {
		t.Fatalf("directory reader client scope roles = %#v", backend.clientScopeMappings[directoryReaderClientID]["realm-management"])
	}
	if !backend.clientScopeMappings[directoryReaderClientID]["realm-management"]["view-realm"] {
		t.Fatalf("directory reader client scope roles = %#v", backend.clientScopeMappings[directoryReaderClientID]["realm-management"])
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
