package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/storage/memory"
	"github.com/vishu42/tflive/internal/authn"

	"github.com/vishu42/tflive/internal/authorization"
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

// newAuthorization runs a real engine over memory. Seeding is what these tests
// vary, rather than a fake's boolean: SeedRoot is add-only, so what matters is
// whether the root relationship is already present.
func newAuthorization(t *testing.T) *authorization.Authorization {
	t.Helper()
	auth, err := authorization.NewWithDatastore(context.Background(), memory.New(), "tflive-test")
	if err != nil {
		t.Fatalf("build authorization: %v", err)
	}
	t.Cleanup(auth.Close)
	return auth
}

// failAfterBootstrap wraps a working datastore and starts failing every tuple
// read and write once bootstrap has finished.
//
// The store and the model must be resolvable for the Authorization to exist at
// all, so the failure cannot be present from the start. Flipping it afterwards
// is how a provider outage during seeding is simulated -- and SeedRoot must
// fail closed on one rather than carry on.
type failAfterBootstrap struct {
	storage.OpenFGADatastore
	failing bool
	err     error
}

func (d *failAfterBootstrap) ReadUserTuple(ctx context.Context, store string, filter storage.ReadUserTupleFilter, options storage.ReadUserTupleOptions) (*openfgav1.Tuple, error) {
	if d.failing {
		return nil, d.err
	}
	return d.OpenFGADatastore.ReadUserTuple(ctx, store, filter, options)
}

func (d *failAfterBootstrap) ReadUsersetTuples(ctx context.Context, store string, filter storage.ReadUsersetTuplesFilter, options storage.ReadUsersetTuplesOptions) (storage.TupleIterator, error) {
	if d.failing {
		return nil, d.err
	}
	return d.OpenFGADatastore.ReadUsersetTuples(ctx, store, filter, options)
}

func (d *failAfterBootstrap) ReadStartingWithUser(ctx context.Context, store string, filter storage.ReadStartingWithUserFilter, options storage.ReadStartingWithUserOptions) (storage.TupleIterator, error) {
	if d.failing {
		return nil, d.err
	}
	return d.OpenFGADatastore.ReadStartingWithUser(ctx, store, filter, options)
}

func (d *failAfterBootstrap) Write(ctx context.Context, store string, deletes storage.Deletes, writes storage.Writes, opts ...storage.TupleWriteOption) error {
	if d.failing {
		return d.err
	}
	return d.OpenFGADatastore.Write(ctx, store, deletes, writes, opts...)
}

// newFailingAuthorization boots normally and then fails every tuple operation.
func newFailingAuthorization(t *testing.T, err error) *authorization.Authorization {
	t.Helper()
	datastore := &failAfterBootstrap{OpenFGADatastore: memory.New(), err: err}
	auth, err2 := authorization.NewWithDatastore(context.Background(), datastore, "tflive-test")
	if err2 != nil {
		t.Fatalf("build authorization: %v", err2)
	}
	t.Cleanup(auth.Close)
	datastore.failing = true
	return auth
}

// seedRootRelationship writes the root relationship, so a test can start from a
// store where it already stands.
func seedRootRelationship(t *testing.T, auth *authorization.Authorization, sub string) *authorization.Authorization {
	t.Helper()
	subject, err := authorization.SubjectFromOIDCSub(sub)
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub: %v", err)
	}
	relationship, err := authorization.NewStructuralRelationship(subject, authorization.Platform, authorization.RelationRoot)
	if err != nil {
		t.Fatalf("NewStructuralRelationship: %v", err)
	}
	if err := auth.Grant(context.Background(), relationship); err != nil {
		t.Fatalf("seed root relationship: %v", err)
	}
	return auth
}

// rootIsSeeded reports whether the root relationship exists on the platform.
func rootIsSeeded(t *testing.T, auth *authorization.Authorization, sub string) bool {
	t.Helper()
	held, err := auth.Can(context.Background(), sub, authorization.RelationRoot, authorization.Platform)
	if err != nil {
		t.Fatalf("check root relationship: %v", err)
	}
	return held
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
	authorizer := newAuthorization(t)

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

	// The relationship is asserted through the engine rather than through a
	// record of the call, so what is checked is the state the model evaluates.
	if !rootIsSeeded(t, authorizer, DefaultRootSubject) {
		t.Fatalf("root relationship missing for %s", DefaultRootSubject)
	}
	// root is structural rather than grantable, which is what keeps the grant
	// API from writing it. NewGrant must keep refusing it.
	rootSubject, err := authorization.SubjectFromOIDCSub(DefaultRootSubject)
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub: %v", err)
	}
	if _, err := authorization.NewGrant(rootSubject, authorization.Platform, authorization.RelationRoot); err == nil {
		t.Fatal("NewGrant(root) succeeded; the grant API must not be able to write the root relationship")
	}
}

// The password reaches the table hashed. A plaintext column would be a
// credential store anyone with read access owns outright.
func TestSeedRootHashesThePassword(t *testing.T) {
	accounts := &fakeAccounts{}

	if err := SeedRoot(context.Background(), accounts, newAuthorization(t), testRootConfig(), fixedClock()); err != nil {
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
	authorizer := seedRootRelationship(t, newAuthorization(t), DefaultRootSubject)

	if err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}

	if len(accounts.ensured) != 0 {
		t.Fatalf("ensured %d accounts over an existing one, want 0", len(accounts.ensured))
	}
	// The relationship stands either way; what this pins is that an existing
	// account is left alone.
	if !rootIsSeeded(t, authorizer, DefaultRootSubject) {
		t.Fatal("root relationship missing after seeding over an existing account")
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
	if err := SeedRoot(context.Background(), accounts, seedRootRelationship(t, newAuthorization(t), DefaultRootSubject), config, fixedClock()); err != nil {
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
	authorizer := newAuthorization(t)

	if err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}
	if !rootIsSeeded(t, authorizer, DefaultRootSubject) {
		t.Fatal("root relationship was not written for an existing account that lacked it")
	}
}

// Fail closed. Running with no reachable administrator is the worse failure,
// so a seeding error stops the boot rather than being logged past.
func TestSeedRootFailsClosed(t *testing.T) {
	outage := errors.New("connection refused")

	for name, seed := range map[string]func(*testing.T) (*fakeAccounts, *authorization.Authorization){
		"account lookup fails": func(t *testing.T) (*fakeAccounts, *authorization.Authorization) {
			return &fakeAccounts{lookupErr: outage}, newAuthorization(t)
		},
		"account write fails": func(t *testing.T) (*fakeAccounts, *authorization.Authorization) {
			return &fakeAccounts{ensureErr: outage}, newAuthorization(t)
		},
		"authorization is unreachable": func(t *testing.T) (*fakeAccounts, *authorization.Authorization) {
			return &fakeAccounts{}, newFailingAuthorization(t, outage)
		},
	} {
		t.Run(name, func(t *testing.T) {
			accounts, authorizer := seed(t)
			err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock())
			if err == nil {
				t.Fatal("SeedRoot succeeded despite a failure")
			}
			if name != "authorization is unreachable" && !errors.Is(err, outage) {
				t.Fatalf("error = %v, want it to wrap the underlying failure", err)
			}
		})
	}
}

func TestSeedRootRejectsAnEmptyPassword(t *testing.T) {
	config := testRootConfig()
	config.Password = ""

	err := SeedRoot(context.Background(), &fakeAccounts{}, newAuthorization(t), config, fixedClock())
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

		if err := SeedRoot(context.Background(), &fakeAccounts{}, newAuthorization(t), config, fixedClock()); err == nil {
			t.Fatalf("SeedRoot accepted the unusable subject %q", subject)
		}
	}
}

func TestSeedRootDefaultsTheUsername(t *testing.T) {
	accounts := &fakeAccounts{}
	config := testRootConfig()
	config.Username = ""

	if err := SeedRoot(context.Background(), accounts, newAuthorization(t), config, fixedClock()); err != nil {
		t.Fatalf("SeedRoot returned error: %v", err)
	}
	if accounts.ensured[0].Username != DefaultRootUsername {
		t.Fatalf("Username = %q, want %q", accounts.ensured[0].Username, DefaultRootUsername)
	}
}

// The relationship is checked for before it is written, which is what makes the
// reconcile add-only rather than a write that happens to be idempotent.
//
// Seeding twice is the assertion: OpenFGA rejects a write of a tuple that
// already exists, so a second run that wrote unconditionally would fail.
func TestSeedRootChecksTheTupleBeforeWriting(t *testing.T) {
	authorizer := newAuthorization(t)

	for attempt := 1; attempt <= 2; attempt++ {
		if err := SeedRoot(context.Background(), &fakeAccounts{}, authorizer, testRootConfig(), fixedClock()); err != nil {
			t.Fatalf("SeedRoot attempt %d returned error: %v", attempt, err)
		}
	}
	if !rootIsSeeded(t, authorizer, DefaultRootSubject) {
		t.Fatal("root relationship missing after two seeds")
	}
}

// An unset subject is not an invalid one: it means the default, which is the
// only value #212 expects anyone to run.
func TestSeedRootDefaultsTheSubject(t *testing.T) {
	accounts := &fakeAccounts{}
	config := testRootConfig()
	config.Subject = ""

	if err := SeedRoot(context.Background(), accounts, newAuthorization(t), config, fixedClock()); err != nil {
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

	if err := SeedRoot(context.Background(), accounts, seedRootRelationship(t, newAuthorization(t), DefaultRootSubject), config, fixedClock()); err != nil {
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
	authorizer := newAuthorization(t)

	err := SeedRoot(context.Background(), accounts, authorizer, testRootConfig(), fixedClock())
	if err == nil {
		t.Fatal("SeedRoot returned nil for a username held by another account")
	}
	if rootIsSeeded(t, authorizer, DefaultRootSubject) {
		t.Fatal("root relationship was written for an account that was not created")
	}
}
