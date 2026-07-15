# OpenFGA Model Provisioning Design

**Status:** Approved

**Date:** 2026-07-15

**Issue:** [AUTH-004](https://github.com/vishu42/tflive/issues/6)

**Scope:** Version and provision the fixed per-stack authorization model for
the configured single tflive tenant.

## Purpose

This design defines the first tflive OpenFGA authorization model and the
repeatable process used to create or verify its store and immutable model
version. It implements the role and permission semantics already approved in
the authentication and authorization security architecture.

The result must give deployments explicit OpenFGA store and model IDs. The
tflive API will receive those IDs through environment configuration and will
never select a store by name or silently use the latest model.

Runtime authorization checks, relationship writes, grant replacement,
last-owner protection, and API enforcement remain in later authorization
tickets. This ticket provides their versioned model and provisioning
foundation only.

## Approved Decisions

- Keep the canonical OpenFGA model as API-format JSON in the repository so the
  provisioner sends the exact reviewed representation without a second DSL-to-
  JSON generation path.
- Define direct `owner`, `operator`, `approver`, and `viewer` relations on the
  `stack` type and derive all permissions from those roles.
- Do not add direct type restrictions to derived permissions. OpenFGA must
  reject relationship writes targeting those relations.
- Implement provisioning as a small Go REST client and one-shot command,
  following the established Keycloak provisioner pattern.
- Separate intentional first-time `bootstrap` behavior from non-mutating
  `verify` behavior.
- Emit generated identifiers as environment assignments on stdout. Deployment
  administrators copy those values into local or deployment environment
  configuration.
- Require `OPENFGA_STORE_ID` and `OPENFGA_MODEL_ID` for verification and later
  application use. Never infer either identifier at runtime.
- Treat concurrent bootstrap runs as unsupported because OpenFGA store names
  are not a uniqueness boundary. Serialize bootstrap and fail on any discovered
  ambiguity.
- Use OpenFGA CLI v0.7.15 for repository model tests and verify compatibility
  against the deployed OpenFGA v1.15.1 server.

## Repository Structure

The implementation adds these focused components:

```text
openfga/
  authorization-model.json
  authorization-model-tests.fga.yaml
internal/openfga/
  config.go
  client.go
  model.go
  provisioner.go
cmd/openfga-provisioner/
  main.go
Dockerfile.openfga-provisioner
```

Tests live beside their Go sources. Existing Compose verification, root setup
documentation, authentication documentation, and sprint tracking are updated
in place.

`internal/openfga` is limited to store and authorization-model provisioning.
It does not expose relationship tuple operations. The later runtime adapter can
reuse appropriate low-level conventions without coupling authorization checks
to bootstrap behavior.

## Authorization Model

The model uses OpenFGA schema 1.1 and canonical identifiers of the form
`user:<keycloak-sub>` and `stack:<stack-id>`.

Its semantic form is:

```text
model
  schema 1.1

type user

type stack
  relations
    define owner: [user]
    define operator: [user]
    define approver: [user]
    define viewer: [user]

    define can_view: owner or operator or approver or viewer
    define can_operate: owner or operator
    define can_approve: owner or approver
    define can_manage_access: owner
```

Only the four direct roles have `directly_related_user_types` metadata. The
four derived permission relations contain rewrite expressions but no direct
type restrictions. A tuple targeting a derived permission is therefore invalid
at the OpenFGA write boundary rather than merely discouraged by application
code.

The JSON file is versioned in Git, while each successful OpenFGA write creates
an immutable authorization model ID. Deployments select one exact model ID
until they intentionally update configuration.

## Configuration

The command reads:

| Variable | Bootstrap | Verify | Meaning |
|---|---|---|---|
| `OPENFGA_API_URL` | Required | Required | Base URL for the OpenFGA HTTP API |
| `OPENFGA_STORE_NAME` | Optional | Optional | Bootstrap discovery name; defaults to `tflive` |
| `OPENFGA_STORE_ID` | Not required | Required | Exact configured store identifier |
| `OPENFGA_MODEL_ID` | Not required | Required | Exact configured authorization model identifier |
| `OPENFGA_HTTP_TIMEOUT` | Optional | Optional | Positive request timeout; defaults to ten seconds |
| `OPENFGA_API_TOKEN` | Optional | Optional | Bearer credential for protected deployments |

The API URL must use HTTP or HTTPS and contain a host. User information,
fragments, and query strings are rejected. Plain HTTP remains valid for the
documented local Compose network; production transport policy is enforced by
the later runtime configuration ticket.

Store and model IDs are non-empty opaque values. The client URL-escapes them
instead of depending on their current ULID representation. The API token is a
secret and must never appear in logs, stdout, or surfaced errors.

`.env.example` includes empty `OPENFGA_STORE_ID` and `OPENFGA_MODEL_ID`
placeholders, not usable generated defaults. Local bootstrap output is copied
into `.env`; production values belong in deployment configuration or secret-
backed configuration according to the platform's policy.

## Provisioning Operations

### Bootstrap

Bootstrap is an explicit subcommand:

```bash
docker compose run --rm openfga-provision bootstrap
```

It performs the following sequence:

1. Load and validate configuration and the checked-in authorization model.
2. Read every page of OpenFGA stores and filter by exact configured store name.
3. Create the store when none exists, reuse it when exactly one exists, and
   fail when more than one matches.
4. Read every authorization model version in the selected store.
5. Normalize each model and compare it semantically with the repository model.
6. Reuse the single matching model, write a new immutable model when none
   matches, and fail if multiple matching versions make selection ambiguous.
7. Print only the selected identifiers to stdout:

   ```text
   OPENFGA_STORE_ID=<generated-store-id>
   OPENFGA_MODEL_ID=<generated-model-id>
   ```

Operational messages go to stderr. A successful unchanged rerun emits the same
identifiers and creates no resources.

Semantic normalization ignores server-generated model IDs, treats JSON object
key order as irrelevant, orders type definitions by type name, and orders
directly related user-type metadata deterministically. It preserves relation
rewrite structure, schema version, conditions, and all security-relevant
metadata.

If a future repository model changes, bootstrap finds or creates that exact
immutable version and emits its ID. The application remains on its configured
old model until deployment configuration is deliberately updated.

### Verify

Verify is the default container command and is also available explicitly:

```bash
docker compose run --rm openfga-provision verify
```

It requires both explicit IDs, fetches the exact store and model, confirms that
the store exists and the model semantically equals the repository model, and
performs no mutation. It does not list by name, select the latest model, or
fall back to bootstrap when configuration is missing.

Successful verification emits the same two environment assignments to stdout.
This keeps command output machine-readable while still proving the effective
configuration.

## Idempotency and Recovery

The workflow is safe for serialized reruns:

- A failure after store creation but before model creation leaves one named
  store that the next bootstrap reuses.
- A failure after model creation but before output leaves one matching model
  that the next bootstrap reuses.
- Pagination is exhausted before any uniqueness decision is made, preventing a
  duplicate hidden on a later page.
- Duplicate named stores or multiple identical model versions fail closed
  instead of choosing implicitly.
- Verification is read-only and may be repeated without side effects.

OpenFGA does not make store names unique, so two simultaneous bootstrap
commands could both create a store. Documentation requires one bootstrap at a
time. If a race or manual action creates ambiguity, future runs stop and report
the conflicting resource count for manual resolution.

## REST Client and Error Handling

The Go client uses the standard library and exposes only the operations needed
to list, create, and read stores and authorization models. Every request uses
the caller context and the configured HTTP timeout.

The client must:

- paginate until the server returns no continuation token;
- URL-escape all resource identifiers;
- send and accept JSON with explicit content types;
- treat only documented success statuses as success;
- cap response and error bodies before decoding or including safe excerpts;
- reject malformed or structurally incomplete responses;
- distinguish context cancellation and deadline expiry in wrapped internal
  errors;
- redact the configured API token if a hostile server echoes it;
- never include authorization headers or request bodies in errors.

Missing configuration, invalid URLs, non-positive timeouts, unavailable
servers, unexpected statuses, malformed JSON, ambiguous resources, and model
mismatches all produce non-zero command exits with actionable, non-secret
messages.

## Compose and Container Integration

`Dockerfile.openfga-provisioner` uses pinned build and runtime images, compiles
only the provisioner, includes CA certificates, and runs as a dedicated
unprivileged user.

`docker-compose.yaml` adds `openfga-provision` with:

- a `service_healthy` dependency on `openfga`;
- no published ports;
- `restart: "no"`;
- the OpenFGA API URL on the internal Compose network;
- optional empty store/model ID mappings so bootstrap can run before they are
  recorded;
- optional token and timeout mappings;
- `verify` as the default command.

The clean-checkout sequence starts OpenFGA, runs bootstrap, copies the two
assignments into `.env`, and then runs verification. Application services added
by later tickets consume the same explicit IDs.

## Testing Strategy

### Model Matrix

The OpenFGA CLI test file references the canonical JSON model and covers every
role-permission combination:

| Role | `can_view` | `can_operate` | `can_approve` | `can_manage_access` |
|---|---:|---:|---:|---:|
| `owner` | Allow | Allow | Allow | Allow |
| `operator` | Allow | Allow | Deny | Deny |
| `approver` | Allow | Deny | Allow | Deny |
| `viewer` | Allow | Deny | Deny | Deny |

An unrelated user is denied every permission. Tests also confirm each direct
role is assignable only to `user` subjects.

The command is pinned to OpenFGA CLI v0.7.15. Because that CLI bundles a newer
engine than the deployed server, live OpenFGA v1.15.1 verification remains a
required compatibility gate for the simple schema 1.1 model.

### Go Tests

Configuration tests cover valid local settings, defaults, missing values,
malformed URLs, invalid timeouts, explicit IDs, and optional credentials.

HTTP client tests cover pagination, identifier escaping, cancellation,
deadlines, bounded bodies, malformed responses, unexpected statuses,
authorization headers, and secret redaction.

A stateful fake backend exercises:

- first store and model creation;
- two unchanged bootstrap runs returning identical IDs;
- reuse of an existing uniquely named store;
- reuse of an existing semantically matching model;
- creation of one version for a changed model;
- recovery from store-created and model-created partial failures;
- duplicate named stores and duplicate matching models;
- exact-ID verification success;
- missing stores, missing models, and model mismatches.

Command tests cover subcommand validation, environment loading, cancellation,
wrapped failures, stdout/stderr separation, and absence of tokens or request
bodies from output.

Structural tests inspect the canonical model and prove that only the four role
relations declare directly related users. Live tests attempt writes to
`can_view`, `can_operate`, `can_approve`, and `can_manage_access` and require
OpenFGA to reject each derived relation as a direct tuple target.

### Integration and Regression Verification

The completion gate runs:

1. OpenFGA CLI model tests.
2. Go package and command tests.
3. The Compose contract test.
4. A clean live bootstrap against OpenFGA v1.15.1.
5. A second bootstrap proving stable identifiers and no duplicate resources.
6. Exact-ID verification using the emitted environment values.
7. Live rejected writes for every derived permission.
8. The complete Go and frontend test/build suites.
9. Compose configuration validation and `git diff --check`.

## Documentation and Tracking

`README.md` documents the two-phase local setup and verification commands.
`docs/authentication.md` gains the OpenFGA model, role matrix, generated-ID
handling, serialized bootstrap requirement, safe reruns, model upgrades, and
failure recovery. It explicitly distinguishes deployment administrators from
the per-stack `operator` relation.

`docs/sprint/authn_and_authz/README.md` moves AUTH-004 to Done only after model,
unit, live, Compose, and full regression verification pass. No generated ID,
API token, password, access token, or usable production default is committed.

## Out of Scope

- Runtime authorization check and list APIs.
- Relationship grant, replacement, or revocation workflows.
- Last-owner protection and higher-consistency mutation confirmation.
- API route enforcement and platform-administrator bypass behavior.
- Production authentication configuration for OpenFGA.
- High availability, backup, restore, and disaster recovery.

Those responsibilities remain with the later tickets identified in the
authentication and authorization sprint backlog.
