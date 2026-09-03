package domain

// One installed template, and the state derived by comparing its
// desired, planned, and applied snapshots.

import (
	"encoding/json"
	"reflect"
	"time"
)

// StackTemplateLifecycle identifies whether an installed template can run.
type StackTemplateLifecycle string

const (
	StackTemplateActive     StackTemplateLifecycle = "active"
	StackTemplateDestroying StackTemplateLifecycle = "destroying"
	StackTemplateDestroyed  StackTemplateLifecycle = "destroyed"
	StackTemplateFailed     StackTemplateLifecycle = "failed"
	StackTemplateOrphaned   StackTemplateLifecycle = "orphaned"
)

// Valid reports whether the lifecycle is one of the supported states.
func (lifecycle StackTemplateLifecycle) Valid() bool {
	switch lifecycle {
	case StackTemplateActive, StackTemplateDestroying, StackTemplateDestroyed, StackTemplateFailed, StackTemplateOrphaned:
		return true
	default:
		return false
	}
}

// StackTemplate is one long-lived template install/component instance in a stack.
type StackTemplate struct {
	// ID uniquely identifies this installed component instance.
	ID StackTemplateID
	// TenantID scopes the install to one tenant.
	TenantID TenantID
	// StackID is the stack this component is installed into.
	StackID StackID
	// ComponentKey is the human/stable key unique among active installs in a stack.
	ComponentKey string
	// SourceTemplateID is the stable source identity shared by all revisions.
	SourceTemplateID SourceTemplateID
	// DesiredTemplateRevisionID is the template revision to use for the next run.
	DesiredTemplateRevisionID TemplateRevisionID
	// LastAppliedTemplateRevisionID is the template revision from the last successful apply.
	LastAppliedTemplateRevisionID TemplateRevisionID
	// WorkspaceName is the Terraform workspace used for this component.
	WorkspaceName string
	// InstalledConfigJSON is the config captured when the component was
	// installed. It is history: desired state is DesiredConfigJSON, and nothing
	// derives desired state from this.
	InstalledConfigJSON json.RawMessage
	// DesiredConfigJSON is the config to snapshot into the next run.
	DesiredConfigJSON json.RawMessage
	// LastAppliedRunID is the run that last applied this component successfully.
	LastAppliedRunID TemplateRunID
	// LastAppliedConfigJSON is the config the last successful apply ran with.
	// Nil means no apply has been recorded with one; an empty object is a
	// legitimate value, so absence cannot be spelled '{}'.
	LastAppliedConfigJSON json.RawMessage
	// LastAppliedAt is when the last successful apply completed.
	LastAppliedAt time.Time
	// LastPlannedRunID is the latest plan run that completed for this component.
	LastPlannedRunID TemplateRunID
	// LastPlannedTemplateRevisionID is the revision that plan ran against.
	LastPlannedTemplateRevisionID TemplateRevisionID
	// LastPlannedConfigJSON is the config that plan ran with. Nil like
	// LastAppliedConfigJSON, and for the same reason.
	LastPlannedConfigJSON json.RawMessage
	// LastPlannedAt is when that plan completed.
	LastPlannedAt time.Time
	// CreatedBy is the user that installed this component.
	CreatedBy UserID
	// Lifecycle determines whether this component can run or is being removed.
	Lifecycle StackTemplateLifecycle
}

// PlanState answers "will the thing that was reviewed be the thing that runs?".
// It is the safety gate on apply: only PlanMatches means the completed plan
// still describes desired state.
type PlanState string

const (
	// PlanNone means no plan has completed for this component.
	PlanNone PlanState = "none"
	// PlanStale means a plan completed but desired has moved since.
	PlanStale PlanState = "stale"
	// PlanMatches means the completed plan is exactly what would run now.
	PlanMatches PlanState = "matches"
)

// LiveState answers "is anything pending?" by comparing desired against what
// the last apply put live. It gates no action on its own; it is what lets the
// UI distinguish an unapplied edit from a steady state.
type LiveState string

const (
	// LiveNever means nothing has been applied yet.
	LiveNever LiveState = "never"
	// LiveDiffers means desired has moved away from what is live.
	LiveDiffers LiveState = "differs"
	// LiveMatches means the last apply's intent equals desired. It does not
	// claim reality matches desired — that is drift, and needs a refresh.
	LiveMatches LiveState = "matches"
)

// DesiredConfig is the config a run started now would snapshot. It is a method
// rather than a bare field read so every caller agrees on one answer; there used
// to be a fallback to the install-time config here, copied into each call site,
// and the copies were free to disagree.
//
// The fallback is gone: desired_config_json is `not null default '{}'`, so a
// persisted row always has a desired config, and an empty one means the config
// really is empty rather than absent.
func (stackTemplate StackTemplate) DesiredConfig() json.RawMessage {
	return defaultConfigJSON(stackTemplate.DesiredConfigJSON)
}

// PlanState reports whether the latest completed plan still describes desired
// state. A recorded plan whose config is missing reports PlanStale: the point of
// the gate is to refuse an apply nobody can vouch for, so anything unverifiable
// fails closed.
func (stackTemplate StackTemplate) PlanState() PlanState {
	if stackTemplate.LastPlannedRunID == "" {
		return PlanNone
	}
	if stackTemplate.matchesDesired(stackTemplate.plannedSnapshot()) {
		return PlanMatches
	}
	return PlanStale
}

// LiveState reports whether desired state has been applied. It fails closed the
// same way PlanState does — an unverifiable apply reads as pending work rather
// than as a steady state.
func (stackTemplate StackTemplate) LiveState() LiveState {
	if stackTemplate.LastAppliedRunID == "" {
		return LiveNever
	}
	if stackTemplate.matchesDesired(stackTemplate.appliedSnapshot()) {
		return LiveMatches
	}
	return LiveDiffers
}

// snapshot is one recorded (revision, config) pair. A stack template carries
// three of them — desired, planned, and applied — and every state it can be in
// is the distance between two, so a snapshot travels as one value rather than
// as two fields that a caller could pair up wrongly.
type snapshot struct {
	revisionID TemplateRevisionID
	configJSON json.RawMessage
}

// plannedSnapshot is what the latest completed plan ran against.
func (stackTemplate StackTemplate) plannedSnapshot() snapshot {
	return snapshot{
		revisionID: stackTemplate.LastPlannedTemplateRevisionID,
		configJSON: stackTemplate.LastPlannedConfigJSON,
	}
}

// appliedSnapshot is what the last successful apply put live.
func (stackTemplate StackTemplate) appliedSnapshot() snapshot {
	return snapshot{
		revisionID: stackTemplate.LastAppliedTemplateRevisionID,
		configJSON: stackTemplate.LastAppliedConfigJSON,
	}
}

// matchesDesired compares a recorded snapshot against desired. Both halves
// matter: a revision-only change leaves the config identical on both sides, and
// on an all-optional template both sides are '{}', so a config-only check would
// pass while the module version changed underneath.
func (stackTemplate StackTemplate) matchesDesired(recorded snapshot) bool {
	if recorded.revisionID != stackTemplate.DesiredTemplateRevisionID {
		return false
	}
	// Nil is "no config was ever recorded", which is not the same as "recorded
	// as empty". A template whose variables are all optional stores '{}' and
	// compares equal here like any other config; only a row whose run ID is set
	// while its config snapshot is null reaches this branch, and there is
	// nothing to vouch for it, so it reads as pending work.
	if recorded.configJSON == nil {
		return false
	}
	return sameJSONConfig(recorded.configJSON, stackTemplate.DesiredConfig())
}

// sameJSONConfig compares two configs by parsed structure. Comparing the bytes
// would be wrong: these are jsonb columns, and Postgres orders object keys by
// length then bytewise where Go sorts them alphabetically, so two identical
// configs can differ byte-for-byte and the user would be told to re-plan forever.
// Unmarshalling into any yields map[string]any/[]any/float64/string/bool/nil,
// and reflect.DeepEqual on maps ignores key order. Unparseable input reports
// false so callers fail closed.
func sameJSONConfig(left json.RawMessage, right json.RawMessage) bool {
	var leftValue, rightValue any
	if err := json.Unmarshal(defaultConfigJSON(left), &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(defaultConfigJSON(right), &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// defaultConfigJSON treats an empty desired config as the empty object it is
// persisted as, so a template installed with no config compares equal to a run
// that snapshotted '{}'.
func defaultConfigJSON(configJSON json.RawMessage) json.RawMessage {
	if len(configJSON) == 0 {
		return json.RawMessage(`{}`)
	}
	return configJSON
}
