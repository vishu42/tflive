package app

import (
	"context"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/traits"
)

func contextWithPlatformAdmin() context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject:    "admin-subject",
		RealmRoles: []string{"platform-admin"},
	})
}

func contextWithOrdinaryUser() context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject:    "user-subject",
		RealmRoles: []string{"stack-creator"},
	})
}

type fakeUserDirectory struct {
	users []DirectoryUser
	err   error
}

func (f *fakeUserDirectory) SearchUsers(_ context.Context, _ string, _, _ int) ([]DirectoryUser, error) {
	return f.users, f.err
}

func (f *fakeUserDirectory) GetUserByID(_ context.Context, id string) (DirectoryUser, error) {
	if f.err != nil {
		return DirectoryUser{}, f.err
	}
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return DirectoryUser{}, nil
}

type fakeErrorDirectory struct {
	err error
}

func (f *fakeErrorDirectory) SearchUsers(_ context.Context, _ string, _, _ int) ([]DirectoryUser, error) {
	return nil, f.err
}

func (f *fakeErrorDirectory) GetUserByID(_ context.Context, _ string) (DirectoryUser, error) {
	return DirectoryUser{}, f.err
}

func TestSearchUsersReturnsResults(t *testing.T) {
	t.Parallel()

	expected := []DirectoryUser{
		{ID: "u1", Username: "alice", Email: "alice@example.com", FirstName: "Alice", LastName: "Smith"},
		{ID: "u2", Username: "bob", Email: "bob@example.com", FirstName: "Bob", LastName: "Jones"},
	}
	service := NewService(Service{
		UserDirectory: &fakeUserDirectory{users: expected},
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
