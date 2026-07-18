# Stack Creation Authorization Design

**Issue:** AUTH-011 / GitHub issue #13  
**Goal:** Restrict stack creation to global creators and grant the authenticated creator confirmed owner access to every new stack.

## Scope

This change authorizes stack creation and establishes ownership for newly created stacks. It does not add authorization checks to existing stack, template, run, log, or artifact endpoints; AUTH-013 owns that endpoint permission matrix. It does not implement durable relationship-write retry or reconciliation; AUTH-012 owns recovery from cross-datastore partial failures.

## Authorization Boundary

Keycloak remains the authentication source and the source of the two global roles currently defined by the sprint: `platform-admin` and `stack-creator`. The existing authentication middleware normalizes those roles into the request principal.

OpenFGA remains the source of stack-scoped authorization. After a stack is persisted, the application creates and confirms an `owner` relationship for the principal subject on that stack. Subsequent stack-specific permissions will be checked through OpenFGA by AUTH-013.

This split is deliberate for the current model: a stack does not exist before creation, and the provisioned OpenFGA model only contains `user` and `stack` types. A future backlog item evaluates moving global permissions to OpenFGA through a tenant or platform authorization object.

## Design

`app.Service` gains an `authz.Authorizer` dependency. `CreateStack` will:

1. Read the authenticated principal from the context, as it does today.
2. Permit the request only if the principal has `platform-admin` or `stack-creator`; otherwise return a dedicated forbidden application error before generating an ID or calling the stack repository.
3. Persist the stack with the authenticated principal as `CreatedBy`.
4. Construct canonical OpenFGA identifiers from the principal subject and persisted stack ID.
5. Write one `owner` grant with confirmation enabled. The authorization adapter confirms the grant with its higher-consistency request behavior before success is returned.
6. Return the created stack only after ownership confirmation succeeds.

The API entrypoint constructs the existing OpenFGA authorization adapter from the configured OpenFGA security values and injects it into `app.Service`. `writeAppError` maps the new forbidden error to the existing stable `403` response and continues to map authorization dependency failures using `authz.HTTPStatus`.

## Failure Behavior

| Condition | Result |
|---|---|
| Missing or invalid principal | Existing `401` unauthenticated response. |
| Authenticated principal lacks both global roles | Stable `403`; no stack ID is generated and no repository or OpenFGA call occurs. |
| Stack persistence fails | Existing persistence error behavior; no owner write occurs. |
| Owner write, timeout, malformed response, or confirmation fails | The stack remains persisted, creation returns the stable authorization `503`, and AUTH-012 will make this state durably recoverable. |
| Repeated relationship delivery | The OpenFGA adapter's idempotent desired-state semantics make the owner grant safe to retry. |

`platform-admin` may create stacks through its global role. This ticket does not add ordinary stack permission checks or self-approval behavior; AUTH-013 and AUTH-015 own those rules.

## Testing

Application and API tests will cover:

- successful creation by `stack-creator` and `platform-admin` principals;
- rejection of an authenticated user with neither role, with no persisted stack or authorization write;
- owner grant construction using the authenticated Keycloak subject and generated stack ID, with confirmation requested;
- stack persistence failure preventing an owner write;
- authorization write and confirmation failures after persistence, including stable `503` API mapping;
- repeat-safe owner relationship delivery through the adapter contract;
- API startup wiring of the OpenFGA adapter.

Focused application, API, and authorization tests will run during development, followed by `go test ./...`. The sprint backlog marks AUTH-011 Done only after the acceptance criteria and verification pass.
# Stack Creation Authorization Design

**Issue:** AUTH-011 / GitHub issue #13

Stack creation is authorized by the normalized Keycloak `platform-admin` and
`stack-creator` realm roles because a stack does not exist until after the
creation decision. Once persisted, the application writes a confirmed OpenFGA
`owner` relationship from the authenticated subject to the new stack.

The application returns `403` before persistence for callers without either
role. It returns OpenFGA's stable `503` dependency outcome when owner delivery
or confirmation fails after persistence. AUTH-012 owns durable retry and
reconciliation of that cross-datastore partial state. AUTH-013 owns all later
stack-scoped endpoint checks.

OpenFGA remains the authority for stack-scoped permissions. The backlog tracks
a future migration of global permissions into an OpenFGA tenant or platform
object; that would require model and provisioning changes and is outside this
issue.
