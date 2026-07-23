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
	backend := newFakeProvisionBackend(cfg)

	for run := 1; run <= 2; run++ {
		result, err := provisionWithBackend(context.Background(), cfg, backend)
		if err != nil {
			t.Fatalf("provisionWithBackend() run %d error = %v", run, err)
		}
		if result.Realm != "tflive" || result.WebClientID != "tflive-web" || result.APIClientID != "tflive-api" {
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
	if got, want := backend.createdRoles, 2; got != want {
		t.Fatalf("created roles = %d, want %d", got, want)
	}
	if got, want := backend.createdClients, 3; got != want {
		t.Fatalf("created clients = %d, want %d", got, want)
	}
	if got, want := backend.createdScopes, 1; got != want {
		t.Fatalf("created scopes = %d, want %d", got, want)
	}
	if got, want := backend.createdMappers, 1; got != want {
		t.Fatalf("created mappers = %d, want %d", got, want)
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
	if got, want := backend.roles["platform-admin"].Description, platformAdminDescription; got != want {
		t.Fatalf("platform-admin description = %q, want %q", got, want)
	}
	if got, want := backend.roles["stack-creator"].Description, stackCreatorDescription; got != want {
		t.Fatalf("stack-creator description = %q, want %q", got, want)
	}

	web := backend.clients[cfg.WebClientID]
	if !web.PublicClient || !web.StandardFlowEnabled || web.BearerOnly || web.ImplicitFlowEnabled || web.DirectAccessGrantsEnabled || web.ServiceAccountsEnabled {
		t.Fatalf("web client flow spec = %#v", web)
	}
	if web.Attributes["pkce.code.challenge.method"] != "S256" {
		t.Fatalf("web PKCE method = %q", web.Attributes["pkce.code.challenge.method"])
	}
	if !equalStrings(web.RedirectURIs, cfg.RedirectURIs) || !equalStrings(web.WebOrigins, cfg.WebOrigins) {
		t.Fatalf("web allowlists = redirects %#v origins %#v", web.RedirectURIs, web.WebOrigins)
	}

	api := backend.clients[cfg.APIClientID]
	if !api.BearerOnly || api.PublicClient || api.StandardFlowEnabled || api.DirectAccessGrantsEnabled || api.ServiceAccountsEnabled {
		t.Fatalf("API client flow spec = %#v", api)
	}

	mapper := backend.mappers[audienceScopeName+"/"+audienceMapperName]
	if mapper.ProtocolMapper != "oidc-audience-mapper" || mapper.Config["included.client.audience"] != cfg.APIClientID || mapper.Config["access.token.claim"] != "true" {
		t.Fatalf("audience mapper = %#v", mapper)
	}
	if !backend.defaultScopes[cfg.WebClientID][audienceScopeName] || !backend.defaultScopes[cfg.WebClientID]["roles"] {
		t.Fatalf("web default scopes = %#v", backend.defaultScopes[cfg.WebClientID])
	}

	if !backend.realmRoleMappings[cfg.PlatformAdminUsername]["platform-admin"] {
		t.Fatalf("platform user realm roles = %#v", backend.realmRoleMappings[cfg.PlatformAdminUsername])
	}
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

func TestProvisionWithBackendRejectsInvalidEffectiveToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		token   ExampleAccessToken
		wantErr string
	}{
		{name: "missing API audience", token: ExampleAccessToken{Audience: []string{"account"}, RealmRoles: []string{"platform-admin"}}, wantErr: "missing audience tflive-api"},
		{name: "missing platform role", token: ExampleAccessToken{Audience: []string{"tflive-api"}, RealmRoles: []string{"default-roles-tflive"}}, wantErr: "missing realm role platform-admin"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := configForServer(t, "http://keycloak.example.test")
			backend := newFakeProvisionBackend(cfg)
			backend.exampleToken = tt.token

			_, err := provisionWithBackend(context.Background(), cfg, backend)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("provisionWithBackend() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestProvisionWithBackendRequiresKeycloakBuiltins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*fakeProvisionBackend)
		wantErr string
	}{
		{name: "roles client scope", mutate: func(b *fakeProvisionBackend) { delete(b.scopes, "roles") }, wantErr: "required client scope roles"},
		{name: "realm management client", mutate: func(b *fakeProvisionBackend) { delete(b.clients, "realm-management") }, wantErr: "required client realm-management"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := configForServer(t, "http://keycloak.example.test")
			backend := newFakeProvisionBackend(cfg)
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
	scopes              map[string]ClientScopeSpec
	mappers             map[string]ProtocolMapperSpec
	users               map[string]UserSpec
	defaultScopes       map[string]map[string]bool
	realmRoleMappings   map[string]map[string]bool
	clientRoleMappings  map[string]map[string]map[string]bool
	clientScopeMappings map[string]map[string]map[string]bool
	exampleToken        ExampleAccessToken

	createdRealms  int
	createdRoles   int
	createdClients int
	createdScopes  int
	createdMappers int
	createdUsers   int
}

func newFakeProvisionBackend(cfg Config) *fakeProvisionBackend {
	return &fakeProvisionBackend{
		realms: map[string]RealmSpec{}, roles: map[string]RoleSpec{},
		clients: map[string]ClientSpec{"realm-management": {ClientID: "realm-management"}},
		scopes:  map[string]ClientScopeSpec{"roles": {Name: "roles", Protocol: "openid-connect"}},
		mappers: map[string]ProtocolMapperSpec{}, users: map[string]UserSpec{},
		defaultScopes: map[string]map[string]bool{}, realmRoleMappings: map[string]map[string]bool{},
		clientRoleMappings:  map[string]map[string]map[string]bool{},
		clientScopeMappings: map[string]map[string]map[string]bool{},
		exampleToken:        ExampleAccessToken{Audience: []string{cfg.APIClientID}, RealmRoles: []string{"platform-admin"}},
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

func (f *fakeProvisionBackend) EnsureClientScope(_ context.Context, _ string, spec ClientScopeSpec) (ResourceRef, error) {
	if _, ok := f.scopes[spec.Name]; !ok {
		f.createdScopes++
	}
	f.scopes[spec.Name] = spec
	return ResourceRef{ID: "scope-" + spec.Name, Name: spec.Name}, nil
}

func (f *fakeProvisionBackend) LookupClientScope(_ context.Context, _ string, name string) (ResourceRef, error) {
	if _, ok := f.scopes[name]; !ok {
		return ResourceRef{}, fmt.Errorf("required client scope %s was not found", name)
	}
	return ResourceRef{ID: "scope-" + name, Name: name}, nil
}

func (f *fakeProvisionBackend) EnsureProtocolMapper(_ context.Context, _ string, scope ResourceRef, spec ProtocolMapperSpec) error {
	key := scope.Name + "/" + spec.Name
	if _, ok := f.mappers[key]; !ok {
		f.createdMappers++
	}
	f.mappers[key] = spec
	return nil
}

func (f *fakeProvisionBackend) EnsureDefaultClientScope(_ context.Context, _ string, client, scope ResourceRef) error {
	if f.defaultScopes[client.Name] == nil {
		f.defaultScopes[client.Name] = map[string]bool{}
	}
	f.defaultScopes[client.Name][scope.Name] = true
	return nil
}

func (f *fakeProvisionBackend) EnsureUser(_ context.Context, _ string, spec UserSpec) (ResourceRef, error) {
	if _, ok := f.users[spec.Username]; !ok {
		f.createdUsers++
	}
	f.users[spec.Username] = spec
	return ResourceRef{ID: "user-" + spec.Username, Name: spec.Username}, nil
}

func (f *fakeProvisionBackend) EnsureRealmRoleMapping(_ context.Context, _ string, user ResourceRef, roles []ResourceRef) error {
	if f.realmRoleMappings[user.Name] == nil {
		f.realmRoleMappings[user.Name] = map[string]bool{}
	}
	for _, role := range roles {
		f.realmRoleMappings[user.Name][role.Name] = true
	}
	return nil
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

func (f *fakeProvisionBackend) ExampleAccessToken(_ context.Context, _ string, _, _ ResourceRef) (ExampleAccessToken, error) {
	return f.exampleToken, nil
}
