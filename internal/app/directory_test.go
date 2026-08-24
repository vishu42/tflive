package app

import (
	"context"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/traits"
)

// The tier now lives in OpenFGA, not in the token: testPlatformAuthorizer
// makes admin-subject an administrator and user-subject an editor.
func contextWithPlatformAdmin() context.Context {
	return platformContext("admin-subject")
}

func contextWithOrdinaryUser() context.Context {
	return platformContext("user-subject")
}

type fakeUserDirectory struct {
	users  []DirectoryUser
	err    error
	getErr error
}

func (f *fakeUserDirectory) SearchUsers(_ context.Context, _ string, _, _ int) ([]DirectoryUser, error) {
	return f.users, f.err
}

func (f *fakeUserDirectory) GetUser(_ context.Context, userID string) (*DirectoryUser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, u := range f.users {
		if u.ID == userID {
			return &u, nil
		}
	}
	return nil, nil
}

type fakeErrorDirectory struct {
	err error
}

func (f *fakeErrorDirectory) SearchUsers(_ context.Context, _ string, _, _ int) ([]DirectoryUser, error) {
	return nil, f.err
}

func (f *fakeErrorDirectory) GetUser(_ context.Context, _ string) (*DirectoryUser, error) {
	return nil, f.err
}

func TestSearchUsersReturnsResults(t *testing.T) {
	t.Parallel()

	expected := []DirectoryUser{
		{ID: "u1", Username: "alice", Email: "alice@example.com", FirstName: "Alice", LastName: "Smith"},
		{ID: "u2", Username: "bob", Email: "bob@example.com", FirstName: "Bob", LastName: "Jones"},
	}
	service := NewService(Service{
		UserDirectory: &fakeUserDirectory{users: expected},
		Authorizer:    testPlatformAuthorizer(),
	})

	users, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: traits.TenantID("tenant_1"),
		Query:    "ali",
		First:    0,
		Max:      20,
	})
	if err != nil {
		t.Fatalf("SearchUsers returned error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	if users[0].Username != "alice" {
		t.Fatalf("first user username = %q, want alice", users[0].Username)
	}
}

func TestSearchUsersRequiresPlatformAdmin(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		UserDirectory: &fakeUserDirectory{users: []DirectoryUser{}},
		Authorizer:    testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(contextWithOrdinaryUser(), SearchUsersCommand{
		TenantID: traits.TenantID("tenant_1"),
		Query:    "test",
		First:    0,
		Max:      20,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestSearchUsersRequiresAuthentication(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		UserDirectory: &fakeUserDirectory{users: []DirectoryUser{}},
		Authorizer:    testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(context.Background(), SearchUsersCommand{
		TenantID: traits.TenantID("tenant_1"),
		Query:    "test",
		First:    0,
		Max:      20,
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestSearchUsersRejectsEmptyQuery(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		UserDirectory: &fakeUserDirectory{users: []DirectoryUser{}},
		Authorizer:    testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: traits.TenantID("tenant_1"),
		Query:    "",
		First:    0,
		Max:      20,
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestSearchUsersRejectsInvalidPagination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		first int
		max   int
	}{
		{name: "negative first", first: -1, max: 20},
		{name: "zero max", first: 0, max: 0},
		{name: "max exceeds 50", first: 0, max: 51},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := NewService(Service{
				UserDirectory: &fakeUserDirectory{users: []DirectoryUser{}},
				Authorizer:    testPlatformAuthorizer(),
			})

			_, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
				TenantID: traits.TenantID("tenant_1"),
				Query:    "test",
				First:    test.first,
				Max:      test.max,
			})
			if !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("error = %v, want ErrInvalidCommand", err)
			}
		})
	}
}

func TestSearchUsersDirectoryUnavailable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		UserDirectory: nil,
		Authorizer:    testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: traits.TenantID("tenant_1"),
		Query:    "test",
		First:    0,
		Max:      20,
	})
	if !errors.Is(err, ErrDirectoryUnavailable) {
		t.Fatalf("error = %v, want ErrDirectoryUnavailable", err)
	}
}

func TestSearchUsersDirectoryError(t *testing.T) {
	t.Parallel()

	directoryErr := errors.New("keycloak connection refused")
	service := NewService(Service{
		UserDirectory: &fakeErrorDirectory{err: directoryErr},
		Authorizer:    testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: traits.TenantID("tenant_1"),
		Query:    "test",
		First:    0,
		Max:      20,
	})
	if !errors.Is(err, directoryErr) {
		t.Fatalf("error = %v, want %v", err, directoryErr)
	}
}

func TestSearchUsersEmptyResults(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		UserDirectory: &fakeUserDirectory{users: nil},
		Authorizer:    testPlatformAuthorizer(),
	})

	users, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: traits.TenantID("tenant_1"),
		Query:    "noone",
		First:    0,
		Max:      20,
	})
	if err != nil {
		t.Fatalf("SearchUsers returned error: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("got %d users, want 0", len(users))
	}
}
