package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/domain"
)

// The tier now lives in OpenFGA, not in the token: testPlatformAuthorizer
// makes admin-subject an administrator and user-subject an editor.
func contextWithPlatformAdmin() context.Context {
	return platformContext("admin-subject")
}

func contextWithOrdinaryUser() context.Context {
	return platformContext("user-subject")
}

// fakeUserRepository is the identity projection held in memory. It answers
// searches with whatever it was seeded with rather than matching the query,
// since the matching itself is the store's job and is tested there.
type fakeUserRepository struct {
	users     []UserProfile
	searchErr error
	lookupErr error
	upserted  []UserProfile
}

func (f *fakeUserRepository) UpsertUser(_ context.Context, profile UserProfile, _ time.Time) error {
	f.upserted = append(f.upserted, profile)
	return nil
}

func (f *fakeUserRepository) SearchUsers(_ context.Context, _ string, _, _ int) ([]UserProfile, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.users, nil
}

func (f *fakeUserRepository) UsersBySubs(_ context.Context, subs []string) (map[string]UserProfile, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	wanted := make(map[string]struct{}, len(subs))
	for _, sub := range subs {
		wanted[sub] = struct{}{}
	}
	found := make(map[string]UserProfile)
	for _, user := range f.users {
		if _, ok := wanted[user.Sub]; ok {
			found[user.Sub] = user
		}
	}
	return found, nil
}

func TestSearchUsersReturnsResults(t *testing.T) {
	t.Parallel()

	expected := []UserProfile{
		{Sub: "u1", DisplayName: "Alice Smith", Email: "alice@example.com"},
		{Sub: "u2", DisplayName: "Bob Jones", Email: "bob@example.com"},
	}
	service := NewService(Service{
		Users:      &fakeUserRepository{users: expected},
		Authorizer: testPlatformAuthorizer(),
	})

	users, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: domain.TenantID("tenant_1"),
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
	if users[0].DisplayName != "Alice Smith" {
		t.Fatalf("first user display name = %q, want Alice Smith", users[0].DisplayName)
	}
}

func TestSearchUsersRequiresPlatformAdmin(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Users:      &fakeUserRepository{users: []UserProfile{}},
		Authorizer: testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(contextWithOrdinaryUser(), SearchUsersCommand{
		TenantID: domain.TenantID("tenant_1"),
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
		Users:      &fakeUserRepository{users: []UserProfile{}},
		Authorizer: testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(context.Background(), SearchUsersCommand{
		TenantID: domain.TenantID("tenant_1"),
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
		Users:      &fakeUserRepository{users: []UserProfile{}},
		Authorizer: testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: domain.TenantID("tenant_1"),
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
				Users:      &fakeUserRepository{users: []UserProfile{}},
				Authorizer: testPlatformAuthorizer(),
			})

			_, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
				TenantID: domain.TenantID("tenant_1"),
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

func TestSearchUsersRepositoryError(t *testing.T) {
	t.Parallel()

	searchErr := errors.New("connection refused")
	service := NewService(Service{
		Users:      &fakeUserRepository{searchErr: searchErr},
		Authorizer: testPlatformAuthorizer(),
	})

	_, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: domain.TenantID("tenant_1"),
		Query:    "test",
		First:    0,
		Max:      20,
	})
	if !errors.Is(err, searchErr) {
		t.Fatalf("error = %v, want %v", err, searchErr)
	}
}

func TestSearchUsersEmptyResults(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Users:      &fakeUserRepository{users: nil},
		Authorizer: testPlatformAuthorizer(),
	})

	users, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
		TenantID: domain.TenantID("tenant_1"),
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

// RecordSignIn deliberately authorizes nothing: it is called at the callback,
// after authentication succeeded and before any principal exists.
func TestRecordSignInProjectsWithoutAPrincipal(t *testing.T) {
	t.Parallel()

	users := &fakeUserRepository{}
	service := NewService(Service{Users: users, Authorizer: testPlatformAuthorizer()})

	profile := UserProfile{Sub: "u1", DisplayName: "Alice Smith", Email: "alice@example.com"}
	if err := service.RecordSignIn(context.Background(), profile); err != nil {
		t.Fatalf("RecordSignIn returned error: %v", err)
	}
	if len(users.upserted) != 1 || users.upserted[0] != profile {
		t.Fatalf("upserted = %+v, want exactly %+v", users.upserted, profile)
	}
}

func TestRecordSignInRejectsEmptySubject(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Users: &fakeUserRepository{}, Authorizer: testPlatformAuthorizer()})

	err := service.RecordSignIn(context.Background(), UserProfile{DisplayName: "Nobody"})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

// The projection is where the accepted limitation is enforced: a subject that
// has never signed in has no row, and so cannot be granted a role.
func TestAssignStackRoleRejectsUserThatNeverSignedIn(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Work:       newRecordingWork(nil),
		Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()},
		Users:      &fakeUserRepository{},
		Clock:      fixedClock{now: time.Now()},
	})

	_, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_abc"),
		UserSub:  "never-signed-in",
		Role:     "operator",
	})
	if !errors.Is(err, ErrUserNotProvisioned) {
		t.Fatalf("error = %v, want ErrUserNotProvisioned", err)
	}
}

// The returned grant carries the display fields resolved by the check the
// assign already had to perform, so a caller can render the new row without
// re-reading the grants list.
func TestAssignStackRoleReturnsProjectedDisplayFields(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Work:       newRecordingWork(nil),
		Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()},
		Users: &fakeUserRepository{users: []UserProfile{
			{Sub: "user_456", DisplayName: "Casey Jones", Email: "casey@example.com"},
		}},
		Clock: fixedClock{now: time.Now()},
	})

	view, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_abc"),
		UserSub:  "user_456",
		Role:     "operator",
	})
	if err != nil {
		t.Fatalf("AssignStackRole returned error: %v", err)
	}
	if view.DisplayName != "Casey Jones" || view.Email != "casey@example.com" {
		t.Fatalf("view = %#v, want the projected display name and email", view)
	}
}

// A Service wired without a UserRepository is reachable: the grants and search
// routes register unconditionally. Every entry point that touches the
// projection has to answer "unavailable" rather than nil-panic.
func TestUserProjectionEntryPointsRequireARepository(t *testing.T) {
	t.Parallel()

	newUnwiredService := func() *Service {
		return NewService(Service{
			Work:       newRecordingWork(nil),
			Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()},
			Clock:      fixedClock{now: time.Now()},
		})
	}

	tests := map[string]func(*Service) error{
		"RecordSignIn": func(service *Service) error {
			return service.RecordSignIn(context.Background(), UserProfile{Sub: "u1"})
		},
		"SearchUsers": func(service *Service) error {
			_, err := service.SearchUsers(contextWithPlatformAdmin(), SearchUsersCommand{
				TenantID: domain.TenantID("tenant_123"),
				Query:    "ali",
				Max:      20,
			})
			return err
		},
		"ListStackGrants": func(service *Service) error {
			_, err := service.ListStackGrants(adminContext(), ListStackGrantsCommand{
				TenantID: domain.TenantID("tenant_123"),
				StackID:  domain.StackID("stack_abc"),
			})
			return err
		},
		"AssignStackRole": func(service *Service) error {
			_, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
				TenantID: domain.TenantID("tenant_123"),
				StackID:  domain.StackID("stack_abc"),
				UserSub:  "user_456",
				Role:     "operator",
			})
			return err
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := call(newUnwiredService())
			if !errors.Is(err, authz.ErrUnavailable) {
				t.Fatalf("error = %v, want authz.ErrUnavailable", err)
			}
		})
	}
}
