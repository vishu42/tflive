package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
)

type fakeAccounts struct {
	account   authn.LocalAccount
	found     bool
	lookupErr error
	ensureErr error
	// usernameTaken models the insert being refused by the username unique
	// constraint: the sub was free, another account holds the name.
	usernameTaken bool

	ensured []authn.LocalAccount
}

func (f *fakeAccounts) LocalAccountBySubject(_ context.Context, subject string) (authn.LocalAccount, error) {
	if f.lookupErr != nil {
		return authn.LocalAccount{}, f.lookupErr
	}
	if !f.found || f.account.Subject != subject {
		return authn.LocalAccount{}, authn.ErrLocalAccountNotFound
	}
	return f.account, nil
}

func (f *fakeAccounts) EnsureLocalAccount(_ context.Context, account authn.LocalAccount, _ time.Time) (bool, error) {
	if f.ensureErr != nil {
		return false, f.ensureErr
	}
	if f.usernameTaken {
		return false, nil
	}
	f.ensured = append(f.ensured, account)
	return true, nil
}

type fakeAuthorizer struct {
	authz.Authorizer

	allowed  bool
	checkErr error
	writeErr error

	checked []authz.CheckRequest
	written []authz.Grant
}

func (f *fakeAuthorizer) Check(_ context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	f.checked = append(f.checked, request)
	return authz.CheckResult{Allowed: f.allowed}, f.checkErr
}

func (f *fakeAuthorizer) WriteRelationships(_ context.Context, mutation authz.Mutation) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, mutation.Grants()...)
	return nil
}

func testRootConfig() RootConfig {
	return RootConfig{Username: "root", Password: "hunter2"}
}

func fixedClock() func() time.Time {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return now }
}

func TestSeedRootCreatesTheAccountAndTheTuple(t *testing.T) {
	accounts := &fakeAccounts{}
	authorizer := &fakeAuthorizer{}

	if err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}

	if len(accounts.ensured) != 1 {
		t.Fatalf("ensured %d accounts, want 1", len(accounts.ensured))
	}
	account := accounts.ensured[0]
	if account.Subject != DefaultRootSubject {
		t.Fatalf("Subject = %q, want %q", account.Subject, DefaultRootSubject)
	}
	if account.Username != "root" {
		t.Fatalf("Username = %q, want root", account.Username)
	}

	if len(authorizer.written) != 1 {
		t.Fatalf("wrote %d tuples, want 1", len(authorizer.written))
	}
	grant := authorizer.written[0]
	if grant.Subject().String() != "user:"+DefaultRootSubject {
		t.Fatalf("subject = %q, want user:%s", grant.Subject(), DefaultRootSubject)
	}
	if grant.Relation() != authz.RelationRoot {
		t.Fatalf("relation = %q, want root", grant.Relation())
	}
	if grant.Object().String() != "platform:"+authz.PlatformID {
		t.Fatalf("object = %q, want platform:%s", grant.Object(), authz.PlatformID)
	}
	if !grant.Structural() {
		t.Fatal("the root tuple is not marked structural")
	}
}

// The password reaches the table hashed. A plaintext column would be a
// credential store anyone with read access owns outright.
func TestSeedRootHashesThePassword(t *testing.T) {
	accounts := &fakeAccounts{}

	if err := SeedRoot(context.Background(), accounts, &fakeAuthorizer{}, testRootConfig(), fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}

	hash := accounts.ensured[0].PasswordHash
	if hash == "hunter2" {
		t.Fatal("the password was stored in plaintext")
	}
	if !authn.VerifyPassword(hash, "hunter2") {
		t.Fatal("the stored hash does not verify against the configured password")
	}
}

// Add-only. #212 reconciles at every boot, and an operator who has rotated the
// root password must not have it reset from config on the next restart -- which
// would look like it worked until then.
func TestSeedRootLeavesAnExistingAccountAlone(t *testing.T) {
	accounts := &fakeAccounts{
		found:   true,
		account: authn.LocalAccount{Subject: DefaultRootSubject, Username: "root", PasswordHash: authn.DummyPasswordHash},
	}
	authorizer := &fakeAuthorizer{allowed: true}

	if err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}

	if len(accounts.ensured) != 0 {
		t.Fatalf("ensured %d accounts over an existing one, want 0", len(accounts.ensured))
	}
	if len(authorizer.written) != 0 {
		t.Fatalf("wrote %d tuples when one already stood, want 0", len(authorizer.written))
	}
}

// Hashing costs argon2id's full memory and time. Skipping it when the account
// already exists is what keeps every restart after the first cheap.
func TestSeedRootDoesNotHashWhenTheAccountExists(t *testing.T) {
	accounts := &fakeAccounts{
		found:   true,
		account: authn.LocalAccount{Subject: DefaultRootSubject, Username: "root"},
	}

	config := testRootConfig()
	config.Password = ""

	// An empty password would fail validation on the create path. Reaching a
	// clean return proves the create path was not taken.
	if err := SeedRoot(context.Background(), accounts, &fakeAuthorizer{allowed: true}, config, fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}
}

// The account and the tuple are reconciled independently: a boot that created
// the row and then failed before the tuple must complete on the next one.
func TestSeedRootWritesAMissingTupleForAnExistingAccount(t *testing.T) {
	accounts := &fakeAccounts{
		found:   true,
		account: authn.LocalAccount{Subject: DefaultRootSubject, Username: "root"},
	}
	authorizer := &fakeAuthorizer{allowed: false}

	if err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}
	if len(authorizer.written) != 1 {
		t.Fatalf("wrote %d tuples, want the missing one written", len(authorizer.written))
	}
}

// Fail closed. Running with no reachable administrator is the worse failure,
// so a seeding error stops the boot rather than being logged past.
func TestSeedRootFailsClosed(t *testing.T) {
	outage := errors.New("connection refused")

	for name, seed := range map[string]func() (*fakeAccounts, *fakeAuthorizer){
		"account lookup fails": func() (*fakeAccounts, *fakeAuthorizer) {
			return &fakeAccounts{lookupErr: outage}, &fakeAuthorizer{}
		},
		"account write fails": func() (*fakeAccounts, *fakeAuthorizer) {
			return &fakeAccounts{ensureErr: outage}, &fakeAuthorizer{}
		},
		"tuple check fails": func() (*fakeAccounts, *fakeAuthorizer) {
			return &fakeAccounts{}, &fakeAuthorizer{checkErr: outage}
		},
		"tuple write fails": func() (*fakeAccounts, *fakeAuthorizer) {
			return &fakeAccounts{}, &fakeAuthorizer{writeErr: outage}
		},
	} {
		t.Run(name, func(t *testing.T) {
			accounts, authorizer := seed()
			err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock())
			if err == nil {
				t.Fatal("SeedRoot succeeded despite a failure")
			}
			if !errors.Is(err, outage) {
				t.Fatalf("error = %v, want it to wrap the underlying failure", err)
			}
		})
	}
}

func TestSeedRootRejectsAnEmptyPassword(t *testing.T) {
	config := testRootConfig()
	config.Password = ""

	err := SeedRoot(context.Background(), &fakeAccounts{}, &fakeAuthorizer{}, config, fixedClock())
	if err == nil {
		t.Fatal("SeedRoot accepted an empty root password")
	}
}

// The sub becomes user:<sub> in a tuple, and authz refuses ':' there. A
// configured sub that cannot be an OpenFGA subject would seed an account that
// signs in and is granted nothing.
func TestSeedRootRejectsASubjectThatCannotBeATupleToken(t *testing.T) {
	for _, subject := range []string{"local:root", "local#root", "local*root", "local root"} {
		config := testRootConfig()
		config.Subject = subject

		if err := SeedRoot(context.Background(), &fakeAccounts{}, &fakeAuthorizer{}, config, fixedClock()); err == nil {
			t.Fatalf("SeedRoot accepted the unusable subject %q", subject)
		}
	}
}

func TestSeedRootDefaultsTheUsername(t *testing.T) {
	accounts := &fakeAccounts{}
	config := testRootConfig()
	config.Username = ""

	if err := SeedRoot(context.Background(), accounts, &fakeAuthorizer{}, config, fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}
	if accounts.ensured[0].Username != DefaultRootUsername {
		t.Fatalf("Username = %q, want %q", accounts.ensured[0].Username, DefaultRootUsername)
	}
}

// The tuple is checked for before it is written, which is what makes the
// reconcile add-only rather than a write that happens to be idempotent.
func TestSeedRootChecksTheTupleBeforeWriting(t *testing.T) {
	authorizer := &fakeAuthorizer{}

	if err := SeedRoot(context.Background(), &fakeAccounts{}, authorizer, testRootConfig(), fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}
	if len(authorizer.checked) != 1 {
		t.Fatalf("checked %d times, want 1", len(authorizer.checked))
	}
	if authorizer.checked[0].Relation != authz.RelationRoot {
		t.Fatalf("checked relation = %q, want root", authorizer.checked[0].Relation)
	}
}

// An unset subject is not an invalid one: it means the default, which is the
// only value #212 expects anyone to run.
func TestSeedRootDefaultsTheSubject(t *testing.T) {
	accounts := &fakeAccounts{}
	config := testRootConfig()
	config.Subject = ""

	if err := SeedRoot(context.Background(), accounts, &fakeAuthorizer{}, config, fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}
	if accounts.ensured[0].Subject != DefaultRootSubject {
		t.Fatalf("Subject = %q, want %q", accounts.ensured[0].Subject, DefaultRootSubject)
	}
}

// Existence is asked by sub, not by username. Renaming root after first boot
// used to make the lookup miss and the insert collide with the existing row on
// the primary key, which failed the boot -- and failed it again on every
// restart, because the collision was not something a retry could clear.
func TestSeedRootDoesNotReinsertWhenTheUsernameChanged(t *testing.T) {
	accounts := &fakeAccounts{
		found:   true,
		account: authn.LocalAccount{Subject: DefaultRootSubject, Username: "root", PasswordHash: authn.DummyPasswordHash},
	}

	config := testRootConfig()
	config.Username = "administrator"

	if err := SeedRoot(context.Background(), accounts, &fakeAuthorizer{allowed: true}, config, fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}
	if len(accounts.ensured) != 0 {
		t.Fatalf("ensured %d accounts over an existing root, want 0", len(accounts.ensured))
	}
}

// The sub is free but the name is not, so the insert is refused by the username
// constraint. Continuing would write the root tuple for a sub with no account
// behind it, leaving the operator signed in as somebody else and not root.
func TestSeedRootRejectsAUsernameHeldByAnotherAccount(t *testing.T) {
	accounts := &fakeAccounts{usernameTaken: true}
	authorizer := &fakeAuthorizer{}

	err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock())
	if err == nil {
		t.Fatal("SeedRoot returned nil for a username held by another account")
	}
	if len(authorizer.written) != 0 {
		t.Fatalf("wrote %d tuples for an account that was not created, want 0", len(authorizer.written))
	}
}
