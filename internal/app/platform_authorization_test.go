package app

import (
	"context"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authorization"
	"github.com/vishu42/tflive/internal/domain"
)

// The helpers below seed real tuples into a real engine. The fake they replaced
// restated the model's tier ordering in Go -- its own comment noted that a
// changed tier had to be fixed in two places -- which is exactly the
// duplication running the real model removes. A test now states only what a
// subject was granted; what that implies is the model's answer.

// platformAdmin, platformEditor and platformViewer seed one subject's tier.
func platformAdmin(subject string) func(*testing.T, *authorization.Authorization) {
	return func(t *testing.T, auth *authorization.Authorization) {
		grantPlatform(t, auth, subject, authorization.RelationAdmin)
	}
}

func platformEditor(subject string) func(*testing.T, *authorization.Authorization) {
	return func(t *testing.T, auth *authorization.Authorization) {
		grantPlatform(t, auth, subject, authorization.RelationEditor)
	}
}

func platformViewer(subject string) func(*testing.T, *authorization.Authorization) {
	return func(t *testing.T, auth *authorization.Authorization) {
		grantPlatform(t, auth, subject, authorization.RelationViewer)
	}
}

// newPlatformAuthorizer seeds platform tiers, and stores the parent edge on the
// stack ids the suite uses.
//
// The edge matters because the model routes administrator access to a stack
// through "can_administer from parent" rather than through any bypass in Go.
// These stacks come from fake repositories rather than from CreateStack, so
// nothing has written it. Tests that are about the edge itself use an id absent
// from this list.
func newPlatformAuthorizer(t *testing.T, tiers ...func(*testing.T, *authorization.Authorization)) *authorization.Authorization {
	t.Helper()
	auth := newTestAuthorization(t)
	for _, tier := range tiers {
		tier(t, auth)
	}
	for _, stackID := range linkedTestStacks {
		linkStackToPlatform(t, auth, stackID)
	}
	return auth
}

// linkedTestStacks are the stack ids newPlatformAuthorizer stores a parent edge
// for. Anything outside this list is unreachable by a platform administrator,
// which is what the parent-edge tests rely on.
var linkedTestStacks = []string{"stack_123", "stack_abc", "stack_a", "stack_b"}

// testPlatformAuthorizer covers the subjects the app tests authenticate as, so
// a test whose subject is incidental to what it asserts can drop it in.
func testPlatformAuthorizer(t *testing.T) *authorization.Authorization {
	t.Helper()
	return newPlatformAuthorizer(t,
		platformAdmin(keycloakSubject),
		platformAdmin("admin-subject"),
		platformAdmin("admin_123"),
		platformEditor("user-subject"),
		platformEditor("user_123"),
	)
}

func platformContext(subject string) context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: subject})
}

// The whole point of #141: the answer comes from OpenFGA. A principal with no
// tuple is refused and one holding it is allowed, and there is no longer any
// claim on the principal that could say otherwise -- authn.Principal carries
// identity only, which the compiler now enforces.
func TestPlatformCapabilitiesComeFromOpenFGANotRealmRoles(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Users:         &fakeUserRepository{users: []UserProfile{}},
		Authorization: newPlatformAuthorizer(t, platformAdmin("granted-subject")),
	})
	command := SearchUsersCommand{TenantID: domain.TenantID("tenant_1"), Query: "a", Max: 20}

	ungranted := authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject: "ungranted-subject",
		Name:    "Ada",
		Email:   "ada@example.test",
	})
	if _, err := service.SearchUsers(ungranted, command); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ungranted subject error = %v, want ErrForbidden", err)
	}
	if _, err := service.SearchUsers(platformContext("granted-subject"), command); err != nil {
		t.Fatalf("granted subject error = %v, want nil", err)
	}
}

// The escalation the split capabilities exist to prevent: one shared catalog
// gate let anyone who could read the catalog publish to it.
func TestPlatformViewerReadsTheCatalogButCannotPublishToIt(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		TemplateRevisions:     &recordingTemplateRepository{},
		TemplateRegistrations: &recordingTemplateRegistrationRepository{},
		Authorization:         newPlatformAuthorizer(t, platformViewer("viewer-subject")),
	})
	ctx := platformContext("viewer-subject")

	if _, err := service.ListTemplateRevisions(ctx, ListTemplateRevisionsCommand{TenantID: domain.TenantID("tenant_1")}); err != nil {
		t.Fatalf("ListTemplateRevisions error = %v, want nil", err)
	}
	_, err := service.RegisterTemplate(ctx, RegisterTemplateCommand{
		TenantID:  domain.TenantID("tenant_1"),
		RepoOwner: "acme",
		RepoName:  "infra",
		SourceRef: "main",
		RootPath:  ".",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("RegisterTemplate error = %v, want ErrForbidden", err)
	}
}

// A platform administrator reaches a stack it holds no direct role on through
// the model's parent edge, not through a bypass in this package.
//
// The parent edge is what carries it, so the test proves the route rather than
// the outcome: with the edge the administrator is allowed, and without it the
// same administrator is refused. A short-circuit on "is an administrator" in Go
// would allow both and fail the second half.
func TestPlatformAdminReachesAStackThroughTheModel(t *testing.T) {
	t.Parallel()

	auth := newPlatformAuthorizer(t, platformAdmin("admin-subject"))
	linkStackToPlatform(t, auth, "stack_linked")

	if err := authorizeStack(platformContext("admin-subject"), auth, domain.StackID("stack_linked"), authorization.RelationCanOperate, ErrForbidden); err != nil {
		t.Fatalf("authorizeStack with a parent edge = %v, want nil", err)
	}

	err := authorizeStack(platformContext("admin-subject"), auth, domain.StackID("stack_unlinked"), authorization.RelationCanOperate, ErrForbidden)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("authorizeStack without a parent edge = %v, want ErrForbidden -- the answer must come from the model", err)
	}
}

// Superseded by TestCreateStackWritesTheParentEdgeWithTheOwnerGrant in
// stack_authorization_test.go. GrantStackOwner was the queue handler's entry
// point; creation writes both tuples itself now, so the assertion moved to
// where the write happens.

// The parent edge must not be reachable through the grant API. NewGrant is the
// door that API uses, and it has to keep refusing a structural relation even
// though provisioning can now write one.
func TestGrantAPICannotWriteTheParentEdge(t *testing.T) {
	t.Parallel()

	object, err := authorization.ObjectFromID(authorization.TypeStack, "stack_abc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorization.NewGrant(authorization.PlatformSubject, object, authorization.RelationParent); !errors.Is(err, authorization.ErrInvalidInput) {
		t.Fatalf("NewGrant(parent) error = %v, want ErrInvalidInput", err)
	}
	subject, err := authorization.SubjectFromOIDCSub("user_123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorization.NewStructuralRelationship(subject, object, authorization.RelationOwner); !errors.Is(err, authorization.ErrInvalidInput) {
		t.Fatalf("NewStructuralRelationship(owner) error = %v, want ErrInvalidInput", err)
	}
}
