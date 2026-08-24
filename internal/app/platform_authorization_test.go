package app

import (
	"context"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)

// platformAuthorizer stands in for the tuples #212 seeds, answering platform
// capability checks from a per-subject tier rather than from a realm role
// claim. It mirrors one property of the model that the app layer now relies on
// and no longer implements itself: because can_manage_access includes
// "can_administer from parent", an administrator answers true for every stack
// relation too, without any short-circuit in Go.
type platformAuthorizer struct {
	administrators map[string]bool
	capabilities   map[string]map[string]bool
	stackAllowed   bool
	checks         []authz.CheckRequest
}

// platformAdmin and platformEditor describe one subject's tier. The
// capabilities each grants are the model's, restated here so a test that
// changes a tier fails against the model's own test suite too.
func platformAdmin(subject string) func(*platformAuthorizer) {
	return func(authorizer *platformAuthorizer) {
		authorizer.administrators[subject] = true
		authorizer.grant(subject,
			authz.RelationCanAdminister,
			authz.RelationCanCreateStack,
			authz.RelationCanPublishTemplate,
			authz.RelationCanReadTemplate,
		)
	}
}

func platformEditor(subject string) func(*platformAuthorizer) {
	return func(authorizer *platformAuthorizer) {
		authorizer.grant(subject,
			authz.RelationCanCreateStack,
			authz.RelationCanPublishTemplate,
			authz.RelationCanReadTemplate,
		)
	}
}

func platformViewer(subject string) func(*platformAuthorizer) {
	return func(authorizer *platformAuthorizer) {
		authorizer.grant(subject, authz.RelationCanReadTemplate)
	}
}

func newPlatformAuthorizer(tiers ...func(*platformAuthorizer)) *platformAuthorizer {
	authorizer := &platformAuthorizer{
		administrators: map[string]bool{},
		capabilities:   map[string]map[string]bool{},
	}
	for _, tier := range tiers {
		tier(authorizer)
	}
	return authorizer
}

func (authorizer *platformAuthorizer) grant(subject string, relations ...authz.Relation) {
	if authorizer.capabilities[subject] == nil {
		authorizer.capabilities[subject] = map[string]bool{}
	}
	for _, relation := range relations {
		authorizer.capabilities[subject][relation.String()] = true
	}
}

func (authorizer *platformAuthorizer) allow(request authz.CheckRequest) bool {
	subject := request.Subject.ID()
	if authorizer.administrators[subject] {
		return true
	}
	if request.Object == authz.Platform {
		return authorizer.capabilities[subject][request.Relation.String()]
	}
	return authorizer.stackAllowed
}

func (authorizer *platformAuthorizer) Check(_ context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	authorizer.checks = append(authorizer.checks, request)
	return authz.CheckResult{Allowed: authorizer.allow(request)}, nil
}

func (authorizer *platformAuthorizer) BatchCheck(_ context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	results := make([]authz.CheckResult, len(request.Checks))
	for i, check := range request.Checks {
		authorizer.checks = append(authorizer.checks, check)
		results[i].Allowed = authorizer.allow(check)
	}
	return authz.BatchCheckResult{Results: results}, nil
}

func (authorizer *platformAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}

func (authorizer *platformAuthorizer) WriteRelationships(context.Context, authz.Mutation) error {
	return nil
}

func (authorizer *platformAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return nil
}

// testPlatformAuthorizer covers the subjects the app tests authenticate as, so
// a test whose subject is incidental to what it asserts can drop it in.
func testPlatformAuthorizer() *platformAuthorizer {
	return newPlatformAuthorizer(
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
		UserDirectory: &fakeUserDirectory{users: []DirectoryUser{}},
		Authorizer:    newPlatformAuthorizer(platformAdmin("granted-subject")),
	})
	command := SearchUsersCommand{TenantID: traits.TenantID("tenant_1"), Query: "a", Max: 20}

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
		Authorizer:            newPlatformAuthorizer(platformViewer("viewer-subject")),
	})
	ctx := platformContext("viewer-subject")

	if _, err := service.ListTemplateRevisions(ctx, ListTemplateRevisionsCommand{TenantID: traits.TenantID("tenant_1")}); err != nil {
		t.Fatalf("ListTemplateRevisions error = %v, want nil", err)
	}
	_, err := service.RegisterTemplate(ctx, RegisterTemplateCommand{
		TenantID:  traits.TenantID("tenant_1"),
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
// the model's parent edge, not through a bypass in this package. Asserting the
// Check was issued is the point: a short-circuit would skip it.
func TestPlatformAdminReachesAStackThroughTheModel(t *testing.T) {
	t.Parallel()

	authorizer := newPlatformAuthorizer(platformAdmin("admin-subject"))
	err := authorizeStack(platformContext("admin-subject"), authorizer, traits.StackID("stack_abc"), authz.RelationCanOperate, ErrForbidden)
	if err != nil {
		t.Fatalf("authorizeStack error = %v, want nil", err)
	}
	var asked bool
	for _, check := range authorizer.checks {
		if check.Object.String() == "stack:stack_abc" && check.Relation == authz.RelationCanOperate {
			asked = true
		}
	}
	if !asked {
		t.Fatal("authorizeStack must ask OpenFGA about the stack rather than short-circuit on administrator")
	}
}

// A stack whose parent edge was never written is invisible to every platform
// tier, so provisioning must write it with the owner grant rather than beside
// it. The model's own suite pins the consequence; this pins the cause.
func TestGrantStackOwnerWritesTheParentEdgeWithTheOwnerGrant(t *testing.T) {
	t.Parallel()

	authorizer := &recordingAuthorizer{}
	service := NewService(Service{Authorizer: authorizer})

	if err := service.GrantStackOwner(context.Background(), GrantStackOwnerCommand{
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_abc"),
		Subject:  "user_123",
	}); err != nil {
		t.Fatalf("GrantStackOwner error = %v", err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("write calls = %d, want 1 -- both tuples must land in one write", authorizer.calls)
	}

	written := map[string]string{}
	for _, grant := range authorizer.mutation.Grants() {
		written[grant.Relation().String()] = grant.Subject().String() + " -> " + grant.Object().String()
	}
	if got, want := written["owner"], "user:user_123 -> stack:stack_abc"; got != want {
		t.Fatalf("owner grant = %q, want %q", got, want)
	}
	if got, want := written["parent"], "platform:tflive -> stack:stack_abc"; got != want {
		t.Fatalf("parent edge = %q, want %q", got, want)
	}
}

// The parent edge must not be reachable through the grant API. NewGrant is the
// door that API uses, and it has to keep refusing a structural relation even
// though provisioning can now write one.
func TestGrantAPICannotWriteTheParentEdge(t *testing.T) {
	t.Parallel()

	object, err := authz.ObjectFromID(authz.TypeStack, "stack_abc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authz.NewGrant(authz.PlatformSubject, object, authz.RelationParent); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("NewGrant(parent) error = %v, want ErrInvalidInput", err)
	}
	subject, err := authz.SubjectFromOIDCSub("user_123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authz.NewStructuralRelationship(subject, object, authz.RelationOwner); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("NewStructuralRelationship(owner) error = %v, want ErrInvalidInput", err)
	}
}
