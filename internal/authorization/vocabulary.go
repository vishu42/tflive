package authorization

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ErrInvalidInput reports input that could not name a subject, object or
// relation at all -- a malformed identifier, not a refused request.
//
// It is the only sentinel this package exports. A denial is (false, nil) and a
// provider failure is an ordinary wrapped error; callers branch on neither.
// This one is different because callers genuinely act on it: a malformed stack
// id must read as invalid input rather than as a server fault.
var ErrInvalidInput = errors.New("invalid authorization input")

type identifier struct {
	objectType ObjectType
	id         string
}

// newIdentifier validates both halves and refuses anything that could change a
// tuple's meaning rather than merely be malformed. ':' would forge the type
// prefix, '#' would make the value a userset reference, and '*' would make it
// the typed wildcard that matches every user. objectType gets the same check as
// id: callers pass it as an exported ObjectType value, not always an in-package
// literal, so it must not be able to smuggle a separator into the rendered form.
func newIdentifier(objectType ObjectType, id string) (identifier, error) {
	if objectType == "" || !safeTupleToken(string(objectType)) {
		return identifier{}, fmt.Errorf("%w: invalid object type", ErrInvalidInput)
	}
	if !safeTupleToken(id) {
		return identifier{}, fmt.Errorf("%w: invalid %s identifier", ErrInvalidInput, objectType)
	}
	return identifier{objectType: objectType, id: id}, nil
}

// Type returns the declared type.
//
//	ObjectFromID(TypeStack, "abc").Type()  → TypeStack
//	Object{}.Type()                        → ""
func (ident identifier) Type() ObjectType {
	return ident.objectType
}

// ID returns the bare identifier, without the type prefix. It is the value the
// caller originally supplied, so code needing the raw OIDC "sub" back does not
// have to strip a prefix off String().
//
//	SubjectFromOIDCSub("alice").ID()    → "alice"
//	ObjectFromID(TypeStack, "abc").ID() → "abc"
//	Object{}.ID()                       → ""
func (ident identifier) ID() string {
	return ident.id
}

// String renders the canonical "type:id" form a provider adapter puts on the
// wire.
//
//	ObjectFromID(TypeStack, "abc").String()  → "stack:abc"
//	SubjectFromOIDCSub("alice").String()     → "user:alice"
//	Object{}.String()                        → ""
func (ident identifier) String() string {
	if ident.objectType == "" {
		return ""
	}
	return string(ident.objectType) + ":" + ident.id
}

// valid reports whether both halves are still ones newIdentifier would accept.
// The zero value is never valid, so a struct literal cannot bypass validation.
func (ident identifier) valid() bool {
	return ident.objectType != "" && safeTupleToken(string(ident.objectType)) && safeTupleToken(ident.id)
}

// ObjectType is an object type declared in the authorization model. It is not
// validated against a local list: OpenFGA validates type against the model, so
// an unknown type is refused against the single source of truth.
type ObjectType string

const (
	TypeUser     ObjectType = "user"
	TypeStack    ObjectType = "stack"
	TypePlatform ObjectType = "platform"
)

// PlatformID is the id of the platform singleton. Every global authorization
// question is asked about this one object, so the value is fixed here rather
// than configured: a second platform object would not fail, it would silently
// partition every global grant.
const PlatformID = "tflive"

// Subject is the tuple's user slot: who is acting.
type Subject struct {
	identifier
}

// subjectTypes are the object types allowed in a tuple's user slot. This is the
// constraint Subject exists to carry and Object must not: a stack is a resource
// and can never be an actor, so it must never reach the user slot.
//
// The platform singleton is here for the parent edge, whose tuple puts it in
// the user slot: {platform:tflive, parent, stack:X}. TypeStack must never join
// it.
var subjectTypes = map[ObjectType]bool{
	TypeUser:     true,
	TypePlatform: true,
}

// Platform is the singleton every global capability is checked against, and
// PlatformSubject is that same singleton in the user slot, where the parent
// edge puts it. Both are built from PlatformID, which safeTupleToken accepts,
// so no call site has to handle a construction error that cannot happen.
var (
	Platform        = mustObject(TypePlatform, PlatformID)
	PlatformSubject = mustSubject(TypePlatform, PlatformID)
)

// mustObject and mustSubject build the package-level singletons above. They
// panic at init, which for a constant id can only be a typo in this file.
func mustObject(objectType ObjectType, id string) Object {
	object, err := ObjectFromID(objectType, id)
	if err != nil {
		panic(fmt.Sprintf("authorization: invalid object %s:%s: %v", objectType, id, err))
	}
	return object
}

func mustSubject(objectType ObjectType, id string) Subject {
	ident, err := newIdentifier(objectType, id)
	if err != nil {
		panic(fmt.Sprintf("authorization: invalid subject %s:%s: %v", objectType, id, err))
	}
	subject := Subject{identifier: ident}
	if !subject.Valid() {
		panic(fmt.Sprintf("authorization: %s may not occupy the user slot", objectType))
	}
	return subject
}

// SubjectFromOIDCSub returns the canonical authorization identifier for sub.
//
// Named for its original and still most common caller, a verified ID token's
// "sub" claim, where the character rules matter most because that identifier
// is the one tflive does not originate. Local accounts now reach it too, with
// subs tflive does choose; the rules are the same either way, which is why
// they are enforced here rather than at each caller.
//
//	SubjectFromOIDCSub("00u1b2c3")      → Subject{"user:00u1b2c3"}, nil
//	SubjectFromOIDCSub("kc-sub-123")    → Subject{"user:kc-sub-123"}, nil
//	SubjectFromOIDCSub("alice#member")  → Subject{}, ErrInvalidInput  (userset)
//	SubjectFromOIDCSub("*")             → Subject{}, ErrInvalidInput  (everyone)
func SubjectFromOIDCSub(sub string) (Subject, error) {
	ident, err := newIdentifier(TypeUser, sub)
	if err != nil {
		return Subject{}, err
	}
	return Subject{identifier: ident}, nil
}

// Valid reports whether the subject is a canonical identifier whose type may
// occupy a tuple's user slot. Unlike Object.Valid it checks the type against
// subjectTypes, which is the whole reason Subject is its own type rather than
// an alias for Object.
//
//	SubjectFromOIDCSub("alice").Valid()  → true
//	Subject{}.Valid()                    → false
func (subject Subject) Valid() bool {
	return subject.valid() && subjectTypes[subject.objectType]
}

// Object is the tuple's object slot: the resource being acted on.
type Object struct {
	identifier
}

// ObjectFromID returns the canonical authorization identifier for id. The id
// must be a bare identifier: it is the caller's raw value, never an
// already-prefixed one.
//
//	ObjectFromID(TypeStack, "abc123")             → Object{"stack:abc123"}, nil
//	ObjectFromID(TypeUser, "00u1b2c3")            → Object{"user:00u1b2c3"}, nil
//	ObjectFromID(TypeStack, "stack:abc")          → Object{}, ErrInvalidInput  (already prefixed)
//	ObjectFromID(TypeUser, "al*ce")               → Object{}, ErrInvalidInput  (wildcard char)
//	ObjectFromID(TypeUser, "")                    → Object{}, ErrInvalidInput
//	ObjectFromID(ObjectType("stack:evil"), "abc") → Object{}, ErrInvalidInput  (type forges a prefix)
func ObjectFromID(objectType ObjectType, id string) (Object, error) {
	ident, err := newIdentifier(objectType, id)
	if err != nil {
		return Object{}, err
	}
	return Object{identifier: ident}, nil
}

// Valid reports whether the object is a canonical, validated identifier. Any
// declared type may be an object, so unlike Subject.Valid there is no type
// allowlist here.
//
//	ObjectFromID(TypeStack, "abc").Valid()  → true
//	Object{}.Valid()                        → false
func (object Object) Valid() bool {
	return object.valid()
}

// safeTupleToken reports whether token can appear in a tuple without changing
// its meaning.
//
//	safeTupleToken("can_view")  → true
//	safeTupleToken("a#b")       → false
//	safeTupleToken("")          → false
func safeTupleToken(token string) bool {
	if token == "" || strings.ContainsAny(token, ":#*") {
		return false
	}
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 || strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return false
	}
	return true
}

// Grant is a direct, grantable role assignment for a subject on an object.
type Grant struct {
	subject  Subject
	object   Object
	relation Relation
	// structural marks the edge as stored-but-not-access. Only
	// NewStructuralRelationship sets it, so a grant built the ordinary way can
	// never carry a structural relation past Valid.
	structural bool
}

// Structural reports whether the grant is a stored edge that is not access. A
// reader answering "who has access" skips these.
func (grant Grant) Structural() bool {
	return grant.structural
}

// NewGrant returns a validated direct role assignment. It refuses any relation
// the grant API may not write, so a Grant can never hold a structural edge
// however the Relation reached it.
//
//	NewGrant(user:alice, stack:abc, RelationOwner)    → Grant{…}, nil
//	NewGrant(user:alice, stack:abc, RelationCanView)  → Grant{}, ErrInvalidInput
//	NewGrant(user:alice, stack:abc, <"parent">)       → Grant{}, ErrInvalidInput
//	NewGrant(Subject{}, stack:abc, RelationOwner)     → Grant{}, ErrInvalidInput
//	NewGrant(user:alice, Object{}, RelationOwner)     → Grant{}, ErrInvalidInput
func NewGrant(subject Subject, object Object, relation Relation) (Grant, error) {
	grant := Grant{subject: subject, object: object, relation: relation}
	if !grant.Valid() {
		return Grant{}, fmt.Errorf("%w: invalid direct role grant", ErrInvalidInput)
	}
	return grant, nil
}

// Subject returns the grant subject.
//
//	NewGrant(user:alice, stack:abc, RelationOwner) then .Subject().String()  → "user:alice"
func (grant Grant) Subject() Subject {
	return grant.subject
}

// Object returns the grant object.
//
//	NewGrant(user:alice, stack:abc, RelationOwner) then .Object().String()  → "stack:abc"
func (grant Grant) Object() Object {
	return grant.object
}

// Relation returns the grant's direct relation. It is comparable by value, so
// callers can write grant.Relation() == RelationOwner.
//
//	NewGrant(user:alice, stack:abc, RelationOwner) then .Relation()  → RelationOwner
func (grant Grant) Relation() Relation {
	return grant.relation
}

// Valid reports whether the grant has validated identifiers and a grantable
// relation. Grantable, not merely valid: this is what keeps a structural edge
// out of a Mutation.
//
//	NewGrant(user:alice, stack:abc, RelationOwner) then .Valid()  → true
//	Grant{}.Valid()                                               → false
func (grant Grant) Valid() bool {
	if !grant.subject.Valid() || !grant.object.Valid() {
		return false
	}
	if grant.structural {
		return grant.relation.Structural()
	}
	return grant.relation.Grantable()
}

// NewStructuralRelationship builds a stored edge that is not access: today only
// {platform:tflive, parent, stack:X}, the edge that carries administrator
// inheritance onto a stack.
//
// It is a separate door from NewGrant on purpose. Grant.Valid requires a
// grantable relation, and that refusal is the only thing standing between a
// grant endpoint and this tuple, so the provisioning path gets its own
// constructor rather than the refusal being relaxed for everyone.
//
//	NewStructuralRelationship(platform:tflive, stack:abc, RelationParent) → ok
//	NewStructuralRelationship(user:alice, stack:abc, RelationOwner)       → ErrInvalidInput
func NewStructuralRelationship(subject Subject, object Object, relation Relation) (Grant, error) {
	if !relation.Structural() {
		return Grant{}, fmt.Errorf("%w: %s is not a structural relation", ErrInvalidInput, relation)
	}
	grant := Grant{subject: subject, object: object, relation: relation, structural: true}
	if !grant.Valid() {
		return Grant{}, fmt.Errorf("%w: invalid structural relationship", ErrInvalidInput)
	}
	return grant, nil
}
