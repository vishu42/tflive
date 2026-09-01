package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/app"
)

var _ app.UserRepository = (*Store)(nil)

func TestUpsertUserInsertsThenRefreshes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewStore(openMigratedTestPool(t, ctx))

	firstSeen := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	if err := store.UpsertUser(ctx, app.UserProfile{
		Sub:         "sub-1",
		DisplayName: "Ada Lovelace",
		Email:       "ada@example.com",
	}, firstSeen); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	// A second sign-in with changed claims: the display data follows the
	// token, last_seen_at moves, and first_seen_at does not.
	lastSeen := time.Date(2026, 9, 1, 17, 30, 0, 0, time.UTC)
	if err := store.UpsertUser(ctx, app.UserProfile{
		Sub:         "sub-1",
		DisplayName: "Ada King",
		Email:       "ada.king@example.com",
	}, lastSeen); err != nil {
		t.Fatalf("UpsertUser second call: %v", err)
	}

	var (
		count       int
		displayName string
		email       string
		gotFirst    time.Time
		gotLast     time.Time
	)
	if err := store.pool.QueryRow(ctx, `select count(*) from users`).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users = %d, want 1 — the upsert must not insert a second row", count)
	}
	if err := store.pool.QueryRow(ctx, `
		select display_name, email, first_seen_at, last_seen_at from users where sub = $1
	`, "sub-1").Scan(&displayName, &email, &gotFirst, &gotLast); err != nil {
		t.Fatalf("read user: %v", err)
	}
	if displayName != "Ada King" || email != "ada.king@example.com" {
		t.Fatalf("display data = %q/%q, want the refreshed claims", displayName, email)
	}
	if !gotFirst.Equal(firstSeen) {
		t.Fatalf("first_seen_at = %s, want %s — it must survive a later sign-in", gotFirst, firstSeen)
	}
	if !gotLast.Equal(lastSeen) {
		t.Fatalf("last_seen_at = %s, want %s", gotLast, lastSeen)
	}
}

func TestSearchUsersMatchesNameAndEmailCaseInsensitively(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewStore(openMigratedTestPool(t, ctx))
	seedUsers(t, ctx, store,
		app.UserProfile{Sub: "sub-1", DisplayName: "Ada Lovelace", Email: "ada@example.com"},
		app.UserProfile{Sub: "sub-2", DisplayName: "Grace Hopper", Email: "grace@example.com"},
		app.UserProfile{Sub: "sub-3", DisplayName: "Alan Turing", Email: "alan@bletchley.test"},
	)

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "display name infix", query: "ovelace", want: []string{"sub-1"}},
		{name: "display name is case insensitive", query: "grace", want: []string{"sub-2"}},
		{name: "email domain", query: "bletchley", want: []string{"sub-3"}},
		{name: "matches across both columns", query: "a", want: []string{"sub-1", "sub-3", "sub-2"}},
		{name: "no match", query: "nobody", want: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			users, err := store.SearchUsers(ctx, test.query, 0, 50)
			if err != nil {
				t.Fatalf("SearchUsers: %v", err)
			}
			if len(users) != len(test.want) {
				t.Fatalf("got %d users %v, want %d", len(users), subsOf(users), len(test.want))
			}
			for i, sub := range test.want {
				if users[i].Sub != sub {
					t.Fatalf("users = %v, want %v", subsOf(users), test.want)
				}
			}
		})
	}
}

// A caller typing "%" is searching for a percent sign, not writing a pattern
// that matches the entire table.
func TestSearchUsersTreatsLikeMetacharactersAsLiterals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewStore(openMigratedTestPool(t, ctx))
	seedUsers(t, ctx, store,
		app.UserProfile{Sub: "sub-1", DisplayName: "Ada Lovelace", Email: "ada@example.com"},
		app.UserProfile{Sub: "sub-2", DisplayName: "100% Cotton", Email: "cotton@example.com"},
	)

	users, err := store.SearchUsers(ctx, "%", 0, 50)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(users) != 1 || users[0].Sub != "sub-2" {
		t.Fatalf("users = %v, want only sub-2 — %% must not act as a wildcard", subsOf(users))
	}

	users, err = store.SearchUsers(ctx, "_", 0, 50)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users = %v, want none — _ must not match a single character", subsOf(users))
	}
}

func TestSearchUsersAppliesOffsetAndLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewStore(openMigratedTestPool(t, ctx))
	seedUsers(t, ctx, store,
		app.UserProfile{Sub: "sub-1", DisplayName: "User A", Email: "a@example.com"},
		app.UserProfile{Sub: "sub-2", DisplayName: "User B", Email: "b@example.com"},
		app.UserProfile{Sub: "sub-3", DisplayName: "User C", Email: "c@example.com"},
	)

	users, err := store.SearchUsers(ctx, "User", 1, 1)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(users) != 1 || users[0].Sub != "sub-2" {
		t.Fatalf("users = %v, want just sub-2 at offset 1", subsOf(users))
	}
}

func TestUsersBySubsOmitsSubjectsThatNeverSignedIn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewStore(openMigratedTestPool(t, ctx))
	seedUsers(t, ctx, store,
		app.UserProfile{Sub: "sub-1", DisplayName: "Ada Lovelace", Email: "ada@example.com"},
	)

	profiles, err := store.UsersBySubs(ctx, []string{"sub-1", "never-signed-in"})
	if err != nil {
		t.Fatalf("UsersBySubs: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %#v, want exactly one", profiles)
	}
	if profile, ok := profiles["sub-1"]; !ok || profile.DisplayName != "Ada Lovelace" {
		t.Fatalf("profiles[sub-1] = %#v, ok = %v", profile, ok)
	}
	if _, ok := profiles["never-signed-in"]; ok {
		t.Fatal("a subject with no row must be absent, not present and empty")
	}
}

func TestUsersBySubsWithNoSubjectsIssuesNoQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewStore(openMigratedTestPool(t, ctx))

	profiles, err := store.UsersBySubs(ctx, nil)
	if err != nil {
		t.Fatalf("UsersBySubs: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("profiles = %#v, want empty", profiles)
	}
}

func seedUsers(t *testing.T, ctx context.Context, store *Store, profiles ...app.UserProfile) {
	t.Helper()

	seenAt := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	for _, profile := range profiles {
		if err := store.UpsertUser(ctx, profile, seenAt); err != nil {
			t.Fatalf("seed user %s: %v", profile.Sub, err)
		}
	}
}

func subsOf(users []app.UserProfile) []string {
	subs := make([]string, 0, len(users))
	for _, user := range users {
		subs = append(subs, user.Sub)
	}
	return subs
}
