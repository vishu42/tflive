# Authentication and Authorization Runtime Configuration

**Date:** 2026-07-15

**Issue:** [AUTH-005](https://github.com/vishu42/tflive/issues/7)

**Status:** Approved for implementation planning

## Purpose

Define the API runtime configuration required by the authentication and
authorization foundation. The API must receive validated, typed settings for
its runtime environment, configured tenant, OIDC issuer and audience, and
OpenFGA endpoint, store, model, credential, and request timeout.

Configuration loading remains the first API startup step. Invalid or insecure
settings stop startup before the API connects to Postgres or Temporal or opens
an HTTP listener. Validation errors identify the offending environment
variable and the violated rule without exposing the supplied value.

## Scope

This ticket owns:

- typed API security configuration and environment parsing;
- development and production validation policy;
- secret-safe configuration formatting and startup errors;
- explicit per-request deadlines in the existing OpenFGA client;
- local and production configuration documentation; and
- automated coverage for valid, missing, malformed, development, production,
  deadline, and secret-leakage cases.

This ticket does not implement OIDC token verification, authentication
middleware, tenant-route enforcement, or the runtime OpenFGA authorization
adapter. Those remain the responsibility of AUTH-006, AUTH-007, AUTH-009, and
AUTH-010. This ticket provides the validated inputs those components consume.

## Design Decision

Runtime security settings belong to `internal/config`, the package already
responsible for environment-backed API and worker configuration. They are kept
in focused nested types instead of flattening every security setting into
`APIConfig` or reusing `internal/openfga.Config`.

The existing OpenFGA type is oriented toward model provisioning and includes
provisioner-only state such as the desired store name. The API runtime instead
requires an explicit store ID and model ID. Keeping the two configuration types
separate prevents provisioning policy from leaking into the runtime boundary
and lets AUTH-010 consume an API-specific contract.

## Configuration Types

Add `internal/config/auth.go` with these responsibilities:

```text
RuntimeMode
  development
  production

SecurityConfig
  Mode
  TenantID
  OIDC
  OpenFGA

OIDCConfig
  IssuerURL
  Audience

OpenFGAConfig
  APIURL
  StoreID
  ModelID
  APIToken
  RequestTimeout
```

`RuntimeMode` is a string-backed enum with only `development` and `production`
as valid resolved values. `TenantID` uses `traits.TenantID`. URLs are stored as
parsed `*url.URL` values, and the OpenFGA timeout is stored as `time.Duration`.

`APIToken` uses a redacting secret value rather than a plain printable string.
The secret type exposes only the narrow operations required to determine
whether it is empty and to retrieve its value when constructing an authorized
request. Its normal and Go-syntax formatting forms return `[REDACTED]`.
Formatting a containing OpenFGA or security configuration must not reveal the
token.

`APIConfig` gains one field:

```text
Security SecurityConfig
```

`LoadAPIConfig` delegates security parsing to a focused `loadSecurityConfig`
helper and returns its error before initializing other API dependencies.

## Environment Contract

| Variable | Sensitive | Requirement and default |
|---|---:|---|
| `TFLIVE_ENVIRONMENT` | No | Optional; empty resolves to `development`; valid explicit values are `development` and `production` |
| `TFLIVE_TENANT_ID` | No | Required in every mode; safe configured tenant identifier |
| `OIDC_ISSUER_URL` | No | Required in every mode; exact issuer used by later OIDC verification |
| `OIDC_AUDIENCE` | No | Required in every mode; expected API access-token audience |
| `OPENFGA_API_URL` | No | Required in every mode; OpenFGA API base URL |
| `OPENFGA_STORE_ID` | No | Required in every mode; exact ID emitted by OpenFGA bootstrap |
| `OPENFGA_MODEL_ID` | No | Required in every mode; exact immutable model ID emitted by OpenFGA bootstrap |
| `OPENFGA_API_TOKEN` | Yes | Optional in development and required in production |
| `OPENFGA_HTTP_TIMEOUT` | No | Optional positive Go duration; defaults to `10s` |

The empty environment-mode default is intentionally development for convenient
local startup. A deployment receives production policy only by explicitly
setting `TFLIVE_ENVIRONMENT=production`. Unknown non-empty values fail startup
rather than silently falling back to development.

`.env.example` documents these local values:

```dotenv
TFLIVE_ENVIRONMENT=development
TFLIVE_TENANT_ID=tenant_123
OIDC_ISSUER_URL=http://localhost:8082/realms/tflive
OIDC_AUDIENCE=tflive-api
OPENFGA_API_URL=http://localhost:8080
OPENFGA_STORE_ID=
OPENFGA_MODEL_ID=
OPENFGA_API_TOKEN=
OPENFGA_HTTP_TIMEOUT=10s
```

The store and model IDs remain blank until the deployment administrator copies
the exact assignments emitted by OpenFGA bootstrap. The example does not add an
OIDC client secret because access-token verification does not require one.
Bootstrap administrator credentials remain separate from API runtime
configuration.

## Common Validation

The following rules apply in both development and production:

1. `TFLIVE_TENANT_ID` contains 1 through 128 ASCII characters. It starts with
   an ASCII alphanumeric character and contains only ASCII alphanumerics,
   underscore, or hyphen. This accepts the repository's existing
   `tenant_123` shape and UUIDs while rejecting whitespace, path separators,
   traversal segments, and control characters.
2. `OIDC_ISSUER_URL` and `OPENFGA_API_URL` are absolute `http` or `https` URLs
   with a host. User information, query strings, and fragments are rejected.
   Paths are allowed because the Keycloak issuer includes a realm path and an
   OpenFGA deployment may use a base-path prefix.
3. `OIDC_AUDIENCE` is non-empty and contains no whitespace or control
   characters.
4. `OPENFGA_STORE_ID` and `OPENFGA_MODEL_ID` are non-empty opaque identifiers
   without whitespace or control characters. The runtime never discovers or
   substitutes a latest model.
5. `OPENFGA_HTTP_TIMEOUT` parses with `time.ParseDuration` and is greater than
   zero. An omitted value resolves to ten seconds.
6. An OpenFGA token, when present, contains no whitespace or control
   characters so it can be placed safely in a bearer authorization header.

Environment values that represent identifiers, modes, URLs, audiences, and
durations are trimmed before validation. The bearer token is validated as an
exact value and is not silently normalized.

## Production Validation

When `TFLIVE_ENVIRONMENT=production`, startup additionally requires:

- `OIDC_ISSUER_URL` to use HTTPS;
- `OPENFGA_API_URL` to use HTTPS; and
- `OPENFGA_API_TOKEN` to be non-empty.

Development permits the documented local HTTP issuer and OpenFGA endpoint and
permits a tokenless local OpenFGA service. Production never accepts those
transport or credential allowances.

## Startup and Data Flow

The startup sequence remains:

1. `cmd/api` calls `config.LoadAPIConfig`.
2. `LoadAPIConfig` parses the ordinary API settings and the nested security
   settings.
3. Any configuration failure returns an `ErrInvalidConfig`-wrapped error.
4. Only a completely valid configuration is used to initialize Postgres,
   migrations, Temporal, services, and the HTTP listener.

AUTH-006 reads the typed OIDC issuer and audience. AUTH-009 reads the configured
tenant. AUTH-010 reads the OpenFGA endpoint, exact IDs, credential, and timeout.
None of those consumers reparses environment strings or selects its own
defaults.

## Error and Secret Handling

Every security configuration failure wraps `ErrInvalidConfig` consistently
with the existing API configuration contract. Messages name the environment
variable and state its requirement, for example:

```text
invalid config: OIDC_ISSUER_URL must be an absolute HTTP or HTTPS URL
invalid config: OPENFGA_API_TOKEN is required in production
```

Errors do not include raw environment values. Parser errors that may quote
their input are converted into stable validation messages instead of being
wrapped directly. The API startup path continues to add only the safe
`load api config` context before logging the failure.

The API configuration loader does not consume or surface Keycloak bootstrap
passwords or unrelated client secrets. Tests provide sentinel bootstrap
password, client-secret, and API-token values and assert that none appears in
configuration errors, safe formatting, or captured startup logs.

## OpenFGA Request Deadlines

The existing `internal/openfga.Client` already configures an HTTP client
timeout. It will additionally retain the configured request timeout and derive
a child context with `context.WithTimeout` for every outbound request in
`doJSON`.

An earlier caller deadline remains authoritative because the derived context
cannot extend its parent. The cancel function is always deferred. The HTTP
client timeout remains as a transport-level backstop. If an exported
constructor is handed a non-positive timeout directly instead of receiving a
loader-produced configuration, it uses the same ten-second safe default so an
OpenFGA request never becomes unbounded.

This change makes every current OpenFGA provisioning and verification request
carry an explicit context deadline. AUTH-010 will apply the same runtime
timeout contract to authorization checks and relationship operations.

## Testing Strategy

### Configuration unit tests

Table-driven tests cover:

- omitted mode resolving to development;
- explicit valid development configuration with HTTP and no token;
- explicit valid production configuration with HTTPS and a token;
- every missing required value;
- an unknown environment mode;
- empty, overlong, prefixed, and character-invalid tenant IDs;
- malformed, relative, user-info-bearing, query-bearing, and fragment-bearing
  URLs;
- blank or unsafe audience, store ID, model ID, and token values;
- malformed, zero, and negative durations;
- the ten-second timeout default;
- development acceptance of documented local endpoints;
- production rejection of HTTP issuer and OpenFGA endpoints;
- production rejection of a missing token; and
- redaction under normal, detailed, and Go-syntax formatting.

Existing `LoadAPIConfig` tests receive a shared valid security environment so
their current database, Temporal, and artifact-store assertions remain focused.

### API startup tests

Startup tests prove that malformed or insecure security configuration returns
before Postgres, Temporal, migrations, service wiring, or listener creation.
Captured errors and logs are checked against sentinel bootstrap-password,
client-secret, and OpenFGA-token values.

### OpenFGA client tests

A recording transport inspects request contexts and proves that:

- each request has a deadline derived from the configured timeout;
- an earlier caller deadline is not extended;
- timeout cancellation surfaces through the existing error path; and
- bearer tokens remain redacted from backend error responses.

### Repository verification

Implementation verification includes the complete Go suite, existing web test
suite, frontend production build, configuration/documentation checks, and
`git diff --check`. No live OpenFGA service is required for the new unit tests.

## Documentation and Completion

Update `.env.example` and `docs/authentication.md` with the runtime variables,
development defaults, production requirements, and the distinction between
bootstrap credentials and API runtime credentials. Document that exact
OpenFGA store and model IDs must be copied from bootstrap before the API can
start.

After implementation and verification pass, update AUTH-005 to `Done` in
`docs/sprint/authn_and_authz/README.md`. The completion evidence must map the
tests and operational documentation back to every issue acceptance criterion.

## Acceptance-Criteria Mapping

| Acceptance criterion | Design response |
|---|---|
| Typed parsing and actionable errors | Nested enum, URL, tenant, secret, ID, and duration types with variable-specific validation messages |
| Refuse insecure or incomplete production startup | Explicit production HTTPS and OpenFGA credential guards run before dependency initialization |
| Do not log bootstrap passwords, client secrets, or API tokens | Bootstrap/client secrets are not consumed; token uses redacting formatting; errors and logs are sentinel-tested |
| Non-empty, validated tenant | Required 1–128 character safe identifier contract |
| Explicit OpenFGA deadlines | Per-request `context.WithTimeout` plus HTTP transport timeout |
| Valid, missing, malformed, development, and production tests | Dedicated table-driven config tests, startup tests, and request-deadline tests |
