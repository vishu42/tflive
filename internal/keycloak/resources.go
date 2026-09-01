package keycloak

import (
	"context"
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
	if spec.Secret != "" {
		resource["secret"] = spec.Secret
	}
	return resource
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
