package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Provision authenticates and reconciles the complete Keycloak desired state.
func Provision(ctx context.Context, cfg Config) (Result, error) {
	client := NewClient(cfg)
	if err := client.Authenticate(ctx); err != nil {
		return Result{}, err
	}
	return provisionWithBackend(ctx, cfg, client)
}

func (c *Client) EnsureRealm(ctx context.Context, spec RealmSpec) error {
	segments := []string{"admin", "realms", spec.Name}
	resource := map[string]any{}
	status, err := c.doJSONStatus(ctx, http.MethodGet, segments, nil, nil, []int{http.StatusOK, http.StatusNotFound}, &resource)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		create := applyRealmSpec(map[string]any{}, spec)
		if err := c.doJSON(ctx, http.MethodPost, []string{"admin", "realms"}, nil, create, []int{http.StatusCreated}, nil); err != nil {
			return err
		}
		resource = map[string]any{}
		if err := c.doJSON(ctx, http.MethodGet, segments, nil, nil, []int{http.StatusOK}, &resource); err != nil {
			return err
		}
	}
	resource = applyRealmSpec(resource, spec)
	return c.doJSON(ctx, http.MethodPut, segments, nil, resource, []int{http.StatusNoContent}, nil)
}

func (c *Client) EnsureRole(ctx context.Context, realm string, spec RoleSpec) (ResourceRef, error) {
	segments := []string{"admin", "realms", realm, "roles", spec.Name}
	resource := map[string]any{}
	status, err := c.doJSONStatus(ctx, http.MethodGet, segments, nil, nil, []int{http.StatusOK, http.StatusNotFound}, &resource)
	if err != nil {
		return ResourceRef{}, err
	}
	if status == http.StatusNotFound {
		create := applyRoleSpec(map[string]any{}, spec)
		if err := c.doJSON(ctx, http.MethodPost, []string{"admin", "realms", realm, "roles"}, nil, create, []int{http.StatusCreated}, nil); err != nil {
			return ResourceRef{}, err
		}
		resource = map[string]any{}
		if err := c.doJSON(ctx, http.MethodGet, segments, nil, nil, []int{http.StatusOK}, &resource); err != nil {
			return ResourceRef{}, err
		}
	}
	resource = applyRoleSpec(resource, spec)
	if err := c.doJSON(ctx, http.MethodPut, segments, nil, resource, []int{http.StatusNoContent}, nil); err != nil {
		return ResourceRef{}, err
	}
	return refFromResource(resource, spec.Name)
}

func (c *Client) EnsureClient(ctx context.Context, realm string, spec ClientSpec) (ResourceRef, error) {
	ref, found, err := c.findClient(ctx, realm, spec.ClientID)
	if err != nil {
		return ResourceRef{}, err
	}
	if !found {
		create := applyClientSpec(map[string]any{}, spec)
		if err := c.doJSON(ctx, http.MethodPost, []string{"admin", "realms", realm, "clients"}, nil, create, []int{http.StatusCreated}, nil); err != nil {
			return ResourceRef{}, err
		}
		ref, found, err = c.findClient(ctx, realm, spec.ClientID)
		if err != nil {
			return ResourceRef{}, err
		}
		if !found {
			return ResourceRef{}, fmt.Errorf("client %s was not visible after creation", spec.ClientID)
		}
	}

	resource := map[string]any{}
	segments := []string{"admin", "realms", realm, "clients", ref.ID}
	if err := c.doJSON(ctx, http.MethodGet, segments, nil, nil, []int{http.StatusOK}, &resource); err != nil {
		return ResourceRef{}, err
	}
	resource = applyClientSpec(resource, spec)
	if err := c.doJSON(ctx, http.MethodPut, segments, nil, resource, []int{http.StatusNoContent}, nil); err != nil {
		return ResourceRef{}, err
	}
	return ref, nil
}

func (c *Client) LookupClient(ctx context.Context, realm, clientID string) (ResourceRef, error) {
	ref, found, err := c.findClient(ctx, realm, clientID)
	if err != nil {
		return ResourceRef{}, err
	}
	if !found {
		return ResourceRef{}, fmt.Errorf("required client %s was not found", clientID)
	}
	return ref, nil
}

func (c *Client) findClient(ctx context.Context, realm, clientID string) (ResourceRef, bool, error) {
	var resources []map[string]any
	query := url.Values{"clientId": {clientID}}
	if err := c.doJSON(ctx, http.MethodGet, []string{"admin", "realms", realm, "clients"}, query, nil, []int{http.StatusOK}, &resources); err != nil {
		return ResourceRef{}, false, err
	}
	matches := filterResources(resources, "clientId", clientID)
	if len(matches) > 1 {
		return ResourceRef{}, false, fmt.Errorf("multiple clients with clientId %s", clientID)
	}
	if len(matches) == 0 {
		return ResourceRef{}, false, nil
	}
	ref, err := refFromResource(matches[0], clientID)
	return ref, true, err
}

func (c *Client) EnsureClientScope(ctx context.Context, realm string, spec ClientScopeSpec) (ResourceRef, error) {
	ref, found, err := c.findClientScope(ctx, realm, spec.Name)
	if err != nil {
		return ResourceRef{}, err
	}
	if !found {
		create := applyClientScopeSpec(map[string]any{}, spec)
		if err := c.doJSON(ctx, http.MethodPost, []string{"admin", "realms", realm, "client-scopes"}, nil, create, []int{http.StatusCreated}, nil); err != nil {
			return ResourceRef{}, err
		}
		ref, found, err = c.findClientScope(ctx, realm, spec.Name)
		if err != nil {
			return ResourceRef{}, err
		}
		if !found {
			return ResourceRef{}, fmt.Errorf("client scope %s was not visible after creation", spec.Name)
		}
	}

	resource := map[string]any{}
	segments := []string{"admin", "realms", realm, "client-scopes", ref.ID}
	if err := c.doJSON(ctx, http.MethodGet, segments, nil, nil, []int{http.StatusOK}, &resource); err != nil {
		return ResourceRef{}, err
	}
	resource = applyClientScopeSpec(resource, spec)
	if err := c.doJSON(ctx, http.MethodPut, segments, nil, resource, []int{http.StatusNoContent}, nil); err != nil {
		return ResourceRef{}, err
	}
	return ref, nil
}

func (c *Client) LookupClientScope(ctx context.Context, realm, name string) (ResourceRef, error) {
	ref, found, err := c.findClientScope(ctx, realm, name)
	if err != nil {
		return ResourceRef{}, err
	}
	if !found {
		return ResourceRef{}, fmt.Errorf("required client scope %s was not found", name)
	}
	return ref, nil
}

func (c *Client) findClientScope(ctx context.Context, realm, name string) (ResourceRef, bool, error) {
	var resources []map[string]any
	if err := c.doJSON(ctx, http.MethodGet, []string{"admin", "realms", realm, "client-scopes"}, nil, nil, []int{http.StatusOK}, &resources); err != nil {
		return ResourceRef{}, false, err
	}
	matches := filterResources(resources, "name", name)
	if len(matches) > 1 {
		return ResourceRef{}, false, fmt.Errorf("multiple client scopes named %s", name)
	}
	if len(matches) == 0 {
		return ResourceRef{}, false, nil
	}
	ref, err := refFromResource(matches[0], name)
	return ref, true, err
}

func (c *Client) EnsureProtocolMapper(ctx context.Context, realm string, scope ResourceRef, spec ProtocolMapperSpec) error {
	base := []string{"admin", "realms", realm, "client-scopes", scope.ID, "protocol-mappers", "models"}
	var resources []map[string]any
	if err := c.doJSON(ctx, http.MethodGet, base, nil, nil, []int{http.StatusOK}, &resources); err != nil {
		return err
	}
	matches := filterResources(resources, "name", spec.Name)
	if len(matches) > 1 {
		return fmt.Errorf("multiple protocol mappers named %s", spec.Name)
	}
	payload := protocolMapperResource(spec)
	if len(matches) == 0 {
		return c.doJSON(ctx, http.MethodPost, base, nil, payload, []int{http.StatusCreated}, nil)
	}
	mapperRef, err := refFromResource(matches[0], spec.Name)
	if err != nil {
		return err
	}
	payload["id"] = mapperRef.ID
	return c.doJSON(ctx, http.MethodPut, append(base, mapperRef.ID), nil, payload, []int{http.StatusNoContent}, nil)
}

func (c *Client) EnsureDefaultClientScope(ctx context.Context, realm string, client, scope ResourceRef) error {
	return c.doJSON(
		ctx,
		http.MethodPut,
		[]string{"admin", "realms", realm, "clients", client.ID, "default-client-scopes", scope.ID},
		nil,
		nil,
		[]int{http.StatusNoContent},
		nil,
	)
}

func (c *Client) EnsureUser(ctx context.Context, realm string, spec UserSpec) (ResourceRef, error) {
	ref, found, err := c.findUser(ctx, realm, spec.Username)
	if err != nil {
		return ResourceRef{}, err
	}
	if !found {
		create := map[string]any{
			"username":      spec.Username,
			"email":         spec.Email,
			"firstName":     spec.FirstName,
			"lastName":      spec.LastName,
			"enabled":       spec.Enabled,
			"emailVerified": spec.EmailVerified,
			"credentials": []map[string]any{{
				"type":      "password",
				"value":     spec.Password,
				"temporary": false,
			}},
		}
		if err := c.doJSON(ctx, http.MethodPost, []string{"admin", "realms", realm, "users"}, nil, create, []int{http.StatusCreated}, nil); err != nil {
			return ResourceRef{}, err
		}
		ref, found, err = c.findUser(ctx, realm, spec.Username)
		if err != nil {
			return ResourceRef{}, err
		}
		if !found {
			return ResourceRef{}, fmt.Errorf("user %s was not visible after creation", spec.Username)
		}
	}

	resource := map[string]any{}
	segments := []string{"admin", "realms", realm, "users", ref.ID}
	if err := c.doJSON(ctx, http.MethodGet, segments, nil, nil, []int{http.StatusOK}, &resource); err != nil {
		return ResourceRef{}, err
	}
	delete(resource, "credentials")
	resource["username"] = spec.Username
	resource["email"] = spec.Email
	resource["firstName"] = spec.FirstName
	resource["lastName"] = spec.LastName
	resource["enabled"] = spec.Enabled
	resource["emailVerified"] = spec.EmailVerified
	if err := c.doJSON(ctx, http.MethodPut, segments, nil, resource, []int{http.StatusNoContent}, nil); err != nil {
		return ResourceRef{}, err
	}
	return ref, nil
}

func (c *Client) findUser(ctx context.Context, realm, username string) (ResourceRef, bool, error) {
	var resources []map[string]any
	query := url.Values{"username": {username}, "exact": {"true"}}
	if err := c.doJSON(ctx, http.MethodGet, []string{"admin", "realms", realm, "users"}, query, nil, []int{http.StatusOK}, &resources); err != nil {
		return ResourceRef{}, false, err
	}
	matches := filterResources(resources, "username", username)
	if len(matches) > 1 {
		return ResourceRef{}, false, fmt.Errorf("multiple users with username %s", username)
	}
	if len(matches) == 0 {
		return ResourceRef{}, false, nil
	}
	ref, err := refFromResource(matches[0], username)
	return ref, true, err
}

func (c *Client) EnsureRealmRoleMapping(ctx context.Context, realm string, user ResourceRef, roles []ResourceRef) error {
	return c.doJSON(
		ctx,
		http.MethodPost,
		[]string{"admin", "realms", realm, "users", user.ID, "role-mappings", "realm"},
		nil,
		rolePayload(roles),
		[]int{http.StatusNoContent},
		nil,
	)
}

func (c *Client) ClientRole(ctx context.Context, realm string, client ResourceRef, roleName string) (ResourceRef, error) {
	resource := map[string]any{}
	if err := c.doJSON(
		ctx,
		http.MethodGet,
		[]string{"admin", "realms", realm, "clients", client.ID, "roles", roleName},
		nil,
		nil,
		[]int{http.StatusOK},
		&resource,
	); err != nil {
		return ResourceRef{}, err
	}
	return refFromResource(resource, roleName)
}

func (c *Client) EnsureClientRoleMapping(ctx context.Context, realm string, user, client ResourceRef, roles []ResourceRef) error {
	return c.doJSON(
		ctx,
		http.MethodPost,
		[]string{"admin", "realms", realm, "users", user.ID, "role-mappings", "clients", client.ID},
		nil,
		rolePayload(roles),
		[]int{http.StatusNoContent},
		nil,
	)
}

func (c *Client) ExampleAccessToken(ctx context.Context, realm string, client, user ResourceRef) (ExampleAccessToken, error) {
	var response struct {
		Audience    json.RawMessage `json:"aud"`
		RealmAccess struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	query := url.Values{"userId": {user.ID}}
	if err := c.doJSON(
		ctx,
		http.MethodGet,
		[]string{"admin", "realms", realm, "clients", client.ID, "evaluate-scopes", "generate-example-access-token"},
		query,
		nil,
		[]int{http.StatusOK},
		&response,
	); err != nil {
		return ExampleAccessToken{}, err
	}
	audience, err := parseAudience(response.Audience)
	if err != nil {
		return ExampleAccessToken{}, err
	}
	return ExampleAccessToken{Audience: audience, RealmRoles: response.RealmAccess.Roles}, nil
}

func applyRealmSpec(resource map[string]any, spec RealmSpec) map[string]any {
	resource["realm"] = spec.Name
	resource["enabled"] = spec.Enabled
	resource["accessTokenLifespan"] = spec.AccessTokenLifespan
	resource["sslRequired"] = spec.SSLRequired
	resource["registrationAllowed"] = spec.RegistrationAllowed
	return resource
}

func applyRoleSpec(resource map[string]any, spec RoleSpec) map[string]any {
	resource["name"] = spec.Name
	resource["description"] = spec.Description
	resource["composite"] = spec.Composite
	return resource
}

func applyClientSpec(resource map[string]any, spec ClientSpec) map[string]any {
	resource["clientId"] = spec.ClientID
	resource["name"] = spec.Name
	resource["enabled"] = spec.Enabled
	resource["protocol"] = spec.Protocol
	resource["bearerOnly"] = spec.BearerOnly
	resource["publicClient"] = spec.PublicClient
	resource["standardFlowEnabled"] = spec.StandardFlowEnabled
	resource["implicitFlowEnabled"] = spec.ImplicitFlowEnabled
	resource["directAccessGrantsEnabled"] = spec.DirectAccessGrantsEnabled
	resource["serviceAccountsEnabled"] = spec.ServiceAccountsEnabled
	resource["authorizationServicesEnabled"] = spec.AuthorizationServicesEnabled
	resource["fullScopeAllowed"] = spec.FullScopeAllowed
	resource["redirectUris"] = append([]string(nil), spec.RedirectURIs...)
	resource["webOrigins"] = append([]string(nil), spec.WebOrigins...)
	setOwnedAttributes(resource, spec.Attributes)
	return resource
}

func applyClientScopeSpec(resource map[string]any, spec ClientScopeSpec) map[string]any {
	resource["name"] = spec.Name
	resource["protocol"] = spec.Protocol
	setOwnedAttributes(resource, spec.Attributes)
	return resource
}

func protocolMapperResource(spec ProtocolMapperSpec) map[string]any {
	return map[string]any{
		"name":            spec.Name,
		"protocol":        spec.Protocol,
		"protocolMapper":  spec.ProtocolMapper,
		"consentRequired": spec.ConsentRequired,
		"config":          spec.Config,
	}
}

func setOwnedAttributes(resource map[string]any, owned map[string]string) {
	attributes := map[string]any{}
	if existing, ok := resource["attributes"].(map[string]any); ok {
		for key, value := range existing {
			attributes[key] = value
		}
	}
	for key, value := range owned {
		attributes[key] = value
	}
	resource["attributes"] = attributes
}

func filterResources(resources []map[string]any, field, value string) []map[string]any {
	matches := make([]map[string]any, 0, 1)
	for _, resource := range resources {
		if got, _ := resource[field].(string); got == value {
			matches = append(matches, resource)
		}
	}
	return matches
}

func refFromResource(resource map[string]any, fallbackName string) (ResourceRef, error) {
	id, _ := resource["id"].(string)
	if strings.TrimSpace(id) == "" {
		return ResourceRef{}, fmt.Errorf("Keycloak resource %s is missing id", fallbackName)
	}
	name := fallbackName
	if value, _ := resource["name"].(string); value != "" {
		name = value
	} else if value, _ := resource["clientId"].(string); value != "" {
		name = value
	} else if value, _ := resource["username"].(string); value != "" {
		name = value
	}
	return ResourceRef{ID: id, Name: name}, nil
}

func rolePayload(roles []ResourceRef) []map[string]string {
	payload := make([]map[string]string, 0, len(roles))
	for _, role := range roles {
		payload = append(payload, map[string]string{"id": role.ID, "name": role.Name})
	}
	return payload
}

func parseAudience(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, fmt.Errorf("decode example access-token audience: %w", err)
	}
	return multiple, nil
}
